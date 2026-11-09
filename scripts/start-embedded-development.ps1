param([switch]$Runner,[switch]$PrepareOnly,[switch]$BuildOnly,[ValidateSet('1','2')][string]$UiInstance='1',[string]$Runtime='')
$ErrorActionPreference='Stop'
if($Runner -and ($PrepareOnly -or $BuildOnly -or $UiInstance -ne '1')){throw 'Runner mode owns both UI instances; do not combine it with PrepareOnly, BuildOnly or UiInstance 2.'}
$repo=Split-Path -Parent $PSScriptRoot
$bin=Join-Path $repo 'bin'
New-Item -ItemType Directory -Path $bin -Force|Out-Null
$environmentNames=@('DSH_WORK_EMBEDDED_POWERSHELL','GOFLAGS','DSH_WORK_EMBEDDED_MODFILE','DSH_WORK_EXECUTABLE','DSH_WORK_EMBEDDED_CHECKPOINT','DSH_WORK_EMBEDDED_ROOT','DSH_WORK_EMBEDDED_UI_INSTANCE','DSH_WORK_CEF_EXECUTABLE','DSH_WORK_WEBVIEW_DEBUG_PORT')
$savedEnvironment=@{}
foreach($name in $environmentNames){$savedEnvironment[$name]=[Environment]::GetEnvironmentVariable($name,'Process')}
Push-Location $repo
try {
 # Normalize only flags emitted by earlier versions of this exact candidate.
 # The pure helper inspects effective go env (including GOENV fallback), never
 # edits persistent settings, and refuses unrelated modfiles.
 $flagResult=node (Join-Path $PSScriptRoot 'e67-go-flags.mjs') (Join-Path $bin 'e67.mod')
 if($LASTEXITCODE -ne 0){throw 'Candidate GOFLAGS preflight failed; no development process was started.'}
 $flagState=$flagResult | ConvertFrom-Json
 $env:GOFLAGS=$flagState.flags
 Write-Host "[e67] GOFLAGS source: $($flagState.source); removed historical candidate flags: $($flagState.removed); GOENV: $($flagState.goenv)"
 Write-Host "[e67] Go: $((Get-Command go).Source); Wails: $((Get-Command wails3).Source)"
 $version=go list -m -f '{{.Version}}' github.com/wailsapp/wails/v3
 if($LASTEXITCODE -ne 0){throw "Resolve pinned Wails version failed (exit $LASTEXITCODE)"}
 $module=go list -m -f '{{.Dir}}' github.com/wailsapp/wails/v3
 if($LASTEXITCODE -ne 0 -or $version -ne 'v3.0.0-beta.16'){throw 'Embedded development requires pinned Wails v3.0.0-beta.16'}
 $copy=Join-Path $bin 'e67-wails-v3.0.0-beta.16'
 if(!(Test-Path -LiteralPath $copy)){Copy-Item -LiteralPath $module -Destination $copy -Recurse}
 function Patch([string]$relative,[string]$before,[string]$after){
  $path=Join-Path $copy $relative
  $source=[IO.File]::ReadAllText($path)
  if($source.Contains($after)){return}
  if(($source.Split([string[]]@($before),[StringSplitOptions]::None)).Count -ne 2){throw "Pinned Wails seam mismatch: $relative"}
  (Get-Item -LiteralPath $path).IsReadOnly=$false
  [IO.File]::WriteAllText($path,$source.Replace($before,$after))
 }
 Patch 'pkg/application/webview_window_options.go' 'WebView2CompositionHosting bool' "WebView2CompositionHosting bool`n`t// Explicit per-window choice; default composition ordering remains unchanged.`n`tWebView2CompositionBehindChildWindows bool"
 Patch 'pkg/application/webview_window_windows.go' 'w.chromium.CompositionControllerEnabled = options.Windows.WebView2CompositionHosting' "w.chromium.CompositionControllerEnabled = options.Windows.WebView2CompositionHosting`n`tw.chromium.CompositionBehindChildWindows = options.Windows.WebView2CompositionBehindChildWindows"
 $chromium=Join-Path $copy 'internal/webview2/pkg/edge/chromium.go'
 $source=[IO.File]::ReadAllText($chromium)
 if(!$source.Contains('CompositionBehindChildWindows bool')){
  $pattern='(?m)^(\s*CompositionControllerEnabled\s+bool[^\r\n]*)'
  if(([regex]::Matches($source,$pattern)).Count -ne 1){throw 'Pinned Chromium field mismatch'}
  $source=[regex]::Replace($source,$pattern,'$1'+"`n`tCompositionBehindChildWindows bool")
  (Get-Item -LiteralPath $chromium).IsReadOnly=$false;[IO.File]::WriteAllText($chromium,$source)
 }
 Patch 'internal/webview2/pkg/edge/chromium.go' 'newCompositionHost(e.hwnd)' 'newCompositionHost(e.hwnd, !e.CompositionBehindChildWindows)'
 Patch 'internal/webview2/pkg/edge/composition_host.go' 'func newCompositionHost(hwnd uintptr)' 'func newCompositionHost(hwnd uintptr, aboveChildWindows bool)'
 Patch 'internal/webview2/pkg/edge/composition_host.go' 'CreateTargetForHwnd(hwnd, true)' 'CreateTargetForHwnd(hwnd, aboveChildWindows)'
 $mod=Join-Path $bin 'e67.mod'
 [IO.File]::WriteAllText($mod,[IO.File]::ReadAllText((Join-Path $repo 'go.mod'))+"`nreplace github.com/wailsapp/wails/v3 => "+$copy.Replace('\','/')+"`n")
 Copy-Item -LiteralPath (Join-Path $repo 'go.sum') -Destination (Join-Path $bin 'e67.sum') -Force
 if($PrepareOnly){Write-Host "Prepared candidate module: $mod";return}
 node (Join-Path $PSScriptRoot 'verify-node.mjs')
 if($LASTEXITCODE -ne 0){throw 'Node/npm preflight failed. Select Node >=24.9.0 in this ordinary terminal before starting the candidate.'}
 $cef=Join-Path $bin 'embedded-cef-vs2022/Release/dsh_cef_broker.exe'
 if(!(Test-Path -LiteralPath $cef)){throw "Build the approved native broker first: $cef"}
 # Only module package loading/build receives this flag. Global GOFLAGS also
 # reaches x/tools' GO111MODULE=off language-version probe and breaks bindings.
 $env:DSH_WORK_EMBEDDED_MODFILE=$mod.Replace('\','/')
 if(!$Runtime){$Runtime=Join-Path $repo 'tools/dsh/run-dsh.cmd'}
 $env:DSH_WORK_EXECUTABLE=(Resolve-Path -LiteralPath $Runtime).Path
 $env:DSH_WORK_EMBEDDED_CHECKPOINT='1'
 $env:DSH_WORK_EMBEDDED_ROOT=Join-Path $bin 'e67-ui-state'
 $env:DSH_WORK_EMBEDDED_UI_INSTANCE=$UiInstance
 $env:DSH_WORK_CEF_EXECUTABLE=$cef
 $env:DSH_WORK_WEBVIEW_DEBUG_PORT=if($UiInstance -eq '1'){'9333'}else{'9343'}
 if($Runner){
  # Preserve the actual ordinary-terminal interpreter, including its own policy.
  $env:DSH_WORK_EMBEDDED_POWERSHELL=(Get-Process -Id $PID).Path
  Write-Host "[e67] Runner PowerShell: $env:DSH_WORK_EMBEDDED_POWERSHELL; effective policy: $(Get-ExecutionPolicy)"
  $runnerExecutable=Join-Path $bin 'e67-dev-runner.exe'
  go build -o $runnerExecutable ./cmd/e67-dev-runner
  if($LASTEXITCODE -ne 0){throw "Runner build failed (exit $LASTEXITCODE)"}
  & $runnerExecutable serve
  if($LASTEXITCODE -ne 0){throw "Development runner exited with a diagnostic failure (exit $LASTEXITCODE); see bin/e67-dev-runner/runner.log"}
  return
 }
 if($BuildOnly){
  wails3 task build DEV=true
  if($LASTEXITCODE -ne 0){throw "Candidate build failed (exit $LASTEXITCODE)"}
  Write-Host 'Candidate build completed; no UI was started.'
  return
 }
 if($UiInstance -eq '1'){
  Write-Host 'Starting real DSH Work candidate; keep this ordinary terminal open.'
  wails3 task dev
  if($LASTEXITCODE -ne 0){throw "Wails development loop failed (exit $LASTEXITCODE)"}
 } else {
  $exe=Join-Path $bin 'dsh-work.exe'
  if(!(Test-Path -LiteralPath $exe)){throw 'Start UI 1 and wait for its dev build first'}
  & $exe
  if($LASTEXITCODE -ne 0){throw "Candidate UI exited unsuccessfully (exit $LASTEXITCODE)"}
 }
}finally{
 foreach($name in $environmentNames){[Environment]::SetEnvironmentVariable($name,$savedEnvironment[$name],'Process')}
 Pop-Location
}
