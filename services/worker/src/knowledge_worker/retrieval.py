"""Unified, versioned retrieval index.

The legacy :mod:`index` module remains available for the existing document
workflow.  This module owns the stage 3 retrieval contract: every generation
contains the exact model identity used to create it, the active pointer is
switched only after validation, and a missing local model produces an explicit
keyword-only response.  The portable JSON representation is deliberately
authoritative so a clean offline installation does not depend on LanceDB.
"""

from __future__ import annotations

import hashlib
import json
import math
import os
import re
import shutil
import threading
import time
import uuid
from pathlib import Path
from typing import Any, Iterable


SOURCE_TYPES = {"knowledge", "raw", "architecture"}
REVIEW_STATUSES = {"stable", "unreviewed", "derived", "deprecated"}
MAX_TOP_K = 100
MAX_CHUNK_CHARS = 12000
RRF_K = 60


def _now_ms() -> int:
    return int(time.time() * 1000)


def _sha256(text: str) -> str:
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def _safe_id(value: str) -> str:
    value = str(value or "").strip()
    if not value or len(value) > 240 or "/" in value or "\\" in value or ".." in value:
        raise ValueError("retrieval identifiers must be non-empty and path-safe")
    return value


def _terms(value: str) -> list[str]:
    value = str(value or "").lower()
    words = re.findall(r"[^\W_]+", value, flags=re.UNICODE)
    cjk = "".join(char for char in value if "\u4e00" <= char <= "\u9fff")
    # Keep exact symbols and CJK character pairs.  Punctuation is retained in
    # symbol scoring separately, while the lexical scorer remains portable.
    pairs = [cjk[i : i + 2] for i in range(max(0, len(cjk) - 1))]
    return list(dict.fromkeys(words + pairs + [char for char in cjk]))


def estimate_tokens(text: str) -> int:
    """Conservative token estimate used when no model tokenizer is active."""

    return max(0, len(re.findall(r"\S+", text))) + sum(
        1 for char in text if "\u4e00" <= char <= "\u9fff"
    )


def _location(chunk: dict[str, Any]) -> dict[str, Any]:
    value = chunk.get("location")
    return dict(value) if isinstance(value, dict) else {}


def normalize_chunk(raw: dict[str, Any], *, library_id: str = "") -> dict[str, Any]:
    """Validate and normalize the wire chunk contract.

    A chunk can be supplied by old document import code.  Its missing fields
    are filled as an unreviewed raw source, preserving compatibility while
    making its security scope explicit.
    """

    if not isinstance(raw, dict):
        raise ValueError("retrieval chunk must be an object")
    item = dict(raw)
    item["id"] = _safe_id(item.get("id", ""))
    text = str(item.get("text", ""))
    if not text.strip():
        raise ValueError("retrieval chunk text is required")
    item["text"] = text
    item["sourceType"] = str(item.get("sourceType") or "raw")
    if item["sourceType"] not in SOURCE_TYPES:
        raise ValueError("sourceType must be knowledge, raw, or architecture")
    item["reviewStatus"] = str(item.get("reviewStatus") or ("stable" if item["sourceType"] == "knowledge" else "unreviewed"))
    if item["reviewStatus"] not in REVIEW_STATUSES:
        raise ValueError("invalid retrieval reviewStatus")
    item["libraryId"] = str(item.get("libraryId") or library_id)
    if not item["libraryId"]:
        raise ValueError("retrieval chunk libraryId is required")
    item["projectId"] = str(item.get("projectId") or "")
    if item["sourceType"] == "architecture" and not item["projectId"]:
        raise ValueError("architecture chunks require projectId")
    item["sourceURI"] = str(item.get("sourceURI") or item.get("uri") or "")
    item["title"] = str(item.get("title") or "")
    item["filePath"] = str(item.get("filePath") or "")
    item["lineStart"] = max(0, int(item.get("lineStart") or 0))
    item["lineEnd"] = max(item["lineStart"], int(item.get("lineEnd") or item["lineStart"]))
    item["contentHash"] = str(item.get("contentHash") or _sha256(text))
    item["symbol"] = str(item.get("symbol") or "")
    item["language"] = str(item.get("language") or "")
    item["permissionScope"] = str(item.get("permissionScope") or ("project" if item["sourceType"] == "architecture" else "library"))
    item["documentId"] = str(item.get("documentId") or "")
    item["tokenCount"] = max(0, int(item.get("tokenCount") or estimate_tokens(text)))
    item["tokenLimit"] = max(0, int(item.get("tokenLimit") or 0))
    item["truncated"] = bool(item.get("truncated", False))
    item["location"] = _location(item)
    return item


