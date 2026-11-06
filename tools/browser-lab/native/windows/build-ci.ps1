$ErrorActionPreference='Stop'
$suite=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$work=Join-Path $suite '.work/windows-sdk'
New-Item -ItemType Directory -Force $work | Out-Null
$name='cef_binary_156.0.15+gcf671c0+chromium-156.0.8078.80_windows64'
$sdk=Join-Path $work $name
if(!(Test-Path $sdk)) {
 & curl.exe --fail --location --retry 3 --output "$work/cef.tar.bz2" ('https://cef-builds.spotifycdn.com/'+[Uri]::EscapeDataString($name)+'.tar.bz2')
 if($LASTEXITCODE -ne 0){throw 'CEF SDK download failed'}
 & tar.exe -xjf "$work/cef.tar.bz2" -C $work
 if($LASTEXITCODE -ne 0){throw 'CEF SDK extraction failed'}
}
try { & "$PSScriptRoot/build.ps1" -CefSdk $sdk } catch { Write-Host $_.ScriptStackTrace; throw }
if($LASTEXITCODE -ne 0){throw 'CEF candidate build failed'}
