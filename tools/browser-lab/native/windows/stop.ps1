param([Parameter(Mandatory)][string]$RunRoot)
$ErrorActionPreference='Stop'
$RunRoot=[IO.Path]::GetFullPath($RunRoot)
$temp=[IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')+'\'
if(!$RunRoot.StartsWith($temp,[StringComparison]::OrdinalIgnoreCase) -or $RunRoot.TrimEnd('\') -eq $temp.TrimEnd('\')){throw 'Refusing cleanup outside a private TEMP child'}
$path=Join-Path $RunRoot 'private-session.json'
if(!(Test-Path $path)){Write-Host 'No processes were registered';exit 0}
$s=Get-Content $path -Raw|ConvertFrom-Json
if($s.schemaVersion -ne 1 -or [IO.Path]::GetFullPath($s.runRoot) -ne $RunRoot -or [IO.Path]::GetFullPath($s.dataRoot) -ne [IO.Path]::GetFullPath("$RunRoot/data")){throw 'Cleanup session scope mismatch'}
$headers=@{'X-Browser-Lab-Token'=$s.token};$steps=@();$forced=@()
foreach($entry in @(@('gateway','/shutdown'),@('control','/command?action=quit'),@('backend','/shutdown'))){
 $url=$s.endpoints.($entry[0])+$entry[1]
 try{$r=Invoke-WebRequest $url -Method Post -Body '{}' -ContentType 'application/json' -Headers $headers -NoProxy -TimeoutSec 15;$steps+=@{endpoint=$entry[0];status=[int]$r.StatusCode}}catch{$steps+=@{endpoint=$entry[0];error=$_.Exception.Message}}
 if($entry[0] -eq 'control'){for($i=0;$i -lt 70;$i++){if(!(Get-NetTCPConnection -State Listen -LocalPort 19481 -ErrorAction SilentlyContinue)){break};Start-Sleep -Milliseconds 150}}
}
foreach($property in $s.processes.PSObject.Properties){
 $record=$property.Value;if($property.Name -ne 'fixture'){try{$p=Get-Process -Id $record.pid -ErrorAction Stop;$null=$p.WaitForExit(3000)}catch{}};$proc=Get-CimInstance Win32_Process -Filter "ProcessId=$($record.pid)"
 if(!$proc){continue}
 try{$actual=Get-Process -Id $record.pid -ErrorAction Stop}catch{continue}
 if($actual.StartTime.ToUniversalTime().Ticks -ne ([datetime]$record.started).ToUniversalTime().Ticks){throw 'PID was reused; process left intact'}
 if(![string]::Equals($proc.ExecutablePath,$record.executable,[StringComparison]::OrdinalIgnoreCase)){throw 'Process executable mismatch; left intact'}
 if($record.script -and !$proc.CommandLine.Replace('\','/').Contains($record.script.Replace('\','/'))){throw 'Node script identity mismatch; left intact'}
 if($property.Name -ne 'fixture'){$forced+=$property.Name}
 Stop-Process -Id $record.pid -ErrorAction Stop
 $null=$actual.WaitForExit(2000)
}
$scope=[IO.Path]::GetFullPath($s.dataRoot).TrimEnd('\')+'\'
$profilePattern='(?i)--user-data-dir(?:=|\s+)"?'+[regex]::Escape($scope)
function Test-OwnedProcess($candidate) {
 $staged=$candidate.ExecutablePath -and $candidate.ExecutablePath.StartsWith($scope,[StringComparison]::OrdinalIgnoreCase)
 $webview=$candidate.Name -eq 'msedgewebview2.exe' -and $candidate.CommandLine -and [regex]::IsMatch($candidate.CommandLine.Replace('/','\'),$profilePattern)
 return $staged -or $webview
}
# WebView2 exits asynchronously after its last controller is released. Wait for
# this exact private profile, not a port or an unrelated WebView2 installation.
$deadline=[DateTime]::UtcNow.AddSeconds(10)
while([DateTime]::UtcNow -lt $deadline) {
 $owned=@(Get-CimInstance Win32_Process|Where-Object {Test-OwnedProcess $_})
 $ports=@(19478..19484|ForEach-Object {Get-NetTCPConnection -State Listen -LocalPort $_ -ErrorAction SilentlyContinue})
 if(!$owned.Count -and !$ports.Count){break}
 Start-Sleep -Milliseconds 250
}
# If graceful shutdown failed, only staged lab executables (or this profile's
# WebView2 children) are eligible for forced cleanup. Still fail the gate.
foreach($candidate in @(Get-CimInstance Win32_Process)){
 if(!(Test-OwnedProcess $candidate)){continue}
 try{$actual=Get-Process -Id $candidate.ProcessId -ErrorAction Stop}catch{continue}
 if([Math]::Abs(($actual.StartTime.ToUniversalTime()-$candidate.CreationDate.ToUniversalTime()).TotalMilliseconds) -gt 1){throw 'Scoped process identity changed; left intact'}
 $forced+=$candidate.Name;Stop-Process -Id $candidate.ProcessId -ErrorAction Stop;$null=$actual.WaitForExit(2000)
}
$remaining=@(Get-CimInstance Win32_Process|Where-Object {Test-OwnedProcess $_})
$listeners=@(19478..19484|ForEach-Object {Get-NetTCPConnection -State Listen -LocalPort $_ -ErrorAction SilentlyContinue}|Select-Object LocalPort,OwningProcess)
$listenerOwners=@(foreach($listener in $listeners){$p=Get-CimInstance Win32_Process -Filter "ProcessId=$($listener.OwningProcess)";@{pid=$listener.OwningProcess;exists=($null -ne $p);name=$p.Name;parent=$p.ParentProcessId;owned=($p -and (Test-OwnedProcess $p))}})
@{schemaVersion=1;steps=$steps;forcedRegisteredProcesses=$forced;remainingProcesses=@($remaining|Select-Object ProcessId,Name);remainingListeners=$listeners;listenerOwners=$listenerOwners}|ConvertTo-Json -Depth 7|Set-Content "$RunRoot/evidence/cleanup.json"
if($remaining.Count -or $listeners.Count -or $forced.Count){throw 'Cleanup was not fully graceful; see cleanup.json'}
Write-Host 'Browser Lab stopped with zero remaining processes/listeners'
