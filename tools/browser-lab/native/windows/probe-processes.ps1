param([string]$OutputPath = "$PSScriptRoot/evidence/processes.json", [string]$CefExe = "$PSScriptRoot/build/tests/cefsimple/Release/cefsimple.exe")
$ErrorActionPreference='Stop'
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class R78Tokens {
 [DllImport("kernel32.dll",SetLastError=true)] static extern IntPtr OpenProcess(uint access,bool inherit,int pid);
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool OpenProcessToken(IntPtr p,uint access,out IntPtr t);
 [DllImport("advapi32.dll",SetLastError=true)] static extern bool GetTokenInformation(IntPtr t,int cls,IntPtr b,int n,out int needed);
 [DllImport("advapi32.dll")] static extern bool IsTokenRestricted(IntPtr token);
 [DllImport("advapi32.dll")] static extern IntPtr GetSidSubAuthorityCount(IntPtr sid);
 [DllImport("advapi32.dll")] static extern IntPtr GetSidSubAuthority(IntPtr sid,uint index);
 [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr h);
 public static object Inspect(int pid){
  IntPtr p=OpenProcess(0x1000,false,pid);if(p==IntPtr.Zero)return new{error=Marshal.GetLastWin32Error()};
  IntPtr t;if(!OpenProcessToken(p,8,out t)){int e=Marshal.GetLastWin32Error();CloseHandle(p);return new{error=e};}
  try {
   int n;GetTokenInformation(t,25,IntPtr.Zero,0,out n);IntPtr b=Marshal.AllocHGlobal(n);
   uint integrity=0;try{if(GetTokenInformation(t,25,b,n,out n)){IntPtr sid=Marshal.ReadIntPtr(b);byte count=Marshal.ReadByte(GetSidSubAuthorityCount(sid));integrity=(uint)Marshal.ReadInt32(GetSidSubAuthority(sid,(uint)(count-1)));}}finally{Marshal.FreeHGlobal(b);}
   int app=-1;IntPtr a=Marshal.AllocHGlobal(4);try{if(GetTokenInformation(t,29,a,4,out n))app=Marshal.ReadInt32(a);}finally{Marshal.FreeHGlobal(a);}
   return new{integrityRid=integrity,restricted=IsTokenRestricted(t),appContainer=app};
  }finally{CloseHandle(t);CloseHandle(p);}
 }
}
'@
$exe=[IO.Path]::GetFullPath($CefExe)
$processes=@(Get-CimInstance Win32_Process -Filter "name='cefsimple.exe'" | Where-Object { $_.ExecutablePath -eq $exe } | ForEach-Object {
 [ordered]@{pid=$_.ProcessId;parent=$_.ParentProcessId;commandLine=$_.CommandLine;token=[R78Tokens]::Inspect($_.ProcessId)}
})
ConvertTo-Json -InputObject $processes -Depth 6 | Set-Content $OutputPath
$processes | ConvertTo-Json -Depth 6
