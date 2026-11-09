param([int]$FixturePid,[long]$Started,[long]$Owner,[string]$Action,[string]$OwnTempFile,[string]$ExpectedTitle)
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
[DllImport("user32.dll")]public static extern int GetDlgCtrlID(IntPtr hwnd);
[DllImport("user32.dll")]public static extern IntPtr GetDlgItem(IntPtr dialog,int id);
[DllImport("user32.dll",CharSet=CharSet.Unicode,SetLastError=true)]public static extern IntPtr SendMessageTimeout(IntPtr hwnd,uint message,UIntPtr w,IntPtr l,uint flags,uint timeout,out UIntPtr result);
}
"@
function Assert-Fixture {if((Get-Process -Id $FixturePid).StartTime.ToUniversalTime().ToFileTimeUtc() -ne $Started){throw 'Fixture process identity changed'}}
Assert-Fixture
$script:dialog=[IntPtr]::Zero
$deadline=[DateTime]::UtcNow.AddSeconds(12)
do {
 Assert-Fixture
 [FixtureWindows]::EnumWindows({param($h,$p);[uint32]$ownerPid=0;[FixtureWindows]::GetWindowThreadProcessId($h,[ref]$ownerPid)|Out-Null
 if($ownerPid -eq $FixturePid -and [FixtureWindows]::GetWindow($h,4).ToInt64() -eq $Owner -and [FixtureWindows]::IsWindowVisible($h)){
  $element=[System.Windows.Automation.AutomationElement]::FromHandle($h)
  if($element.Current.Name -eq $ExpectedTitle){$script:dialog=$h;return $false}
 };return $true},[IntPtr]::Zero)|Out-Null
 if($script:dialog -eq [IntPtr]::Zero){Start-Sleep -Milliseconds 40}
}while($script:dialog -eq [IntPtr]::Zero -and [DateTime]::UtcNow -lt $deadline)
if($script:dialog -eq [IntPtr]::Zero){throw 'Scoped fixture picker not found'}
Assert-Fixture
$root=[System.Windows.Automation.AutomationElement]::FromHandle($script:dialog)
if($root.Current.ProcessId -ne $FixturePid){throw 'Wrong fixture UIA process'}
$title=$root.Current.Name
$scope=[System.Windows.Automation.TreeScope]::Descendants
if($Action -eq 'select' -or $Action -eq 'diagnose'){
 if(!(Test-Path -LiteralPath $OwnTempFile -PathType Leaf) -or !(Split-Path $OwnTempFile -Leaf).StartsWith('e71-own-')){throw 'Not a fixture file'}
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
