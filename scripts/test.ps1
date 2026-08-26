$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot

pnpm typecheck
pnpm test:web
go -C 'services/api' test ./...
& (Join-Path $PSScriptRoot 'test-worker.ps1')
