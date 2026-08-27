#!/usr/bin/env python3
"""Run a deterministic bilingual retrieval quality and latency benchmark."""

import argparse
import json
import math
import sys
import tempfile
import time
from pathlib import Path

WORKER_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(WORKER_ROOT / "src"))

from knowledge_worker.index import HybridIndex


def percentile(values, fraction):
    ordered = sorted(values)
    rank = max(0, min(len(ordered) - 1, int(math.ceil(fraction * len(ordered))) - 1))
    return ordered[rank]


def load_fixture(path):
    with path.open("r", encoding="utf-8") as stream:
        fixture = json.load(stream)
    if not fixture.get("documents") or not fixture.get("queries"):
        raise ValueError("fixture must contain documents and queries")
    return fixture


def build_index(fixture, data_root):
    index = HybridIndex(data_root)
    chunks = []
    for document in fixture["documents"]:
        chunks.append({
                "id": document["id"] + "-chunk",
                "documentId": document["id"],
                "text": document["text"],
                "location": {"kind": "fixture"},
                "contentHash": document["id"],
            })
    index.rebuild(fixture["libraryId"], chunks)
    return index


def scale_fixture(fixture, chunk_count):
    if chunk_count < len(fixture["documents"]):
        raise ValueError("stress chunk count must be at least the fixture document count")
    documents = []
    source_ids = []
    for index in range(chunk_count):
        source = fixture["documents"][index % len(fixture["documents"])]
        document_id = "{}-scale-{:07d}".format(source["id"], index)
        documents.append({"id": document_id, "text": source["text"]})
        source_ids.append(source["id"])
    queries = []
    for query in fixture["queries"]:
        relevant = {
            document_id
            for document_id, source_id in zip(
                (document["id"] for document in documents), source_ids
            )
            if source_id in query["relevant"]
        }
        queries.append({"query": query["query"], "relevant": sorted(relevant)})
    scaled = dict(fixture)
    scaled["documents"] = documents
    scaled["queries"] = queries
    return scaled


def run_suite(fixture, data_root, top_k, iterations, warmup):
    index = build_index(fixture, data_root)
    for _ in range(warmup):
        for mode in ("lexical", "vector", "hybrid"):
            for query in fixture["queries"]:
                index.search(query["query"], [fixture["libraryId"]], top_k, mode)
    reports = {
        mode: measure(index, fixture, top_k, iterations, mode)
        for mode in ("lexical", "vector", "hybrid")
    }
    return index.backend, reports


def measure(index, fixture, top_k, iterations, mode):
    samples = []
    hits = 0
    queries = fixture["queries"]
    for _ in range(iterations):
        for query in queries:
            started = time.perf_counter()
            response = index.search(
                query["query"],
                [fixture["libraryId"]],
                top_k,
                mode,
            )
            samples.append((time.perf_counter() - started) * 1000)
            returned = {item["documentId"] for item in response["results"]}
            if returned.intersection(query["relevant"]):
                hits += 1
    return {
        "retrievalMode": mode,
        "queryCount": len(queries) * iterations,
        "recallAtK": hits / float(max(1, len(queries) * iterations)),
        "p50Ms": percentile(samples, 0.50),
        "p95Ms": percentile(samples, 0.95),
        "p99Ms": percentile(samples, 0.99),
        "minMs": min(samples),
        "maxMs": max(samples),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fixture",
        type=Path,
        default=Path(__file__).with_name("retrieval_eval.json"),
    )
    parser.add_argument("--iterations", type=int, default=100)
    parser.add_argument("--warmup", type=int, default=10)
    parser.add_argument(
        "--stress-chunks",
        type=int,
        help="also run a deterministic replicated-fixture stress suite at this chunk count",
    )
    parser.add_argument(
        "--stress-iterations",
        type=int,
        default=1,
        help="iterations for the optional stress suite",
    )
    parser.add_argument("--top-k", type=int, default=10)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if args.iterations < 1 or args.warmup < 0 or args.stress_iterations < 1:
        parser.error("iterations must be positive and warmup cannot be negative")
    if args.top_k < 1 or args.top_k > 100:
        parser.error("top-k must be between 1 and 100")
    if args.stress_chunks is not None and args.stress_chunks < 1:
        parser.error("stress-chunks must be positive")

    fixture = load_fixture(args.fixture.resolve())
    with tempfile.TemporaryDirectory(prefix="kah-retrieval-benchmark-") as data_root:
        backend, reports = run_suite(
            fixture, data_root, args.top_k, args.iterations, args.warmup
        )

    report = {
        "fixture": str(args.fixture.resolve()),
        "libraryId": fixture["libraryId"],
        "documentCount": len(fixture["documents"]),
        "chunkCount": len(fixture["documents"]),
        "queryCount": len(fixture["queries"]),
        "topK": args.top_k,
        "iterations": args.iterations,
        "warmup": args.warmup,
        "backend": backend,
        "recallTarget": 0.80,
        "referenceScale": len(fixture["documents"]) >= 1000000,
        "modes": reports,
    }
    if args.stress_chunks is not None:
        stress_fixture = scale_fixture(fixture, args.stress_chunks)
        with tempfile.TemporaryDirectory(prefix="kah-retrieval-stress-") as data_root:
            stress_backend, stress_reports = run_suite(
                stress_fixture,
                data_root,
                args.top_k,
                args.stress_iterations,
                args.warmup,
            )
        report["stress"] = {
            "backend": stress_backend,
            "documentCount": len(stress_fixture["documents"]),
            "chunkCount": len(stress_fixture["documents"]),
            "queryCount": len(stress_fixture["queries"]),
            "iterations": args.stress_iterations,
            "referenceTargetChunks": 1000000,
            "referenceScale": len(stress_fixture["documents"]) >= 1000000,
            "modes": stress_reports,
        }
    encoded = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    print(encoded, end="")
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded, encoding="utf-8")


if __name__ == "__main__":
    main()