def chunk_text(
    text: str,
    *,
    base: dict[str, Any],
    max_chars: int = MAX_CHUNK_CHARS,
    code: bool = False,
) -> list[dict[str, Any]]:
    """Split paragraphs or source blocks without silently truncating input."""

    text = str(text or "")
    if not text:
        return []
    max_chars = max(256, int(max_chars))
    if code:
        # A top-level declaration is a useful boundary for the four supported
        # source languages.  The line fallback still preserves every byte.
        boundaries = re.compile(r"(?m)^(?=(?:export\s+)?(?:async\s+)?(?:function|class|interface|type|func|def)\b|^namespace\b)")
        pieces = [piece for piece in boundaries.split(text) if piece.strip()]
    else:
        # Keep the separator at the start of the next piece so concatenating
        # all emitted chunks reconstructs the exact source bytes.
        pieces = [piece for piece in re.split(r"(?=\n\s*\n)", text) if piece]
    if not pieces:
        pieces = [text]
    result: list[dict[str, Any]] = []
    line_cursor = int(base.get("lineStart") or 1)
    ordinal = 0
    for piece in pieces:
        start = 0
        while start < len(piece):
            part = piece[start : start + max_chars]
            if not part:
                break
            line_start = line_cursor + piece[:start].count("\n")
            line_end = line_start + max(0, part.count("\n"))
            item = dict(base)
            item["text"] = part
            item["lineStart"] = line_start
            item["lineEnd"] = line_end
            item["id"] = str(base.get("id") or _sha256(part)) + (f"-part-{ordinal}" if len(pieces) > 1 or start else "")
            item["tokenCount"] = estimate_tokens(part)
            item["tokenLimit"] = 0
            item["truncated"] = False
            item["contentHash"] = _sha256(part)
            result.append(normalize_chunk(item, library_id=str(base.get("libraryId") or "")))
            ordinal += 1
            start += len(part)
        # A following piece starts after the newline characters already
        # present in this piece.  Do not add an extra line: that shifts the
        # location of every paragraph after the first one.
        line_cursor += piece.count("\n")
    return result


