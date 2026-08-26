param(
  [switch]$SkipInstall
)

$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot

if (-not $SkipInstall) {
  pnpm install --frozen-lockfile=$false
  go mod download -C 'services/api'
}

$pythonCommand = Get-Command python -ErrorAction SilentlyContinue
if ($null -ne $pythonCommand) {
  $env:KAH_PYTHON = $pythonCommand.Source
}

pnpm dev

