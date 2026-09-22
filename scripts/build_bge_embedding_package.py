"""Build the pinned BGE-small-zh-v1.5 offline embedding package.

The source directory is populated outside git from a fixed Hugging Face
conversion revision.  This script deliberately copies only data resources,
computes their SHA-256 digests, and writes a deterministic ZIP package.  It
does not download anything and never executes a file from the source tree.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import zipfile
from pathlib import Path


MODEL_ID = "BAAI/bge-small-zh-v1.5"
UPSTREAM_COMMIT = "7999e1d3359715c523056ef9478215996d62a620"
CONVERSION_REPOSITORY = "https://huggingface.co/JasonYANG170/bge-small-zh-v1.5-ONNX"
CONVERSION_COMMIT = "88d1b438c9c44428e0f7578c6ac3cd8e6d4edeae"
LICENSE_REPOSITORY = "https://github.com/FlagOpen/FlagEmbedding"
LICENSE_COMMIT = "fd1a2bdf69488ffebe0327999d4400d8c8058a0b"
TOKENIZER_SHA = "48cea5d44424912a6fd1ea647bf4fe50b55ab8b1e5879c3275f80e339e8fae26"
MODEL_SHA = "ba917299b47626c45a420351c179948adb1b24354c89601c265570d43d8554ab"
LICENSE_SHA = "587a673933425dbc36ec61268d3b954051b2d3ef3c9b322ede357976055ffdd5"
POOLING_CONFIG_SHA = "aaa8861589f80c961a03cc86c7eeaef7605c1676b9ab55329d33a304738769c6"


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def resource(path: Path, role: str) -> dict[str, object]:
    return {
        "role": role,
        "path": path.name,
        "size": path.stat().st_size,
        "sha256": sha256(path),
    }


def manifest(source: Path) -> dict[str, object]:
    model = source / "model.onnx"
    tokenizer = source / "tokenizer.json"
    license_file = source / "LICENSE"
    required = (model, tokenizer, license_file)
    missing = [path.name for path in required if not path.is_file()]
    if missing:
        raise FileNotFoundError("missing BGE source resources: " + ", ".join(missing))
    source_manifest_path = source / "build-manifest.json"
    if not source_manifest_path.is_file():
        raise FileNotFoundError("conversion build-manifest.json is required")
    source_manifest = json.loads(source_manifest_path.read_text(encoding="utf-8"))
    if source_manifest.get("source") != MODEL_ID or source_manifest.get("revision") != UPSTREAM_COMMIT:
        raise ValueError("conversion manifest does not identify the pinned upstream model revision")
    source_model = source_manifest.get("files", {}).get("onnx/model.onnx", {})
    if source_model.get("sha256") != MODEL_SHA or source_model.get("bytes") != model.stat().st_size:
        raise ValueError("ONNX resource does not match the pinned conversion manifest")
    tokenizer_digest = sha256(tokenizer)
    if tokenizer_digest != TOKENIZER_SHA:
        raise ValueError(f"tokenizer SHA mismatch: {tokenizer_digest}")
    if sha256(model) != MODEL_SHA:
        raise ValueError("ONNX SHA mismatch")
    if sha256(license_file) != LICENSE_SHA:
        raise ValueError("license SHA mismatch")
    return {
        "schema": "kah-embedding-model/v1",
        "modelType": "text-embedding",
        "id": MODEL_ID,
        "version": "1.5-onnx-fp32",
        "upstreamCommit": UPSTREAM_COMMIT,
        "license": "MIT",
        "dimension": 512,
        "maxTokens": 512,
        "tokenizer": {
            "configHash": tokenizer_digest,
            "type": "huggingface-tokenizer",
            "runtimeVersion": "0.21.0",
        },
        "pooling": {
            "type": "cls",
            "normalize": True,
        },
        "poolingConfig": {
            "path": "1_Pooling/config.json",
            "sha256": POOLING_CONFIG_SHA,
            "upstreamCommit": UPSTREAM_COMMIT,
            "mode": "cls_token",
        },
        "queryPrefix": "为这个句子生成表示以用于检索相关文章：",
        "passagePrefix": "",
        "runtime": {
            "name": "onnxruntime",
            "version": "1.20.0",
            "device": "cpu",
        },
        "files": [
            resource(model, "model"),
            resource(tokenizer, "tokenizer"),
            resource(license_file, "license"),
        ],
        "conversionProvenance": {
            "repository": CONVERSION_REPOSITORY,
            "commit": CONVERSION_COMMIT,
            "sourceModel": MODEL_ID,
            "sourceCommit": UPSTREAM_COMMIT,
            "sourceTokenizerSha256": tokenizer_digest,
            "licenseRepository": LICENSE_REPOSITORY,
            "licenseCommit": LICENSE_COMMIT,
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
            "upstreamPoolingConfigSha256": POOLING_CONFIG_SHA,
        },
    }


def write_deterministic_zip(output: Path, source: Path, package_manifest: dict[str, object]) -> None:
    output.parent.mkdir(parents=True, exist_ok=True)
    raw_manifest = json.dumps(package_manifest, ensure_ascii=False, indent=2, sort_keys=True).encode("utf-8") + b"\n"
    with zipfile.ZipFile(output, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
        entries = [("LICENSE", source / "LICENSE"), ("manifest.json", None), ("model.onnx", source / "model.onnx"), ("tokenizer.json", source / "tokenizer.json")]
        for name, path in entries:
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            info.create_system = 0
            info.external_attr = 0o600 << 16
            payload = raw_manifest if path is None else path.read_bytes()
            archive.writestr(info, payload)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    package_manifest = manifest(args.source)
    write_deterministic_zip(args.output, args.source, package_manifest)
    print(json.dumps({
        "output": str(args.output),
        "bytes": args.output.stat().st_size,
        "sha256": sha256(args.output),
        "manifest": package_manifest,
    }, ensure_ascii=False, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
