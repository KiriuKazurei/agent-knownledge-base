"""Verified, offline-first local embedding model packages.

Embedding packages are data-only archives. The worker never imports code from
an archive and never downloads a model during startup. A package is usable
only after its manifest, every declared file, and the CPU runtime pass their
respective checks.
"""

from __future__ import annotations

import hashlib
import importlib.metadata
import importlib.util
import json
import math
import os
import re
import shutil
import stat
import tempfile
import threading
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path, PurePosixPath
from typing import Any, Iterable


MANIFEST_SCHEMA = "kah-embedding-model/v1"
MAX_PACKAGE_BYTES = 500 * 1024 * 1024
MAX_ENTRY_COUNT = 512
MAX_ENTRY_BYTES = MAX_PACKAGE_BYTES
MAX_BATCH_SIZE = 128
MAX_TEXT_BYTES = 4 * 1024 * 1024
MODEL_ROOT_NAME = "embedding_models"
BGE_ID = "BAAI/bge-small-zh-v1.5"
BGE_COMMIT = "7999e1d3359715c523056ef9478215996d62a620"
_MODEL_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,79}(?:/[A-Za-z0-9][A-Za-z0-9._-]{0,79})?$")
_ALLOWED_ROLES = {"model", "tokenizer", "vocab", "license", "config", "resource"}
_SCRIPT_SUFFIXES = {
    ".bat", ".cmd", ".com", ".dll", ".exe", ".js", ".mjs", ".ps1", ".py",
    ".pyc", ".rb", ".sh", ".so", ".vbs", ".wasm",
}


class EmbeddingPackageError(ValueError):
    """A package or manifest failed a deterministic validation check."""


def _sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _normal_model_id(value: Any) -> str:
    value = str(value or "").strip()
    if not _MODEL_ID_RE.fullmatch(value) or value in {".", ".."}:
        raise EmbeddingPackageError("invalid embedding model id")
    return value


def _safe_entry(name: str) -> str:
    """Validate an archive/resource path as a relative POSIX path."""

    if not name or "\x00" in name or "\\" in name:
        raise EmbeddingPackageError("embedding package contains an unsafe path")
    path = PurePosixPath(name)
    if path.is_absolute() or any(part in {"", ".", ".."} for part in path.parts):
        raise EmbeddingPackageError("embedding package contains an unsafe path: " + name)
    # A colon in the first component would be interpreted as a drive on
    # Windows even when the archive uses POSIX separators.
    if ":" in path.parts[0]:
        raise EmbeddingPackageError("embedding package contains an unsafe path: " + name)
    suffix = Path(path.name).suffix.lower()
    if suffix in _SCRIPT_SUFFIXES:
        raise EmbeddingPackageError("embedding package contains an executable resource: " + name)
    return str(path)


def _safe_resource_path(root: Path, value: Any) -> Path:
    relative = _safe_entry(str(value or ""))
    target = (root / relative).resolve()
    root = root.resolve()
    if target != root and root not in target.parents:
        raise EmbeddingPackageError("embedding resource escapes its model directory: " + relative)
    return target


def _require_sha(value: Any) -> str:
    result = str(value or "").lower()
    if len(result) != 64 or any(ch not in "0123456789abcdef" for ch in result):
        raise EmbeddingPackageError("resource sha256 must be a 64 character hexadecimal digest")
    return result


