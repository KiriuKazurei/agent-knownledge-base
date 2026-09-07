"""Offline PDF OCR adapter.

The parser owns the PDF page lifecycle while this module owns the optional
OCR engine.  Keeping the boundary small lets tests inject a deterministic
engine and keeps the normal worker usable when OCR resources are not installed.
"""

from __future__ import annotations

import os
import json
from pathlib import Path
from typing import Any

from .ocr_resources import verify_resource_directory


class OfflineOCRError(UnicodeError):
    """A PDF page could not be OCR'ed using the local offline resources."""


def _path_from_env(name: str) -> str | None:
    value = os.environ.get(name, "").strip()
    return value or None


def _existing_model_root() -> Path:
    configured = _path_from_env("KAH_OCR_MODEL_DIR")
    if configured is None:
        raise OfflineOCRError(
            "offline OCR models are not installed; set KAH_OCR_MODEL_DIR "
            "to a verified local RapidOCR model directory"
        )
    root = Path(configured).expanduser().resolve()
    try:
        verify_resource_directory(root)
    except Exception as error:
        raise OfflineOCRError(str(error)) from error
    return root


def _model_file(root: Path, env_name: str, default_name: str) -> str:
    value = _path_from_env(env_name)
    if value:
        path = Path(value).expanduser()
        if not path.is_absolute():
            path = root / path
    else:
        path = root / default_name
    if path.resolve() != (root / default_name).resolve():
        raise OfflineOCRError(f"offline OCR override is not the manifest-verified file: {env_name}")
    if not path.is_file():
        raise OfflineOCRError(f"offline OCR model file does not exist: {path}")
    return str(path)


def _rapidocr_engine() -> Any:
    """Build RapidOCR with explicit local model files only.

    RapidOCR has changed constructor details between minor releases.  The
    current API accepts ``params`` and older compatible builds accept the same
    parameter names.  No constructor is attempted until all local files have
    been checked, preventing an accidental model download in offline mode.
    """

    root = _existing_model_root()
    resource = verify_resource_directory(root)
    defaults = resource["models"]
    det = _model_file(root, "KAH_OCR_DET_MODEL", defaults["det"])
    rec = _model_file(root, "KAH_OCR_REC_MODEL", defaults["rec"])
    cls = _model_file(root, "KAH_OCR_CLS_MODEL", defaults["cls"])
    dictionary = _model_file(root, "KAH_OCR_REC_DICT", defaults["dict"])
    try:
        from rapidocr import RapidOCR
    except ImportError as error:  # pragma: no cover - depends on optional env
        raise OfflineOCRError(
            "RapidOCR is not installed; install the worker OCR extra in the offline image"
        ) from error

    params = {
        "Global.model_root_dir": str(root),
        "Global.text_score": 0.5,
        "Global.return_word_box": True,
        "Det.model_path": det,
        "Cls.model_path": cls,
        "Rec.model_path": rec,
        "Rec.rec_keys_path": dictionary,
    }
    try:
        return RapidOCR(params=params)
    except TypeError:
        # A few older releases use a positional parameter dictionary.
        try:
            return RapidOCR(params)
        except Exception as error:  # pragma: no cover - optional env
            raise OfflineOCRError(f"failed to initialize offline RapidOCR: {error}") from error
    except Exception as error:  # pragma: no cover - optional env
        raise OfflineOCRError(f"failed to initialize offline RapidOCR: {error}") from error


_ENGINE: Any | None = None
_ENGINE_KEY: str | None = None


def get_ocr_engine() -> Any:
    """Return the process-local OCR engine, loading models at most once."""

    global _ENGINE, _ENGINE_KEY
    root = _existing_model_root()
    resource = verify_resource_directory(root)
    key = json.dumps([str(root), resource["manifest"], [os.environ.get(name, "") for name in
                      ("KAH_OCR_DET_MODEL", "KAH_OCR_REC_MODEL", "KAH_OCR_CLS_MODEL", "KAH_OCR_REC_DICT")]], sort_keys=True)
    if _ENGINE is None or _ENGINE_KEY != key:
        _ENGINE = None
        _ENGINE_KEY = None
        _ENGINE = _rapidocr_engine()
        _ENGINE_KEY = key
    return _ENGINE


def reset_ocr_engine() -> None:
    """Release the cached engine; intended for tests and controlled shutdown."""

    global _ENGINE, _ENGINE_KEY
    _ENGINE = None
    _ENGINE_KEY = None


def normalize_result(result: Any) -> list[dict[str, Any]]:
    """Normalize RapidOCR v2/v3 output to text, score and quadrilateral boxes."""

    if result is None:
        return []
    # RapidOCR v3 returns an object with boxes/txts/scores.  Older releases
    # return ``(boxes, txts, scores)``.
    if hasattr(result, "txts"):
        boxes = getattr(result, "boxes", None)
        texts = getattr(result, "txts", None)
        scores = getattr(result, "scores", None)
        boxes = [] if boxes is None else boxes
        texts = [] if texts is None else texts
        scores = [] if scores is None else scores
    elif isinstance(result, (tuple, list)) and len(result) >= 3:
        boxes = [] if result[0] is None else result[0]
        texts = [] if result[1] is None else result[1]
        scores = [] if result[2] is None else result[2]
    else:
        return []
    normalized: list[dict[str, Any]] = []
    for index, text in enumerate(texts):
        value = str(text or "").strip()
        if not value:
            continue
        score = float(scores[index]) if index < len(scores) else 0.0
        box = boxes[index].tolist() if index < len(boxes) and hasattr(boxes[index], "tolist") else (boxes[index] if index < len(boxes) else [])
        normalized.append({"text": value, "score": score, "box": box})
    return normalized


def recognize(engine: Any, image: Any) -> list[dict[str, Any]]:
    """Run an injected or RapidOCR-compatible engine on one rendered page."""

    try:
        return normalize_result(engine(image))
    except OfflineOCRError:
        raise
    except Exception as error:
        raise OfflineOCRError(f"offline OCR failed: {error}") from error
