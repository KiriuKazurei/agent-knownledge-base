import hashlib
import html
import json
import mimetypes
import os
import re
import uuid
from pathlib import Path


TEXT_EXTENSIONS = {
    ".txt", ".md", ".markdown", ".rst", ".csv", ".tsv", ".log", ".ini", ".conf", ".properties", ".go", ".py", ".js", ".jsx",
    ".ts", ".tsx", ".java", ".cs", ".rs", ".cpp", ".c", ".h", ".json",
    ".yaml", ".yml", ".toml", ".xml", ".sql", ".sh", ".ps1", ".css",
}


def _declared_encoding(raw):
    header = raw[:8192].decode("ascii", errors="ignore")
    match = re.search(r"charset\s*=\s*[\"']?\s*([A-Za-z0-9._:-]+)", header, flags=re.I)
    if not match:
        return None
    aliases = {
        "gb2312": "gb18030",
        "gbk": "gb18030",
        "gb18030": "gb18030",
        "936": "gb18030",
        "utf8": "utf-8",
        "utf-8": "utf-8",
        "utf16": "utf-16",
        "utf-16": "utf-16",
        "big5": "big5",
        "cp950": "big5",
    }
    return aliases.get(match.group(1).lower().replace("_", "-"))


def _text_quality(text):
    score = 0
    for character in text:
        if character == "\ufffd":
            score -= 10000
        elif character == "\x00":
            score -= 1000
        elif character in "\r\n\t" or character.isprintable():
            score += 1
        else:
            score -= 30
        if "\u4e00" <= character <= "\u9fff":
            score += 8
    return score


def _decode_candidate(raw, encoding):
    try:
        return raw.decode(encoding)
    except (LookupError, UnicodeDecodeError):
        return None


def _read_text(path):
    raw = path.read_bytes()
    if not raw:
        return ""
    bom_encodings = (
        (b"\xef\xbb\xbf", "utf-8-sig"),
        (b"\xff\xfe\x00\x00", "utf-32"),
        (b"\x00\x00\xfe\xff", "utf-32"),
        (b"\xff\xfe", "utf-16"),
        (b"\xfe\xff", "utf-16"),
    )
    for prefix, encoding in bom_encodings:
        if raw.startswith(prefix):
            decoded = _decode_candidate(raw, encoding)
            if decoded is None:
                raise UnicodeError("invalid {} document".format(encoding))
            return decoded

    declared = _declared_encoding(raw)
    if declared:
        decoded = _decode_candidate(raw, declared)
        if decoded is not None:
            return decoded

    utf8_text = _decode_candidate(raw, "utf-8")
    if utf8_text is not None:
        return utf8_text

    candidates = []
    if len(raw) >= 4 and len(raw) % 2 == 0:
        candidates.extend(("utf-16-le", "utf-16-be"))
    candidates.extend(("gb18030", "big5", "shift_jis", "euc_jp", "cp1252"))
    best = None
    best_score = None
    for encoding in candidates:
        decoded = _decode_candidate(raw, encoding)
        if decoded is None:
            continue
        score = _text_quality(decoded)
        if best is None or score > best_score:
            best, best_score = decoded, score
    if best is not None:
        return best
    raise UnicodeError("unable to decode text document without replacement characters")


def _text_blocks(path):
    text = _read_text(path)
    lines = text.splitlines()
    content_lines = list(enumerate(lines, 1))
    if lines and lines[0].strip() == "---":
        closing = None
        for index in range(1, len(lines)):
            if lines[index].strip() == "---":
                closing = index
                break
        if closing is not None:
            content_lines = list(enumerate(lines[closing + 1:], closing + 2))
    blocks = []
    current_heading = None
    buffer = []
    start_line = 1
    for line_number, line in content_lines:
        heading = re.match(r"^\s{0,3}(#{1,6})\s+(.+?)\s*$", line)
        if heading and buffer:
            blocks.append(("\n".join(buffer), {"kind": "text", "heading": current_heading, "lineStart": start_line, "lineEnd": line_number - 1}))
            buffer = []
        if heading:
            current_heading = heading.group(2)
            start_line = line_number
        if not buffer:
            start_line = line_number
        buffer.append(line)
    if buffer:
        blocks.append(("\n".join(buffer), {"kind": "text", "heading": current_heading, "lineStart": start_line, "lineEnd": len(lines) or 1}))
    return blocks
def _render_pdf_page(pdf_document, page_index, scale=200 / 72):
    """Render one PDF page without retaining the whole document in memory."""

    page = pdf_document.get_page(page_index)
    try:
        bitmap = page.render(scale=scale)
        try:
            return bitmap.to_pil().copy()
        finally:
            close = getattr(bitmap, "close", None)
            if close:
                close()
    finally:
        close = getattr(page, "close", None)
        if close:
            close()