def validate_manifest(manifest: dict[str, Any]) -> dict[str, Any]:
    """Validate and normalize the data-only model manifest."""

    if not isinstance(manifest, dict):
        raise EmbeddingPackageError("embedding manifest must be an object")
    if manifest.get("schema") != MANIFEST_SCHEMA:
        raise EmbeddingPackageError("embedding manifest schema must be " + MANIFEST_SCHEMA)
    model_id = _normal_model_id(manifest.get("id"))
    version = str(manifest.get("version", "")).strip()
    if not version or len(version) > 120:
        raise EmbeddingPackageError("embedding manifest version is required")
    upstream = str(manifest.get("upstreamCommit", "")).strip()
    if not upstream or len(upstream) > 160:
        raise EmbeddingPackageError("embedding upstreamCommit is required")
    license_name = str(manifest.get("license", "")).strip()
    if not license_name or len(license_name) > 200:
        raise EmbeddingPackageError("embedding license is required")
    dimension = manifest.get("dimension")
    if not isinstance(dimension, int) or isinstance(dimension, bool) or dimension <= 0 or dimension > 8192:
        raise EmbeddingPackageError("embedding dimension is invalid")
    max_tokens = manifest.get("maxTokens")
    if not isinstance(max_tokens, int) or isinstance(max_tokens, bool) or max_tokens < 1 or max_tokens > 32768:
        raise EmbeddingPackageError("embedding maxTokens is invalid")

    runtime = manifest.get("runtime")
    if not isinstance(runtime, dict) or str(runtime.get("name", "")).lower() not in {"onnxruntime", "onnx-runtime"}:
        raise EmbeddingPackageError("embedding runtime must be ONNX Runtime")
    if not str(runtime.get("version", "")).strip():
        raise EmbeddingPackageError("embedding runtime version is required")
    if str(runtime.get("device", "cpu")).lower() != "cpu":
        raise EmbeddingPackageError("only CPU embedding packages are supported")

    tokenizer = manifest.get("tokenizer")
    if not isinstance(tokenizer, dict) or not str(tokenizer.get("configHash", "")).strip():
        raise EmbeddingPackageError("tokenizer config hash is required")
    tokenizer_hash = str(tokenizer.get("configHash", "")).lower()
    if len(tokenizer_hash) != 64 or any(ch not in "0123456789abcdef" for ch in tokenizer_hash):
        raise EmbeddingPackageError("tokenizer config hash must be a SHA-256 digest")
    if not str(tokenizer.get("runtimeVersion", "")).strip():
        raise EmbeddingPackageError("tokenizer runtime version is required")
    pooling = manifest.get("pooling")
    if not isinstance(pooling, dict) or str(pooling.get("type", "")).lower() not in {"mean", "cls"}:
        raise EmbeddingPackageError("pooling.type must be mean or cls")
    if "normalize" in pooling and not isinstance(pooling["normalize"], bool):
        raise EmbeddingPackageError("pooling.normalize must be boolean")

    query_prefix = manifest.get("queryPrefix", "")
    passage_prefix = manifest.get("passagePrefix", "")
    if not isinstance(query_prefix, str) or not isinstance(passage_prefix, str):
        raise EmbeddingPackageError("queryPrefix and passagePrefix must be strings")
    if len(query_prefix.encode("utf-8")) > MAX_TEXT_BYTES or len(passage_prefix.encode("utf-8")) > MAX_TEXT_BYTES:
        raise EmbeddingPackageError("embedding prefix is too large")

    files = manifest.get("files")
    if not isinstance(files, list) or not files:
        raise EmbeddingPackageError("embedding manifest files are required")
    normalized_files: list[dict[str, Any]] = []
    seen: set[str] = set()
    roles: set[str] = set()
    total = 0
    for item in files:
        if not isinstance(item, dict):
            raise EmbeddingPackageError("embedding manifest file must be an object")
        path = _safe_entry(str(item.get("path", "")))
        if path in seen or path == "manifest.json":
            raise EmbeddingPackageError("duplicate embedding resource path")
        role = str(item.get("role", "")).strip().lower()
        if role not in _ALLOWED_ROLES:
            raise EmbeddingPackageError("embedding resource role is invalid: " + role)
        if role in roles and role in {"model", "tokenizer", "license"}:
            raise EmbeddingPackageError("embedding manifest has duplicate " + role + " resource")
        roles.add(role)
        seen.add(path)
        size = item.get("size")
        if not isinstance(size, int) or isinstance(size, bool) or size < 0 or size > MAX_ENTRY_BYTES:
            raise EmbeddingPackageError("embedding resource size is invalid")
        total += size
        if total > MAX_PACKAGE_BYTES:
            raise EmbeddingPackageError("embedding resources exceed 500 MiB")
        normalized_files.append({
            "path": path,
            "size": size,
            "sha256": _require_sha(item.get("sha256")),
            "role": role,
        })
    if "model" not in roles:
        raise EmbeddingPackageError("embedding manifest must declare a model resource")
    if "tokenizer" not in roles:
        raise EmbeddingPackageError("embedding manifest must declare a tokenizer resource")
    if "license" not in roles:
        raise EmbeddingPackageError("embedding manifest must declare a license resource")
    tokenizer_file = next(item for item in normalized_files if item["role"] == "tokenizer")
    if tokenizer_file["sha256"] != tokenizer_hash:
        raise EmbeddingPackageError("tokenizer config hash must match the tokenizer resource SHA-256")

    normalized = dict(manifest)
    normalized.update({
        "schema": MANIFEST_SCHEMA,
        "id": model_id,
        "version": version,
        "upstreamCommit": upstream,
        "license": license_name,
        "dimension": dimension,
        "maxTokens": max_tokens,
        "files": normalized_files,
        "runtime": dict(runtime),
        "tokenizer": {**dict(tokenizer), "configHash": tokenizer_hash},
        "pooling": {"type": str(pooling["type"]).lower(), "normalize": bool(pooling.get("normalize", True))},
        "queryPrefix": query_prefix,
        "passagePrefix": passage_prefix,
    })
    normalized.setdefault("modelType", "text-embedding")
    return normalized


