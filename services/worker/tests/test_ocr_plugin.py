import hashlib
import json
import os
import sys
import tempfile
import unittest
import zipfile
from pathlib import Path
from unittest.mock import patch
sys.path.insert(0,str(Path(__file__).parents[1]/"src"))
from knowledge_worker import ocr_plugin

class OcrPluginTests(unittest.TestCase):
    def setUp(self):
        self.temp=tempfile.TemporaryDirectory()
        self.root=Path(self.temp.name)
        self.env=patch.dict(os.environ,{"KAH_DATA_ROOT":str(self.root),"KAH_OCR_MODEL_DIR":"external"})
        self.env.start()
        self.addCleanup(self.env.stop)
        self.addCleanup(self.temp.cleanup)

    def package(self, tamper=False):
        path=self.root/"plugin.zip"
        files={name:b"test" for name in ("det.onnx","rec.onnx","cls.onnx","dict.txt")}
        manifest={"schema":"kah-ocr-resource/v1","version":"test","files":[{"name":name,"sha256":hashlib.sha256(data).hexdigest()} for name,data in files.items()]}
        with zipfile.ZipFile(path,"w") as z:
            z.writestr("manifest.json",json.dumps(manifest))
            for name,data in files.items():z.writestr(name,b"tampered" if tamper else data)
        return path

    def test_install_disable_restart_uninstall_and_reinstall(self):
        package=self.package()
        self.assertTrue(ocr_plugin.install(package)["enabled"])
        self.assertIn("plugins",ocr_plugin.active_model_root())
        ocr_plugin.set_enabled(False)
        self.assertFalse(ocr_plugin.status()["enabled"])
        with self.assertRaisesRegex(RuntimeError,"disabled"):ocr_plugin.active_model_root()
        ocr_plugin.set_enabled(True)
        (self.root/"source.pdf").write_bytes(b"keep user document")
        self.assertFalse(ocr_plugin.uninstall()["installed"])
        self.assertFalse((self.root/"plugins/ocr/models").exists())
        self.assertTrue((self.root/"source.pdf").exists())
        with self.assertRaisesRegex(RuntimeError,"disabled"):ocr_plugin.active_model_root()
        self.assertTrue(ocr_plugin.install(package)["enabled"])

    def test_hash_mismatch_leaves_plugin_uninstalled(self):
        with self.assertRaisesRegex(RuntimeError,"hash mismatch"):ocr_plugin.install(self.package(True))
        self.assertFalse(ocr_plugin.status()["installed"])

    def test_zip_traversal_is_rejected(self):
        path=self.root/"bad.zip"
        with zipfile.ZipFile(path,"w") as z:z.writestr("../escaped","bad")
        with self.assertRaises(ValueError):ocr_plugin.install(path)
        self.assertFalse((self.root/"plugins/escaped").exists())
