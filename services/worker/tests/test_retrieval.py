import json
import tempfile
import threading
import unittest
from pathlib import Path

from knowledge_worker.retrieval import UnifiedRetrievalIndex, chunk_text


class FakeEngine:
    def __init__(self, model_id="fake", version="1", config_hash="c" * 64, fail=False):
        self.manifest = {"maxTokens": 16}
        self.model_id = model_id
        self.version = version
        self.config_hash = config_hash
        self.fail = fail

    def embed(self, texts, is_query=False):
        if self.fail:
            raise RuntimeError("fake embedding failure")
        vectors = []
        for value in texts:
            vectors.append([1.0, 0.0] if "苹果" in value or "apple" in value.lower() else [0.0, 1.0])
        return {"vectors": vectors, "tokenCount": [2 for _ in texts], "truncated": [False for _ in texts]}


class FakeEmbeddings:
    def __init__(self, engine=None):
        self.engine = engine

    def active(self):
        if self.engine is None:
            return None, {"semanticAvailable": False, "degradedReason": "test keyword mode"}
        return self.engine, {
            "semanticAvailable": True,
            "modelId": self.engine.model_id,
            "modelVersion": self.engine.version,
            "configHash": self.engine.config_hash,
            "dimension": 2,
        }


def chunk(chunk_id, text, source_type="raw", review_status="unreviewed", library_id="library-1", project_id=""):
    return {
        "id": chunk_id,
        "text": text,
        "sourceType": source_type,
        "reviewStatus": review_status,
        "libraryId": library_id,
        "projectId": project_id,
        "sourceURI": "test://" + chunk_id,
        "contentHash": "h-" + chunk_id,
    }


class RetrievalTests(unittest.TestCase):
    def test_chunking_preserves_long_text_without_silent_truncation(self):
        text = "段落一\n\n" + ("x" * 13000) + "\n\n段落三"
        values = chunk_text(text, base=chunk("base", text), max_chars=1024)
        self.assertGreater(len(values), 2)
        self.assertEqual("".join(value["text"] for value in values), text)
        self.assertTrue(all(value["truncated"] is False for value in values))

    def test_scope_and_stable_knowledge_filter_before_ranking(self):
        with tempfile.TemporaryDirectory() as directory:
            index = UnifiedRetrievalIndex(directory, FakeEmbeddings())
            index.rebuild("library-1", [
                chunk("raw", "原始苹果资料"),
                chunk("draft", "苹果草稿", "knowledge", "unreviewed"),
                chunk("stable", "正式苹果知识", "knowledge", "stable"),
                chunk("other", "苹果其他库", library_id="library-2"),
            ], complete=True)
            index.rebuild("library-2", [chunk("other", "苹果其他库", library_id="library-2")], complete=True)
            result = index.search("苹果", {"libraryIds": ["library-1"], "allowedLibraryIds": ["library-1"], "allowRaw": False}, 10)
            self.assertEqual([value["id"] for value in result["results"]], ["stable"])
            with self.assertRaises(ValueError):
                index.search("苹果", {"libraryIds": ["library-1"], "allowRaw": True})

    def test_model_identity_mismatch_degrades_and_exact_symbol_is_preserved(self):
        with tempfile.TemporaryDirectory() as directory:
            embeddings = FakeEmbeddings(FakeEngine("model-a"))
            index = UnifiedRetrievalIndex(directory, embeddings)
            value = chunk("symbol", "调用 CreateUser 服务", library_id="library-1")
            value["symbol"] = "CreateUser"
            index.rebuild("library-1", [value], complete=True)
            embeddings.engine = FakeEngine("model-b")
            result = index.search("CreateUser", {"libraryIds": ["library-1"], "allowedLibraryIds": ["library-1"], "allowRaw": True}, 10)
            self.assertTrue(result["degraded"])
            self.assertIn("model", result["degradedReason"])
            self.assertTrue(result["results"][0]["symbolMatch"])

    def test_failed_rebuild_keeps_previous_pointer_and_restart_recovers_building(self):
        with tempfile.TemporaryDirectory() as directory:
            embeddings = FakeEmbeddings()
            index = UnifiedRetrievalIndex(directory, embeddings)
            first = index.rebuild("library-1", [chunk("old", "旧内容")], complete=True)
            embeddings.engine = FakeEngine(fail=True)
            with self.assertRaises(RuntimeError):
                index.rebuild("library-1", [chunk("new", "新内容")], complete=True)
            self.assertEqual(index.status("library-1")["generation"], first["generation"])
            root = Path(directory) / "retrieval_indexes" / "library-1" / "g-crash.building"
            root.mkdir(parents=True)
            (root / "metadata.json").write_text(json.dumps({"modelId": "fake"}), encoding="utf-8")
            restarted = UnifiedRetrievalIndex(directory, FakeEmbeddings())
            self.assertFalse(root.exists())
            self.assertEqual(restarted.progress("library-1")["status"], "failed")

    def test_incremental_upsert_is_marked_partial_until_full_rebuild(self):
        with tempfile.TemporaryDirectory() as directory:
            index = UnifiedRetrievalIndex(directory, FakeEmbeddings())
            result = index.upsert("library-1", "document-1", [chunk("raw", "原始资料")])
            self.assertTrue(result["needsRebuild"])
            self.assertFalse(result["corpusComplete"])


if __name__ == "__main__":
    unittest.main()
