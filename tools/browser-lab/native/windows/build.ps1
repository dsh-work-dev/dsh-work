[CmdletBinding(DefaultParameterSetName='Bundle')]
param(
 [Parameter(Mandatory,ParameterSetName='Bundle')][string]$CefBundle,
 [Parameter(Mandatory,ParameterSetName='SDK')][string]$CefSdk,
 [ValidateSet('bottom','stock')][string]$Composition='bottom'
)
$ErrorActionPreference='Stop'
$suite=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$repo=[IO.Path]::GetFullPath((Join-Path $suite '../..'))
$work=Join-Path $suite ('.work/build-'+[guid]::NewGuid().ToString('N'))
$bundle=Join-Path $suite '.work/bundle'
New-Item -ItemType Directory -Force $work,$bundle|Out-Null
if($PSCmdlet.ParameterSetName -eq 'SDK'){
 $CefSdk=[IO.Path]::GetFullPath($CefSdk)
 $versionFile=Join-Path $CefSdk 'include/cef_version.h'
 if(!(Test-Path $versionFile) -or !(Select-String -LiteralPath $versionFile -SimpleMatch '156.0.15+gcf671c0+chromium-156.0.8078.80' -Quiet)){throw 'This overlay is pinned to CEF 156.0.15+gcf671c0+chromium-156.0.8078.80'}
 $sdkCopy=Join-Path $work 'cef-sdk';Copy-Item $CefSdk $sdkCopy -Recurse
 Copy-Item "$PSScriptRoot/cef-overlay/*.cc","$PSScriptRoot/cef-overlay/startup_trace.h" "$sdkCopy/tests/cefsimple" -Force
 if(Get-Command cmake -ErrorAction SilentlyContinue){$cmake='cmake';$prefix=@()}else{$cmake='python';$prefix=@('-m','cmake')}
 $vswhere=Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
 if(!(Test-Path $vswhere)){throw 'Visual Studio discovery tool is required'}
 $major=([version](& $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationVersion)).Major
 $generator=switch($major){17 {'Visual Studio 17 2022'} 18 {'Visual Studio 18 2026'} default {throw "Unsupported installed Visual Studio major: $major"}}
 & $cmake @prefix -S $sdkCopy -B "$work/cef-build" -G $generator -A x64 -DUSE_SANDBOX=ON
 if($LASTEXITCODE -ne 0){throw 'CEF configure failed'}
 & $cmake @prefix --build "$work/cef-build" --config Release --target cefsimple --parallel 8
 if($LASTEXITCODE -ne 0){throw 'CEF build failed'}
 $CefBundle="$work/cef-build/tests/cefsimple/Release"
}
$CefBundle=[IO.Path]::GetFullPath($CefBundle)
foreach($file in @('cefsimple.exe','cefsimple.dll','libcef.dll','icudtl.dat')){if(!(Test-Path (Join-Path $CefBundle $file))){throw "Missing prepared CEF file: $file"}}
New-Item -ItemType Directory -Force "$bundle/cef","$work/host"|Out-Null
Copy-Item "$CefBundle/*" "$bundle/cef" -Recurse -Force
Copy-Item "$PSScriptRoot/host/*" "$work/host" -Recurse
if($Composition -eq 'bottom'){
 Push-Location $repo
 try{
  go mod download github.com/wailsapp/wails/v3
  if($LASTEXITCODE -ne 0){throw 'Cannot download pinned Wails module'}
  $module=go list -m -f '{{.Dir}}' github.com/wailsapp/wails/v3
  if($LASTEXITCODE -ne 0 -or !$module -or !(Test-Path $module)){throw 'Cannot resolve pinned Wails module directory'}
 }finally{Pop-Location}
 Copy-Item $module "$work/wails-fork" -Recurse
 $file=Join-Path $work 'wails-fork/internal/webview2/pkg/edge/composition_host.go'
 (Get-Item $file).IsReadOnly=$false
 $source=[IO.File]::ReadAllText($file)
 if(([regex]::Matches($source,'CreateTargetForHwnd\(hwnd, true\)')).Count -ne 1){throw 'Pinned composition patch does not match exactly once'}
 [IO.File]::WriteAllText($file,$source.Replace('CreateTargetForHwnd(hwnd, true)','CreateTargetForHwnd(hwnd, false)'))
 Push-Location "$work/host"
 try{go mod edit '-replace=github.com/wailsapp/wails/v3=../wails-fork';if($LASTEXITCODE -ne 0){throw 'Isolated module replace failed'}}finally{Pop-Location}
}
Push-Location "$work/host"
try{
 go test -run '^TestGuestWindowProbeReusesCallback$' -count=1 .
 if($LASTEXITCODE -ne 0){throw 'Window probe regression failed'}
 go build -o "$bundle/host.exe" .
 if($LASTEXITCODE -ne 0){throw 'Native host build failed'}
}finally{Pop-Location}
Push-Location "$suite/native/gateway"
try{go build -o "$bundle/gateway.exe" .;if($LASTEXITCODE -ne 0){throw 'Real authorization gateway build failed'}}finally{Pop-Location}
if(Get-Command cl.exe -ErrorAction SilentlyContinue){$setup=''}else{
 $vswhere=Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio/Installer/vswhere.exe'
 if(!(Test-Path $vswhere)){throw 'MSVC is required; run from its x64 developer terminal'}
 $vs=& $vswhere -latest -products '*' -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
 if(!$vs){throw 'No MSVC x64 toolchain found'}
 $setup='call "'+$vs+'\VC\Auxiliary\Build\vcvars64.bat" >nul && '
}
foreach($path in @($PSScriptRoot,$bundle,$work)){if($path.Contains('"')){throw 'Build paths must not contain quotes'}}
$compile=$setup+'cl /nologo /std:c++17 /EHsc /W4 /O2 /DUNICODE /D_UNICODE "'+$PSScriptRoot+'\capture.cpp" /Fo:"'+$work+'\capture.obj" /Fe:"'+$bundle+'\capture.exe" /link d3d11.lib dxgi.lib windowsapp.lib windowscodecs.lib ole32.lib user32.lib'
& cmd.exe /d /s /c $compile
if($LASTEXITCODE -ne 0){throw 'Scoped WGC capture helper build failed'}
$gitRef=git -C $repo rev-parse HEAD
@{schemaVersion=1;kind='browser-lab-windows-bundle';created=(Get-Date).ToUniversalTime().ToString('o');sourceCommit=$gitRef;composition=$Composition;wails='v3.0.0-beta.16';cefOverlay='156.0.15+gcf671c0+chromium-156.0.8078.80';go=(& go version);playwright='1.58.2';syntheticAdmission=$true}|ConvertTo-Json|Set-Content "$bundle/build.json"
Write-Host "Built isolated test bundle: $bundle"