def _ocr_pdf_page(pdf_document, page_index, engine):
    from .ocr import recognize

    image = _render_pdf_page(pdf_document, page_index)
    try:
        lines = recognize(engine, image)
        if not lines:
            # Only an actually rendered uniformly white page counts as blank.
            # Empty OCR output alone never establishes completeness.
            blank = False
            if hasattr(image, "convert"):
                gray = image.convert("L")
                try:
                    low, high = gray.getextrema()
                    blank = low >= 248 and high - low <= 3
                finally:
                    gray.close()
            return "", 1.0 if blank else 0.0, []
    finally:
        close = getattr(image, "close", None)
        if close:
            close()
    lines.sort(key=lambda item: (
        min((point[1] for point in item.get("box", []) if len(point) > 1), default=0),
        min((point[0] for point in item.get("box", []) if point), default=0),
    ))
    text = "\n".join(item["text"] for item in lines).strip()
    score = sum(item["score"] for item in lines) / len(lines)
    return text, score, [item.get("box", []) for item in lines]


def _pdf_blocks(path, ocr_mode="auto", ocr_engine=None):
    if ocr_mode not in {"off", "auto", "force"}:
        raise ValueError("ocr_mode must be off, auto, or force")
    from pypdf import PdfReader

    reader = PdfReader(str(path))
    blocks = []
    ocr_pages = []
    untrusted_pages = []
    missing_pages = []
    blank_pages = []
    pdf_document = None
    if ocr_mode != "off":
        try:
            import pypdfium2 as pdfium

            pdf_document = pdfium.PdfDocument(str(path))
        except ImportError as error:
            pdf_document = None
            if ocr_mode == "force":
                from .ocr import OfflineOCRError

                raise OfflineOCRError("pypdfium2 is not installed; offline PDF rendering is unavailable") from error
        except Exception as error:
            from .ocr import OfflineOCRError

            raise OfflineOCRError(f"failed to open PDF for offline OCR: {error}") from error
    try:
        for index, page in enumerate(reader.pages):
            text_error = None
            try:
                text = (page.extract_text() or "").strip()
            except Exception as error:
                text_error = error
                text = ""
            unsafe = "\ufffd" in text or "\x00" in text
            # Images can contain the body even when a header has valid text.
            has_images = bool(getattr(page, "images", []))
            use_ocr = ocr_mode == "force" or not text or unsafe or has_images
            if use_ocr and ocr_mode != "off":
                if unsafe:
                    untrusted_pages.append(index + 1)
                if pdf_document is None:
                    from .ocr import OfflineOCRError

                    raise OfflineOCRError(
                        f"PDF page {index + 1} requires OCR but pypdfium2 is not installed"
                    )
                if ocr_engine is None:
                    from .ocr import get_ocr_engine

                    ocr_engine = get_ocr_engine()
                ocr_text, confidence, boxes = _ocr_pdf_page(pdf_document, index, ocr_engine)
                if ocr_text:
                    if confidence < 0.8:
                        missing_pages.append(index + 1)
                    ocr_pages.append(index + 1)
                    blocks.append((ocr_text, {
                        "kind": "pdf-ocr", "page": index + 1, "extraction": "ocr",
                        "ocrConfidence": round(confidence, 4), "ocrBoxes": boxes,
                    }))
                    continue
                if confidence == 1.0 and not unsafe and text_error is None:
                    blank_pages.append(index + 1)
                    continue
                if unsafe:
                    raise UnicodeError("PDF text extraction produced replacement characters and OCR returned no text")
                missing_pages.append(index + 1)
                continue
            if text_error is not None:
                raise ValueError(f"PDF page {index + 1} text extraction failed: {text_error}") from text_error
            if not text or has_images:
                missing_pages.append(index + 1)
            if unsafe:
                raise UnicodeError("PDF text extraction produced replacement characters")
            if text:
                blocks.append((text, {"kind": "pdf", "page": index + 1, "extraction": "pdf-text"}))
    finally:
        if pdf_document is not None:
            close = getattr(pdf_document, "close", None)
            if close:
                close()
    return blocks, {"ocrPages": ocr_pages, "untrustedPages": untrusted_pages, "pageCount": len(reader.pages), "missingPages": missing_pages, "blankPages": blank_pages, "status": "incomplete" if missing_pages else "complete"}
def _docx_blocks(path):
    from docx import Document
    document = Document(str(path))
    blocks = []
    heading = None
    for index, paragraph in enumerate(document.paragraphs):
        text = paragraph.text.strip()
        if not text:
            continue
        style = paragraph.style.name if paragraph.style else ""
        if style.lower().startswith("heading"):
            heading = text
        blocks.append((text, {"kind": "docx", "paragraph": index + 1, "heading": heading, "style": style}))
    for table_index, table in enumerate(document.tables):
        rows = ["\t".join(cell.text.strip() for cell in row.cells) for row in table.rows]
        blocks.append(("\n".join(rows), {"kind": "docx-table", "table": table_index + 1}))
    return blocks


