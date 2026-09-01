$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $PSScriptRoot
$smoke = Join-Path $root 'bin\work-smoke.exe'

if (-not (Test-Path -LiteralPath $smoke)) {
    throw "Missing $smoke. Run the real-DSH smoke build first."
}

$env:WORK_DSH_EXECUTABLE = Join-Path $root 'tools\dsh\run-dsh.cmd'
& $smoke
if ($LASTEXITCODE -ne 0) {
    throw "Real DSH smoke test failed with exit code $LASTEXITCODE."
}
