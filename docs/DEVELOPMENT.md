# Development and packaging

## Toolchain

- Node.js 24.14.0 and pnpm 11.21.0
- Go 1.26.1
- Python 3.14 x64 in `.venv`

Dependency versions are captured in `pnpm-lock.yaml`, `go.sum`, and `uv.lock`/the packaged worker environment. Target machines do not need these runtimes because release artifacts include the API and worker executables.

## Commands

```powershell
pnpm install
go mod download -C services/api
py -3.14 -m venv .venv
.\.venv\Scripts\python -m pip install -e ".\services\worker[dev,index]"
pnpm test
pnpm dev
pnpm build
pnpm package:win
```

The Electron development main process starts `go run ./cmd/server` from `services/api` and passes the worker module path relative to the repository root.

## Release layout

`electron-builder` places `kah-api.exe` and the PyInstaller-built `kah-worker.exe` in `resources/backend`. The application resolves resources through `process.resourcesPath`; it never assumes a checkout location.

