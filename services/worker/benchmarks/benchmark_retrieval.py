#!/usr/bin/env python3
"""Run a deterministic bilingual retrieval quality and latency benchmark."""

import argparse
import hashlib
import json
import math
import os
import sys
import tempfile
import time
import platform
import zipfile
from pathlib import Path

WORKER_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(WORKER_ROOT / "src"))

from knowledge_worker.retrieval import UnifiedRetrievalIndex
from knowledge_worker.embedding import EmbeddingRegistry
from knowledge_worker.index import HybridIndex


class BatchedNativeHybridIndex(HybridIndex):
    """Benchmark-only adapter for HybridIndex's 128-item embedding limit.

    The product index currently prepares one list in a single call.  The
    embedding engine deliberately caps each call at 128 texts, so the native
    stress harness batches the same product path without changing application
    code.  Any failed batch is fatal to a required-native benchmark rather
    than silently creating a partial keyword-only table.
    """

    _EMBED_BATCH_SIZE = 128

    def _prepare_chunks(self, chunks, is_query=False):
        values = list(chunks or [])
        if len(values) <= self._EMBED_BATCH_SIZE:
            return super()._prepare_chunks(values, is_query=is_query)
        prepared = []
        semantic = {}
        for start in range(0, len(values), self._EMBED_BATCH_SIZE):
            batch, batch_semantic = super()._prepare_chunks(
                values[start : start + self._EMBED_BATCH_SIZE],
                is_query=is_query,
            )
            if not batch_semantic.get("semanticAvailable"):
                raise RuntimeError(
                    "native stress batch lost semantic embedding: "
                    + str(batch_semantic.get("degradedReason", "unknown error"))
                )
            prepared.extend(batch)
            semantic = batch_semantic
        return prepared, semantic


def percentile(values, fraction):
    ordered = sorted(values)
    rank = max(0, min(len(ordered) - 1, int(math.ceil(fraction * len(ordered))) - 1))
    return ordered[rank]


def process_memory_bytes():
    """Return the current process working set when the platform exposes it."""

    if os.name != "nt":
        return None
    try:
        import ctypes
        from ctypes import wintypes

        class ProcessMemoryCounters(ctypes.Structure):
            _fields_ = [
                ("cb", wintypes.DWORD),
                ("PageFaultCount", wintypes.DWORD),
                ("PeakWorkingSetSize", ctypes.c_size_t),
                ("WorkingSetSize", ctypes.c_size_t),
                ("QuotaPeakPagedPoolUsage", ctypes.c_size_t),
                ("QuotaPagedPoolUsage", ctypes.c_size_t),
                ("QuotaPeakNonPagedPoolUsage", ctypes.c_size_t),
                ("QuotaNonPagedPoolUsage", ctypes.c_size_t),
                ("PagefileUsage", ctypes.c_size_t),
                ("PeakPagefileUsage", ctypes.c_size_t),
                ("PrivateUsage", ctypes.c_size_t),
            ]

        counters = ProcessMemoryCounters()
        counters.cb = ctypes.sizeof(ProcessMemoryCounters)
        process = ctypes.windll.kernel32.GetCurrentProcess()
        get_counters = ctypes.windll.psapi.GetProcessMemoryInfo
        get_counters.argtypes = [ctypes.c_void_p, ctypes.POINTER(ProcessMemoryCounters), wintypes.DWORD]
        get_counters.restype = wintypes.BOOL
        if not get_counters(process, ctypes.byref(counters), counters.cb):
            return None
        return int(counters.WorkingSetSize)
    except (AttributeError, OSError, TypeError, ValueError):
        return None


def machine_info():
    return {
        "platform": platform.platform(),
        "processor": platform.processor(),
        "machine": platform.machine(),
        "pythonVersion": platform.python_version(),
        "pythonImplementation": platform.python_implementation(),
        "cpuCount": os.cpu_count(),
        "processMemoryMeasurement": "Windows GetProcessMemoryInfo WorkingSetSize",
    }