def bge_catalog_entry() -> dict[str, Any]:
    """Return the fixed BGE catalog metadata.

    Both the upstream model and the conversion repository are immutable
    revisions.  Every URL is pinned to a commit and every downloaded byte is
    checked before the package is installed.
    """

    return {
        "id": BGE_ID,
        "version": "1.5-onnx-fp32",
        "upstream": "https://huggingface.co/BAAI/bge-small-zh-v1.5",
        "upstreamCommit": BGE_COMMIT,
        "license": "MIT",
        "dimension": 512,
        "maxTokens": 512,
        "tokenizer": {"configHash": "48cea5d44424912a6fd1ea647bf4fe50b55ab8b1e5879c3275f80e339e8fae26", "type": "huggingface-tokenizer", "runtimeVersion": "0.21.0"},
        "runtime": {"name": "onnxruntime", "version": "1.20.0", "device": "cpu"},
        "pooling": {"type": "cls", "normalize": True},
        "poolingConfig": {
            "path": "1_Pooling/config.json",
            "sha256": "AAA8861589F80C961A03CC86C7EEAEF7605C1676B9AB55329D33A304738769C6",
            "upstreamCommit": BGE_COMMIT,
            "mode": "cls_token",
        },
        "queryPrefix": "为这个句子生成表示以用于检索相关文章：",
        "passagePrefix": "",
        "files": [
            {
                "role": "model",
                "path": "model.onnx",
                "size": 94835369,
                "sha256": "ba917299b47626c45a420351c179948adb1b24354c89601c265570d43d8554ab",
                "url": "https://huggingface.co/JasonYANG170/bge-small-zh-v1.5-ONNX/resolve/88d1b438c9c44428e0f7578c6ac3cd8e6d4edeae/onnx/model.onnx?download=true",
            },
            {
                "role": "tokenizer",
                "path": "tokenizer.json",
                "size": 439125,
                "sha256": "48cea5d44424912a6fd1ea647bf4fe50b55ab8b1e5879c3275f80e339e8fae26",
                "url": "https://huggingface.co/JasonYANG170/bge-small-zh-v1.5-ONNX/resolve/88d1b438c9c44428e0f7578c6ac3cd8e6d4edeae/tokenizer.json?download=true",
            },
            {
                "role": "license",
                "path": "LICENSE",
                "size": 1065,
                "sha256": "587a673933425dbc36ec61268d3b954051b2d3ef3c9b322ede357976055ffdd5",
                "url": "https://raw.githubusercontent.com/FlagOpen/FlagEmbedding/fd1a2bdf69488ffebe0327999d4400d8c8058a0b/LICENSE",
            },
        ],
        "onlineInstall": True,
        "conversionProvenance": {
            "repository": "https://huggingface.co/JasonYANG170/bge-small-zh-v1.5-ONNX",
            "commit": "88d1b438c9c44428e0f7578c6ac3cd8e6d4edeae",
            "sourceModel": BGE_ID,
            "sourceCommit": BGE_COMMIT,
            "sourceTokenizerSha256": "48cea5d44424912a6fd1ea647bf4fe50b55ab8b1e5879c3275f80e339e8fae26",
            "licenseRepository": "https://github.com/FlagOpen/FlagEmbedding",
            "licenseCommit": "fd1a2bdf69488ffebe0327999d4400d8c8058a0b",
            "upstreamPoolingConfigSha256": "aaa8861589f80c961a03cc86c7eeaef7605c1676b9ab55329d33a304738769c6",
            "conversion": {
                "tool": "optimum.exporters.onnx.main_export",
                "opset": 17,
                "dtype": "fp32",
                "task": "feature-extraction",
                "packages": {
                    "torch": "2.14.0",
                    "transformers": "4.57.6",
                    "optimum-onnx": "0.1.0",
                    "onnx": "1.22.0",
                    "onnxruntime": "1.30.0",
                    "onnx-ir": "1.0.0",
                },
            },
        },
    }


def catalog() -> list[dict[str, Any]]:
    return [bge_catalog_entry()]


def _runtime_info() -> tuple[str, int]:
    """Return installed CPU runtime version and package bytes, if present."""

    try:
        version = importlib.metadata.version("onnxruntime")
    except importlib.metadata.PackageNotFoundError:
        return "", 0
    try:
        spec = importlib.util.find_spec("onnxruntime")
        if spec is None or not spec.origin:
            return version, 0
        root = Path(spec.origin).resolve().parent
        total = sum(path.stat().st_size for path in root.rglob("*") if path.is_file())
        return version, total
    except (OSError, ValueError):
        return version, 0


def _installed_version(package: str) -> str:
    try:
        return importlib.metadata.version(package)
    except importlib.metadata.PackageNotFoundError:
        return ""


def _version_tuple(value: str) -> tuple[int, ...]:
    numbers = re.findall(r"\d+", str(value or ""))
    return tuple(int(number) for number in numbers[:4])


def _runtime_compatibility(manifest: dict[str, Any]) -> dict[str, Any]:
    expected_runtime = str(manifest.get("runtime", {}).get("version", ""))
    expected_tokenizer = str(manifest.get("tokenizer", {}).get("runtimeVersion", ""))
    actual_runtime = _installed_version("onnxruntime")
    actual_tokenizer = _installed_version("tokenizers")
    mismatches: list[str] = []
    if not actual_runtime:
        mismatches.append("onnxruntime is not installed")
    elif _version_tuple(actual_runtime) < _version_tuple(expected_runtime):
        mismatches.append("onnxruntime expected >= %s, actual %s" % (expected_runtime, actual_runtime))
    if not actual_tokenizer:
        mismatches.append("tokenizers is not installed")
    elif _version_tuple(actual_tokenizer) < _version_tuple(expected_tokenizer):
        mismatches.append("tokenizers expected >= %s, actual %s" % (expected_tokenizer, actual_tokenizer))
    return {
        "expectedRuntimeVersion": expected_runtime,
        "runtimeVersion": actual_runtime,
        "expectedTokenizerRuntimeVersion": expected_tokenizer,
        "tokenizerRuntimeVersion": actual_tokenizer,
        "mismatches": mismatches,
    }


