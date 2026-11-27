param([int]$FixturePid,[long]$Started,[long]$Owner,[string]$Action,[string]$OwnTempFile,[string]$ExpectedTitle,[string]$OwnTempRoot)
$ErrorActionPreference='Stop'
Add-Type -AssemblyName UIAutomationClient,UIAutomationTypes
Add-Type @"
using System;using System.Runtime.InteropServices;public static class FixtureWindows {
public delegate bool Enumerate(IntPtr h,IntPtr p);
[DllImport("user32.dll")]public static extern bool EnumWindows(Enumerate f,IntPtr p);
[DllImport("user32.dll")]public static extern uint GetWindowThreadProcessId(IntPtr h,out uint pid);
[DllImport("user32.dll")]public static extern IntPtr GetWindow(IntPtr h,uint kind);
[DllImport("user32.dll")]public static extern bool IsWindowVisible(IntPtr h);
[DllImport("user32.dll")]public static extern bool IsChild(IntPtr parent,IntPtr child);
[DllImport("user32.dll")]public static extern bool EnumChildWindows(IntPtr parent,Enumerate callback,IntPtr p);
[DllImport("user32.dll",CharSet=CharSet.Unicode)]public static extern int GetClassName(IntPtr hwnd,System.Text.StringBuilder text,int count);
[DllImport("user32.dll",CharSet=CharSet.Unicode)]public static extern int GetWindowText(IntPtr hwnd,System.Text.StringBuilder text,int count);
[DllImport("user32.dll")]public static extern int GetDlgCtrlID(IntPtr hwnd);
[DllImport("user32.dll")]public static extern IntPtr GetDlgItem(IntPtr dialog,int id);
[DllImport("user32.dll",CharSet=CharSet.Unicode,SetLastError=true)]public static extern IntPtr SendMessageTimeout(IntPtr hwnd,uint message,UIntPtr w,IntPtr l,uint flags,uint timeout,out UIntPtr result);
}
"@
function Assert-Fixture {if((Get-Process -Id $FixturePid).StartTime.ToUniversalTime().ToFileTimeUtc() -ne $Started){throw 'Fixture process identity changed'}}
if($OwnTempRoot){
 $resolvedRoot=[IO.Path]::GetFullPath($OwnTempRoot)
 if(!(Split-Path $resolvedRoot -Leaf).StartsWith('dsh-e76-artifact-') -or [IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($OwnTempFile)) -ne $resolvedRoot -or [IO.Path]::GetFileName($OwnTempFile) -ne 'index.html' -or $ExpectedTitle -ne '打开 HTML（允许读取所在文件夹内的资源）'){throw 'Not an artifact self-fixture'}
 [uint32]$ownerPid=0;[FixtureWindows]::GetWindowThreadProcessId([IntPtr]$Owner,[ref]$ownerPid)|Out-Null
 if($ownerPid -ne $FixturePid){throw 'Artifact fixture does not own parent'}
}
Assert-Fixture
$script:dialog=[IntPtr]::Zero
$deadline=[DateTime]::UtcNow.AddSeconds(12)
do {
 Assert-Fixture
 [FixtureWindows]::EnumWindows({param($h,$p);[uint32]$ownerPid=0;[FixtureWindows]::GetWindowThreadProcessId($h,[ref]$ownerPid)|Out-Null
 if($ownerPid -eq $FixturePid -and [FixtureWindows]::GetWindow($h,4).ToInt64() -eq $Owner -and [FixtureWindows]::IsWindowVisible($h)){
  if($OwnTempRoot){$name=[Text.StringBuilder]::new(512);[FixtureWindows]::GetWindowText($h,$name,512)|Out-Null;$title=$name.ToString()}else{$element=[System.Windows.Automation.AutomationElement]::FromHandle($h);$title=$element.Current.Name}
  if($title -eq $ExpectedTitle){$script:dialog=$h;return $false}
 };return $true},[IntPtr]::Zero)|Out-Null
 if($script:dialog -eq [IntPtr]::Zero){Start-Sleep -Milliseconds 40}
}while($script:dialog -eq [IntPtr]::Zero -and [DateTime]::UtcNow -lt $deadline)
if($script:dialog -eq [IntPtr]::Zero){throw 'Scoped fixture picker not found'}
Assert-Fixture
# Artifact IFileOpenDialog may outlive a prior STA's UIA provider. Its native
# Edit/1148 and Button/1 identities are exact and every message is bounded.
if($OwnTempRoot){
 if($Action -eq 'observe'){Write-Output ('SCOPED_ARTIFACT_PICKER '+$script:dialog);exit 0}
 if($Action -eq 'cancel'){$target=$script:dialog;$message=0x10;$pointer=[IntPtr]::Zero}else{
  if($Action -ne 'select' -or !(Test-Path -LiteralPath $OwnTempFile -PathType Leaf)){throw 'Wrong artifact fixture action/file'}
  $script:editHwnd=[IntPtr]::Zero;$script:editCount=0
  [FixtureWindows]::EnumChildWindows($script:dialog,{param($h,$p);[uint32]$pidValue=0;[FixtureWindows]::GetWindowThreadProcessId($h,[ref]$pidValue)|Out-Null;$cls=[Text.StringBuilder]::new(128);[FixtureWindows]::GetClassName($h,$cls,128)|Out-Null
   if($pidValue -eq $FixturePid -and $cls.ToString() -eq 'Edit' -and [FixtureWindows]::GetDlgCtrlID($h) -eq 1148 -and [FixtureWindows]::IsChild($script:dialog,$h)){$script:editHwnd=$h;$script:editCount++};return $true
  },[IntPtr]::Zero)|Out-Null
  if($script:editCount -ne 1){throw 'No unique artifact Edit/1148'}
  Assert-Fixture;$text=[Runtime.InteropServices.Marshal]::StringToHGlobalUni($OwnTempFile)
  try{[UIntPtr]$result=[UIntPtr]::Zero;if([FixtureWindows]::SendMessageTimeout($script:editHwnd,0xC,[UIntPtr]::Zero,$text,0x22,2000,[ref]$result) -eq [IntPtr]::Zero -or $result -eq [UIntPtr]::Zero){throw 'Artifact filename message failed'}}finally{[Runtime.InteropServices.Marshal]::FreeHGlobal($text)}
  $target=[FixtureWindows]::GetDlgItem($script:dialog,1);$message=0xF5;$pointer=[IntPtr]::Zero
  [uint32]$pidValue=0;[FixtureWindows]::GetWindowThreadProcessId($target,[ref]$pidValue)|Out-Null;$cls=[Text.StringBuilder]::new(128);[FixtureWindows]::GetClassName($target,$cls,128)|Out-Null
  if($pidValue -ne $FixturePid -or $cls.ToString() -ne 'Button' -or ![FixtureWindows]::IsChild($script:dialog,$target)){throw 'Wrong artifact Button/1'}
 }
 Assert-Fixture;[UIntPtr]$result=[UIntPtr]::Zero
 if([FixtureWindows]::SendMessageTimeout($target,$message,[UIntPtr]::Zero,$pointer,0x22,2000,[ref]$result) -eq [IntPtr]::Zero){throw 'Artifact action message failed'}
 Write-Output ('SCOPED_ARTIFACT_'+$Action+' '+$script:dialog+' owner='+$Owner+' pid='+$FixturePid);exit 0
}
$root=[System.Windows.Automation.AutomationElement]::FromHandle($script:dialog)
if($root.Current.ProcessId -ne $FixturePid){throw 'Wrong fixture UIA process'}
$title=$root.Current.Name
$scope=[System.Windows.Automation.TreeScope]::Descendants
if($Action -eq 'select' -or $Action -eq 'diagnose'){
 if(!(Test-Path -LiteralPath $OwnTempFile -PathType Leaf) -or (!$OwnTempRoot -and !(Split-Path $OwnTempFile -Leaf).StartsWith('e71-own-'))){throw 'Not a fixture file'}
 $hostControl=$root.FindFirst($scope,[System.Windows.Automation.PropertyCondition]::new([System.Windows.Automation.AutomationElement]::AutomationIdProperty,'1148'))
 if(!$hostControl){throw 'No scoped filename host'}
 $script:editHwnd=[IntPtr]::Zero
 $script:editCount=0
 [FixtureWindows]::EnumChildWindows([IntPtr]$hostControl.Current.NativeWindowHandle,{param($h,$p)
  [uint32]$candidatePid=0;[FixtureWindows]::GetWindowThreadProcessId($h,[ref]$candidatePid)|Out-Null
  $cls=[Text.StringBuilder]::new(128);[FixtureWindows]::GetClassName($h,$cls,128)|Out-Null
  $id=[FixtureWindows]::GetDlgCtrlID($h)
  Write-Output ('SCOPED_NATIVE_FILENAME_CHILD class='+$cls+' id='+$id) | Write-Host
  if($candidatePid -eq $FixturePid -and $cls.ToString() -eq 'Edit' -and $id -eq 1148 -and [FixtureWindows]::IsChild($script:dialog,$h)){$script:editHwnd=$h;$script:editCount++}
  return $true
 },[IntPtr]::Zero)|Out-Null
 if($script:editHwnd -eq [IntPtr]::Zero -or $script:editCount -ne 1){throw 'No unique exact native filename Edit/1148'}
 Assert-Fixture
 $edit=[System.Windows.Automation.AutomationElement]::FromHandle($script:editHwnd)
 Write-Output ('SCOPED_FILENAME_UIA id='+$edit.Current.AutomationId+' type='+$edit.Current.ControlType.ProgrammaticName+' class='+$edit.Current.ClassName)
 $value=$null
 $supports=$edit.TryGetCurrentPattern([System.Windows.Automation.ValuePattern]::Pattern,[ref]$value)
 Write-Output ('SCOPED_FILENAME_VALUE_PATTERN='+$supports)
 if($Action -eq 'diagnose'){exit 0}
 Assert-Fixture
 if($supports){$value.SetValue($OwnTempFile)}else{
  # The native Edit identity is exact even when UIA normalizes to its ComboBox.
  # Fixed text-message adaptation is authorized only for this fixture file.
  $pointer=[Runtime.InteropServices.Marshal]::StringToHGlobalUni($OwnTempFile)
  try{
   [UIntPtr]$result=[UIntPtr]::Zero
   $ok=[FixtureWindows]::SendMessageTimeout($script:editHwnd,0x000C,[UIntPtr]::Zero,$pointer,0x22,2000,[ref]$result)
   if($ok -eq [IntPtr]::Zero -or $result -eq [UIntPtr]::Zero){throw 'Scoped filename WM_SETTEXT failed'}
   Write-Output 'SCOPED_NATIVE_EDIT_WM_SETTEXT'
  }finally{[Runtime.InteropServices.Marshal]::FreeHGlobal($pointer)}
 }
 $id='1'
}elseif($Action -eq 'cancel'){$id='2'}elseif($Action -eq 'observe'){Write-Output ('SCOPED_PICKER '+$script:dialog);exit 0}else{throw 'Unknown fixture action'}
$buttonHwnd=[FixtureWindows]::GetDlgItem($script:dialog,[int]$id)
[uint32]$buttonPid=0;[FixtureWindows]::GetWindowThreadProcessId($buttonHwnd,[ref]$buttonPid)|Out-Null
$buttonClass=[Text.StringBuilder]::new(128);[FixtureWindows]::GetClassName($buttonHwnd,$buttonClass,128)|Out-Null
if($buttonPid -ne $FixturePid -or ![FixtureWindows]::IsChild($script:dialog,$buttonHwnd) -or $buttonClass.ToString() -ne 'Button' -or [FixtureWindows]::GetDlgCtrlID($buttonHwnd) -ne [int]$id){throw 'No exact scoped action Button/ID'}
Assert-Fixture
$button=[System.Windows.Automation.AutomationElement]::FromHandle($buttonHwnd)
Write-Output ('SCOPED_NATIVE_BUTTON class='+$buttonClass+' id='+$id)
$invoke=$null
if($button.TryGetCurrentPattern([System.Windows.Automation.InvokePattern]::Pattern,[ref]$invoke)){
 Write-Output 'SCOPED_BUTTON_INVOKE_PATTERN'
 $invoke.Invoke()
}else{
 if($id -ne '1'){throw 'Scoped cancel button lacks InvokePattern'}
 [UIntPtr]$result=[UIntPtr]::Zero
 if([FixtureWindows]::SendMessageTimeout($buttonHwnd,0x00F5,[UIntPtr]::Zero,[IntPtr]::Zero,0x22,2000,[ref]$result) -eq [IntPtr]::Zero){throw 'Scoped button BM_CLICK failed'}
 Write-Output 'SCOPED_NATIVE_BUTTON_BM_CLICK'
}
Write-Output ('SCOPED_PICKER_'+$Action+' '+$script:dialog+' owner='+$Owner+' pid='+$FixturePid+' title='+$title)
