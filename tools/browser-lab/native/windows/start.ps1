param([Parameter(Mandatory)][string]$Bundle,[Parameter(Mandatory)][string]$RunRoot,[Parameter(Mandatory)][string]$RunId)
$ErrorActionPreference='Stop'
$suite=[IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../..'))
$Bundle=[IO.Path]::GetFullPath($Bundle);$RunRoot=[IO.Path]::GetFullPath($RunRoot)
$temp=[IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')+'\'
if(!$RunRoot.StartsWith($temp,[StringComparison]::OrdinalIgnoreCase) -or $RunRoot.TrimEnd('\') -eq $temp.TrimEnd('\')){throw 'RunRoot must be a private TEMP child'}
if((Get-Process -Id $PID).SessionId -eq 0){throw 'Native verification requires an interactive desktop, not session 0'}
Add-Type @'
using System;using System.Runtime.InteropServices;
public static class BrowserLabPackage { [DllImport("kernel32.dll",CharSet=CharSet.Unicode)] public static extern int GetCurrentPackageFullName(ref uint length,IntPtr name); }
'@
[uint32]$length=0
if([BrowserLabPackage]::GetCurrentPackageFullName([ref]$length,[IntPtr]::Zero) -ne 15700){throw 'Run native tests from an unpackaged ordinary terminal, not MSIX context'}
foreach($port in 19478..19484){if(Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue){throw "Lab port $port is occupied; existing process left intact"}}
foreach($file in @('host.exe','gateway.exe','capture.exe','cef/cefsimple.exe','cef/cefsimple.dll','build.json')){if(!(Test-Path (Join-Path $Bundle $file))){throw "Missing bundle file: $file"}}
New-Item -ItemType Directory -Force "$RunRoot/evidence","$RunRoot/data"|Out-Null
$data="$RunRoot/data"
Copy-Item "$Bundle/cef" $data -Recurse
Copy-Item "$Bundle/host.exe","$Bundle/gateway.exe","$Bundle/capture.exe" $data
Copy-Item "$suite/fixtures/artifact" $data -Recurse
New-Item -ItemType Directory "$data/private"|Out-Null
Set-Content "$data/private/outside.txt" 'BROWSER_LAB_PRIVATE_SENTINEL'
New-Item -ItemType Junction -Path "$data/artifact/escape" -Target "$data/private"|Out-Null
& icacls "$data/cef" /grant '*S-1-15-2-2:(OI)(CI)(RX)'|Out-Null
if($LASTEXITCODE -ne 0){throw 'CEF LPAC ACL setup failed'}
$env:BROWSER_LAB_ROOT=$RunRoot;$env:BROWSER_LAB_DATA_ROOT=$data
$env:BROWSER_LAB_CEF_EXE="$data/cef/cefsimple.exe";$env:BROWSER_LAB_CAPTURE_EXE="$data/capture.exe"
$env:BROWSER_LAB_TOKEN=[guid]::NewGuid().ToString('N')+[guid]::NewGuid().ToString('N')
$buildInfo=Get-Content "$Bundle/build.json" -Raw|ConvertFrom-Json
$env:BROWSER_LAB_NATIVE_PROTOCOL=if($buildInfo.nativeProtocol){[string]$buildInfo.nativeProtocol}else{'1'}
$headers=@{'X-Browser-Lab-Token'=$env:BROWSER_LAB_TOKEN}
$node=(Get-Command node -ErrorAction Stop).Source
$session=[ordered]@{schemaVersion=1;runId=$RunId;runRoot=$RunRoot;dataRoot=$data;evidenceRoot="$RunRoot/evidence";token=$env:BROWSER_LAB_TOKEN;cefExe=$env:BROWSER_LAB_CEF_EXE;build=(Get-Content "$Bundle/build.json" -Raw|ConvertFrom-Json);endpoints=@{fixture='http://127.0.0.1:19478';cef='http://127.0.0.1:19479';shell='http://127.0.0.1:19480';control='http://127.0.0.1:19481';gateway='http://127.0.0.1:19482';backend='http://127.0.0.1:19483';artifact='http://127.0.0.1:19484'};processes=@{}}
function Save-Session {$session|ConvertTo-Json -Depth 8|Set-Content "$RunRoot/private-session.json"}
function Start-LabProcess($Name,$Exe,$Arguments,$ScriptPath=''){
 $options=@{FilePath=$Exe;WorkingDirectory=$data;WindowStyle='Hidden';PassThru=$true;RedirectStandardOutput="$RunRoot/evidence/$Name.stdout.log";RedirectStandardError="$RunRoot/evidence/$Name.stderr.log"};if($Arguments.Count){$options.ArgumentList=$Arguments};$p=Start-Process @options
 $session.processes[$Name]=@{pid=$p.Id;executable=[IO.Path]::GetFullPath($Exe);script=$ScriptPath;started=$p.StartTime.ToUniversalTime().ToString('o')};Save-Session;return $p
}
function Wait-Lab($Url,$Process){
 for($i=0;$i -lt 150;$i++){
  if($Process.HasExited){throw "Lab process exited $($Process.ExitCode); see evidence logs"}
  try{return Invoke-RestMethod $Url -NoProxy -Headers $headers -TimeoutSec 2}catch{Start-Sleep -Milliseconds 200}
 };throw "Lab readiness timeout: $Url"
}
Save-Session
$fixture=Start-LabProcess 'fixture' $node "`"$suite/fixtures/server.mjs`"" "$suite/fixtures/server.mjs"
Wait-Lab ($session.endpoints.fixture+'/interactions') $fixture|Out-Null
$hostProcess=Start-LabProcess 'host' "$data/host.exe" @()
Wait-Lab ($session.endpoints.cef+'/json/list') $hostProcess|Out-Null
Wait-Lab ($session.endpoints.shell+'/json/list') $hostProcess|Out-Null
for($i=0;$i -lt 150 -and !(Test-Path "$RunRoot/session.json");$i++){Start-Sleep -Milliseconds 200}
if(!(Test-Path "$RunRoot/session.json")){throw 'Native child discovery did not complete'}
if ($env:BROWSER_LAB_ARCH_PROBE -eq '1') { Write-Host 'Architecture probe: sidecars omitted'; exit 0 }
$backend=Start-LabProcess 'backend' $node "`"$suite/platforms/windows/backend.mjs`"" "$suite/platforms/windows/backend.mjs"
Wait-Lab ($session.endpoints.backend+'/health') $backend|Out-Null
$gateway=Start-LabProcess 'gateway' "$data/gateway.exe" @()
Wait-Lab ($session.endpoints.gateway+'/state') $gateway|Out-Null
Write-Host "Browser Lab ready: $RunId"
