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
    from .ocr_plugin import active_model_root
    try:
        configured = active_model_root() or None
    except RuntimeError as error:
        raise OfflineOCRError(str(error)) from error
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

    The pinned RapidOCR 3.9.2 API requires enum values for model/version
    parameters. All model and dictionary paths are explicitly local and
    verified before constructing the engine.
    """

    root = _existing_model_root()
    resource = verify_resource_directory(root)
    defaults = resource["models"]
    det = _model_file(root, "KAH_OCR_DET_MODEL", defaults["det"])
    rec = _model_file(root, "KAH_OCR_REC_MODEL", defaults["rec"])
    cls = _model_file(root, "KAH_OCR_CLS_MODEL", defaults["cls"])
    dictionary = _model_file(root, "KAH_OCR_REC_DICT", defaults["dict"])
    try:
        from rapidocr import RapidOCR, OCRVersion, ModelType
    except ImportError as error:  # pragma: no cover - depends on optional env
        raise OfflineOCRError(
            "RapidOCR is not installed; install the worker OCR extra in the offline image"
        ) from error

    params = {
        "Global.model_root_dir": str(root),
        "Global.text_score": 0.5,
        "Global.return_word_box": False,
        "Global.log_level": "warning",
        "Det.ocr_version": OCRVersion(resource["manifest"].get("ocrVersion", "PP-OCRv4")),
        "Det.model_type": ModelType.MOBILE,
        "Rec.ocr_version": OCRVersion(resource["manifest"].get("ocrVersion", "PP-OCRv4")),
        "Rec.model_type": ModelType.MOBILE,
        "EngineConfig.onnxruntime.intra_op_num_threads": 2,
        "EngineConfig.onnxruntime.inter_op_num_threads": 1,
        "Det.model_path": det,
        "Cls.model_path": cls,
        "Rec.model_path": rec,
        "Rec.rec_keys_path": dictionary,
    }
    try:
        return RapidOCR(params=params)
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


def _box_bounds(line: dict[str, Any]) -> tuple[float, float, float, float] | None:
    """Return a robust bounding rectangle for a normalized OCR line."""

    points = line.get("box", [])
    coordinates = [
        (float(point[0]), float(point[1]))
        for point in points
        if isinstance(point, (list, tuple)) and len(point) >= 2
    ]
    if not coordinates:
        return None
    xs, ys = zip(*coordinates)
    return min(xs), min(ys), max(xs), max(ys)


def _row_major(lines: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Keep the historical y-then-x ordering when layout is uncertain."""

    return sorted(
        lines,
        key=lambda item: (
            (_box_bounds(item) or (0.0, 0.0, 0.0, 0.0))[1],
            (_box_bounds(item) or (0.0, 0.0, 0.0, 0.0))[0],
        ),
    )


