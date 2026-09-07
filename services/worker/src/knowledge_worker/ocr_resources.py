"""Validation for the local, offline OCR resource package."""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
from typing import Any


MANIFEST_NAME = "manifest.json"
MANIFEST_SCHEMA = "kah-ocr-resource/v1"
MODEL_KEYS = ("det", "rec", "cls", "dict")
DEFAULT_MODEL_FILES = {
    "det": "det.onnx",
    "rec": "rec.onnx",
    "cls": "cls.onnx",
    "dict": "dict.txt",
}


class OCRResourceError(RuntimeError):
    """The local OCR resource package is absent or fails verification."""


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _safe_relative_path(root: Path, value: Any) -> Path:
    if not isinstance(value, str) or not value.strip():
        raise OCRResourceError("OCR manifest contains an empty file path")
    relative = Path(value)
    if relative.is_absolute() or ".." in relative.parts:
        raise OCRResourceError(f"OCR manifest contains an unsafe file path: {value}")
    resolved = (root / relative).resolve()
    if resolved != root and root not in resolved.parents:
        raise OCRResourceError(f"OCR manifest file escapes the model directory: {value}")
    return resolved


def verify_resource_directory(root_value: str | os.PathLike[str]) -> dict[str, Any]:
    """Verify a manifest and every declared file, returning resolved model paths."""

    root = Path(root_value).expanduser().resolve()
    if not root.is_dir():
        raise OCRResourceError(f"OCR model directory does not exist: {root}")
    manifest_path = root / MANIFEST_NAME
    if not manifest_path.is_file():
        raise OCRResourceError(f"OCR resource manifest is missing: {manifest_path}")
    try:
        manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise OCRResourceError(f"OCR resource manifest cannot be read: {error}") from error
    if not isinstance(manifest, dict) or manifest.get("schema") != MANIFEST_SCHEMA:
        raise OCRResourceError(f"OCR resource manifest schema must be {MANIFEST_SCHEMA}")
    declared_files = manifest.get("files")
    if not isinstance(declared_files, list) or not declared_files:
        raise OCRResourceError("OCR resource manifest must declare files")

    checked: dict[str, Path] = {}
    for item in declared_files:
        if not isinstance(item, dict):
            raise OCRResourceError("OCR resource manifest file entries must be objects")
        relative = item.get("name")
        path = _safe_relative_path(root, relative)
        expected = str(item.get("sha256", "")).lower()
        if len(expected) != 64 or any(character not in "0123456789abcdef" for character in expected):
            raise OCRResourceError(f"OCR resource has invalid SHA-256 for {relative}")
        if not path.is_file():
            raise OCRResourceError(f"OCR resource file is missing: {path}")
        actual = _sha256(path)
        if actual != expected:
            raise OCRResourceError(f"OCR resource hash mismatch for {relative}")
        checked[str(relative)] = path

    configured = manifest.get("models")
    if configured is None:
        configured = DEFAULT_MODEL_FILES
    if not isinstance(configured, dict):
        raise OCRResourceError("OCR resource manifest models must be an object")
    model_paths: dict[str, str] = {}
    for key in MODEL_KEYS:
        relative = configured.get(key, DEFAULT_MODEL_FILES[key])
        path = _safe_relative_path(root, relative)
        if str(relative) not in checked:
            raise OCRResourceError(f"OCR model {key} is not declared in manifest: {relative}")
        model_paths[key] = str(path)
    return {
        "schema": manifest["schema"],
        "version": str(manifest.get("version", "unknown")),
        "root": str(root),
        "manifest": manifest,
        "models": model_paths,
        "fileCount": len(checked),
    }


def health_report() -> dict[str, Any]:
    """Return a safe, non-throwing resource health response for RPC callers."""

    configured = os.environ.get("KAH_OCR_MODEL_DIR", "").strip()
    if not configured:
        return {"available": False, "code": "model_dir_missing", "message": "KAH_OCR_MODEL_DIR is not configured"}
    try:
        resource = verify_resource_directory(configured)
    except OCRResourceError as error:
        return {"available": False, "code": "resource_invalid", "message": str(error)}
    # Resource integrity and a successful engine initialization are different gates.
    from .ocr import get_ocr_engine
    try:
        get_ocr_engine()
    except Exception as error:
        return {"available": False, "resourcesVerified": True, "engineReady": False,
                "code": "engine_unavailable", "message": str(error), "version": resource["version"]}
    return {
        "available": True,
        "resourcesVerified": True,
        "engineReady": True,
        "code": "ready",
        "schema": resource["schema"],
        "version": resource["version"],
        "fileCount": resource["fileCount"],
    }
