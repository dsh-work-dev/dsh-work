param([Parameter(Mandatory=$true)][int]$UiProcessId,[Parameter(Mandatory=$true)][int]$BrokerProcessId,[Parameter(Mandatory=$true)][string]$Output,[int]$Seconds=20)
$ErrorActionPreference='Stop'
# Read-only Win32 geometry sampling of the exact held UI/broker instances.
Add-Type @"
using System;using System.Collections.Generic;using System.Runtime.InteropServices;
public static class LayoutWindows {
 public delegate bool EnumProc(IntPtr w,IntPtr p);
 [StructLayout(LayoutKind.Sequential)] public struct Rect {public int Left,Top,Right,Bottom;}
 [StructLayout(LayoutKind.Sequential)] public struct Point {public int X,Y;}
 [DllImport("user32.dll")] static extern bool EnumWindows(EnumProc f,IntPtr p);
 [DllImport("user32.dll")] static extern bool EnumChildWindows(IntPtr w,EnumProc f,IntPtr p);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr w,out uint p);
 [DllImport("user32.dll")] public static extern IntPtr GetParent(IntPtr w);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr w);
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr w,out Rect r);
 [DllImport("user32.dll")] public static extern bool ClientToScreen(IntPtr w,ref Point p);
 public static IntPtr[] Roots(uint pid){var a=new List<IntPtr>();EnumWindows((w,p)=>{uint n;GetWindowThreadProcessId(w,out n);if(n==pid)a.Add(w);return true;},IntPtr.Zero);return a.ToArray();}
 public static IntPtr[] Children(IntPtr root,uint pid){var a=new List<IntPtr>();EnumChildWindows(root,(w,p)=>{uint n;GetWindowThreadProcessId(w,out n);if(n==pid)a.Add(w);return true;},IntPtr.Zero);return a.ToArray();}
}
"@
$ui=Get-Process -Id $UiProcessId;$broker=Get-Process -Id $BrokerProcessId
$null=$ui.Handle;$null=$broker.Handle
$identity=@{ui=@{pid=$ui.Id;started=$ui.StartTime.ToFileTimeUtc();path=$ui.Path};broker=@{pid=$broker.Id;started=$broker.StartTime.ToFileTimeUtc();path=$broker.Path}}
$rows=[System.Collections.Generic.List[object]]::new();$until=[DateTime]::UtcNow.AddSeconds($Seconds)
try {
 while([DateTime]::UtcNow -lt $until){
  if($ui.HasExited -or $broker.HasExited){throw 'Held UI or broker exited during layout check'}
  foreach($root in [LayoutWindows]::Roots($UiProcessId)){
   $origin=[LayoutWindows+Point]::new();if(-not [LayoutWindows]::ClientToScreen($root,[ref]$origin)){throw 'UI origin unavailable'}
   foreach($w in [LayoutWindows]::Children($root,$BrokerProcessId)){
    $rect=[LayoutWindows+Rect]::new();if(-not [LayoutWindows]::GetWindowRect($w,[ref]$rect)){throw 'Guest rectangle unavailable'}
    $rows.Add(@{at=[DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds();root=$root.ToInt64();hwnd=$w.ToInt64();parent=([LayoutWindows]::GetParent($w)).ToInt64();visible=[LayoutWindows]::IsWindowVisible($w);x=$rect.Left-$origin.X;y=$rect.Top-$origin.Y;width=$rect.Right-$rect.Left;height=$rect.Bottom-$rect.Top})
   }
  }
  Start-Sleep -Milliseconds 25
 }
}finally {
 @{identity=$identity;rows=$rows.ToArray()}|ConvertTo-Json -Depth 8|Set-Content -LiteralPath $Output
 $ui.Dispose();$broker.Dispose()
}