def _xlsx_blocks(path):
    from openpyxl import load_workbook
    workbook = load_workbook(str(path), read_only=True, data_only=True)
    blocks = []
    for sheet in workbook.worksheets:
        rows = []
        start = 1
        for row_index, row in enumerate(sheet.iter_rows(values_only=True), 1):
            values = ["" if value is None else str(value) for value in row]
            if not any(values):
                continue
            rows.append("\t".join(values))
            if len(rows) >= 60:
                blocks.append(("\n".join(rows), {"kind": "xlsx", "sheet": sheet.title, "rowStart": start, "rowEnd": row_index}))
                rows, start = [], row_index + 1
        if rows:
            blocks.append(("\n".join(rows), {"kind": "xlsx", "sheet": sheet.title, "rowStart": start, "rowEnd": sheet.max_row}))
    workbook.close()
    return blocks


def _pptx_blocks(path):
    from pptx import Presentation
    presentation = Presentation(str(path))
    blocks = []
    for slide_index, slide in enumerate(presentation.slides):
        texts = []
        for shape in slide.shapes:
            if hasattr(shape, "text") and shape.text.strip():
                texts.append(shape.text.strip())
        blocks.append(("\n".join(texts), {"kind": "pptx", "slide": slide_index + 1}))
    return blocks


def _html_blocks(path):
    try:
        from bs4 import BeautifulSoup
        soup = BeautifulSoup(_read_text(path), "html.parser")
        for node in soup(["script", "style", "noscript", "template"]):
            node.decompose()
        title = soup.title.get_text(" ", strip=True) if soup.title else path.stem
        blocks = []
        heading = None
        for node in soup.find_all(["h1", "h2", "h3", "p", "li", "pre", "table"]):
            text = node.get_text(" ", strip=True)
            if not text:
                continue
            if node.name in {"h1", "h2", "h3"}:
                heading = text
            blocks.append((text, {"kind": "html", "heading": heading, "element": node.name, "title": title}))
        return blocks
    except ImportError:
        text = re.sub(r"<script[\s\S]*?</script>", " ", _read_text(path), flags=re.I)
        text = html.unescape(re.sub(r"<[^>]+>", " ", text))
        return [(re.sub(r"\s+", " ", text), {"kind": "html"})]


def _split(text, location, target=1200, overlap=120):
    text = text.strip()
    if not text:
        return []
    if len(text) <= target:
        return [(text, dict(location))]
    chunks = []
    start = 0
    while start < len(text):
        end = min(len(text), start + target)
        if end < len(text):
            boundary = max(text.rfind("\n", start + target // 2, end), text.rfind("。", start + target // 2, end), text.rfind(". ", start + target // 2, end))
            if boundary > start:
                end = boundary + 1
        part = text[start:end].strip()
        if part:
            loc = dict(location)
            loc["charStart"], loc["charEnd"] = start, end
            chunks.append((part, loc))
        if end >= len(text):
            break
        start = max(start + 1, end - overlap)
    return chunks


def parse_document(path_value, document_id, media_type="", original_name="", ocr_mode=None, ocr_engine=None):
    path = Path(path_value).resolve(strict=True)
    suffix = (Path(original_name).suffix or path.suffix).lower()
    if suffix in {".html", ".htm"} or "html" in media_type:
        blocks = _html_blocks(path)
    elif suffix in TEXT_EXTENSIONS or media_type.startswith("text/"):
        blocks = _text_blocks(path)
    elif suffix == ".pdf":
        if ocr_mode is None:
            ocr_mode = os.environ.get("KAH_PDF_OCR_MODE", "auto").strip().lower() or "auto"
        blocks, extraction = _pdf_blocks(path, ocr_mode, ocr_engine)
    elif suffix == ".docx":
        blocks = _docx_blocks(path)
    elif suffix == ".xlsx":
        blocks = _xlsx_blocks(path)
    elif suffix == ".pptx":
        blocks = _pptx_blocks(path)
    else:
        raise ValueError("Unsupported document format: {}".format(suffix or media_type))

    chunks = []
    ordinal = 0
    for text, location in blocks:
        for part, part_location in _split(text, location, overlap=0):
            digest = hashlib.sha256(part.encode("utf-8")).hexdigest()
            stable = uuid.uuid5(uuid.NAMESPACE_URL, "{}:{}:{}".format(document_id, ordinal, digest))
            chunks.append({"id": str(stable), "text": part, "location": part_location, "contentHash": digest})
            ordinal += 1
    media = media_type or mimetypes.guess_type(original_name or path.name)[0] or "application/octet-stream"
    result = {"title": path.stem, "mediaType": media, "chunks": chunks}
    if suffix == ".pdf":
        result["extraction"] = extraction
        result["extraction"]["ocrMode"] = ocr_mode

    return result
