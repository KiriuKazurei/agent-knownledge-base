import json
import os
import sys
import traceback
from concurrent.futures import ThreadPoolExecutor
import threading

from . import __version__, ocr_plugin
from .embedding import EmbeddingRegistry
from .index import HybridIndex
from .retrieval import UnifiedRetrievalIndex
from .ocr_resources import health_report
from .parsers import parse_document


def _configure_utf8_stdio():
    for stream in (sys.stdin, sys.stdout, sys.stderr):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8", errors="replace", newline="\n")


_configure_utf8_stdio()

class RpcServer:
    def __init__(self):
        data_root = os.environ.get("KAH_DATA_ROOT", ".run-data")
        self.index = HybridIndex(data_root)
        self.embeddings = self.index.embeddings
        self.retrieval = UnifiedRetrievalIndex(data_root, self.embeddings)
        self.methods = {
            "health": self.health,
            "ocr_health": self.ocr_health,
            "ocr_plugin_status": lambda _: ocr_plugin.status(),
            "ocr_plugin_install": lambda params: ocr_plugin.install(params["path"]),
            "ocr_plugin_enabled": lambda params: ocr_plugin.set_enabled(params["enabled"]),
            "ocr_plugin_uninstall": lambda _: ocr_plugin.uninstall(),
            "parse": self.parse,
            "index_upsert": self.index_upsert,
            "index_delete": self.index_delete,
            "index_rebuild": self.index_rebuild,
            "search": self.search,
            "retrieval_rebuild": self.retrieval_rebuild,
            "retrieval_upsert": self.retrieval_upsert,
            "retrieval_delete": self.retrieval_delete,
            "retrieval_search": self.retrieval_search,
            "retrieval_cancel": self.retrieval_cancel,
            "retrieval_progress": self.retrieval_progress,
            "retrieval_status": self.retrieval_status,
            "embedding_models": lambda _: {"models": self.embeddings.list()},
            "embedding_install": lambda params: self.embeddings.import_package(params["path"]),
            "embedding_install_online": lambda params: self.embeddings.install_online(params["modelId"]),
            "embedding_enable": lambda params: self.embeddings.enable(params["modelId"], params["enabled"]),
            "embedding_uninstall": self.embedding_uninstall,
            "embedding_health": lambda params: self.embeddings.health(params["modelId"]),
            "embedding_embed": self.embedding_embed,
            # Stable model RPC names used by the Go client. The embedding_*
            # aliases remain for development builds that already use them.
            "model_load": lambda params: self.embeddings.load(params["modelId"]),
            "model_unload": lambda params: self.embeddings.unload(params["modelId"]),
            "model_health": lambda params: self.embeddings.health(params["modelId"]),
            "model_embed": self.embedding_embed,
            "index_progress": self.index_progress,
        }

    def health(self, _params):
        active, semantic = self.embeddings.active()
        return {"status": "ok", "version": __version__, "indexBackend": self.index.backend,
                "semanticAvailable": bool(active), "degradedReason": semantic.get("degradedReason", ""),
                "embeddingModels": self.embeddings.list()}

    def ocr_health(self, _params):
        return health_report()

    def parse(self, params):
        return parse_document(
            params["path"],
            params["documentId"],
            params.get("mediaType", ""),
            params.get("originalName", ""),
            params.get("ocrMode"),
        )

    def index_upsert(self, params):
        result = self.index.upsert(params["libraryId"], params["documentId"], params.get("chunks", []))
        # Existing document import callers automatically keep the unified raw
        # corpus current.  Formal knowledge and architecture sources are
        # refreshed through retrieval_rebuild because they are revision based.
        self.retrieval.upsert(params["libraryId"], params["documentId"], params.get("chunks", []))
        return result

    def index_rebuild(self, params):
        library_id = params["libraryId"]
        progress_path = self.index.root / library_id / "progress.json"
        progress_path.parent.mkdir(parents=True, exist_ok=True)
        progress_path.write_text(json.dumps({"status": "running", "completed": 0, "total": len(params.get("chunks", [])), "libraryId": library_id}), encoding="utf-8")
        try:
            result = self.index.rebuild(library_id, params.get("chunks", []))
            progress_path.write_text(json.dumps({"status": "completed", "completed": result.get("count", 0), "total": result.get("count", 0), "generation": result.get("version", ""), "libraryId": library_id, "semanticAvailable": result.get("semanticAvailable", False)}), encoding="utf-8")
            return result
        except Exception as error:
            progress_path.write_text(json.dumps({"status": "failed", "completed": 0, "total": len(params.get("chunks", [])), "libraryId": library_id, "error": str(error)}), encoding="utf-8")
            raise

    def index_delete(self, params):
        result = self.index.delete(params["libraryId"], params["documentId"])
        self.retrieval.delete(params["libraryId"], params["documentId"])
        return result

    def search(self, params):
        return self.index.search(
            params["query"],
            params.get("libraryIds", []),
            params.get("topK", 10),
            params.get("retrievalMode", "hybrid"),
        )

    def retrieval_rebuild(self, params):
        return self.retrieval.rebuild(
            params["libraryId"],
            params.get("chunks", []),
            model_id=params.get("modelId", ""),
            complete=True,
        )

    def retrieval_upsert(self, params):
        return self.retrieval.upsert(params["libraryId"], params.get("documentId", ""), params.get("chunks", []))

    def retrieval_delete(self, params):
        return self.retrieval.delete(params["libraryId"], params.get("documentId", ""))

    def retrieval_search(self, params):
        return self.retrieval.search(
            params.get("query", ""),
            params.get("filters") or {},
            params.get("topK", 10),
            params.get("retrievalMode", "hybrid"),
        )

    def retrieval_cancel(self, params):
        return self.retrieval.cancel(params["libraryId"])

    def retrieval_progress(self, params):
        return self.retrieval.progress(params["libraryId"])

    def retrieval_status(self, params):
        return self.retrieval.status(params["libraryId"])

    def embedding_uninstall(self, params):
        model_id = params["modelId"]
        if self.retrieval.active_model_in_use(model_id):
            raise RuntimeError("embedding model is active in a retrieval generation; switch or rebuild before uninstall")
        return self.embeddings.uninstall(model_id)

    def embedding_embed(self, params):
        model_id = params.get("modelId", "")
        engine, metadata = self.embeddings.active()
        if model_id and (engine is None or metadata.get("modelId") != model_id):
            result = self.embeddings.health(model_id)
            if not result.get("available"):
                raise RuntimeError(result.get("error", "embedding model unavailable"))
            engine = self.embeddings.engines.get(model_id)
            if engine is None:
                raise RuntimeError("embedding model is not enabled")
        if engine is None:
            raise RuntimeError("embedding_model_unavailable: enable a verified local model first")
        return engine.embed(params.get("texts", []), bool(params.get("isQuery", params.get("query", False))))

    def index_progress(self, params):
        library_id = params.get("libraryId", "")
        if not library_id:
            raise ValueError("libraryId is required")
        path = self.index.root / library_id / "progress.json"
        if not path.exists():
            return {"status": "idle", "completed": 0, "total": 0, "libraryId": library_id}
        try:
            return json.loads(path.read_text(encoding="utf-8"))
        except (OSError, ValueError):
            return {"status": "unknown", "completed": 0, "total": 0, "libraryId": library_id}

    def dispatch(self, request):
        request_id = request.get("id")
        try:
            if request.get("jsonrpc") != "2.0":
                raise ValueError("jsonrpc must be 2.0")
            method = request.get("method")
            if method not in self.methods:
                return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32601, "message": "Method not found"}}
            result = self.methods[method](request.get("params") or {})
            return {"jsonrpc": "2.0", "id": request_id, "result": result}
        except (KeyError, TypeError, ValueError) as error:
            return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32602, "message": str(error)}}
        except Exception as error:
            print(traceback.format_exc(), file=sys.stderr, flush=True)
            return {"jsonrpc": "2.0", "id": request_id, "error": {"code": -32603, "message": str(error)}}

    def serve(self):
        # JSON-RPC requests are independent. Running dispatch concurrently is
        # required for a cancellation/progress request to reach a long local
        # generation rebuild while that rebuild is embedding batches.
        executor = ThreadPoolExecutor(max_workers=4, thread_name_prefix="kah-worker-rpc")
        output_lock = threading.Lock()

        def respond(request):
            response = self.dispatch(request)
            with output_lock:
                print(json.dumps(response, ensure_ascii=True, separators=(",", ":")), flush=True)

        for raw in sys.stdin:
            raw = raw.strip()
            if not raw:
                continue
            try:
                request = json.loads(raw)
            except ValueError as error:
                respond({"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": str(error)}})
                continue
            executor.submit(respond, request)
        executor.shutdown(wait=True)


def main():
    RpcServer().serve()
