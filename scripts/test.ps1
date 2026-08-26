$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot

pnpm typecheck
pnpm test:web
go test ./... -C 'services/api'

$python314 = py -3.14 -c "import sys; print(sys.executable)" 2>$null
if ($LASTEXITCODE -eq 0) {
  & $python314 -m unittest discover -s 'services/worker/tests' -v
} else {
  Write-Warning 'Python 3.14 is not installed; worker tests were not executed.'
}