def sha256_file(path):
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def load_real_validation(path):
    with path.open("r", encoding="utf-8") as stream:
        value = json.load(stream)
    active = value.get("active", {})
    health = value.get("health", {})
    package = value.get("package", {})
    if not active.get("semanticAvailable") or not health.get("available"):
        raise ValueError("real BGE validation evidence is not semantic-available")
    if not active.get("modelId") or not active.get("modelVersion"):
        raise ValueError("real BGE validation evidence is missing model identity")
    return {
        "path": str(path.resolve()),
        "modelId": str(active["modelId"]),
        "modelVersion": str(active["modelVersion"]),
        "configHash": str(active.get("configHash", "")),
        "dimension": int(active.get("dimension", 0) or 0),
        "semanticAvailable": bool(active.get("semanticAvailable")),
        "healthAvailable": bool(health.get("available")),
        "packageSha256": str(package.get("sha256", "")),
    }


def embedding_package_info(path):
    path = path.resolve()
    if not path.is_file():
        raise ValueError("embedding package does not exist: " + str(path))
    package_info = {
        "path": str(path),
        "bytes": path.stat().st_size,
        "sha256": sha256_file(path),
    }
    with zipfile.ZipFile(path) as archive:
        manifest = json.loads(archive.read("manifest.json").decode("utf-8"))
    package_info.update({
        "modelId": str(manifest.get("id", "")),
        "modelVersion": str(manifest.get("version", "")),
        "dimension": int(manifest.get("dimension", 0) or 0),
        "runtime": dict(manifest.get("runtime", {})),
        "pooling": dict(manifest.get("pooling", {})),
    })
    return package_info


def load_fixture(path):
    with path.open("r", encoding="utf-8") as stream:
        fixture = json.load(stream)
    if not fixture.get("documents") or not fixture.get("queries"):
        raise ValueError("fixture must contain documents and queries")
    return fixture


def build_index(fixture, data_root, embedding_package=None, native_lance=False):
    data_root = Path(data_root)
    process_before = process_memory_bytes()
    registry = EmbeddingRegistry(data_root)
    model_evidence = {}
    if embedding_package is not None:
        package = Path(embedding_package).resolve()
        import_started = time.perf_counter()
        imported = registry.import_package(str(package))
        import_elapsed = (time.perf_counter() - import_started) * 1000
        enable_started = time.perf_counter()
        enabled = registry.enable(str(imported["id"]), True)
        enable_elapsed = (time.perf_counter() - enable_started) * 1000
        health_started = time.perf_counter()
        health = registry.health(str(imported["id"]))
        health_elapsed = (time.perf_counter() - health_started) * 1000
        if not health.get("available"):
            raise RuntimeError("embedding package health check failed: " + str(health.get("error", "unknown error")))
        model_dir = data_root / "models" / "embedding_models" / str(imported["id"]).replace("/", "__")
        manifest = json.loads((model_dir / "manifest.json").read_text(encoding="utf-8"))
        model_file_bytes = sum(
            int(item.get("size", 0) or 0)
            for item in manifest.get("files", [])
            if item.get("role") == "model"
        )
        process_after_model = process_memory_bytes()
        model_evidence = {
            "imported": imported,
            "enabled": enabled,
            "health": health,
            "timingMs": {
                "packageImport": import_elapsed,
                "enable": enable_elapsed,
                "healthAndRuntimeLoad": health_elapsed,
            },
            "memory": {
                "processWorkingSetBeforeModelBytes": process_before,
                "processWorkingSetAfterModelBytes": process_after_model,
                "processWorkingSetDeltaModelBytes": (
                    process_after_model - process_before
                    if process_before is not None and process_after_model is not None
                    else None
                ),
                "modelResourceBytes": model_file_bytes,
                "installedResourceBytes": int(imported.get("sizeBytes", 0) or 0),
                "runtimeBytes": int(health.get("runtimeBytes", 0) or 0),
                "packageBytes": package.stat().st_size,
                "measurement": "modelResourceBytes/runtimeBytes are package metadata; process delta is resident working-set evidence",
            },
        }
    index = BatchedNativeHybridIndex(data_root) if native_lance else UnifiedRetrievalIndex(data_root, registry)
    chunks = []
    for document in fixture["documents"]:
        chunks.append({
                "id": document["id"] + "-chunk",
                "documentId": document["id"],
                "text": document["text"],
                "location": {"kind": "fixture"},
                "contentHash": document["id"],
                "sourceType": "raw",
                "reviewStatus": "unreviewed",
                "libraryId": fixture["libraryId"],
                "sourceURI": "fixture://" + document["id"],
            })
    index_started = time.perf_counter()
    if native_lance:
        generation = index.rebuild(fixture["libraryId"], chunks)
    else:
        generation = index.rebuild(fixture["libraryId"], chunks, complete=True)
    process_after_index = process_memory_bytes()
    index.benchmark_metadata = {
        "indexBuildMs": (time.perf_counter() - index_started) * 1000,
        "indexChunkCount": len(chunks),
        "generation": generation.get("generation", generation.get("version", "")),
        "backend": getattr(index, "backend", "portable-json"),
        "processWorkingSetBeforeBytes": process_before,
        "processWorkingSetAfterIndexBytes": process_after_index,
        "processWorkingSetDeltaIndexBytes": (
            process_after_index - process_before
            if process_before is not None and process_after_index is not None
            else None
        ),
        "model": model_evidence,
    }
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


