"""Managed, removable OCR resource plugin. Never executes code from imported ZIPs."""
import json
import os
import shutil
import tempfile
import zipfile
from pathlib import Path
from .ocr_resources import verify_resource_directory


def plugin_root():
    base = Path(os.environ.get("KAH_DATA_ROOT", ".run-data")).resolve()
    root = base / "plugins" / "ocr"
    # Reject links before destructive operations, including linked parent directories.
    if (base / "plugins").is_symlink() or root.is_symlink() or root.resolve() != root:
        raise ValueError("OCR plugin directory must not redirect outside the data root")
    return root


def state():
    path = plugin_root() / "state.json"
    return json.loads(path.read_text(encoding="utf-8")) if path.exists() else None


def save_state(value):
    root = plugin_root()
    root.mkdir(parents=True, exist_ok=True)
    temporary = root / "state.json.tmp"
    temporary.write_text(json.dumps(value), encoding="utf-8")
    temporary.replace(root / "state.json")


def active_model_root():
    value = state()
    if value is not None:
        if not value.get("installed") or not value.get("enabled"):
            raise RuntimeError("ocr_plugin_disabled: install and enable the OCR plugin in Settings")
        return str(plugin_root() / "models")
    return os.environ.get("KAH_OCR_MODEL_DIR", "").strip()


def status():
    value = state() or {"installed": False, "enabled": False}
    root = plugin_root() / "models"
    installed = root.is_dir()  # expose interrupted removals so users can retry uninstall
    return {"id": "ocr", "name": "离线 PDF OCR", "installed": installed,
            "enabled": installed and bool(value.get("enabled")),
            "version": value.get("version", ""),
            "sizeBytes": sum(p.stat().st_size for p in root.rglob("*") if p.is_file()) if installed else 0}


def install(package_path):
    if status()["installed"]:
        raise ValueError("OCR plugin is already installed; uninstall before replacing it")
    root = plugin_root()
    root.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="install-", dir=root) as folder:
        stage = Path(folder)
        with zipfile.ZipFile(package_path) as archive:
            entries = archive.infolist()
            if len(entries) > 64 or sum(item.file_size for item in entries) > 256 * 1024 * 1024:
                raise ValueError("OCR plugin package exceeds resource limits")
            seen = set()
            for item in entries:
                name = item.filename
                if name in seen or name not in {"manifest.json", "det.onnx", "rec.onnx", "cls.onnx", "dict.txt", "LICENSE", "NOTICE"}:
                    raise ValueError("Unexpected or duplicate OCR package entry: " + name)
                seen.add(name)
                if (item.external_attr >> 16) & 0o170000 == 0o120000:
                    raise ValueError("OCR package links are not allowed")
                with archive.open(item) as source, (stage / name).open("wb") as target:
                    shutil.copyfileobj(source, target, length=1024*1024)
        resource = verify_resource_directory(stage)
        target = root / "models"
        if target.exists():
            raise ValueError("OCR model directory already exists; uninstall the previous resources")
        stage.rename(target)
        save_state({"installed": True, "enabled": True, "version": resource["version"]})
    from .ocr import reset_ocr_engine
    reset_ocr_engine()
    return status()


def set_enabled(enabled):
    if not isinstance(enabled, bool):
        raise ValueError("enabled must be boolean")
    value = state() or {}
    if not status()["installed"]:
        raise ValueError("OCR plugin is not installed")
    if enabled:
        verify_resource_directory(plugin_root() / "models")
    value["installed"] = True
    value["enabled"] = enabled
    save_state(value)
    from .ocr import reset_ocr_engine
    reset_ocr_engine()
    return status()


def uninstall():
    from .ocr import reset_ocr_engine
    reset_ocr_engine()
    # Persist disabled first: even an interrupted removal must not reactivate OCR.
    save_state({"installed": False, "enabled": False})
    root = plugin_root()
    target = root / "models"
    if target.is_symlink() or target.resolve().parent != root:
        raise ValueError("Unsafe OCR resource deletion target")
    if target.exists():
        shutil.rmtree(target)
    return status()