def _row_bands(
    geometry: list[tuple[dict[str, Any], tuple[float, float, float, float]]],
) -> list[list[tuple[dict[str, Any], tuple[float, float, float, float]]]]:
    """Group OCR lines that share a visual baseline despite small skew."""

    if not geometry:
        return []
    heights = sorted(max(1.0, bounds[3] - bounds[1]) for _, bounds in geometry)
    median_height = heights[len(heights) // 2]
    tolerance = max(8.0, median_height * 0.7)
    bands: list[list[tuple[dict[str, Any], tuple[float, float, float, float]]]] = []
    for item in sorted(geometry, key=lambda value: (value[1][1], value[1][0])):
        if not bands:
            bands.append([item])
            continue
        previous = bands[-1]
        baseline = sum(bounds[1] for _, bounds in previous) / len(previous)
        if item[1][1] - baseline <= tolerance:
            previous.append(item)
        else:
            bands.append([item])
    return bands


def _detect_tables(
    geometry: list[tuple[dict[str, Any], tuple[float, float, float, float]]], page_width: int,
) -> list[dict[str, Any]]:
    """Recover regular OCR tables from repeated row and column coordinates.

    We require at least three consecutive rows with the same 3-8 cell
    positions.  Missing or irregular cells fail closed to ordinary text order,
    avoiding invented table relationships.
    """

    bands = _row_bands(geometry)
    candidates: list[tuple[int, list[tuple[dict[str, Any], tuple[float, float, float, float]]]]] = [
        (index, sorted(band, key=lambda value: value[1][0]))
        for index, band in enumerate(bands)
        if 3 <= len(band) <= 8
    ]
    tables: list[dict[str, Any]] = []
    cursor = 0
    while cursor < len(candidates):
        run = [candidates[cursor]]
        cursor += 1
        while cursor < len(candidates) and candidates[cursor][0] == run[-1][0] + 1:
            run.append(candidates[cursor])
            cursor += 1
        if len(run) < 3:
            continue
        column_count = len(run[0][1])
        if any(len(row) != column_count for _, row in run):
            continue
        expected = [bounds[0] for _, bounds in run[0][1]]
        tolerance = page_width * 0.1
        if any(
            any(abs(bounds[0] - expected[column]) > tolerance for column, (_, bounds) in enumerate(row))
            for _, row in run[1:]
        ):
            continue
        cells = [cell for _, row in run for cell in row]
        min_x = min(bounds[0] for _, bounds in cells)
        min_y = min(bounds[1] for _, bounds in cells)
        max_x = max(bounds[2] for _, bounds in cells)
        max_y = max(bounds[3] for _, bounds in cells)
        tables.append({
            "top": min_y,
            "lineIds": {id(line) for line, _ in cells},
            "orderedLines": [line for _, row in run for line, _ in row],
            "metadata": {
                "columnCount": column_count,
                "headers": [line["text"] for line, _ in run[0][1]],
                "rows": [[line["text"] for line, _ in row] for _, row in run[1:]],
                "box": [[round(min_x, 2), round(min_y, 2)], [round(max_x, 2), round(max_y, 2)]],
            },
        })
    return tables


def _table_aware_row_major(
    geometry: list[tuple[dict[str, Any], tuple[float, float, float, float]]], tables: list[dict[str, Any]],
) -> list[dict[str, Any]]:
    """Apply recovered table row order while retaining surrounding prose."""

    positions: dict[int, tuple[float, int]] = {}
    for table in tables:
        for index, line in enumerate(table["orderedLines"]):
            positions[id(line)] = (table["top"], index)
    return [
        line for line, _ in sorted(
            geometry,
            key=lambda value: (
                positions.get(id(value[0]), (value[1][1], 0))[0],
                0 if id(value[0]) in positions else 1,
                positions.get(id(value[0]), (0.0, 0))[1] if id(value[0]) in positions else value[1][0],
            ),
        )
    ]


def order_reading_lines(
    lines: list[dict[str, Any]], image_size: tuple[int, int] | None,
) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Order an OCR page conservatively and detect simple two-column layouts.

    RapidOCR returns detected text in geometric order.  That is correct for
    tables, but it interleaves the left and right columns of ordinary reports.
    We only switch to column-major order when exactly two well-populated,
    separated x-start clusters are present.  Any third column, malformed box,
    or spanning body line keeps the safer historical row-major order.
    """

    ordered = _row_major(lines)
    if not image_size or len(lines) < 6:
        return ordered, {"mode": "row-major", "columnCount": 1}
    page_width, page_height = image_size
    if page_width <= 0 or page_height <= 0:
        return ordered, {"mode": "row-major", "columnCount": 1}

    geometry: list[tuple[dict[str, Any], tuple[float, float, float, float]]] = []
    for line in lines:
        bounds = _box_bounds(line)
        if bounds is None:
            return ordered, {"mode": "row-major", "columnCount": 1, "reason": "missing-box"}
        geometry.append((line, bounds))

    tables = _detect_tables(geometry, page_width)
    if tables:
        return _table_aware_row_major(geometry, tables), {
            "mode": "row-major",
            "columnCount": 1,
            "tableCount": len(tables),
            "tables": [table["metadata"] for table in tables],
        }

    top_cutoff = page_height * 0.18
    bottom_cutoff = page_height * 0.84
    spanning_width = page_width * 0.45
    top = [(line, bounds) for line, bounds in geometry if bounds[1] < top_cutoff and bounds[2] - bounds[0] >= spanning_width]
    bottom = [(line, bounds) for line, bounds in geometry if bounds[1] >= bottom_cutoff]
    body = [(line, bounds) for line, bounds in geometry if (line, bounds) not in top and (line, bounds) not in bottom]
    if not body or any(bounds[2] - bounds[0] >= spanning_width for _, bounds in body):
        return ordered, {"mode": "row-major", "columnCount": 1, "tableCount": 0, "reason": "spanning-body"}

    # Cluster each line's left edge.  Four stable clusters normally signal a
    # table; a two-column prose layout has exactly two populated clusters.
    tolerance = page_width * 0.12
    clusters: list[list[tuple[dict[str, Any], tuple[float, float, float, float]]]] = []
    for item in sorted(body, key=lambda value: value[1][0]):
        if not clusters:
            clusters.append([item])
            continue
        previous = clusters[-1]
        center = sum(bounds[0] for _, bounds in previous) / len(previous)
        if item[1][0] - center <= tolerance:
            previous.append(item)
        else:
            clusters.append([item])
    populated = [cluster for cluster in clusters if len(cluster) >= 3]
    if len(populated) != 2:
        return ordered, {"mode": "row-major", "columnCount": 1, "tableCount": 0, "reason": "not-two-columns"}

    left, right = sorted(populated, key=lambda cluster: sum(bounds[0] for _, bounds in cluster) / len(cluster))
    left_start = sum(bounds[0] for _, bounds in left) / len(left)
    right_start = sum(bounds[0] for _, bounds in right) / len(right)
    if right_start - left_start < page_width * 0.25:
        return ordered, {"mode": "row-major", "columnCount": 1, "tableCount": 0, "reason": "columns-too-close"}

    divider = (left_start + right_start) / 2
    left_body = [(line, bounds) for line, bounds in body if bounds[0] < divider]
    right_body = [(line, bounds) for line, bounds in body if bounds[0] >= divider]
    if len(left_body) < 3 or len(right_body) < 3:
        return ordered, {"mode": "row-major", "columnCount": 1, "tableCount": 0, "reason": "unbalanced-columns"}

    for line, _ in left_body:
        line["column"] = 1
    for line, _ in right_body:
        line["column"] = 2
    result = [line for line, _ in sorted(top, key=lambda value: (value[1][1], value[1][0]))]
    result.extend(line for line, _ in sorted(left_body, key=lambda value: (value[1][1], value[1][0])))
    result.extend(line for line, _ in sorted(right_body, key=lambda value: (value[1][1], value[1][0])))
    result.extend(line for line, _ in sorted(bottom, key=lambda value: (value[1][1], value[1][0])))
    return result, {
        "mode": "column-major",
        "columnCount": 2,
        "tableCount": 0,
        "dividerX": round(divider, 2),
        "readingOrder": "top-left-right-bottom",
    }
