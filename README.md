# Knowledge Agent Hub

Knowledge Agent Hub is a local-first Windows desktop knowledge manager with an Electron/React interface, a Gin API, and a Python document/index worker. It imports common document formats and Agent Skills, keeps portable content-addressed copies and managed Skill folders, exposes evidence-first search and on-demand Skill delivery to agents, and can optionally generate cited answers through OpenAI, Anthropic, or LM Studio compatible endpoints.

## Repository layout

- `apps/desktop` — Electron, React, and TypeScript desktop application.
- `services/api` — Gin API, SQLite metadata store, authentication, imports, backups, and orchestration.
- `services/worker` — JSON-RPC document parser and embedded hybrid index worker.
- `contracts/openapi.yaml` — public `/api/v1` contract.
- `docs` — architecture, development, security, and acceptance notes.

All paths stored by the application are relative to a configured data root. No development-machine drive path is required at runtime.

## Development

Prerequisites: Node.js 24.14+, pnpm 11.21+, Go 1.26+, and Python 3.14 x64.

```powershell
pnpm install
py -3.14 -m venv .venv
.\.venv\Scripts\python -m pip install -e ".\services\worker[dev,index]"
pnpm dev
```

The development app writes only to `.run-data/`. See `docs/DEVELOPMENT.md` for individual service commands and packaging.

## Security defaults

The API binds to `127.0.0.1`, Electron runs with context isolation and sandboxing, agent tokens are hashed, provider secrets use Windows Credential Manager, and remote-model access is disabled per knowledge base until explicitly enabled.
