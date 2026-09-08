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

    def test_two_column_layout_reads_each_column_before_the_next(self):
        def line(text, left, top, right=None):
            return {"text": text, "score": .99, "box": [[left, top], [right or left + 120, top], [right or left + 120, top + 24], [left, top + 24]]}

        lines = [
            line("标题", 40, 30, 920),
            line("L1", 60, 180), line("R1", 620, 180),
            line("L2", 60, 280), line("R2", 620, 280),
            line("L3", 60, 380), line("R3", 620, 380),
            line("页脚", 40, 960),
        ]
        ordered, layout = ocr.order_reading_lines(lines, (1000, 1000))
        self.assertEqual([item["text"] for item in ordered], ["标题", "L1", "L2", "L3", "R1", "R2", "R3", "页脚"])
        self.assertEqual(layout["mode"], "column-major")
        self.assertEqual(layout["columnCount"], 2)

    def test_table_like_four_columns_keeps_row_major_order(self):
        def line(text, left, top):
            return {"text": text, "score": .99, "box": [[left, top], [left + 80, top], [left + 80, top + 24], [left, top + 24]]}

        lines = [
            line("T01", 60, 180), line("合同", 260, 180), line("12", 500, 180), line("通过", 720, 180),
            line("T02", 60, 280), line("报告", 260, 280), line("8", 500, 280), line("复核", 720, 280),
            line("T03", 60, 380), line("附件", 260, 380), line("3", 500, 380), line("失败", 720, 380),
        ]
        ordered, layout = ocr.order_reading_lines(lines, (1000, 1000))
        self.assertEqual([item["text"] for item in ordered], ["T01", "合同", "12", "通过", "T02", "报告", "8", "复核", "T03", "附件", "3", "失败"])
        self.assertEqual(layout["mode"], "row-major")
        self.assertEqual(layout["tableCount"], 1)
        self.assertEqual(layout["tables"][0]["headers"], ["T01", "合同", "12", "通过"])
        self.assertEqual(layout["tables"][0]["rows"], [["T02", "报告", "8", "复核"], ["T03", "附件", "3", "失败"]])

    def test_table_rows_tolerate_small_vertical_ocr_skew(self):
        def line(text, left, top):
            return {"text": text, "score": .99, "box": [[left, top], [left + 80, top], [left + 80, top + 24], [left, top + 24]]}

        lines = [
            line("编号", 60, 100), line("类型", 260, 102), line("页数", 500, 99), line("状态", 720, 101),
            line("T01", 60, 200), line("合同", 260, 202), line("12", 500, 201), line("通过", 720, 199),
            line("T02", 60, 300), line("报告", 260, 301), line("8", 500, 299), line("复核", 720, 302),
        ]
        ordered, layout = ocr.order_reading_lines(lines, (1000, 1000))
        self.assertEqual([item["text"] for item in ordered], ["编号", "类型", "页数", "状态", "T01", "合同", "12", "通过", "T02", "报告", "8", "复核"])
        self.assertEqual(layout["tables"][0]["headers"], ["编号", "类型", "页数", "状态"])
        self.assertEqual(layout["tables"][0]["rows"], [["T01", "合同", "12", "通过"], ["T02", "报告", "8", "复核"]])

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
