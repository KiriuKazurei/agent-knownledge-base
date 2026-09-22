import json
import hashlib
import math
import os
import re
import threading
import time
from pathlib import Path

from .embedding import EmbeddingRegistry


class HybridIndex:
    _DEFAULT_DEGRADED_REASON = "No local embedding model is enabled; keyword retrieval is active."

    def __init__(self, data_root):
        self.root = Path(data_root) / "indexes"
        self.root.mkdir(parents=True, exist_ok=True)
        self.lock = threading.RLock()
        self._cache = {}
        self.embeddings = EmbeddingRegistry(data_root)
        try:
            import lancedb
            self.lancedb = lancedb
        except ImportError:
            self.lancedb = None

    @property
    def backend(self):
        return "lancedb+portable" if self.lancedb is not None else "portable-json"

    @staticmethod
    def _features(value):
        """Build language-neutral features for the portable vector fallback.

        The application can later replace this with provider embeddings without
        changing the index/search contract.  Word features keep Latin/code
        queries useful while character n-grams make Chinese text searchable
        without requiring whitespace tokenisation.
        """
        value = value.lower()
        words = re.findall(r"[^\W_]+", value, flags=re.UNICODE)
        features = list(words)
        cjk = "".join(char for char in value if "\u4e00" <= char <= "\u9fff")
        features.extend(cjk[index:index + 2] for index in range(max(0, len(cjk) - 1)))
        features.extend(char for char in cjk)
        return features

    @classmethod
    def _vector(cls, value, dimensions=256):
        vector = [0.0] * dimensions
        for feature in cls._features(value):
            digest = hashlib.blake2b(feature.encode("utf-8"), digest_size=8).digest()
            index = int.from_bytes(digest[:4], "little") % dimensions
            vector[index] += 1.0
        norm = math.sqrt(sum(item * item for item in vector))
        return [item / norm for item in vector] if norm else vector

    @staticmethod
    def _cosine(left, right):
        if not left or not right:
            return 0.0
        return max(0.0, min(1.0, sum(a * b for a, b in zip(left, right))))

    def _path(self, library_id):
        path = self.root / library_id / self._active_version(library_id)
        path.mkdir(parents=True, exist_ok=True)
        return path / "chunks.json"

    def _active_meta(self, library_id):
        pointer = self.root / library_id / "active.json"
        if pointer.exists():
            try:
                value = json.loads(pointer.read_text(encoding="utf-8"))
                if isinstance(value, dict):
                    return value
            except (ValueError, OSError):
                pass
        return {"version": self._active_version(library_id), "semanticAvailable": False,
                "degradedReason": self._DEFAULT_DEGRADED_REASON}
    def _active_version(self, library_id):
        pointer = self.root / library_id / "active.json"
        if pointer.exists():
            try:
                version = json.loads(pointer.read_text(encoding="utf-8")).get("version", "")
                if version and (self.root / library_id / version).is_dir():
                    return version
            except (ValueError, OSError):
                pass
        return "v1"

    def _load(self, library_id):
        if library_id in self._cache:
            return self._cache[library_id]
        path = self._path(library_id)
        if path.exists():
            try:
                values = json.loads(path.read_text(encoding="utf-8"))
            except (ValueError, OSError):
                values = []
        else:
            values = []
        self._cache[library_id] = values
        return values

    def _prepare_chunks(self, chunks, is_query=False):
        values = list(chunks or [])
        engine, semantic = self.embeddings.active()
        embeddings = []
        if engine is not None and values:
            try:
                embedded = engine.embed([str(item.get("text", "")) for item in values], is_query=is_query)
                embeddings = embedded.get("vectors", [])
                if len(embeddings) != len(values):
                    raise RuntimeError("embedding result count does not match chunk count")
            except Exception as exc:
                semantic = {"semanticAvailable": False, "degradedReason": "Embedding failed; keyword retrieval is active: " + str(exc)}
                engine = None
        prepared = []
        for index, chunk in enumerate(values):
            item = dict(chunk)
            # `vector` remains in the wire shape for old clients. It is a real
            # model vector only when semanticAvailable is true. The portable
            # fallback is explicitly named legacySimilarity and is never
            # reported as semantic retrieval.
            if engine is not None and index < len(embeddings):
                item["vector"] = embeddings[index]
                item["embedding"] = embeddings[index]
                item["semanticAvailable"] = True
                item["modelId"] = semantic.get("modelId", "")
                item["modelVersion"] = semantic.get("modelVersion", "")
                item["dimension"] = semantic.get("dimension", 0)
                item["tokenizerConfigHash"] = semantic.get("tokenizerConfigHash", "")
                item["configHash"] = semantic.get("configHash", semantic.get("tokenizerConfigHash", ""))
            else:
                item["vector"] = item.get("legacySimilarity") or self._vector(str(item.get("text", "")))
                item["legacySimilarity"] = item["vector"]
                item["semanticAvailable"] = False
            item.setdefault("sourceType", "document")
            item.setdefault("reviewStatus", "ready")
            item.setdefault("permissionScope", "library")
            prepared.append(item)
        return prepared, semantic

    def _retrieval_metadata(self):
        _, semantic = self.embeddings.active()
        return semantic

    def _libraries_support_semantic(self, library_ids, semantic):
        """Return whether every stored chunk has the active model identity.

        ``vector`` predates local embedding models and may contain the
        deterministic hash representation.  It is therefore not sufficient
        evidence that a LanceDB table can answer a semantic query.  Require
        the per-chunk model identity before allowing either native or portable
        vector ranking.
        """
        if not semantic.get("semanticAvailable"):
            return False
        expected = {
            "modelId": str(semantic.get("modelId", "")),
            "modelVersion": str(semantic.get("modelVersion", "")),
            "configHash": str(semantic.get("configHash", semantic.get("tokenizerConfigHash", ""))),
            "dimension": int(semantic.get("dimension", 0) or 0),
        }
        if not all(expected.values()):
            return False
        for library_id in library_ids:
            for item in self._load(library_id):
                if not item.get("semanticAvailable"):
                    return False
                if any(str(item.get(key, "")) != str(value) for key, value in expected.items() if key != "dimension"):
                    return False
                vector = item.get("embedding")
                if not isinstance(vector, list) or len(vector) != expected["dimension"]:
                    return False
        return True

    def upsert(self, library_id, document_id, chunks):
        with self.lock:
            existing = [item for item in self._load(library_id) if item.get("documentId") != document_id]
            prepared, semantic = self._prepare_chunks(chunks)
            for chunk in prepared:
                value = dict(chunk)
                value["documentId"] = document_id
                value.setdefault("location", {})
                value.setdefault("contentHash", "")
                existing.append(value)
            self._cache[library_id] = existing
            target = self._path(library_id)
            temporary = target.with_suffix(".tmp")
            temporary.write_text(json.dumps(existing, ensure_ascii=False), encoding="utf-8")
            os.replace(str(temporary), str(target))
            self._sync_lance(library_id, existing)
            return {"count": len(chunks), "backend": self.backend, **semantic}
    def rebuild(self, library_id, chunks):
        with self.lock:
            library_root = self.root / library_id
            library_root.mkdir(parents=True, exist_ok=True)
            version = "v" + str(int(time.time() * 1000))
            version_root = library_root / (version + ".tmp")
            version_root.mkdir(parents=True, exist_ok=False)
            values, semantic = self._prepare_chunks(chunks)
            index_meta = dict(semantic)
            index_meta["modelId"] = semantic.get("modelId", "")
            index_meta["modelVersion"] = semantic.get("modelVersion", "")
            index_meta["dimension"] = semantic.get("dimension", 0)
            index_meta["tokenizerConfigHash"] = semantic.get("tokenizerConfigHash", "")
            index_meta["generation"] = version
            target_root = library_root / version
            (version_root / "chunks.json").write_text(json.dumps(values, ensure_ascii=False), encoding="utf-8")
            (version_root / "metadata.json").write_text(json.dumps(index_meta, ensure_ascii=False), encoding="utf-8")
            os.replace(str(version_root), str(target_root))
            pointer = library_root / "active.tmp"
            index_meta["version"] = version
            pointer.write_text(json.dumps(index_meta, ensure_ascii=False), encoding="utf-8")
            os.replace(str(pointer), str(library_root / "active.json"))
            self._cache[library_id] = values
            self._sync_lance(library_id, values)
            return {"count": len(values), "version": version, "backend": self.backend, **semantic}

    def delete(self, library_id, document_id):
        with self.lock:
            values = [item for item in self._load(library_id) if item.get("documentId") != document_id]
            self._cache[library_id] = values
            self._path(library_id).write_text(json.dumps(values, ensure_ascii=False), encoding="utf-8")
            self._sync_lance(library_id, values)
            return {"count": len(values)}

    def _sync_lance(self, library_id, values):
        if self.lancedb is None or not values:
            return
        try:
            db = self.lancedb.connect(str(self._path(library_id).parent / "lance"))
            rows = []
            for item in values:
                row = {"id": item["id"], "document_id": item.get("documentId", ""), "text": item.get("text", ""), "location_json": json.dumps(item.get("location", {}), ensure_ascii=False), "content_hash": item.get("contentHash", ""), "source_type": item.get("sourceType", "document"), "review_status": item.get("reviewStatus", "ready"), "project_id": item.get("projectId", ""), "permission_scope": item.get("permissionScope", "library"), "semantic_available": bool(item.get("semanticAvailable")), "model_id": item.get("modelId", ""), "model_version": item.get("modelVersion", ""), "config_hash": item.get("configHash", item.get("tokenizerConfigHash", "")), "dimension": int(item.get("dimension", 0) or 0)}
                # A legacy hash vector is useful only as a named portable
                # similarity hint.  Do not persist it in LanceDB's vector
                # column, where it could later be mistaken for an embedding.
                if item.get("semanticAvailable") and isinstance(item.get("embedding"), list):
                    row["vector"] = item["embedding"]
                rows.append(row)
            table = db.create_table("chunks", data=rows, mode="overwrite")
            try:
                from lancedb.index import FTS
                # The default English tokenizer drops contiguous CJK text,
                # which would make an otherwise healthy native table return
                # no rows for Chinese keyword queries.  Character n-grams
                # retain native FTS while keeping the portable lexical path
                # available when a query language is not covered.
                table.create_index(
                    "text",
                    config=FTS(
                        base_tokenizer="ngram",
                        ngram_min_length=1,
                        ngram_max_length=3,
                        stem=False,
                        remove_stop_words=False,
                    ),
                    replace=True,
                )
            except Exception:
                pass
        except Exception:
            # The portable index is authoritative and remains usable if a native wheel fails.
            pass

    @staticmethod
    def _lance_item(row, library_id):
        try:
            location = json.loads(row.get("location_json", "{}"))
        except (TypeError, ValueError):
            location = {}
        return {
            "id": row["id"],
            "documentId": row["document_id"],
            "text": row.get("text", ""),
            "location": location,
            "contentHash": row.get("content_hash", ""),
            "libraryId": library_id,
            "sourceType": row.get("source_type", "document"),
            "reviewStatus": row.get("review_status", "ready"),
            "projectId": row.get("project_id", ""),
            "permissionScope": row.get("permission_scope", "library"),
            "semanticAvailable": bool(row.get("semantic_available", False)),
            "modelId": row.get("model_id", ""),
            "modelVersion": row.get("model_version", ""),
            "configHash": row.get("config_hash", ""),
            "dimension": int(row.get("dimension", 0) or 0),
        }

    def _search_lance(self, query, query_vector, library_ids, top_k, retrieval_mode, retrieval_meta=None, requested_mode=None):
        """Search the native LanceDB tables when every requested library has one.

        The portable JSON index remains the availability fallback, but it must
        not be reported as LanceDB search just because a table was synced.
        """
        if self.lancedb is None:
            return None
        retrieval_meta = dict(retrieval_meta or self._retrieval_metadata())
        semantic_available = bool(retrieval_meta.get("semanticAvailable"))
        requested_mode = requested_mode or retrieval_mode
        effective_mode = retrieval_mode
        if not semantic_available and effective_mode in {"vector", "hybrid"}:
            effective_mode = "lexical"
        if effective_mode not in {"lexical", "vector", "hybrid"}:
            effective_mode = "hybrid"
        fetch_limit = max(50, min(int(top_k) * 8, 500))
        lexical_candidates = []
        vector_candidates = []
        try:
            for library_id in library_ids:
                lance_root = self._path(library_id).parent / "lance"
                if not lance_root.exists():
                    return None
                db = self.lancedb.connect(str(lance_root))
                tables = db.list_tables() if hasattr(db, "list_tables") else db.table_names()
                tables = getattr(tables, "tables", tables)
                if "chunks" not in tables:
                    return None
                table = db.open_table("chunks")
                if effective_mode != "vector":
                    for row in table.search(query, query_type="fts").limit(fetch_limit).to_list():
                        item = self._lance_item(row, library_id)
                        item["lexical"] = max(0.0, float(row.get("_score", 0.0)))
                        if not semantic_available:
                            # Preserve the old deterministic hint under its
                            # explicit name without turning it into a vector
                            # score or using it for ranking.
                            item["legacySimilarity"] = self._cosine(self._vector(query), self._vector(item.get("text", "")))
                        lexical_candidates.append(item)
                # Never query LanceDB's vector column with the deterministic
                # hash vector used by old portable indexes.  A native vector
                # query is valid only when a real local model is active.
                if effective_mode != "lexical" and semantic_available and query_vector is not None:
                    for row in table.search(query_vector).limit(fetch_limit).to_list():
                        item = self._lance_item(row, library_id)
                        item["vector"] = 1.0 / (1.0 + max(0.0, float(row.get("_distance", 0.0))))
                        vector_candidates.append(item)
        except Exception:
            return None
        return self._rank_candidates(lexical_candidates, vector_candidates, top_k, effective_mode, "lancedb", retrieval_meta, requested_mode)

    def _rank_candidates(self, lexical_candidates, vector_candidates, top_k, retrieval_mode, search_backend, retrieval_meta=None, requested_mode=None):
        retrieval_meta = dict(retrieval_meta or self._retrieval_metadata())
        semantic_available = bool(retrieval_meta.get("semanticAvailable"))
        requested_mode = requested_mode or retrieval_mode
        effective_mode = retrieval_mode
        if not semantic_available and effective_mode in {"vector", "hybrid"}:
            effective_mode = "lexical"
        if not semantic_available:
            # A caller cannot accidentally reintroduce legacy hash scores by
            # passing vector candidates into the common ranking function.
            vector_candidates = []
        lexical_candidates.sort(key=lambda item: (item["lexical"], item.get("id", "")), reverse=True)
        vector_candidates.sort(key=lambda item: (item["vector"], item.get("id", "")), reverse=True)
        lexical_rank = {item["id"]: index + 1 for index, item in enumerate(lexical_candidates)}
        vector_rank = {item["id"]: index + 1 for index, item in enumerate(vector_candidates)}
        by_id = {item["id"]: dict(item) for item in lexical_candidates + vector_candidates}
        for item in by_id.values():
            item.setdefault("lexical", 0.0)
            item.setdefault("vector", 0.0)
            item["fusion"] = (1 / (60 + lexical_rank[item["id"]]) if item["id"] in lexical_rank else 0) + (1 / (60 + vector_rank[item["id"]]) if item["id"] in vector_rank else 0)
            if effective_mode == "lexical":
                item["final"] = item["lexical"]
            elif effective_mode == "vector":
                item["final"] = item["vector"]
            else:
                item["final"] = item["fusion"]
        candidates = list(by_id.values())
        candidates.sort(key=lambda item: (item["final"], item.get("vector", 0), item.get("lexical", 0), item.get("id", "")), reverse=True)
        degraded_reason = str(retrieval_meta.get("degradedReason", "") or "") if not semantic_available else ""
        if not semantic_available and not degraded_reason:
            degraded_reason = self._DEFAULT_DEGRADED_REASON
        return {
            "results": candidates[:max(1, min(int(top_k), 100))],
            "backend": self.backend,
            "searchBackend": search_backend,
            **retrieval_meta,
            "semanticAvailable": semantic_available,
            "keywordOnly": not semantic_available,
            "degraded": not semantic_available,
            "degradedReason": degraded_reason,
            "requestedRetrievalMode": requested_mode,
            "effectiveRetrievalMode": effective_mode,
            "retrievalMode": effective_mode,
        }

    def search(self, query, library_ids, top_k=10, retrieval_mode="hybrid"):
        terms = self._features(query)
        requested_mode = str(retrieval_mode or "hybrid").strip().lower()
        if requested_mode not in {"lexical", "vector", "hybrid"}:
            requested_mode = "hybrid"
        engine, retrieval_meta = self.embeddings.active()
        retrieval_meta = dict(retrieval_meta)
        query_vector = None
        lexical_candidates = []
        vector_candidates = []
        with self.lock:
            libraries = library_ids or [entry.name for entry in self.root.iterdir() if entry.is_dir()]
            semantic_available = bool(engine is not None and retrieval_meta.get("semanticAvailable"))
            if semantic_available and not self._libraries_support_semantic(libraries, retrieval_meta):
                semantic_available = False
                retrieval_meta["semanticAvailable"] = False
                retrieval_meta["degradedReason"] = "The active embedding model does not match the stored index; rebuild is required."
            if semantic_available:
                try:
                    query_vector = engine.embed([query], is_query=True)["vectors"][0]
                except Exception as exc:
                    semantic_available = False
                    retrieval_meta["semanticAvailable"] = False
                    retrieval_meta["degradedReason"] = "Embedding failed; keyword retrieval is active: " + str(exc)
            if not semantic_available:
                retrieval_meta["semanticAvailable"] = False
            effective_mode = "lexical" if not semantic_available and requested_mode in {"vector", "hybrid"} else requested_mode
            native = self._search_lance(query, query_vector, libraries, top_k, effective_mode, retrieval_meta, requested_mode)
            if native is not None:
                return native
            for library_id in libraries:
                for item in self._load(library_id):
                    text = item.get("text", "").lower()
                    matches = sum(1 for term in terms if term in text)
                    lexical = matches / max(1, len(terms))
                    value = dict(item, libraryId=library_id, lexical=lexical)
                    if lexical > 0:
                        lexical_candidates.append(value)
                    if semantic_available:
                        vector = item.get("embedding")
                        if not isinstance(vector, list) or len(vector) != int(retrieval_meta.get("dimension", 0) or 0):
                            continue
                        value["vector"] = self._cosine(query_vector, vector)
                        if value["vector"] > 0:
                            vector_candidates.append(value)
                    else:
                        # Keep the deterministic score available for old
                        # clients under an explicit name, but never expose it
                        # as a semantic vector score or use it for ranking.
                        legacy_vector = item.get("legacySimilarity")
                        if not isinstance(legacy_vector, list):
                            legacy_vector = item.get("vector")
                        if not isinstance(legacy_vector, list):
                            legacy_vector = self._vector(item.get("text", ""))
                        value["vector"] = 0.0
                        value["legacySimilarity"] = self._cosine(self._vector(query), legacy_vector)

        return self._rank_candidates(lexical_candidates, vector_candidates, top_k, effective_mode, "portable-json", retrieval_meta, requested_mode)
