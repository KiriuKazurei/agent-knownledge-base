import os
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).parents[1]
sys.path.insert(0, str(ROOT / "src"))

from knowledge_worker.index import HybridIndex
from knowledge_worker.parsers import parse_document
from knowledge_worker.server import RpcServer


class WorkerTests(unittest.TestCase):
    def test_markdown_parse_preserves_heading_and_stable_chunks(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "note.md"
            source.write_text("# 标题\n\nKnowledge Agent Hub 支持引用定位。\n", encoding="utf-8")
            first = parse_document(str(source), "document-1", "text/markdown")
            second = parse_document(str(source), "document-1", "text/markdown")
            self.assertTrue(first["chunks"])
            self.assertEqual(first["chunks"][0]["id"], second["chunks"][0]["id"])
            self.assertEqual(first["chunks"][0]["location"]["heading"], "标题")


    def test_utf8_bom_markdown_preserves_chinese_body(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "utf8-notes.md"
            source.write_bytes(b"\xef\xbb\xbf" + "# 中文标题\n\nUTF-8 中文正文必须完整显示。\n".encode("utf-8"))
            result = parse_document(str(source), "document-utf8", "text/markdown")
            self.assertEqual(result["chunks"][0]["location"]["heading"], "中文标题")
            self.assertIn("UTF-8 中文正文必须完整显示。", result["chunks"][0]["text"])

    def test_gb18030_markdown_preserves_chinese_link(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "gb18030-notes.md"
            source.write_bytes("# 目录\n\n[下一篇：中文](./章节/第二篇.md)\n".encode("gb18030"))
            result = parse_document(str(source), "document-gb18030", "text/markdown")
            self.assertIn("[下一篇：中文](./章节/第二篇.md)", result["chunks"][0]["text"])

    def test_utf16_le_without_bom_preserves_chinese_content(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "utf16-notes.md"
            expected = "# \u4e2d\u6587\u6807\u9898\n\nUTF-16 \u4e2d\u6587\u6b63\u6587\u4e0d\u80fd\u4e71\u7801\u3002\n"
            source.write_bytes(expected.encode("utf-16-le"))
            result = parse_document(str(source), "document-utf16", "text/markdown")
            self.assertEqual(result["chunks"][0]["location"]["heading"], "\u4e2d\u6587\u6807\u9898")
            self.assertIn("\u4e2d\u6587\u6b63\u6587\u4e0d\u80fd\u4e71\u7801\u3002", result["chunks"][0]["text"])
            self.assertNotIn("\ufffd", result["chunks"][0]["text"])
            self.assertNotIn("\x00", result["chunks"][0]["text"])

    def test_declared_gbk_html_preserves_chinese_content(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "legacy.html"
            body = "\u4e2d\u6587 HTML \u6b63\u6587\u5fc5\u987b\u6309\u58f0\u660e\u7f16\u7801\u89e3\u6790\u3002"
            source.write_bytes(b'<html><head><meta charset="gbk"></head><body><p>' + body.encode("gb18030") + b"</p></body></html>")
            result = parse_document(str(source), "document-html", "text/html")
            self.assertIn(body, result["chunks"][0]["text"])
            self.assertNotIn("\ufffd", result["chunks"][0]["text"])

    def test_gb18030_csv_preserves_chinese_columns(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "table.csv"
            expected = "\u6807\u9898,\u5185\u5bb9\n\u4e2d\u6587\u8bb0\u5f55,\u65e0\u4e71\u7801\u5bfc\u5165\n"
            source.write_bytes(expected.encode("gb18030"))
            result = parse_document(str(source), "document-csv", "text/csv")
            self.assertIn("\u4e2d\u6587\u8bb0\u5f55,\u65e0\u4e71\u7801\u5bfc\u5165", result["chunks"][0]["text"])

    def test_markdown_frontmatter_is_not_indexed_and_preserves_chinese_location(self):
        with tempfile.TemporaryDirectory() as directory:
            source = Path(directory) / "submission.md"
            source.write_text(
                "---\ntitle: 中文知识\nsummary: 摘要\n tags: []\nlanguage: zh-CN\nprovenance:\n  type: internal\n  basis: 本地文档\n---\n\n# 中文知识\n\n## 核心内容\n\n这是需要进入正式索引的中文正文。\n\n## 适用范围\n\n用于提交审核。\n\n## 限制与不确定性\n\n需要人工确认。\n",
                encoding="utf-8",
            )
            result = parse_document(str(source), "document-submission", "text/markdown")
            self.assertTrue(result["chunks"])
            self.assertNotIn("摘要", result["chunks"][0]["text"])
            self.assertNotIn("provenance", result["chunks"][0]["text"])
            self.assertIn("中文正文", "\n".join(chunk["text"] for chunk in result["chunks"]))
            self.assertGreaterEqual(result["chunks"][0]["location"]["lineStart"], 9)
    def test_portable_index_roundtrip(self):
        with tempfile.TemporaryDirectory() as directory:
            index = HybridIndex(directory)
            index.upsert("library-1", "document-1", [{"id": "chunk-1", "text": "bilingual knowledge 检索", "location": {}, "contentHash": "hash"}])
            result = index.search("knowledge", ["library-1"], 10)
            self.assertEqual(result["results"][0]["id"], "chunk-1")
            self.assertFalse(Path(result["results"][0]["id"]).is_absolute())

    def test_hybrid_search_returns_vector_scores_and_honors_mode(self):
        with tempfile.TemporaryDirectory() as directory:
            index = HybridIndex(directory)
            index.upsert("library-1", "document-1", [
                {"id": "chunk-apple", "text": "苹果和香蕉是水果", "location": {}, "contentHash": "a"},
                {"id": "chunk-database", "text": "数据库索引支持快速检索", "location": {}, "contentHash": "b"},
            ])
            result = index.search("苹果", ["library-1"], 10, "vector")
            self.assertFalse(result["degraded"])
            self.assertEqual(result["retrievalMode"], "vector")
            self.assertEqual(result["results"][0]["id"], "chunk-apple")
            self.assertGreater(result["results"][0]["vector"], 0)
            self.assertEqual(result["results"][0]["final"], result["results"][0]["vector"])

    def test_rpc_unknown_method(self):
        with tempfile.TemporaryDirectory() as directory:
            previous = os.environ.get("KAH_DATA_ROOT")
            os.environ["KAH_DATA_ROOT"] = directory

            try:
                response = RpcServer().dispatch({"jsonrpc": "2.0", "id": 4, "method": "missing"})
            finally:
                if previous is None:
                    os.environ.pop("KAH_DATA_ROOT", None)
                else:
                    os.environ["KAH_DATA_ROOT"] = previous
            self.assertEqual(response["error"]["code"], -32601)


    def test_rebuild_switches_active_index_version(self):
        with tempfile.TemporaryDirectory() as directory:
            index = HybridIndex(directory)
            index.upsert("library-1", "document-1", [{"id": "old", "text": "old content", "location": {}, "contentHash": "old"}])
            result = index.rebuild("library-1", [{"id": "new", "documentId": "document-2", "text": "new content", "location": {}, "contentHash": "new"}])
            self.assertTrue(result["version"].startswith("v"))
            self.assertEqual(index.search("new", ["library-1"], 10)["results"][0]["id"], "new")
            self.assertTrue((Path(directory) / "indexes" / "library-1" / "active.json").exists())
if __name__ == "__main__":
    unittest.main()
