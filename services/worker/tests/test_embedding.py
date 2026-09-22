import hashlib
import json
import os
import sys
import tempfile
import types
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parents[1] / "src"))

from knowledge_worker.embedding import (  # noqa: E402
    BGE_COMMIT,
    BGE_ID,
    EmbeddingEngine,
    EmbeddingPackageError,
    EmbeddingRegistry,
    MANIFEST_SCHEMA,
    _runtime_compatibility,
    bge_catalog_entry,
    validate_manifest,
)


class EmbeddingTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.addCleanup(self.temp.cleanup)

    @staticmethod
    def manifest(model_id="fixture/tiny", model=b"model", tokenizer=b"tokenizer", max_tokens=8):
        files = [
            {"role": "model", "path": "model.onnx", "size": len(model), "sha256": hashlib.sha256(model).hexdigest()},
            {"role": "tokenizer", "path": "tokenizer.json", "size": len(tokenizer), "sha256": hashlib.sha256(tokenizer).hexdigest()},
            {"role": "license", "path": "LICENSE.txt", "size": 3, "sha256": hashlib.sha256(b"MIT").hexdigest()},
        ]
        return {
            "schema": MANIFEST_SCHEMA,
            "modelType": "text-embedding",
            "id": model_id,
            "version": "fixture-1",
            "upstreamCommit": "fixture-commit",
            "license": "MIT",
            "dimension": 2,
            "tokenizer": {"configHash": hashlib.sha256(tokenizer).hexdigest(), "type": "bert", "runtimeVersion": "0.21.0"},
            "maxTokens": max_tokens,
            "pooling": {"type": "cls", "normalize": True},
            "queryPrefix": "Q:",
            "passagePrefix": "P:",
            "runtime": {"name": "onnxruntime", "version": "1.20.0", "device": "cpu"},
            "files": files,
        }, {"model.onnx": model, "tokenizer.json": tokenizer, "LICENSE.txt": b"MIT"}

    def package(self, tamper=False, extra=None):
        manifest, files = self.manifest()
        if tamper:
            manifest["files"][0]["sha256"] = "0" * 64
        path = self.root / "model.zip"
        with zipfile.ZipFile(path, "w") as archive:
            archive.writestr("manifest.json", json.dumps(manifest))
            for name, data in files.items():
                archive.writestr(name, data)
            for name, data in (extra or {}).items():
                archive.writestr(name, data)
        return path

    def test_manifest_requires_data_only_contract(self):
        manifest, _ = self.manifest()
        self.assertEqual(validate_manifest(manifest)["schema"], MANIFEST_SCHEMA)
        for field, value in (("schema", "old"), ("license", ""), ("dimension", 0), ("maxTokens", 0)):
            invalid = dict(manifest)
            invalid[field] = value
            with self.subTest(field=field), self.assertRaises(EmbeddingPackageError):
                validate_manifest(invalid)
        invalid = dict(manifest)
        invalid["files"] = [dict(manifest["files"][0], role="script", path="run.py")]
        with self.assertRaises(EmbeddingPackageError):
            validate_manifest(invalid)
        invalid = dict(manifest)
        invalid["files"] = manifest["files"][:2]
        with self.assertRaisesRegex(EmbeddingPackageError, "license resource"):
            validate_manifest(invalid)
        invalid = dict(manifest)
        invalid["tokenizer"] = dict(manifest["tokenizer"], configHash="0" * 64)
        with self.assertRaisesRegex(EmbeddingPackageError, "tokenizer config hash"):
            validate_manifest(invalid)

    def test_zip_traversal_extra_files_and_hash_mismatch_are_rejected_atomically(self):
        registry = EmbeddingRegistry(self.root)
        traversal = self.root / "traversal.zip"
        with zipfile.ZipFile(traversal, "w") as archive:
            archive.writestr("../escaped", b"x")
        with self.assertRaises(EmbeddingPackageError):
            registry.import_package(str(traversal))
        self.assertFalse((self.root / "escaped").exists())

        with self.assertRaisesRegex(EmbeddingPackageError, "hash or size"):
            registry.import_package(str(self.package(tamper=True)))
        self.assertEqual(list((self.root / "models" / "embedding_models").glob("fixture__tiny")), [])

        with self.assertRaisesRegex(EmbeddingPackageError, "entries do not match"):
            registry.import_package(str(self.package(extra={"README.txt": b"unexpected"})))

    def test_install_disable_delete_and_active_protection(self):
        registry = EmbeddingRegistry(self.root)
        result = registry.import_package(str(self.package()))
        self.assertTrue(result["installed"])
        self.assertFalse(result["enabled"])
        with patch.object(registry, "health", return_value={"available": True, "modelId": "fixture/tiny"}):
            self.assertTrue(registry.enable("fixture/tiny", True)["enabled"])
            with self.assertRaisesRegex(EmbeddingPackageError, "active"):
                registry.uninstall("fixture/tiny")
            self.assertFalse(registry.enable("fixture/tiny", False)["enabled"])
        self.assertFalse(registry.uninstall("fixture/tiny")["installed"])

    def test_catalog_is_pinned_and_online_install_uses_fixed_hashes(self):
        entry = bge_catalog_entry()
        self.assertEqual(entry["id"], BGE_ID)
        self.assertEqual(entry["upstreamCommit"], BGE_COMMIT)
        self.assertEqual(entry["pooling"]["type"], "cls")
        self.assertEqual(entry["poolingConfig"]["sha256"], "AAA8861589F80C961A03CC86C7EEAEF7605C1676B9AB55329D33A304738769C6")
        self.assertTrue(entry["onlineInstall"])
        self.assertEqual(len(entry["files"]), 3)
        self.assertTrue(all(len(item["sha256"]) == 64 for item in entry["files"]))
        self.assertTrue(all(str(item["url"]).startswith("https://") for item in entry["files"]))

    def test_fake_onnx_session_checks_prefix_boundary_and_l2(self):
        manifest, _ = self.manifest(max_tokens=3)
        engine = EmbeddingEngine(self.root, manifest)

        class Encoding:
            def __init__(self, ids):
                self.ids = ids

        class Tokenizer:
            def __init__(self):
                self.inputs = []

            def encode(self, text, add_special_tokens=True):
                self.inputs.append((text, add_special_tokens))
                return Encoding(list(range(len(text))))

            def token_to_id(self, value):
                return 0 if value == "[PAD]" else None

        class Array(list):
            @property
            def ndim(self):
                return 2

            @property
            def shape(self):
                return (len(self), len(self[0]) if self else 0)

        fake_numpy = types.SimpleNamespace(asarray=lambda value, dtype=None: Array(value))
        tokenizer = Tokenizer()
        session = types.SimpleNamespace(
            get_inputs=lambda: [types.SimpleNamespace(name="input_ids"), types.SimpleNamespace(name="attention_mask"), types.SimpleNamespace(name="token_type_ids")],
            run=lambda _outputs, feeds: [[[3.0, 4.0] for _ in feeds["input_ids"]]],
        )
        engine._load_runtime = lambda: (setattr(engine, "session", session), setattr(engine, "tokenizer", tokenizer))
        with patch.dict(sys.modules, {"numpy": fake_numpy}):
            result = engine.embed(["long text"], is_query=True)
        self.assertEqual(tokenizer.inputs[0][0][:2], "Q:")
        self.assertTrue(result["truncated"][0])
        self.assertEqual(result["tokenCount"], [3])
        self.assertAlmostEqual(result["vectors"][0][0], 0.6, places=5)
        self.assertAlmostEqual(result["vectors"][0][1], 0.8, places=5)
        self.assertEqual(result["configHash"], hashlib.sha256(b"tokenizer").hexdigest())

    def test_missing_runtime_is_structured_health_degradation(self):
        registry = EmbeddingRegistry(self.root)
        registry.import_package(str(self.package()))
        health = registry.health("fixture/tiny")
        self.assertFalse(health["available"])
        self.assertIn("embedding_", health["error"])

    def test_runtime_requirement_reports_expected_and_actual_mismatch(self):
        manifest, _ = self.manifest()
        with patch("knowledge_worker.embedding._installed_version", side_effect=lambda name: {"onnxruntime": "1.19.0", "tokenizers": "0.21.0"}.get(name, "")):
            compatibility = _runtime_compatibility(manifest)
        self.assertEqual(compatibility["expectedRuntimeVersion"], "1.20.0")
        self.assertEqual(compatibility["runtimeVersion"], "1.19.0")
        self.assertTrue(any("onnxruntime expected" in item for item in compatibility["mismatches"]))


if __name__ == "__main__":
    unittest.main()
