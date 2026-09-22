"""Exercise the pinned BGE package through the real Worker embedding path."""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import socket
import tempfile
import zipfile
from pathlib import Path
from unittest.mock import patch

from knowledge_worker.embedding import BGE_ID, EmbeddingRegistry
from knowledge_worker.retrieval import UnifiedRetrievalIndex


def norm(vector: list[float]) -> float:
    return math.sqrt(sum(float(value) * float(value) for value in vector))


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--package", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    root = Path(tempfile.mkdtemp(prefix="bge-offline-", dir=args.output.parent))
    registry = EmbeddingRegistry(root)
    evidence: dict[str, object] = {
        "package": {
            "path": str(args.package),
            "bytes": args.package.stat().st_size,
            "sha256": hashlib.sha256(args.package.read_bytes()).hexdigest(),
            "entries": zipfile.ZipFile(args.package).namelist(),
        },
        "offline": {"networkAttempts": 0, "environment": {"HF_HUB_OFFLINE": "1", "TRANSFORMERS_OFFLINE": "1"}},
    }

    def blocked_connect(*_args, **_kwargs):
        evidence["offline"]["networkAttempts"] = int(evidence["offline"]["networkAttempts"]) + 1  # type: ignore[index]
        raise AssertionError("network access attempted during offline Worker validation")

    with patch.dict("os.environ", {"HF_HUB_OFFLINE": "1", "TRANSFORMERS_OFFLINE": "1"}), patch.object(socket.socket, "connect", blocked_connect):
        imported = registry.import_package(str(args.package))
        health = registry.health(BGE_ID)
        enabled = registry.enable(BGE_ID, True)
        engine, active = registry.active()
        if engine is None:
            raise RuntimeError("BGE model was not active after enable")
        long_text = "中文技术资料与 architecture retrieval " * 200
        embedded = engine.embed(["知识库项目架构", "项目架构和语义检索", long_text], is_query=True)
        if len(embedded["vectors"]) != 3 or any(len(vector) != 512 for vector in embedded["vectors"]):
            raise RuntimeError("unexpected BGE vector dimensions")
        if not embedded["truncated"][-1]:
            raise RuntimeError("long input was not reported as truncated")
        chunks = [
            {"id": "knowledge-architecture", "text": "项目架构生成会读取代码并输出技术栈和架构图。", "sourceType": "knowledge", "reviewStatus": "stable", "libraryId": "fixture", "documentId": "knowledge-doc"},
            {"id": "raw-embedding", "text": "Embedding 模型使用 CPU ONNX Runtime 提高中文技术资料检索准确性。", "sourceType": "raw", "reviewStatus": "unreviewed", "libraryId": "fixture", "documentId": "raw-doc"},
            {"id": "architecture-evidence", "text": "services/worker 使用 EmbeddingRegistry 管理本地模型，model.onnx 通过固定哈希校验。", "sourceType": "architecture", "reviewStatus": "derived", "libraryId": "fixture", "projectId": "project-1", "documentId": "architecture-doc", "filePath": "services/worker/src/knowledge_worker/embedding.py", "lineStart": 1, "lineEnd": 10},
        ]
        index = UnifiedRetrievalIndex(root, registry)
        generation = index.rebuild("fixture", chunks, model_id=BGE_ID, complete=True)
        formal = index.search("项目架构", {"allowedLibraryIds": ["fixture"], "libraryIds": ["fixture"], "sourceType": "knowledge"}, top_k=3)
        raw = index.search("ONNX Runtime", {"allowedLibraryIds": ["fixture"], "libraryIds": ["fixture"], "sourceType": "raw", "allowRaw": True}, top_k=3)
        architecture = index.search("EmbeddingRegistry", {"allowedLibraryIds": ["fixture"], "allowedProjects": ["project-1"], "allowedProjectIds": ["project-1"], "libraryIds": ["fixture"], "sourceType": "architecture"}, top_k=3)
        evidence.update({
            "imported": imported,
            "health": health,
            "enabled": enabled,
            "active": active,
            "embedding": {
                "modelId": embedded["modelId"],
                "version": embedded["version"],
                "dimension": embedded["dimension"],
                "tokenCount": embedded["tokenCount"],
                "truncated": embedded["truncated"],
                "norms": [norm(vector) for vector in embedded["vectors"]],
                "configHash": embedded["configHash"],
            },
            "generation": {key: generation[key] for key in ("generation", "count", "semanticAvailable", "modelId", "modelVersion", "configHash", "dimension", "corpusComplete")},
            "search": {
                "formalKnowledge": {"ids": [item["id"] for item in formal["results"]], "semanticAvailable": formal["semanticAvailable"], "degraded": formal["degraded"]},
                "rawEvidence": {"ids": [item["id"] for item in raw["results"]], "semanticAvailable": raw["semanticAvailable"], "degraded": raw["degraded"]},
                "architectureEvidence": {"ids": [item["id"] for item in architecture["results"]], "semanticAvailable": architecture["semanticAvailable"], "degraded": architecture["degraded"]},
            },
        })
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(json.dumps(evidence, ensure_ascii=False, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
