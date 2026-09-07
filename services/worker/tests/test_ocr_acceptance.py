"""Acceptance regressions for extraction quality and offline resource safety."""
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1] / "src"))
from knowledge_worker import parsers


class OcrAcceptanceTests(unittest.TestCase):
    def test_unrecognized_scan_is_not_reported_complete(self):
        page = types.SimpleNamespace(extract_text=lambda: "")
        reader = types.SimpleNamespace(PdfReader=lambda _: types.SimpleNamespace(pages=[page]))
        renderer = types.SimpleNamespace(PdfDocument=lambda _: types.SimpleNamespace(close=lambda: None))
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "scan.pdf"
            source.write_bytes(b"mock PDF; no real engine acceptance")
            with patch.dict(sys.modules, {"pypdf": reader, "pypdfium2": renderer}), patch.object(parsers, "_ocr_pdf_page", return_value=("", 0, [])):
                result = parsers.parse_document(source, "doc", "application/pdf", "scan.pdf", "auto", object())
        self.assertNotEqual(result["extraction"]["status"], "complete", "unclassified empty OCR output must not assert complete extraction")

    def test_broken_text_layer_attempts_ocr(self):
        def broken():
            raise ValueError("broken text layer")
        page = types.SimpleNamespace(extract_text=broken)
        reader = types.SimpleNamespace(PdfReader=lambda _: types.SimpleNamespace(pages=[page]))
        renderer = types.SimpleNamespace(PdfDocument=lambda _: types.SimpleNamespace(close=lambda: None))
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "scan.pdf"
            source.write_bytes(b"mock PDF")
            with patch.dict(sys.modules, {"pypdf": reader, "pypdfium2": renderer}), patch.object(parsers, "_ocr_pdf_page", return_value=("recovered text", .99, [])):
                result = parsers.parse_document(source, "doc", "application/pdf", "scan.pdf", "auto", object())
        self.assertEqual(result["chunks"][0]["text"], "recovered text")

    def test_mixed_page_uses_ocr_despite_header_text(self):
        page = types.SimpleNamespace(extract_text=lambda: "header", images=[object()])
        reader = types.SimpleNamespace(PdfReader=lambda _: types.SimpleNamespace(pages=[page]))
        renderer = types.SimpleNamespace(PdfDocument=lambda _: types.SimpleNamespace(close=lambda: None))
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "mixed.pdf"
            source.write_bytes(b"mock PDF")
            with patch.dict(sys.modules, {"pypdf": reader, "pypdfium2": renderer}), patch.object(parsers, "_ocr_pdf_page", return_value=("header and scanned body", .99, [])) as recognize:
                result = parsers.parse_document(source, "doc", "application/pdf", "mixed.pdf", "auto", object())
        recognize.assert_called_once()
        self.assertIn("scanned body", result["chunks"][0]["text"])

    def test_manifest_override_cannot_load_unverified_file(self):
        from knowledge_worker.ocr import _model_file, OfflineOCRError
        with tempfile.TemporaryDirectory() as folder:
            root = Path(folder)
            (root / "verified.onnx").write_bytes(b"verified")
            (root / "other.onnx").write_bytes(b"other")
            with patch.dict("os.environ", {"KAH_OCR_DET_MODEL": str(root / "other.onnx")}):
                with self.assertRaises(OfflineOCRError):
                    _model_file(root, "KAH_OCR_DET_MODEL", "verified.onnx")

    def test_source_chunks_have_no_overlap(self):
        with tempfile.TemporaryDirectory() as folder:
            source = Path(folder) / "long.txt"
            original = "".join(str(i % 10) for i in range(3100))
            source.write_text(original)
            result = parsers.parse_document(source, "doc", "text/plain", "long.txt")
        self.assertEqual("".join(chunk["text"] for chunk in result["chunks"]), original)
