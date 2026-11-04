param([string]$Root,[string]$ExpectedExecutable)
$ErrorActionPreference='Stop'
$resolved=(Resolve-Path -LiteralPath $Root).ProviderPath
if($resolved -ne $Root -or (Get-Content -LiteralPath (Join-Path $Root 'binding-gate-owner.txt') -Raw).Trim() -ne 'dsh-work-binding-acceptance-v1'){throw 'Scratch ownership mismatch'}
if(![IO.Path]::IsPathFullyQualified($ExpectedExecutable) -or !$ExpectedExecutable.StartsWith($Root+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'Expected executable outside scratch'}
$manifest=Join-Path $Root 'native/com.dsh_work.browser_dev.json'
$m=Get-Content -LiteralPath $manifest -Raw|ConvertFrom-Json
if($m.path -ne $ExpectedExecutable -or $m.allowed_origins.Count -ne 1 -or $m.allowed_origins[0] -ne 'chrome-extension://fpemabjfldbpdmjfdofdnfdaccafobod/'){throw 'Manifest path/source mismatch'}
$rows=@()
foreach($vendor in @('Google\Chrome','Microsoft\Edge')){
 $present=$false
 foreach($view in @([Microsoft.Win32.RegistryView]::Registry32,[Microsoft.Win32.RegistryView]::Registry64)){
  $base=[Microsoft.Win32.RegistryKey]::OpenBaseKey([Microsoft.Win32.RegistryHive]::CurrentUser,$view)
  try{$key=$base.OpenSubKey("Software\$vendor\NativeMessagingHosts\com.dsh_work.browser_dev");if($key){try{$value=[string]$key.GetValue('');if($value -ne $manifest){throw 'Registry points at another manifest'};$present=$true;$rows+=@{browser=$vendor;view=[string]$view;manifest=$value;executable=$m.path}}finally{$key.Dispose()}}}finally{$base.Dispose()}
 }
 if(!$present){throw "Registration disappeared for $vendor"}
}
$rows|ConvertTo-Json -AsArray