def search_index(index, query, filters, top_k, mode):
    """Use the common benchmark filters with either retrieval implementation."""

    if isinstance(index, HybridIndex):
        return index.search(query, list(filters["allowedLibraryIds"]), top_k, mode)
    return index.search(query, filters, top_k, mode)


def run_suite(
    fixture,
    data_root,
    top_k,
    iterations,
    warmup,
    require_lancedb=False,
    embedding_package=None,
    require_semantic=False,
    return_metadata=False,
    native_lance=False,
):
    index = build_index(fixture, data_root, embedding_package, native_lance)
    filters = {"libraryIds": [fixture["libraryId"]], "allowedLibraryIds": [fixture["libraryId"]], "allowRaw": True}
    probe = search_index(index, fixture["queries"][0]["query"], filters, top_k, "hybrid")
    if require_lancedb and probe.get("searchBackend") != "lancedb":
        raise RuntimeError("native LanceDB search was required but unavailable; refusing to benchmark the portable fallback")
    warmup_started = time.perf_counter()
    for _ in range(warmup):
        for mode in ("lexical", "vector", "hybrid"):
            for query in fixture["queries"]:
                search_index(index, query["query"], filters, top_k, mode)
    warmup_elapsed = (time.perf_counter() - warmup_started) * 1000
    measurement_started = time.perf_counter()
    reports = {
        mode: measure(index, fixture, top_k, iterations, mode)
        for mode in ("lexical", "vector", "hybrid")
    }
    validate_mode_reports(reports)
    if require_semantic:
        validate_semantic_reports(reports)
    result = (getattr(index, "backend", "portable-json"), probe.get("searchBackend", "portable-json"), reports)
    if return_metadata:
        metadata = {
            "index": getattr(index, "benchmark_metadata", {}),
            "warmupMs": warmup_elapsed,
            "measurementMs": (time.perf_counter() - measurement_started) * 1000,
            "processWorkingSetAfterSuiteBytes": process_memory_bytes(),
            "warmupIterations": warmup,
            "measuredIterations": iterations,
        }
        return (*result, metadata)
    return result


