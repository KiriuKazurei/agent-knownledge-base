$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $projectRoot

$pythonCandidates = @()
$pythonCommand = Get-Command 'python3.14.exe' -ErrorAction SilentlyContinue
if ($pythonCommand) {
  $pythonCandidates += $pythonCommand.Source
}
if ($env:LOCALAPPDATA) {
  $pythonCandidates += (Join-Path $env:LOCALAPPDATA 'Python\bin\python3.14.exe')
}

$python314 = $pythonCandidates |
  Where-Object { $_ -and (Test-Path -LiteralPath $_) } |
  Select-Object -First 1

if (-not $python314) {
  $pyCommand = Get-Command 'py.exe' -ErrorAction SilentlyContinue
  if ($pyCommand) {
    $resolvedPython = & $pyCommand.Source -3.14 -c "import sys; print(sys.executable)" 2>$null
    if ($LASTEXITCODE -eq 0 -and $resolvedPython) {
      $python314 = $resolvedPython | Select-Object -First 1
    }
  }
}

if (-not $python314 -or -not (Test-Path -LiteralPath $python314)) {
  throw 'Python 3.14 is not installed or could not be located.'
}

Write-Host "Using Python 3.14: $python314"
& $python314 -m unittest discover -s 'services/worker/tests' -v
if ($LASTEXITCODE -ne 0) {
  exit $LASTEXITCODE
}
