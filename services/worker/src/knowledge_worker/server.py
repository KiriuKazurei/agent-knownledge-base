import json
import os
import sys
import traceback

from . import __version__
from .index import HybridIndex
from .parsers import parse_document




def _configure_utf8_stdio():
    for stream in (sys.stdin, sys.stdout):
        if hasattr(stream, "reconfigure"):
            stream.reconfigure(encoding="utf-8", errors="strict", newline="\n")


_configure_utf8_stdio()

class RpcServer:
    def __init__(self):
        data_root = os.environ.get("KAH_DATA_ROOT", ".run-data")
        self.index = HybridIndex(data_root)
        self.methods = {
            "health": self.health,
            "parse": self.parse,
            "index_upsert": self.index_upsert,
            "index_delete": self.index_delete,
            "index_rebuild": self.index_rebuild,
            "search": self.search,
        }

    def health(self, _params):
        return {"status": "ok", "version": __version__, "indexBackend": self.index.backend}

    def parse(self, params):
        return parse_document(params["path"], params["documentId"], params.get("mediaType", ""), params.get("originalName", ""))

    def index_upsert(self, params):
        return self.index.upsert(params["libraryId"], params["documentId"], params.get("chunks", []))

    def index_rebuild(self, params):
        return self.index.rebuild(params["libraryId"], params.get("chunks", []))

    def index_delete(self, params):
        return self.index.delete(params["libraryId"], params["documentId"])

    def search(self, params):
        return self.index.search(
            params["query"],
            params.get("libraryIds", []),
            params.get("topK", 10),
            params.get("retrievalMode", "hybrid"),
        )

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
        for raw in sys.stdin:
            raw = raw.strip()
            if not raw:
                continue
            try:
                request = json.loads(raw)
                response = self.dispatch(request)
            except ValueError as error:
                response = {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": str(error)}}
            print(json.dumps(response, ensure_ascii=False, separators=(",", ":")), flush=True)


def main():
    RpcServer().serve()