def measure(index, fixture, top_k, iterations, mode):
    samples = []
    recall_sum = 0.0
    reciprocal_sum = 0.0
    response_diagnostics = []
    queries = fixture["queries"]
    for iteration in range(iterations):
        for query_index, query in enumerate(queries):
            started = time.perf_counter()
            response = search_index(
                index,
                query["query"],
                {"libraryIds": [fixture["libraryId"]], "allowedLibraryIds": [fixture["libraryId"]], "allowRaw": True},
                top_k,
                mode,
            )
            samples.append((time.perf_counter() - started) * 1000)
            required_response_fields = ("retrievalMode", "semanticAvailable", "keywordOnly", "degraded", "degradedReason")
            missing_fields = [field for field in required_response_fields if field not in response]
            if missing_fields:
                raise AssertionError("retrieval response omitted benchmark diagnostics: " + ", ".join(missing_fields))
            response_diagnostics.append({
                "iteration": iteration,
                "queryIndex": query_index,
                "requestedMode": mode,
                "effectiveRetrievalMode": response.get("retrievalMode", ""),
                "semanticAvailable": bool(response.get("semanticAvailable", False)),
                "keywordOnly": bool(response.get("keywordOnly", False)),
                "degraded": bool(response.get("degraded", False)),
                "degradedReason": str(response.get("degradedReason", "") or ""),
                "searchBackend": str(response.get("searchBackend", "") or ""),
            })
            returned = [item.get("documentId") or item.get("id") for item in response["results"][:top_k]]
            relevant = set(query["relevant"])
            hits = [position for position, document_id in enumerate(returned, 1) if document_id in relevant]
            # Recall@10 is the fraction of all annotated relevant documents
            # retrieved in the top ten. MRR@10 uses the first relevant rank.
            recall_sum += len(set(returned).intersection(relevant)) / float(max(1, len(relevant)))
            reciprocal_sum += (1.0 / hits[0]) if hits else 0.0
    effective_modes = sorted({item["effectiveRetrievalMode"] for item in response_diagnostics})
    semantic_values = sorted({item["semanticAvailable"] for item in response_diagnostics})
    keyword_only_values = sorted({item["keywordOnly"] for item in response_diagnostics})
    degraded_values = sorted({item["degraded"] for item in response_diagnostics})
    degraded_reasons = sorted({item["degradedReason"] for item in response_diagnostics if item["degradedReason"]})
    return {
        "requestedMode": mode,
        "effectiveRetrievalMode": effective_modes[0] if len(effective_modes) == 1 else "mixed",
        "effectiveRetrievalModes": effective_modes,
        "effectiveModeConsistent": len(effective_modes) == 1,
        "semanticAvailable": semantic_values[0] if len(semantic_values) == 1 else None,
        "semanticAvailableValues": semantic_values,
        "keywordOnly": keyword_only_values[0] if len(keyword_only_values) == 1 else None,
        "keywordOnlyValues": keyword_only_values,
        "degraded": degraded_values[0] if len(degraded_values) == 1 else None,
        "degradedValues": degraded_values,
        "degradedReasons": degraded_reasons,
        "responseDiagnostics": response_diagnostics,
        "queryCount": len(queries) * iterations,
        "recallAt10": recall_sum / float(max(1, len(queries) * iterations)),
        "mrrAt10": reciprocal_sum / float(max(1, len(queries) * iterations)),
        "p50Ms": percentile(samples, 0.50),
        "p95Ms": percentile(samples, 0.95),
        "p99Ms": percentile(samples, 0.99),
        "minMs": min(samples),
        "maxMs": max(samples),
        "searchBackends": sorted({item.get("searchBackend", "") for item in response_diagnostics}),
    }


def validate_mode_reports(reports):
    """Fail fast if a benchmark loses the worker's effective mode metadata."""

    for requested_mode, report in reports.items():
        diagnostics = report.get("responseDiagnostics", [])
        if len(diagnostics) != report.get("queryCount"):
            raise AssertionError("benchmark response diagnostics do not cover every measured response")
        for item in diagnostics:
            if item.get("requestedMode") != requested_mode:
                raise AssertionError("benchmark response requestedMode does not match its suite")
        effective_modes = sorted({item.get("effectiveRetrievalMode", "") for item in diagnostics})
        if not effective_modes or "" in effective_modes:
            raise AssertionError("benchmark response has no effective retrieval mode")
        if report.get("effectiveRetrievalModes") != effective_modes:
            raise AssertionError("benchmark effective retrieval modes were summarized incorrectly")
        if len(effective_modes) != 1 and report.get("effectiveRetrievalMode") != "mixed":
            raise AssertionError("inconsistent effective retrieval modes must be reported as mixed")