class EmbeddingEngine:
    """One verified package loaded through CPU ONNX Runtime."""

    def __init__(self, model_dir: Path, manifest: dict[str, Any]):
        self.model_dir = model_dir
        self.manifest = validate_manifest(manifest)
        self.session = None
        self.tokenizer = None
        self.runtime_error = ""
        self._lock = threading.RLock()

    def _resource_by_role(self, role: str) -> Path | None:
        for item in self.manifest["files"]:
            if item.get("role") == role:
                return _safe_resource_path(self.model_dir, item["path"])
        return None

    def _load_runtime(self) -> None:
        with self._lock:
            if self.session is not None and self.tokenizer is not None:
                return
            compatibility = _runtime_compatibility(self.manifest)
            if compatibility["mismatches"]:
                raise RuntimeError("embedding_runtime_incompatible: " + "; ".join(compatibility["mismatches"]))
            try:
                import onnxruntime as ort  # type: ignore
            except Exception as exc:  # pragma: no cover - optional wheel
                raise RuntimeError("embedding_runtime_unavailable: packaged CPU ONNX Runtime is missing") from exc
            try:
                import numpy  # noqa: F401  # type: ignore
            except Exception as exc:  # pragma: no cover - optional wheel
                raise RuntimeError("embedding_numpy_unavailable: packaged NumPy runtime is missing") from exc
            model_path = self._resource_by_role("model")
            tokenizer_path = self._resource_by_role("tokenizer")
            if model_path is None or not model_path.is_file():
                raise RuntimeError("embedding_model_missing: manifest has no model resource")
            if tokenizer_path is None or not tokenizer_path.is_file():
                raise RuntimeError("embedding_tokenizer_missing: manifest has no tokenizer resource")
            try:
                self.session = ort.InferenceSession(str(model_path), providers=["CPUExecutionProvider"])
            except Exception as exc:  # pragma: no cover - depends on model contents
                raise RuntimeError("embedding_model_load_failed: " + str(exc)) from exc
            try:
                from tokenizers import Tokenizer  # type: ignore
                self.tokenizer = Tokenizer.from_file(str(tokenizer_path))
            except Exception as exc:  # pragma: no cover - optional wheel
                self.session = None
                raise RuntimeError("embedding_tokenizer_unavailable: packaged tokenizers runtime is missing") from exc

    def unload(self) -> None:
        with self._lock:
            self.session = None
            self.tokenizer = None

    def health(self) -> dict[str, Any]:
        runtime_version, runtime_bytes = _runtime_info()
        compatibility = _runtime_compatibility(self.manifest)
        try:
            self._load_runtime()
            return {
                "available": True,
                "modelId": self.manifest["id"],
                "version": self.manifest["version"],
                "dimension": self.manifest["dimension"],
                "runtime": "onnxruntime-cpu",
                "runtimeVersion": runtime_version,
                "runtimeBytes": runtime_bytes,
                "expectedRuntimeVersion": compatibility["expectedRuntimeVersion"],
                "tokenizerRuntimeVersion": compatibility["tokenizerRuntimeVersion"],
                "expectedTokenizerRuntimeVersion": compatibility["expectedTokenizerRuntimeVersion"],
                "runtimeMismatch": compatibility["mismatches"],
                "error": "",
            }
        except Exception as exc:
            self.runtime_error = str(exc)
            return {
                "available": False,
                "modelId": self.manifest["id"],
                "version": self.manifest["version"],
                "dimension": self.manifest["dimension"],
                "runtime": "onnxruntime-cpu",
                "runtimeVersion": runtime_version,
                "runtimeBytes": runtime_bytes,
                "expectedRuntimeVersion": compatibility["expectedRuntimeVersion"],
                "tokenizerRuntimeVersion": compatibility["tokenizerRuntimeVersion"],
                "expectedTokenizerRuntimeVersion": compatibility["expectedTokenizerRuntimeVersion"],
                "runtimeMismatch": compatibility["mismatches"],
                "error": self.runtime_error,
            }

    @staticmethod
    def truncate_tokens(token_ids: list[int], max_tokens: int) -> tuple[list[int], bool]:
        if max_tokens <= 0:
            raise ValueError("max_tokens must be positive")
        return token_ids[:max_tokens], len(token_ids) > max_tokens

    @staticmethod
    def l2_normalize(vector: Iterable[float]) -> list[float]:
        values = [float(item) for item in vector]
        norm = math.sqrt(sum(value * value for value in values))
        if norm == 0:
            return values
        return [value / norm for value in values]

    def embed(self, texts: list[str], is_query: bool = False) -> dict[str, Any]:
        if not isinstance(texts, list) or len(texts) > MAX_BATCH_SIZE or not all(isinstance(item, str) for item in texts):
            raise ValueError("texts must be an array of at most 128 strings")
        for text in texts:
            if len(text.encode("utf-8")) > MAX_TEXT_BYTES:
                raise ValueError("embedding input text is too large")
        if not texts:
            return self._result([], [], [])
        self._load_runtime()
        prefix = self.manifest.get("queryPrefix" if is_query else "passagePrefix", "")
        encodings: list[list[int]] = []
        token_counts: list[int] = []
        truncated_flags: list[bool] = []
        for text in texts:
            encoding = self.tokenizer.encode(prefix + text, add_special_tokens=True)
            raw_ids = list(encoding.ids)
            ids, was_truncated = self.truncate_tokens(raw_ids, int(self.manifest["maxTokens"]))
            encodings.append(ids)
            token_counts.append(len(ids))
            truncated_flags.append(was_truncated)
        width = max((len(item) for item in encodings), default=1)
        width = min(width, int(self.manifest["maxTokens"]))
        pad_id = self.tokenizer.token_to_id("[PAD]")
        if pad_id is None:
            pad_id = self.tokenizer.token_to_id("<pad>")
        pad_id = int(pad_id if pad_id is not None else 0)
        input_ids = [ids + [pad_id] * (width - len(ids)) for ids in encodings]
        attention = [[1 if index < len(ids) else 0 for index in range(width)] for ids in encodings]
        type_ids = [[0] * width for _ in encodings]
        import numpy as np  # type: ignore
        names = {item.name for item in self.session.get_inputs()}
        if "input_ids" not in names:
            raise RuntimeError("embedding_model_invalid: input_ids input is missing")
        feeds: dict[str, Any] = {"input_ids": np.asarray(input_ids, dtype="int64")}
        if "attention_mask" in names:
            feeds["attention_mask"] = np.asarray(attention, dtype="int64")
        if "token_type_ids" in names:
            feeds["token_type_ids"] = np.asarray(type_ids, dtype="int64")
        outputs = self.session.run(None, feeds)
        if not outputs:
            raise RuntimeError("embedding_model_invalid: model returned no output")
        matrix = np.asarray(outputs[0])
        if matrix.ndim not in {2, 3} or matrix.shape[0] != len(texts):
            raise RuntimeError("embedding_model_invalid: output shape must be [batch,dim] or [batch,tokens,dim]")
        pooling = str(self.manifest.get("pooling", {}).get("type", "mean")).lower()
        vectors: list[list[float]] = []
        for row, mask in zip(matrix, attention):
            if matrix.ndim == 2:
                vector = row
            elif pooling == "cls":
                vector = row[0]
            else:
                weight = np.asarray(mask, dtype="float32")[:, None]
                vector = (row * weight).sum(axis=0) / max(1.0, float(weight.sum()))
            values = vector.tolist() if hasattr(vector, "tolist") else list(vector)
            if len(values) != int(self.manifest["dimension"]):
                raise RuntimeError("embedding_model_invalid: output dimension does not match manifest")
            if self.manifest.get("pooling", {}).get("normalize", True):
                values = self.l2_normalize(values)
            vectors.append([float(value) for value in values])
        return self._result(vectors, token_counts, truncated_flags)

    def _result(self, vectors: list[list[float]], token_counts: list[int], truncated: list[bool]) -> dict[str, Any]:
        config_hash = self.manifest["tokenizer"]["configHash"]
        return {
            "vectors": vectors,
            "modelId": self.manifest["id"],
            "version": self.manifest["version"],
            "dimension": self.manifest["dimension"],
            "tokenCount": token_counts,
            "truncated": truncated,
            "truncatedCount": sum(1 for item in truncated if item),
            "configHash": config_hash,
            "tokenizerConfigHash": config_hash,
        }


