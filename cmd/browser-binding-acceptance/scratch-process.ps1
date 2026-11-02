param([ValidateSet('native-stop','browser-stop','browser-start')][string]$Mode,[string]$Root,[string]$Browser)
$ErrorActionPreference='Stop'
$resolved=(Resolve-Path -LiteralPath $Root).ProviderPath
$temp=[IO.Path]::GetFullPath([IO.Path]::GetTempPath()).TrimEnd('\')+'\'
if($resolved -ne $Root -or !([IO.Path]::GetDirectoryName($resolved)+'\').Equals($temp,[StringComparison]::OrdinalIgnoreCase) -or [IO.Path]::GetFileName($resolved) -notmatch '^dsh-binding-gate-[a-f0-9]{32}$'){throw 'Scratch path mismatch'}
if((Get-Content -LiteralPath (Join-Path $Root 'binding-gate-owner.txt') -Raw).Trim() -ne 'dsh-work-binding-acceptance-v1'){throw 'Scratch marker mismatch'}
$data=Join-Path $Root 'browser-B'
if((Get-Item -LiteralPath $data -Force).Attributes -band [IO.FileAttributes]::ReparsePoint){throw 'Scratch browser directory is a reparse point'}
if($Mode -eq 'browser-start'){
 if(![IO.Path]::IsPathFullyQualified($Browser) -or !(Test-Path -LiteralPath $Browser -PathType Leaf) -or [IO.Path]::GetFileName($Browser) -notin @('chrome.exe','msedge.exe')){throw 'Invalid installed browser executable'}
 $oldRoot=$env:DSH_WORK_DESKTOP_ROOT;$oldReport=$env:DSH_WORK_DESKTOP_REPORT
 try{
  $env:DSH_WORK_DESKTOP_ROOT=$Root;$env:DSH_WORK_DESKTOP_REPORT=Join-Path $Root 'desktop-report.json'
  Start-Process -FilePath $Browser -ArgumentList @(('"--user-data-dir='+$data+'"'),'--profile-directory=Default','--remote-debugging-port=0','--no-first-run','--no-default-browser-check','about:blank') -WindowStyle Hidden|Out-Null
 }finally{$env:DSH_WORK_DESKTOP_ROOT=$oldRoot;$env:DSH_WORK_DESKTOP_REPORT=$oldReport}
 return
}
$native=Join-Path $Root 'dsh-work.exe';$escaped=[regex]::Escape($data)
$all=@(Get-CimInstance Win32_Process)
$owned=if($Mode -eq 'native-stop'){@($all|Where-Object {$_.ExecutablePath -eq $native})}else{@($all|Where-Object {$_.ExecutablePath -eq $Browser -and $_.CommandLine -match ('(?:^|\s)(?:"--user-data-dir='+$escaped+'"|--user-data-dir="'+$escaped+'"|--user-data-dir='+$escaped+')(?=\s|$)')})}
if(!$owned.Count){throw 'No exact owned process found; refusing a guessed target'}
# Browser descendants belong only to the exact captured selected scratch root.
if($Mode -eq 'browser-stop'){
 $ids=[Collections.Generic.HashSet[uint32]]::new();foreach($p in $owned){[void]$ids.Add([uint32]$p.ProcessId)}
 do{$added=$false;foreach($p in $all){if($ids.Contains([uint32]$p.ParentProcessId) -and !$ids.Contains([uint32]$p.ProcessId)){[void]$ids.Add([uint32]$p.ProcessId);$added=$true}}}while($added)
 $owned=@($all|Where-Object {$ids.Contains([uint32]$_.ProcessId)})
}
$owned|Select-Object ProcessId,ExecutablePath,CreationDate|ConvertTo-Json -AsArray
foreach($p in $owned){$current=Get-CimInstance Win32_Process -Filter "ProcessId=$($p.ProcessId)";if($current -and $current.ExecutablePath -eq $p.ExecutablePath -and $current.CreationDate -eq $p.CreationDate){Stop-Process -Id $p.ProcessId -ErrorAction SilentlyContinue}}
