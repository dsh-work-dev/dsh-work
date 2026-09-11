$ErrorActionPreference = 'Stop'
$env:DSH_WORK_PIPE_TEST = '1'
$env:DSH_WORK_LIVE_TEST = '1'
$env:DSH_WORK_VERSION_RESTORE_REAL = '1'
go test ./internal/workeripc ./internal/desktopbridge ./internal/dshactivity ./internal/platform/windows -run 'TestRealWorkerPipe|TestFetchFlowControlAndCancel|TestLiveDSHWorkerActivity|TestVersionRestoreRealDSH|TestVersionPointRealStartupAndSafeMode' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'Desktop integration failed' }
$env:DSH_WORK_DAEMON_TEST = '1'
go test ./internal/desktopprobe -run TestRealDaemonLifecycle -count=1 -v -timeout 7m
if ($LASTEXITCODE -ne 0) { throw 'Daemon lifecycle validation failed; see .task/daemon-lifecycle evidence' }