class EmbeddingRegistry:
    def __init__(self, data_root: str | os.PathLike[str]):
        base = Path(data_root).expanduser().resolve()
        self.root = base / "models" / MODEL_ROOT_NAME
        self.root.mkdir(parents=True, exist_ok=True)
        if self.root.is_symlink():
            raise EmbeddingPackageError("embedding model root must not be a symlink")
        self.state_path = self.root / "state.json"
        self.engines: dict[str, EmbeddingEngine] = {}
        self.lock = threading.RLock()

    def _read_state(self) -> dict[str, Any]:
        if not self.state_path.exists():
            return {"activeId": "", "models": {}}
        try:
            value = json.loads(self.state_path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {"activeId": "", "models": {}}
        if not isinstance(value, dict) or not isinstance(value.get("models", {}), dict):
            return {"activeId": "", "models": {}}
        return value

    def _write_state(self, value: dict[str, Any]) -> None:
        temporary = self.state_path.with_suffix(".tmp")
        temporary.write_text(json.dumps(value, ensure_ascii=False, sort_keys=True), encoding="utf-8")
        os.replace(temporary, self.state_path)

    def _model_dir(self, model_id: str) -> Path:
        safe = _normal_model_id(model_id).replace("/", "__")
        return self.root / safe

    def _load_manifest(self, model_dir: Path) -> dict[str, Any]:
        path = model_dir / "manifest.json"
        if path.is_symlink() or not path.is_file():
            raise EmbeddingPackageError("embedding manifest is missing")
        return validate_manifest(json.loads(path.read_text(encoding="utf-8")))

    def _verify_directory(self, model_dir: Path) -> dict[str, Any]:
        model_dir = model_dir.resolve()
        if not model_dir.is_dir():
            raise EmbeddingPackageError("embedding model directory is missing")
        manifest = self._load_manifest(model_dir)
        expected = {"manifest.json", *(item["path"] for item in manifest["files"])}
        actual: set[str] = set()
        for path in model_dir.rglob("*"):
            if path.is_symlink():
                raise EmbeddingPackageError("embedding model directory contains a symbolic link")
            if path.is_file():
                actual.add(path.relative_to(model_dir).as_posix())
        if actual != expected:
            extras = sorted(actual - expected)
            missing = sorted(expected - actual)
            raise EmbeddingPackageError("embedding resource file set mismatch: extras=%s missing=%s" % (extras, missing))
        for item in manifest["files"]:
            path = _safe_resource_path(model_dir, item["path"])
            if not path.is_file() or path.stat().st_size != item["size"] or _sha256(path) != item["sha256"]:
                raise EmbeddingPackageError("embedding resource hash or size mismatch: " + item["path"])
        return manifest

    def _model_runtime_bytes(self) -> int:
        return _runtime_info()[1]

    def list(self) -> list[dict[str, Any]]:
        with self.lock:
            state = self._read_state()
            installed = state.get("models", {})
            result: list[dict[str, Any]] = []
            for entry in catalog():
                value = dict(entry)
                value.update({"installed": False, "enabled": False, "sizeBytes": 0, "runtimeBytes": self._model_runtime_bytes(), "health": None})
                item = installed.get(entry["id"])
                if item:
                    model_dir = self._model_dir(entry["id"])
                    value.update({"installed": model_dir.is_dir(), "enabled": bool(item.get("enabled")), "sizeBytes": int(item.get("sizeBytes", 0))})
                    if model_dir.is_dir():
                        value["health"] = self.health(entry["id"])
                result.append(value)
            for model_id, item in installed.items():
                if any(entry["id"] == model_id for entry in result):
                    continue
                try:
                    manifest = self._verify_directory(self._model_dir(model_id))
                    result.append({**manifest, "installed": True, "enabled": bool(item.get("enabled")), "sizeBytes": int(item.get("sizeBytes", 0)), "runtimeBytes": self._model_runtime_bytes(), "health": self.health(model_id)})
                except Exception as exc:
                    result.append({"id": model_id, "installed": True, "enabled": bool(item.get("enabled")), "runtimeBytes": self._model_runtime_bytes(), "health": {"available": False, "modelId": model_id, "error": str(exc)}})
            return result

    def _install_staged(self, stage: Path) -> dict[str, Any]:
        with self.lock:
            manifest = self._verify_directory(stage)
            model_dir = self._model_dir(manifest["id"])
            if model_dir.exists() or model_dir.is_symlink():
                raise EmbeddingPackageError("embedding model is already installed; uninstall before replacing it")
            stage_target = self.root / ("." + model_dir.name + ".install")
            if stage_target.exists():
                shutil.rmtree(stage_target, ignore_errors=True)
            os.replace(stage, stage_target)
            installed = False
            try:
                os.replace(stage_target, model_dir)
                installed = True
                state = self._read_state()
                models = state.setdefault("models", {})
                models[manifest["id"]] = {
                    "enabled": False,
                    "sizeBytes": sum(item["size"] for item in manifest["files"]),
                    "runtimeBytes": self._model_runtime_bytes(),
                    "version": manifest["version"],
                }
                self._write_state(state)
            except Exception:
                if installed and model_dir.exists():
                    shutil.rmtree(model_dir, ignore_errors=True)
                if stage_target.exists():
                    shutil.rmtree(stage_target, ignore_errors=True)
                raise
            return self.status(manifest["id"])

    @staticmethod
    def _zip_is_symlink(item: zipfile.ZipInfo) -> bool:
        mode = (item.external_attr >> 16) & 0xFFFF
        return stat.S_ISLNK(mode) or (item.create_system == 3 and stat.S_IFMT(mode) == stat.S_IFLNK)

    def import_package(self, package_path: str) -> dict[str, Any]:
        package = Path(package_path).expanduser()
        if not package.is_file():
            raise EmbeddingPackageError("embedding package does not exist")
        if package.stat().st_size > MAX_PACKAGE_BYTES:
            raise EmbeddingPackageError("embedding package exceeds 500 MiB")
        try:
            with tempfile.TemporaryDirectory(prefix="embedding-install-", dir=self.root) as temp:
                stage = Path(temp)
                with zipfile.ZipFile(package) as archive:
                    entries = archive.infolist()
                    if len(entries) > MAX_ENTRY_COUNT:
                        raise EmbeddingPackageError("embedding package has too many entries")
                    by_name: dict[str, zipfile.ZipInfo] = {}
                    total = 0
                    for item in entries:
                        name = _safe_entry(item.filename)
                        if name in by_name:
                            raise EmbeddingPackageError("duplicate embedding package entry: " + name)
                        if item.is_dir() or self._zip_is_symlink(item):
                            raise EmbeddingPackageError("embedding package directories and links are not allowed")
                        if item.file_size > MAX_ENTRY_BYTES:
                            raise EmbeddingPackageError("embedding package entry exceeds 500 MiB")
                        total += item.file_size
                        if total > MAX_PACKAGE_BYTES:
                            raise EmbeddingPackageError("embedding package exceeds 500 MiB uncompressed")
                        by_name[name] = item
                    manifest_item = by_name.get("manifest.json")
                    if manifest_item is None:
                        raise EmbeddingPackageError("embedding package manifest.json is required")
                    if manifest_item.file_size > 2 * 1024 * 1024:
                        raise EmbeddingPackageError("embedding manifest is too large")
                    try:
                        raw_manifest = archive.read(manifest_item)
                        manifest = validate_manifest(json.loads(raw_manifest.decode("utf-8")))
                    except (UnicodeError, json.JSONDecodeError) as exc:
                        raise EmbeddingPackageError("embedding manifest is not valid UTF-8 JSON") from exc
                    expected = {"manifest.json", *(item["path"] for item in manifest["files"])}
                    if set(by_name) != expected:
                        raise EmbeddingPackageError("embedding package entries do not match manifest")
                    for name, item in by_name.items():
                        target = stage / name
                        target.parent.mkdir(parents=True, exist_ok=True)
                        with archive.open(item) as source, target.open("wb") as output:
                            remaining = item.file_size
                            while remaining:
                                block = source.read(min(1024 * 1024, remaining))
                                if not block:
                                    raise EmbeddingPackageError("embedding package entry was truncated")
                                output.write(block)
                                remaining -= len(block)
                    (stage / "manifest.json").write_bytes(raw_manifest)
                return self._install_staged(stage)
        except zipfile.BadZipFile as exc:
            raise EmbeddingPackageError("embedding package is not a valid ZIP") from exc

    @staticmethod
    def _download_verified(url: str, target: Path, expected_size: int, expected_sha: str) -> None:
        parsed = urllib.parse.urlparse(url)
        if parsed.scheme != "https" or not parsed.netloc:
            raise EmbeddingPackageError("online model resource URL must use HTTPS")

        original_host = parsed.hostname or ""

        def trusted_host(host: str) -> bool:
            # Hugging Face stores large immutable files behind its own CDN and
            # Xet hosts.  A redirect is accepted only over HTTPS and only to a
            # host owned by the fixed source provider; the final bytes still
            # have to match the manifest SHA-256.
            return host == original_host or host == "raw.githubusercontent.com" or host.endswith(".huggingface.co") or host.endswith(".hf.co")

        class TrustedRedirect(urllib.request.HTTPRedirectHandler):
            def redirect_request(self, req, fp, code, msg, headers, newurl):  # noqa: ANN001
                target = urllib.parse.urlparse(newurl)
                if target.scheme != "https" or not target.hostname or not trusted_host(target.hostname):
                    raise EmbeddingPackageError("online model redirect target is not trusted")
                return super().redirect_request(req, fp, code, msg, headers, newurl)

        digest = hashlib.sha256()
        count = 0
        try:
            request = urllib.request.Request(url, headers={"Accept": "application/octet-stream"})
            with urllib.request.build_opener(TrustedRedirect).open(request, timeout=30) as response, target.open("wb") as output:
                while True:
                    block = response.read(min(1024 * 1024, expected_size + 1 - count))
                    if not block:
                        break
                    count += len(block)
                    if count > expected_size:
                        raise EmbeddingPackageError("downloaded resource exceeds manifest size")
                    digest.update(block)
                    output.write(block)
        except urllib.error.HTTPError as exc:
            raise EmbeddingPackageError("online model resource request failed: HTTP " + str(exc.code)) from exc
        except urllib.error.URLError as exc:
            raise EmbeddingPackageError("online model resource request failed: " + str(exc.reason)) from exc
        if count != expected_size or digest.hexdigest() != expected_sha:
            raise EmbeddingPackageError("downloaded resource hash or size mismatch")

    def install_online(self, model_id: str) -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        entry = next((item for item in catalog() if item["id"] == model_id), None)
        if entry is None:
            raise EmbeddingPackageError("embedding model is not in the fixed catalog")
        if not entry.get("onlineInstall") or not entry.get("files"):
            raise EmbeddingPackageError("online_install_unavailable: " + str(entry.get("onlineDisabledReason", "资源清单未发布/需离线签名包")))
        with tempfile.TemporaryDirectory(prefix="embedding-download-", dir=self.root) as temp:
            stage = Path(temp)
            package_manifest = dict(entry)
            package_manifest["schema"] = MANIFEST_SCHEMA
            package_manifest["modelType"] = "text-embedding"
            download_files = list(entry["files"])
            package_manifest["files"] = [
                {key: item[key] for key in ("role", "path", "size", "sha256")}
                for item in download_files
            ]
            (stage / "manifest.json").write_text(json.dumps(package_manifest, ensure_ascii=False), encoding="utf-8")
            for item in download_files:
                relative = _safe_entry(item["path"])
                target = stage / relative
                target.parent.mkdir(parents=True, exist_ok=True)
                self._download_verified(str(item.get("url", "")), target, int(item["size"]), _require_sha(item["sha256"]))
            return self._install_staged(stage)

    def status(self, model_id: str) -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        with self.lock:
            state = self._read_state()
            item = state.get("models", {}).get(model_id)
            if not item:
                entry = next((entry for entry in catalog() if entry["id"] == model_id), None)
                return {**(entry or {"id": model_id}), "installed": False, "enabled": False, "sizeBytes": 0, "runtimeBytes": self._model_runtime_bytes(), "health": None}
            model_dir = self._model_dir(model_id)
            health = self.health(model_id) if model_dir.is_dir() else {"available": False, "modelId": model_id, "error": "model directory missing"}
            return {"id": model_id, "version": item.get("version", ""), "installed": model_dir.is_dir(), "enabled": bool(item.get("enabled")), "sizeBytes": int(item.get("sizeBytes", 0)), "runtimeBytes": self._model_runtime_bytes(), "health": health}

    def health(self, model_id: str) -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        try:
            model_dir = self._model_dir(model_id)
            manifest = self._verify_directory(model_dir)
            engine = self.engines.get(model_id)
            if engine is None:
                engine = self.engines[model_id] = EmbeddingEngine(model_dir, manifest)
            return engine.health()
        except Exception as exc:
            version, runtime_bytes = _runtime_info()
            return {"available": False, "modelId": model_id, "runtime": "onnxruntime-cpu", "runtimeVersion": version, "runtimeBytes": runtime_bytes, "error": str(exc)}

    def load(self, model_id: str) -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        result = self.health(model_id)
        if not result.get("available"):
            raise RuntimeError(str(result.get("error", "embedding model unavailable")))
        return {**self.status(model_id), "loaded": True}

    def unload(self, model_id: str) -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        engine = self.engines.pop(model_id, None)
        if engine:
            engine.unload()
        return {**self.status(model_id), "loaded": False}

    def enable(self, model_id: str, enabled: bool) -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        if not isinstance(enabled, bool):
            raise ValueError("enabled must be boolean")
        with self.lock:
            state = self._read_state()
            if model_id not in state.get("models", {}) or not self._model_dir(model_id).is_dir():
                raise EmbeddingPackageError("embedding model is not installed")
            if enabled:
                result = self.health(model_id)
                if not result.get("available"):
                    raise RuntimeError("embedding model cannot be enabled: " + str(result.get("error", "health check failed")))
                for other_id, item in state["models"].items():
                    item["enabled"] = other_id == model_id
                state["activeId"] = model_id
            else:
                state["models"][model_id]["enabled"] = False
                if state.get("activeId") == model_id:
                    state["activeId"] = ""
            self._write_state(state)
            return self.status(model_id)

    def uninstall(self, model_id: str, active_index_model: str = "") -> dict[str, Any]:
        model_id = _normal_model_id(model_id)
        with self.lock:
            state = self._read_state()
            if active_index_model and active_index_model == model_id:
                raise EmbeddingPackageError("embedding model is active in an index; switch or rebuild before uninstall")
            if model_id not in state.get("models", {}):
                raise EmbeddingPackageError("embedding model is not installed")
            if state.get("activeId") == model_id or state["models"][model_id].get("enabled"):
                raise EmbeddingPackageError("embedding model is active; disable it before uninstall")
            engine = self.engines.pop(model_id, None)
            if engine:
                engine.unload()
            model_dir = self._model_dir(model_id)
            if model_dir.is_symlink() or model_dir.resolve().parent != self.root.resolve():
                raise EmbeddingPackageError("unsafe embedding model deletion target")
            if model_dir.exists():
                shutil.rmtree(model_dir)
            state["models"].pop(model_id, None)
            self._write_state(state)
            return self.status(model_id)

    def active(self) -> tuple[EmbeddingEngine | None, dict[str, Any]]:
        with self.lock:
            state = self._read_state()
            model_id = str(state.get("activeId", ""))
            if not model_id or not state.get("models", {}).get(model_id, {}).get("enabled"):
                return None, {"semanticAvailable": False, "degradedReason": "No local embedding model is enabled; keyword retrieval is active."}
            status = self.status(model_id)
            if not status.get("health", {}).get("available"):
                return None, {"semanticAvailable": False, "modelId": model_id, "modelVersion": status.get("version", ""), "degradedReason": "Embedding model is unavailable: " + str(status.get("health", {}).get("error", "health check failed"))}
            engine = self.engines.get(model_id)
            if engine is None:
                engine = self.engines[model_id] = EmbeddingEngine(self._model_dir(model_id), self._load_manifest(self._model_dir(model_id)))
            config_hash = engine.manifest["tokenizer"]["configHash"]
            return engine, {"semanticAvailable": True, "modelId": model_id, "modelVersion": status.get("version", ""), "dimension": engine.manifest["dimension"], "tokenizerConfigHash": config_hash, "configHash": config_hash, "degradedReason": ""}
