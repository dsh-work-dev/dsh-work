// Optional research-only browser-process startup tracing. No renderer I/O.
#include <windows.h>
#include <dbghelp.h>
#include <cstdio>
#include <string>

namespace r78 {
inline std::wstring trace_path;
inline void Trace(const char* phase) {
  if (trace_path.empty()) return;
  HANDLE file=CreateFileW(trace_path.c_str(),FILE_APPEND_DATA,FILE_SHARE_READ|FILE_SHARE_WRITE,nullptr,OPEN_ALWAYS,FILE_ATTRIBUTE_NORMAL,nullptr);
  if(file==INVALID_HANDLE_VALUE)return;
  char line[512];int len=snprintf(line,sizeof(line),"pid=%lu tick=%llu %s\r\n",GetCurrentProcessId(),GetTickCount64(),phase);
  DWORD written;WriteFile(file,line,len,&written,nullptr);FlushFileBuffers(file);CloseHandle(file);
}
inline LONG CALLBACK OnException(EXCEPTION_POINTERS* pointers) {
  if(pointers->ExceptionRecord->ExceptionCode!=EXCEPTION_BREAKPOINT && pointers->ExceptionRecord->ExceptionCode!=EXCEPTION_ACCESS_VIOLATION)return EXCEPTION_CONTINUE_SEARCH;
  char line[512];snprintf(line,sizeof(line),"EXCEPTION code=%08lx address=%p RAX=%llu RDX=%llu RIP=%llx LastError=%lu",pointers->ExceptionRecord->ExceptionCode,pointers->ExceptionRecord->ExceptionAddress,static_cast<unsigned long long>(pointers->ContextRecord->Rax),static_cast<unsigned long long>(pointers->ContextRecord->Rdx),static_cast<unsigned long long>(pointers->ContextRecord->Rip),GetLastError());Trace(line);
  HMODULE dbg=LoadLibraryW(L"dbghelp.dll");
  if(dbg){
    using DumpFn=BOOL (WINAPI*)(HANDLE,DWORD,HANDLE,MINIDUMP_TYPE,PMINIDUMP_EXCEPTION_INFORMATION,PMINIDUMP_USER_STREAM_INFORMATION,PMINIDUMP_CALLBACK_INFORMATION);
    auto dump=reinterpret_cast<DumpFn>(GetProcAddress(dbg,"MiniDumpWriteDump"));
    HANDLE file=CreateFileW((trace_path+L".dmp").c_str(),GENERIC_WRITE,FILE_SHARE_READ,nullptr,CREATE_ALWAYS,FILE_ATTRIBUTE_NORMAL,nullptr);
    if(dump&&file!=INVALID_HANDLE_VALUE){MINIDUMP_EXCEPTION_INFORMATION info{GetCurrentThreadId(),pointers,FALSE};BOOL ok=dump(GetCurrentProcess(),GetCurrentProcessId(),file,MiniDumpNormal,&info,nullptr,nullptr);snprintf(line,sizeof(line),"MiniDumpWriteDump ok=%d error=%lu",ok,GetLastError());Trace(line);}
    if(file!=INVALID_HANDLE_VALUE)CloseHandle(file);
  }
  return EXCEPTION_CONTINUE_SEARCH;
}
inline void SetupTrace(){
  const wchar_t* cmd=GetCommandLineW();
  if(wcsstr(cmd,L"--type="))return;
  const wchar_t* pos=wcsstr(cmd,L"--trace-file=");if(!pos)return;pos+=13;
  const wchar_t* end=wcschr(pos,L' ');trace_path.assign(pos,end?end-pos:wcslen(pos));
  AddVectoredExceptionHandler(1,OnException);Trace("RunWinMain entered");
}
}
