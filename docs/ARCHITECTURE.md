# Architecture

## Runtime processes

Electron owns the visible application lifetime. It starts the Gin executable with an ephemeral desktop token and a data root, waits for `/api/v1/health`, then loads the renderer. Gin is the only public network process. It starts the Python worker and communicates through newline-delimited JSON-RPC 2.0 over standard input/output.

Gin owns authorization, SQLite metadata, the object store, jobs, model credentials, provider calls, backups, and the public API. The Python worker owns format extraction, stable chunk boundaries, location metadata, and the local search index. A portable JSON index is always available; when LanceDB is installed the same worker interface can use its persistent index implementation.

## Portable paths

The configured data root contains `knowledge.db`, `objects`, `indexes`, `logs`, `backups`, and `staging`. Database paths are slash-normalized paths relative to that root. Imported bytes are stored as `objects/<first-two-hash-chars>/<sha256>`.

Installed mode defaults to the Electron `userData` directory. Portable mode is selected by `KAH_PORTABLE=1` or a `portable.flag` beside the executable and uses `./data` beside the application.

## Search and KAH

Document queries produce auditable evidence before optional generation. Candidate text matches and vector matches are fused with reciprocal-rank fusion. If no embedding provider is configured, the API returns `degraded: true` with a lexical-only reason rather than failing. Every evidence result contains a document, chunk, hash, and format-specific location.

KAH knowledge is a separate structured directory backed by immutable SQLite revisions. `/knowledge/search` and `/knowledge/resolve` are desktop management endpoints; Agents use `POST /mcp/read` for stable directory entries and revision-pinned content. `POST /mcp/manage` validates and submits review-only drafts. Drafts and rejected revisions are never returned by Read MCP.

## Agent Skills

Global Skills are stored as managed folders under `skills/<name>/` in the data root. Each folder contains a canonical `SKILL.md` entrypoint and may contain scripts, references, assets, or other read-only resources. The API validates and atomically imports one Markdown Skill or one zip package without executing its contents.

Agent Skill protocol guidance is exposed as MCP resources (`kah://skill/read/v1` and `kah://skill/manage/v1`). The desktop manifest endpoint still returns the complete `SKILL.md` plus a relative file manifest; additional files are fetched through a path-confined read-only endpoint. External directory mapping is a separate, desktop-only capability described in `SKILL_MAPPING_PLAN.md`; it creates only user-authorized Windows directory symlinks.

SQLite stores Skill metadata, per-file hashes, and the two library relations `skill_uses_library` and `library_requires_skill`. Skills are included automatically in data-root backups, while the worker document index does not index Skill instructions.

## Trust boundaries

- The renderer cannot access Node.js directly.
- The worker has no listening socket and never receives provider API keys.
- Agent MCP tokens have explicit `mcp_read` or `mcp_manage` scopes and library allow-lists.
- MCP Origin checks reject non-local browser origins before JSON-RPC dispatch.
- Remote generation is blocked unless every selected library permits it.
- Local LM Studio loopback endpoints are classified as local.
