param([string]$Python = $env:KAH_BUILD_PYTHON)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
if (-not $Python) { $Python = Join-Path $projectRoot '.venv\Scripts\python.exe' }
if (-not (Test-Path -LiteralPath $Python)) { throw 'Set KAH_BUILD_PYTHON to a Python environment with services/worker[dev,index,ocr] installed.' }
Push-Location (Join-Path $projectRoot 'services\worker')
try {
  & $Python -c 'import lancedb, docx, openpyxl, pptx, pypdf, pypdfium2, rapidocr, onnxruntime, PyInstaller'
  if ($LASTEXITCODE -ne 0) { throw 'Release Worker dependencies are incomplete.' }
  & $Python -m PyInstaller --noconfirm kah-worker.spec
  if ($LASTEXITCODE -ne 0) { throw 'Worker packaging failed.' }
} finally { Pop-Location }
