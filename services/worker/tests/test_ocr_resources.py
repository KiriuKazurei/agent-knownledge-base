import hashlib
import json
import os
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

ROOT = Path(__file__).parents[1]
sys.path.insert(0, str(ROOT / "src"))

from knowledge_worker.ocr_resources import health_report, verify_resource_directory
from knowledge_worker.server import RpcServer


class OcrResourceTests(unittest.TestCase):
    def _resource_dir(self):
        directory = Path(tempfile.mkdtemp())
        files = {"det.onnx": b"det", "rec.onnx": b"rec", "cls.onnx": b"cls", "dict.txt": "中文\n".encode()}
        entries = []
        for name, content in files.items():
            path = directory / name
            path.write_bytes(content)
            entries.append({"name": name, "sha256": hashlib.sha256(content).hexdigest()})
        (directory / "manifest.json").write_text(json.dumps({
            "schema": "kah-ocr-resource/v1", "version": "test", "files": entries
        }), encoding="utf-8")
        return directory

    def test_manifest_and_hashes_are_verified(self):
        directory = self._resource_dir()
        try:
            result = verify_resource_directory(directory)
            self.assertEqual(result["fileCount"], 4)
            self.assertEqual(Path(result["models"]["det"]).name, "det.onnx")
        finally:
            for path in directory.iterdir():
                path.unlink()
            directory.rmdir()

    def test_hash_mismatch_is_rejected(self):
        directory = self._resource_dir()
        try:
            (directory / "rec.onnx").write_bytes(b"tampered")
            with self.assertRaisesRegex(RuntimeError, "hash mismatch"):
                verify_resource_directory(directory)
        finally:
            for path in directory.iterdir():
                path.unlink()
            directory.rmdir()

    def test_health_and_rpc_report_missing_directory_without_loading_models(self):
        with patch.dict(os.environ, {}, clear=True):
            self.assertEqual(health_report()["code"], "model_dir_missing")
            server = RpcServer.__new__(RpcServer)
            self.assertFalse(server.ocr_health({})["available"])

    def test_valid_resources_do_not_claim_engine_ready_when_initialization_fails(self):
        directory = self._resource_dir()
        try:
            with patch.dict(os.environ, {"KAH_OCR_MODEL_DIR": str(directory)}), patch("knowledge_worker.ocr.get_ocr_engine", side_effect=RuntimeError("missing runtime")):
                result = health_report()
            self.assertTrue(result["resourcesVerified"])
            self.assertFalse(result["available"])
            self.assertFalse(result["engineReady"])
            self.assertEqual(result["code"], "engine_unavailable")
        finally:
            for path in directory.iterdir():
                path.unlink()
            directory.rmdir()


if __name__ == "__main__":
    unittest.main()
