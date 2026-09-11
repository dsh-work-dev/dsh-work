$ErrorActionPreference = 'Stop'
$env:DSH_WORK_PIPE_TEST = '1'
$env:DSH_WORK_LIVE_TEST = '1'
$env:DSH_WORK_VERSION_RESTORE_REAL = '1'
go test ./internal/workeripc ./internal/desktopbridge ./internal/dshactivity ./internal/platform/windows -run 'TestRealWorkerPipe|TestFetchFlowControlAndCancel|TestLiveDSHWorkerActivity|TestVersionRestoreRealDSH|TestVersionPointRealStartupAndSafeMode' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'Desktop integration failed' }
$reportPath = Join-Path $PWD 'bin/desktop-check.json'
$env:DSH_WORK_DESKTOP_ROOT = Join-Path $PWD '.task/desktop-check'
$env:DSH_WORK_DESKTOP_REPORT = $reportPath
$started = Get-Date
& ./bin/dsh-work.exe
if ($LASTEXITCODE -ne 0) { throw 'Desktop process failed' }
if (!(Test-Path -LiteralPath $reportPath) -or (Get-Item -LiteralPath $reportPath).LastWriteTime -lt $started) { throw 'Desktop report missing or stale' }
$result = Get-Content -Raw -LiteralPath $reportPath | ConvertFrom-Json
if (!$result.ok -or !$result.restartVerified -or $result.errors.Count -ne 0 -or !$result.activity.connected) { throw "Desktop validation failed; see $reportPath" }
if ($result.processes.error) { throw 'Process sampling failed' }
if ($result.processes.tcpListeners.Count -ne 0 -or $result.processes.udpEndpoints.Count -ne 0) { throw 'Desktop opened a network listener' }
foreach ($sample in $result.processes.processes) {
    $remaining = Get-Process -Id $sample.Id -ErrorAction SilentlyContinue
    if ($remaining -and $remaining.ProcessName -eq $sample.ProcessName) { throw "Owned process still running: $($sample.Id)" }
}
Write-Output "Desktop validated: $reportPath"
