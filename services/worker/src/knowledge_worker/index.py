import json
import hashlib
import math
import os
import re
import threading
import time
from pathlib import Path


class HybridIndex:
    def __init__(self, data_root):
        self.root = Path(data_root) / "indexes"
        self.root.mkdir(parents=True, exist_ok=True)
        self.lock = threading.RLock()
        self._cache = {}
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

    def upsert(self, library_id, document_id, chunks):
        with self.lock:
            existing = [item for item in self._load(library_id) if item.get("documentId") != document_id]
            for chunk in chunks:
                existing.append({
                    "id": chunk["id"], "documentId": document_id, "text": chunk["text"],
                    "location": chunk.get("location", {}), "contentHash": chunk.get("contentHash", ""),
                    "vector": chunk.get("vector") or self._vector(chunk["text"])
                })
            self._cache[library_id] = existing
            target = self._path(library_id)
            temporary = target.with_suffix(".tmp")
            temporary.write_text(json.dumps(existing, ensure_ascii=False), encoding="utf-8")
            os.replace(str(temporary), str(target))
            self._sync_lance(library_id, existing)
            return {"count": len(chunks), "backend": self.backend}
    def rebuild(self, library_id, chunks):
        with self.lock:
            library_root = self.root / library_id
            library_root.mkdir(parents=True, exist_ok=True)
            version = "v" + str(int(time.time() * 1000))
            version_root = library_root / (version + ".tmp")
            version_root.mkdir(parents=True, exist_ok=False)
            values = []
            for chunk in chunks:
                values.append({
                    "id": chunk["id"], "documentId": chunk["documentId"], "text": chunk["text"],
                    "location": chunk.get("location", {}), "contentHash": chunk.get("contentHash", ""),
                    "vector": chunk.get("vector") or self._vector(chunk["text"])
                })
            target_root = library_root / version
            (version_root / "chunks.json").write_text(json.dumps(values, ensure_ascii=False), encoding="utf-8")
            os.replace(str(version_root), str(target_root))
            pointer = library_root / "active.tmp"
            pointer.write_text(json.dumps({"version": version}, ensure_ascii=False), encoding="utf-8")
            os.replace(str(pointer), str(library_root / "active.json"))
            self._cache[library_id] = values
            self._sync_lance(library_id, values)
            return {"count": len(values), "version": version, "backend": self.backend}

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
            rows = [{"id": item["id"], "document_id": item["documentId"], "text": item["text"], "location_json": json.dumps(item.get("location", {}), ensure_ascii=False), "content_hash": item.get("contentHash", ""), "vector": item.get("vector") or self._vector(item.get("text", ""))} for item in values]
            table = db.create_table("chunks", data=rows, mode="overwrite")
            try:
                table.create_fts_index("text", replace=True)
            except Exception:
                pass
        except Exception:
            # The portable index is authoritative and remains usable if a native wheel fails.
            pass

    def search(self, query, library_ids, top_k=10, retrieval_mode="hybrid"):
        terms = self._features(query)
        query_vector = self._vector(query)
        lexical_candidates = []
        vector_candidates = []
        with self.lock:
            libraries = library_ids or [entry.name for entry in self.root.iterdir() if entry.is_dir()]
            for library_id in libraries:
                for item in self._load(library_id):
                    text = item.get("text", "").lower()
                    matches = sum(1 for term in terms if term in text)
                    lexical = matches / max(1, len(terms))
                    value = dict(item, libraryId=library_id, lexical=lexical)
                    if lexical > 0:
                        lexical_candidates.append(value)
                    vector = item.get("vector") or self._vector(item.get("text", ""))
                    value["vector"] = self._cosine(query_vector, vector)
                    if value["vector"] > 0:
                        vector_candidates.append(value)

        lexical_candidates.sort(key=lambda item: (item["lexical"], item.get("id", "")), reverse=True)
        vector_candidates.sort(key=lambda item: (item["vector"], item.get("id", "")), reverse=True)
        lexical_rank = {item["id"]: index + 1 for index, item in enumerate(lexical_candidates)}
        vector_rank = {item["id"]: index + 1 for index, item in enumerate(vector_candidates)}
        by_id = {item["id"]: item for item in lexical_candidates + vector_candidates}
        candidates = []
        for item in by_id.values():
            item["fusion"] = (1 / (60 + lexical_rank[item["id"]]) if item["id"] in lexical_rank else 0) + (1 / (60 + vector_rank[item["id"]]) if item["id"] in vector_rank else 0)
            if retrieval_mode == "lexical":
                item["final"] = item["lexical"]
            elif retrieval_mode == "vector":
                item["final"] = item["vector"]
            else:
                item["final"] = item["fusion"]
            candidates.append(item)
        candidates.sort(key=lambda item: (item["final"], item.get("vector", 0), item.get("lexical", 0), item.get("id", "")), reverse=True)
        return {"results": candidates[:max(1, min(int(top_k), 100))], "backend": self.backend, "degraded": False, "retrievalMode": retrieval_mode}
