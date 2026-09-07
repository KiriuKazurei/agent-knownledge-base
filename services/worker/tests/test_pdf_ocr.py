import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).parents[1]
sys.path.insert(0, str(ROOT / "src"))

from knowledge_worker import ocr
from knowledge_worker import parsers
from knowledge_worker.server import RpcServer


class _FakePdfDocument:
    def __init__(self, _path):
        self.closed = False

    def get_page(self, _index):
        return object()

    def close(self):
        self.closed = True


class _FakePdfPage:
    def extract_text(self):
        return ""


class _FakePdfReader:
    def __init__(self, _path):
        self.pages = [_FakePdfPage()]


class _FakeOcrEngine:
    def __call__(self, _image):
        return (
            [[[0, 0], [100, 0], [100, 20], [0, 20]]],
            ["扫描页面的中文内容"],
            [0.97],
        )


class PdfOcrTests(unittest.TestCase):
    def _blank_pdf(self, directory):
        source = Path(directory) / "scan.pdf"
        source.write_bytes(b"test-pdf")
        return source

    def test_normalize_rapidocr_object_with_numpy_like_boxes(self):
        class Result:
            boxes = [[[0, 0], [10, 0], [10, 10], [0, 10]]]
            txts = ("中文",)
            scores = (0.91,)

        result = ocr.normalize_result(Result())
        self.assertEqual(result[0]["text"], "中文")
        self.assertEqual(result[0]["score"], 0.91)

    def test_auto_mode_ocr_fills_blank_pdf_and_preserves_page_location(self):
        with tempfile.TemporaryDirectory() as directory:
            source = self._blank_pdf(directory)
            fake_pypdf = types.SimpleNamespace(PdfReader=_FakePdfReader)
            fake_pdfium = types.SimpleNamespace(PdfDocument=_FakePdfDocument)
            with patch.dict(sys.modules, {"pypdf": fake_pypdf, "pypdfium2": fake_pdfium}), patch.object(
                parsers, "_render_pdf_page", return_value=object()
            ):
                result = parsers.parse_document(
                    str(source), "document-scan", "application/pdf", "scan.pdf", "auto", _FakeOcrEngine()
                )
            self.assertEqual(result["extraction"]["ocrPages"], [1])
            self.assertEqual(result["extraction"]["ocrMode"], "auto")
            self.assertEqual(result["chunks"][0]["text"], "扫描页面的中文内容")
            self.assertEqual(result["chunks"][0]["location"]["kind"], "pdf-ocr")
            self.assertEqual(result["chunks"][0]["location"]["page"], 1)
            self.assertEqual(result["chunks"][0]["location"]["extraction"], "ocr")

    def test_off_mode_does_not_require_ocr_for_blank_pdf(self):
        with tempfile.TemporaryDirectory() as directory:
            source = self._blank_pdf(directory)
            fake_pypdf = types.SimpleNamespace(PdfReader=_FakePdfReader)
            with patch.dict(sys.modules, {"pypdf": fake_pypdf}):
                result = parsers.parse_document(str(source), "document-off", "application/pdf", "scan.pdf", "off")
            self.assertEqual(result["chunks"], [])
            self.assertEqual(result["extraction"]["ocrPages"], [])

    def test_rpc_parse_forwards_ocr_mode_to_parser(self):
        server = RpcServer.__new__(RpcServer)
        with patch("knowledge_worker.server.parse_document", return_value={"chunks": []}) as parse:
            server.parse({
                "path": "source.pdf",
                "documentId": "document-rpc",
                "mediaType": "application/pdf",
                "originalName": "source.pdf",
                "ocrMode": "force",
            })
        self.assertEqual(parse.call_args.args, (
            "source.pdf", "document-rpc", "application/pdf", "source.pdf", "force"
        ))


if __name__ == "__main__":
    unittest.main()