def validate_semantic_reports(reports):
    """Require genuine semantic execution for vector and hybrid suites."""

    for requested_mode in ("vector", "hybrid"):
        report = reports.get(requested_mode, {})
        if report.get("semanticAvailable") is not True:
            raise AssertionError(requested_mode + " benchmark did not have semanticAvailable=true")
        if report.get("keywordOnly") is not False:
            raise AssertionError(requested_mode + " benchmark fell back to keywordOnly=true")
        if report.get("degraded") is not False:
            raise AssertionError(requested_mode + " benchmark was degraded")
        if report.get("effectiveRetrievalMode") != requested_mode:
            raise AssertionError(
                requested_mode
                + " benchmark effective retrieval mode was "
                + str(report.get("effectiveRetrievalMode"))
            )


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
    parser.add_argument(
        "--require-lancedb",
        "--native-lance-required",
        dest="native_lance_required",
        action="store_true",
        help="build/search through HybridIndex and fail unless every query uses native LanceDB",
    )
    parser.add_argument(
        "--embedding-package",
        type=Path,
        help="import and enable this verified offline CPU embedding package in each isolated suite",
    )
    parser.add_argument(
        "--model-validation",
        type=Path,
        help="existing real-model validation JSON to bind to the benchmark evidence",
    )
    parser.add_argument(
        "--require-semantic",
        action="store_true",
        help="fail unless vector and hybrid measured responses remain semantic and non-degraded",
    )
    args = parser.parse_args()
    if args.iterations < 1 or args.warmup < 0 or args.stress_iterations < 1:
        parser.error("iterations must be positive and warmup cannot be negative")
    if args.top_k < 1 or args.top_k > 100:
        parser.error("top-k must be between 1 and 100")
    if args.stress_chunks is not None and args.stress_chunks < 1:
        parser.error("stress-chunks must be positive")
    if args.require_semantic and args.embedding_package is None:
        parser.error("--require-semantic requires --embedding-package")

    fixture = load_fixture(args.fixture.resolve())
    validation = None
    package_info = None
    if args.model_validation:
        validation = load_real_validation(args.model_validation.resolve())
    if args.embedding_package:
        package_info = embedding_package_info(args.embedding_package)
        if validation:
            if validation["packageSha256"] and validation["packageSha256"].lower() != package_info["sha256"].lower():
                raise ValueError("embedding package SHA-256 does not match --model-validation evidence")
            if validation["modelId"] and validation["modelId"] != package_info["modelId"]:
                raise ValueError("embedding package model id does not match --model-validation evidence")
    with tempfile.TemporaryDirectory(prefix="kah-retrieval-benchmark-") as data_root:
        backend, search_backend, reports, suite_metadata = run_suite(
            fixture,
            data_root,
            args.top_k,
            args.iterations,
            args.warmup,
            args.native_lance_required,
            args.embedding_package,
            args.require_semantic,
            True,
            args.native_lance_required,
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
        "searchBackend": search_backend,
        "nativeLanceRequired": args.native_lance_required,
        "pythonVersion": platform.python_version(),
        "metrics": {"recall": "Recall@10 = average(|top10 ∩ relevant| / |relevant|)", "mrr": "MRR@10 = average(1 / first relevant rank in top10)"},
        "referenceScale": len(fixture["documents"]) >= 20000,
        "qualityComparable": True,
        "performanceScope": "hybrid/lexical/vector as reported per effective mode",
        "machine": machine_info(),
        "modes": reports,
        "suite": suite_metadata,
    }
    if package_info:
        report["embeddingPackage"] = package_info
    if validation:
        report["realModelValidation"] = validation
    if args.stress_chunks is not None:
        stress_fixture = scale_fixture(fixture, args.stress_chunks)
        with tempfile.TemporaryDirectory(prefix="kah-retrieval-stress-") as data_root:
            stress_backend, stress_search_backend, stress_reports, stress_metadata = run_suite(
                stress_fixture,
                data_root,
                args.top_k,
                args.stress_iterations,
                args.warmup,
                args.native_lance_required,
                args.embedding_package,
                args.require_semantic,
                True,
                args.native_lance_required,
            )
        report["stress"] = {
            "backend": stress_backend,
            "searchBackend": stress_search_backend,
            "documentCount": len(stress_fixture["documents"]),
            "chunkCount": len(stress_fixture["documents"]),
            "queryCount": len(stress_fixture["queries"]),
            "iterations": args.stress_iterations,
            "referenceTargetChunks": 20000,
            "referenceScale": len(stress_fixture["documents"]) >= 20000,
            "qualityComparable": False,
            "qualityAcceptance": "not_measured",
            "corpusKind": "replicated-fixture-stress",
            "qualityComparableReason": "The stress corpus replicates fixture documents; each relevant set is intentionally very large, so Recall@10/MRR@10 are performance-harness metadata and are not quality-acceptance measurements.",
            "performanceScope": "CPU semantic/vector/hybrid performance when an embedding package is supplied; inspect each mode's effectiveRetrievalMode and semanticAvailable fields",
            "modes": stress_reports,
            "suite": stress_metadata,
        }
    encoded = json.dumps(report, ensure_ascii=False, indent=2) + "\n"
    print(encoded, end="")
    if args.output:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(encoded, encoding="utf-8")


if __name__ == "__main__":
    main()