class UnifiedRetrievalIndex:
    def __init__(self, data_root: str | os.PathLike[str], embeddings: Any):
        self.root = Path(data_root) / "retrieval_indexes"
        self.root.mkdir(parents=True, exist_ok=True)
        self.embeddings = embeddings
        self.lock = threading.RLock()
        self._cache: dict[str, list[dict[str, Any]]] = {}
        self._recover_builds()

    def _recover_builds(self) -> None:
        """Keep the previous pointer active after a worker restart."""

        for root in self.root.iterdir() if self.root.exists() else []:
            if not root.is_dir():
                continue
            for building in root.glob("*.building"):
                generation = building.name.removesuffix(".building")
                try:
                    metadata = json.loads((building / "metadata.json").read_text(encoding="utf-8"))
                    identity = {key: metadata.get(key, "") for key in ("semanticAvailable", "modelId", "modelVersion", "configHash", "dimension")}
                except (OSError, ValueError):
                    identity = {"semanticAvailable": False}
                self._write_progress(root.name, {"libraryId": root.name, "generation": generation, "status": "failed", "completed": 0, "total": 0, "error": "retrieval rebuild interrupted by worker restart", **identity})
                shutil.rmtree(building, ignore_errors=True)

    def _library_root(self, library_id: str) -> Path:
        return self.root / _safe_id(library_id)

    def _active_meta(self, library_id: str) -> dict[str, Any]:
        root = self._library_root(library_id)
        pointer = root / "active.json"
        if not pointer.exists():
            return {"status": "missing", "generation": "", "semanticAvailable": False, "degradedReason": "No unified retrieval generation is available."}
        try:
            value = json.loads(pointer.read_text(encoding="utf-8"))
            return value if isinstance(value, dict) else {"status": "invalid", "generation": ""}
        except (OSError, ValueError):
            return {"status": "invalid", "generation": "", "semanticAvailable": False, "degradedReason": "The active retrieval generation pointer is invalid."}

    def _generation_dir(self, library_id: str, generation: str) -> Path:
        return self._library_root(library_id) / _safe_id(generation)

    def _load(self, library_id: str) -> list[dict[str, Any]]:
        if library_id in self._cache:
            return self._cache[library_id]
        meta = self._active_meta(library_id)
        generation = str(meta.get("generation", ""))
        path = self._generation_dir(library_id, generation) / "chunks.json" if generation else None
        values: list[dict[str, Any]] = []
        if path and path.exists():
            try:
                raw = json.loads(path.read_text(encoding="utf-8"))
                if isinstance(raw, list):
                    values = [normalize_chunk(item) for item in raw if isinstance(item, dict)]
            except (OSError, ValueError):
                values = []
        self._cache[library_id] = values
        return values

    def active_model_in_use(self, model_id: str) -> bool:
        """Return true when an active or building generation references model_id."""

        model_id = str(model_id or "")
        if not model_id:
            return False
        for root in self.root.iterdir() if self.root.exists() else []:
            if not root.is_dir():
                continue
            meta = self._active_meta(root.name)
            if meta.get("modelId") == model_id:
                return True
            for path in root.glob("*.building/metadata.json"):
                try:
                    if json.loads(path.read_text(encoding="utf-8")).get("modelId") == model_id:
                        return True
                except (OSError, ValueError):
                    continue
        return False

    @staticmethod
    def _validate_model_meta(meta: dict[str, Any], vectors: Iterable[list[float]]) -> None:
        if not meta.get("semanticAvailable"):
            return
        required = ("modelId", "modelVersion", "configHash", "dimension")
        if any(not meta.get(key) for key in required):
            raise ValueError("semantic generation is missing model identity")
        dimension = int(meta["dimension"])
        if dimension < 1:
            raise ValueError("semantic generation dimension must be positive")
        for vector in vectors:
            if len(vector) != dimension:
                raise ValueError("embedding dimension does not match index generation")

    def _semantic_context(self) -> tuple[Any, dict[str, Any]]:
        engine, meta = self.embeddings.active()
        return engine, {
            "semanticAvailable": bool(engine and meta.get("semanticAvailable")),
            "modelId": str(meta.get("modelId", "")),
            "modelVersion": str(meta.get("modelVersion", meta.get("version", ""))),
            "configHash": str(meta.get("configHash", meta.get("tokenizerConfigHash", ""))),
            "dimension": int(meta.get("dimension", 0) or 0),
            "degradedReason": str(meta.get("degradedReason", "")),
        }

    def _progress_path(self, library_id: str) -> Path:
        root = self._library_root(library_id)
        root.mkdir(parents=True, exist_ok=True)
        return root / "progress.json"

    def _write_progress(self, library_id: str, value: dict[str, Any]) -> None:
        path = self._progress_path(library_id)
        temporary = path.with_suffix(".tmp")
        temporary.write_text(json.dumps(value, ensure_ascii=False), encoding="utf-8")
        os.replace(temporary, path)

    def cancel(self, library_id: str) -> dict[str, Any]:
        path = self._library_root(library_id) / "cancel.request"
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(str(_now_ms()), encoding="ascii")
        return {"libraryId": library_id, "status": "cancellation_requested"}

    def progress(self, library_id: str) -> dict[str, Any]:
        path = self._progress_path(library_id)
        if not path.exists():
            return {"libraryId": library_id, "status": "idle", "completed": 0, "total": 0}
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {"libraryId": library_id, "status": "unknown", "completed": 0, "total": 0}

    def rebuild(self, library_id: str, chunks: list[dict[str, Any]], *, model_id: str = "", complete: bool = False) -> dict[str, Any]:
        library_id = _safe_id(library_id)
        normalized = [normalize_chunk(item, library_id=library_id) for item in chunks]
        root = self._library_root(library_id)
        root.mkdir(parents=True, exist_ok=True)
        generation = f"g-{_now_ms()}-{uuid.uuid4().hex[:12]}"
        building = root / f"{generation}.building"
        cancel_path = root / "cancel.request"
        if cancel_path.exists():
            cancel_path.unlink(missing_ok=True)
        engine, semantic = self._semantic_context()
        if model_id and semantic.get("modelId") != model_id:
            raise ValueError("requested model does not match the enabled local embedding model")
        meta = {
            "schema": "kah.retrieval.generation.v1",
            "generation": generation,
            "libraryId": library_id,
            "createdAt": time.time(),
            **semantic,
            "status": "building",
            "chunkCount": len(normalized),
            "corpusComplete": bool(complete),
            "sourceTypes": sorted({item["sourceType"] for item in normalized}),
        }
        self._write_progress(library_id, {"libraryId": library_id, "generation": generation, "status": "running", "completed": 0, "total": len(normalized)})
        try:
            building.mkdir(parents=True, exist_ok=False)
            # Make an in-flight model identity visible to model management
            # before the first potentially expensive embedding call.
            (building / "metadata.json").write_text(json.dumps(meta, ensure_ascii=False, sort_keys=True), encoding="utf-8")
            vectors: list[list[float]] = []
            if engine is not None and semantic.get("semanticAvailable") and normalized:
                for start in range(0, len(normalized), 64):
                    if cancel_path.exists():
                        raise RuntimeError("retrieval rebuild cancelled")
                    batch = normalized[start : start + 64]
                    result = engine.embed([item["text"] for item in batch], is_query=False)
                    embedded = result.get("vectors", [])
                    if len(embedded) != len(batch):
                        raise ValueError("embedding result count does not match chunk count")
                    for item, vector, token_count, truncated in zip(batch, embedded, result.get("tokenCount", []), result.get("truncated", [])):
                        item["embedding"] = [float(value) for value in vector]
                        item["tokenCount"] = int(token_count)
                        item["truncated"] = bool(truncated)
                        item["tokenLimit"] = int(getattr(engine, "manifest", {}).get("maxTokens", 0) or 0)
                        vectors.append(item["embedding"])
                    self._write_progress(library_id, {"libraryId": library_id, "generation": generation, "status": "running", "completed": min(start + len(batch), len(normalized)), "total": len(normalized)})
            self._validate_model_meta(meta, vectors)
            meta["status"] = "ready"
            (building / "chunks.json").write_text(json.dumps(normalized, ensure_ascii=False), encoding="utf-8")
            (building / "metadata.json").write_text(json.dumps(meta, ensure_ascii=False, sort_keys=True), encoding="utf-8")
            # A generation is visible only once both data files exist and can
            # be decoded.  This is the validation point before pointer swap.
            loaded = json.loads((building / "chunks.json").read_text(encoding="utf-8"))
            if not isinstance(loaded, list) or len(loaded) != len(normalized):
                raise ValueError("generation validation failed")
            target = root / generation
            os.replace(building, target)
            pointer = root / "active.tmp"
            pointer.write_text(json.dumps(meta, ensure_ascii=False, sort_keys=True), encoding="utf-8")
            os.replace(pointer, root / "active.json")
            with self.lock:
                self._cache[library_id] = normalized
            result = {"libraryId": library_id, "generation": generation, "count": len(normalized), "needsRebuild": not bool(complete), **meta}
            self._write_progress(library_id, {"libraryId": library_id, "generation": generation, "status": "completed", "completed": len(normalized), "total": len(normalized), **semantic})
            return result
        except Exception as exc:
            shutil.rmtree(building, ignore_errors=True)
            self._write_progress(library_id, {"libraryId": library_id, "generation": generation, "status": "cancelled" if "cancel" in str(exc).lower() else "failed", "completed": 0, "total": len(normalized), "error": str(exc), **semantic})
            raise
        finally:
            cancel_path.unlink(missing_ok=True)

    def upsert(self, library_id: str, document_id: str, chunks: list[dict[str, Any]]) -> dict[str, Any]:
        """Update one source while retaining other unified sources.

        The active generation is replaced atomically.  This path is used for
        raw document updates; full rebuild remains the model-switch path.
        """

        library_id = _safe_id(library_id)
        document_id = str(document_id or "")
        with self.lock:
            current = [item for item in self._load(library_id) if item.get("documentId") != document_id]
            incoming = [normalize_chunk(item, library_id=library_id) for item in chunks]
            current.extend(incoming)
            complete = bool(self._active_meta(library_id).get("corpusComplete", False))
            return self.rebuild(library_id, current, complete=complete)

    def delete(self, library_id: str, document_id: str) -> dict[str, Any]:
        library_id = _safe_id(library_id)
        with self.lock:
            current = [item for item in self._load(library_id) if item.get("documentId") != document_id]
            complete = bool(self._active_meta(library_id).get("corpusComplete", False))
            return self.rebuild(library_id, current, complete=complete)

    @staticmethod
    def _allowed(item: dict[str, Any], filters: dict[str, Any]) -> bool:
        source_type = str(filters.get("sourceType") or filters.get("sourceTypeFilter") or "all")
        if source_type != "all" and item.get("sourceType") != source_type:
            return False
        source_types = filters.get("sourceTypes")
        if isinstance(source_types, list) and source_types and item.get("sourceType") not in source_types:
            return False
        library_ids = filters.get("libraryIds")
        if isinstance(library_ids, list) and library_ids and item.get("libraryId") not in library_ids:
            return False
        allowed_libraries = filters.get("allowedLibraryIds")
        if isinstance(allowed_libraries, list) and item.get("libraryId") not in allowed_libraries:
            return False
        project_ids = filters.get("projectIds")
        if isinstance(project_ids, list) and project_ids and item.get("projectId") not in project_ids:
            return False
        allowed_projects = filters.get("allowedProjectIds")
        if item.get("sourceType") == "architecture" and (not isinstance(allowed_projects, list) or item.get("projectId") not in allowed_projects):
            return False
        if item.get("sourceType") == "raw" and not bool(filters.get("allowRaw", False)):
            return False
        statuses = filters.get("reviewStatuses")
        if isinstance(statuses, list) and statuses and item.get("reviewStatus") not in statuses:
            return False
        # Formal knowledge retrieval has a stable-only default even when the
        # caller omits an explicit status filter.
        if item.get("sourceType") == "knowledge" and item.get("reviewStatus") != "stable":
            return False
        return True

    @staticmethod
    def _symbol_match(query: str, item: dict[str, Any]) -> float:
        symbol = str(item.get("symbol", ""))
        if not symbol:
            return 0.0
        query = str(query or "").strip().lower()
        return 1.0 if query and (query == symbol.lower() or query in symbol.lower()) else 0.0

    def search(self, query: str, filters: dict[str, Any] | None = None, top_k: int = 10, retrieval_mode: str = "hybrid") -> dict[str, Any]:
        filters = dict(filters or {})
        top_k = max(1, min(int(top_k or 10), MAX_TOP_K))
        if "allowedLibraryIds" not in filters:
            raise ValueError("retrieval authorization scope is required")
        libraries = filters.get("libraryIds") or filters.get("allowedLibraryIds") or []
        if not isinstance(libraries, list):
            raise ValueError("libraryIds must be a list")
        if not libraries:
            raise ValueError("retrieval authorization scope must not be empty")
        with self.lock:
            generation_by_library = {str(library_id): self._active_meta(str(library_id)) for library_id in libraries}
            identities = {
                (
                    bool(meta.get("semanticAvailable")),
                    str(meta.get("modelId", "")),
                    str(meta.get("modelVersion", "")),
                    str(meta.get("configHash", "")),
                    int(meta.get("dimension", 0) or 0),
                )
                for meta in generation_by_library.values()
            }
            generation_meta = generation_by_library[str(libraries[0])] if len(identities) == 1 and libraries else {}
            candidates = [item for library_id in libraries for item in self._load(str(library_id)) if self._allowed(item, filters)]
        terms = _terms(query)
        lexical: list[dict[str, Any]] = []
        for item in candidates:
            lower = item["text"].lower()
            matches = sum(1 for term in terms if term and term in lower)
            exact_symbol = self._symbol_match(query, item)
            score = matches / max(1, len(terms)) + exact_symbol * 2.0
            if score > 0:
                value = dict(item)
                value["lexical"] = score
                value["symbolMatch"] = exact_symbol > 0
                lexical.append(value)
        lexical.sort(key=lambda item: (item["lexical"], item.get("symbolMatch", False), item.get("id", "")), reverse=True)

        engine, active_model = self._semantic_context()
        semantic_available = bool(engine and active_model.get("semanticAvailable"))
        semantic: list[dict[str, Any]] = []
        degraded_reason = ""
        if retrieval_mode in {"hybrid", "vector"} and semantic_available and candidates:
            generation_model = str(generation_meta.get("modelId", ""))
            generation_version = str(generation_meta.get("modelVersion", ""))
            generation_config = str(generation_meta.get("configHash", ""))
            generation_dimension = int(generation_meta.get("dimension", 0) or 0)
            if len(identities) != 1:
                semantic_available = False
                degraded_reason = "Selected libraries use different index model identities; keyword-only retrieval is active until compatible generations are selected."
            elif not generation_model:
                semantic_available = False
                degraded_reason = "The active generation was built without a local embedding model; keyword-only retrieval is active."
            elif (generation_model, generation_version, generation_config, generation_dimension) != (
                active_model.get("modelId", ""), active_model.get("modelVersion", ""), active_model.get("configHash", ""), active_model.get("dimension", 0),
            ):
                semantic_available = False
                degraded_reason = "The active embedding model does not match the index generation; rebuild is required."
            else:
                try:
                    query_vector = engine.embed([query], is_query=True)["vectors"][0]
                    for item in candidates:
                        vector = item.get("embedding")
                        if not isinstance(vector, list) or len(vector) != generation_dimension:
                            continue
                        value = dict(item)
                        value["vector"] = max(0.0, min(1.0, sum(float(left) * float(right) for left, right in zip(query_vector, vector))))
                        if value["vector"] > 0:
                            semantic.append(value)
                    semantic.sort(key=lambda item: (item["vector"], item.get("id", "")), reverse=True)
                except Exception as exc:
                    semantic_available = False
                    degraded_reason = "Embedding query failed; keyword-only retrieval is active: " + str(exc)
        if not semantic_available and retrieval_mode in {"hybrid", "vector"}:
            if not degraded_reason:
                degraded_reason = str(active_model.get("degradedReason") or "Local embedding is unavailable; keyword-only retrieval is active.")
            if retrieval_mode == "vector":
                retrieval_mode = "lexical"

        lexical_rank = {item["id"]: index + 1 for index, item in enumerate(lexical)}
        semantic_rank = {item["id"]: index + 1 for index, item in enumerate(semantic)}
        by_id: dict[str, dict[str, Any]] = {}
        for item in lexical + semantic:
            by_id.setdefault(item["id"], {}).update(item)
        for item in by_id.values():
            item.setdefault("lexical", 0.0)
            item.setdefault("vector", 0.0)
            item["fusion"] = 1 / (RRF_K + lexical_rank[item["id"]]) if item["id"] in lexical_rank else 0.0
            item["fusion"] += 1 / (RRF_K + semantic_rank[item["id"]]) if item["id"] in semantic_rank else 0.0
            item["final"] = item["lexical"] if retrieval_mode == "lexical" else item["vector"] if retrieval_mode == "vector" and semantic_available else item["fusion"]
        results = list(by_id.values())
        results.sort(key=lambda item: (item["final"], item.get("symbolMatch", False), item.get("id", "")), reverse=True)
        # Recheck all scope filters after ranking.  The check is intentionally
        # repeated here so a future native index cannot bypass the policy.
        results = [item for item in results if self._allowed(item, filters)][:top_k]
        corpus_complete = bool(generation_meta.get("corpusComplete", False))
        if not corpus_complete and not degraded_reason:
            degraded_reason = "The active generation contains an incremental source subset; a full unified rebuild is required."
        return {
            "results": results,
            "source": "unified",
            "searchBackend": "portable-json",
            "generation": generation_meta.get("generation", ""),
            "semanticAvailable": semantic_available,
            "keywordOnly": not semantic_available,
            "degraded": (not semantic_available) or (not corpus_complete),
            "degradedReason": degraded_reason if ((not semantic_available) or (not corpus_complete)) else "",
            "modelId": active_model.get("modelId", "") if semantic_available else generation_meta.get("modelId", ""),
            "modelVersion": active_model.get("modelVersion", "") if semantic_available else generation_meta.get("modelVersion", ""),
            "configHash": active_model.get("configHash", "") if semantic_available else generation_meta.get("configHash", ""),
            "dimension": active_model.get("dimension", 0) if semantic_available else generation_meta.get("dimension", 0),
            "corpusComplete": corpus_complete,
            "needsRebuild": not corpus_complete,
            "retrievalMode": retrieval_mode,
        }

    def status(self, library_id: str) -> dict[str, Any]:
        return {**self._active_meta(library_id), "libraryId": library_id, "progress": self.progress(library_id)}
