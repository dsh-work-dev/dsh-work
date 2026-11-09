#include "platform_mac.h"
#include "owner_mac.h"
#include "include/cef_version_info.h"
#include "include/cef_crash_util.h"
#include "../../../internal/localipc/mach_peer.h"
#import <Foundation/Foundation.h>
#include <CommonCrypto/CommonHMAC.h>
#include <array>
#include <atomic>
#include <charconv>
#include <chrono>
#include <cstring>
#include <fcntl.h>
#include <libproc.h>
#include <poll.h>
#include <sys/proc_info.h>
#include <sys/proc.h>
#include <sys/socket.h>
#include <thread>
#include <unistd.h>

namespace {
int transport=-1, owner=-1;
std::atomic<bool> monitor_running{false};
std::thread owner_monitor;
std::atomic<bool> owner_failed{false};
using Clock=std::chrono::steady_clock;
bool Ready(int fd,short event,Clock::time_point deadline) {
  for(;;) {
    auto left=std::chrono::duration_cast<std::chrono::milliseconds>(deadline-Clock::now()).count();
    if(left<=0) return false;
    pollfd p{fd,event,0}; int n=poll(&p,1,static_cast<int>(left));
    if(n<0 && errno==EINTR) continue;
    return n>0;
  }
}
bool Bytes(int fd,bool writing,void* data,size_t size,Clock::time_point end) {
  auto* p=static_cast<char*>(data);
  while(size) {
    if(!Ready(fd,writing?POLLOUT:POLLIN,end)) return false;
    auto n=writing?send(fd,p,size,MSG_DONTWAIT):recv(fd,p,size,MSG_DONTWAIT);
    if(n<0 && (errno==EINTR || errno==EAGAIN || errno==EWOULDBLOCK)) continue;
    if(n<=0) return false;
    p+=n; size-=n;
  }
  return true;
}
bool FrameUntil(int fd,bool writing,std::string& text,Clock::time_point end) {
  uint32_t size=writing?static_cast<uint32_t>(text.size()):0;
  if(!Bytes(fd,writing,&size,4,end) || !size || size>(1U<<20)) return false;
  if(!writing) text.resize(size);
  return Bytes(fd,writing,text.data(),size,end);
}
bool Frame(int fd,bool writing,std::string& text,int seconds=3) {return FrameUntil(fd,writing,text,Clock::now()+std::chrono::seconds(seconds));}
bool OwnerReply(const char* expected,int seconds) {
  const auto end=Clock::now()+std::chrono::seconds(seconds);
  for(;;) {
    std::string reply;if(!FrameUntil(owner,false,reply,end))return false;
    if(reply==expected)return true;
    if(reply!="{\"kind\":\"stop\"}")return false; // Already stopping; repeated stop is idempotent.
  }
}
std::string Get(const char* name) { const char* p=getenv(name); return p?p:""; }
template<class T> bool Number(const std::string& text,T& out) {
  auto [p,e]=std::from_chars(text.data(),text.data()+text.size(),out);
  return e==std::errc() && p==text.data()+text.size() && out>0;
}
NSDictionary* Identity(pid_t pid) {
  proc_bsdinfo b{}; int size=0,error=0;
  for(int i=0;i<3;i++) { errno=0; size=proc_pidinfo(pid,PROC_PIDTBSDINFO,0,&b,sizeof(b)); error=errno; if(size==static_cast<int>(sizeof(b))||error!=EINTR)break; }
  if(size!=static_cast<int>(sizeof(b))) return @{ @"pid":@(pid), @"error":@(error?error:EIO), @"state":error==ESRCH?@"gone":@"unknown" };
  char path[PROC_PIDPATHINFO_MAXSIZE]{}; proc_pidpath(pid,path,sizeof(path));
  return @{ @"pid":@(pid), @"started":@(b.pbi_start_tvsec*1000000ULL+b.pbi_start_tvusec), @"ppid":@(b.pbi_ppid), @"pgid":@(b.pbi_pgid), @"status":@(b.pbi_status), @"state":b.pbi_status==SZOMB?@"zombie":@"live", @"path":@(path) };
}
NSDictionary* Census(bool children) {
  std::array<pid_t,4096> pids{}; errno=0;
  int n=children?proc_listchildpids(getpid(),pids.data(),sizeof(pids)):proc_listpgrppids(getpgrp(),pids.data(),sizeof(pids));
  if(n<0 || (n==0 && errno && errno!=ESRCH)) return @{ @"error":@(errno?errno:EIO) };
  NSMutableArray* list=[NSMutableArray array];
  for(int i=0;i<n;i++) if(pids[i]>0) [list addObject:Identity(pids[i])];
  return @{ @"members":list, @"truncated":@(n==static_cast<int>(pids.size())) };
}
std::string JSON(NSDictionary* value) {
  NSData* data=[NSJSONSerialization dataWithJSONObject:value options:0 error:nil];
  return data?std::string(static_cast<const char*>(data.bytes),data.length):std::string();
}
bool OwnerSend(NSDictionary* value) { auto text=JSON(value); return Frame(owner,true,text); }
bool RegisterProcess(const char* kind,int argc,char** argv);
}

namespace dsh_platform {
std::string TakeEnvironment(const char* name) { auto text=Get(name); unsetenv(name); return text; }
bool OpenTransport(const std::string& path) {
  if(!Number(TakeEnvironment("DSH_WORK_CEF_OWNER_FD"),owner) || fcntl(owner,F_SETFD,FD_CLOEXEC)<0) return false;
  const int owner_one=1;if(setsockopt(owner,SOL_SOCKET,SO_NOSIGPIPE,&owner_one,sizeof(owner_one)))return false;
  if(!RegisterProcess("broker",0,nullptr))return false;
  uint32_t pid=0; uint64_t started=0;
  if(!Number(TakeEnvironment("DSH_WORK_CEF_HOST_PID"),pid) || !Number(TakeEnvironment("DSH_WORK_CEF_HOST_STARTED"),started)) return false;
  int result=EIO; dsh_ipc_peer peer{};
  std::thread io([&]{result=dsh_ipc_connect(path.c_str(),-1,&transport,&peer);}); io.join();
  if(result || peer.pid!=pid || peer.started!=started) return false;
  int flags=fcntl(transport,F_GETFL),one=1;
  return flags>=0 && fcntl(transport,F_SETFL,flags&~O_NONBLOCK)==0 && setsockopt(transport,SOL_SOCKET,SO_NOSIGPIPE,&one,sizeof(one))==0;
}
bool Transfer(bool write,void* data,uint32_t size) {
  auto* p=static_cast<char*>(data);
  while(size) { auto n=write?send(transport,p,size,0):recv(transport,p,size,0); if(n<0&&errno==EINTR)continue; if(n<=0)return false; p+=n;size-=n; }
  return true;
}
bool Authenticate(const std::string& token) {
  std::array<unsigned char,32> nonce{},actual{},expected{};
  auto end=Clock::now()+std::chrono::seconds(3);
  if(!Bytes(transport,false,nonce.data(),32,end)) return false;
  auto mac=[&](const char* role) { std::string data=role; data.append(reinterpret_cast<char*>(nonce.data()),32); CCHmac(kCCHmacAlgSHA256,token.data(),token.size(),data.data(),data.size(),expected.data()); };
  mac("worker"); if(!Bytes(transport,true,expected.data(),32,end)||!Bytes(transport,false,actual.data(),32,end))return false;
  mac("host"); unsigned char difference=0; for(size_t i=0;i<32;i++)difference|=actual[i]^expected[i];
  std::string reply;
  return difference==0 && OwnerSend(@{@"kind":@"initialize"}) && Frame(owner,false,reply) && reply=="{\"kind\":\"initialize\"}";
}
void Erase(std::string& value) { explicit_bzero(value.data(),value.size()); value.clear(); }
void CancelTransport() { if(transport>=0)shutdown(transport,SHUT_RDWR); }
void CloseTransport() { if(transport>=0)close(transport);transport=-1;if(owner>=0)close(owner);owner=-1; }
bool Visible(Window) { return false; }
std::string Hide(Window) { return {}; }
std::string Present(Window,CefRefPtr<CefDictionaryValue>) { return "CEF_PRESENT_UNAVAILABLE"; }
bool ConfigureWindow(CefWindowInfo& window) { window.SetAsWindowless(nullptr);window.runtime_style=CEF_RUNTIME_STYLE_ALLOY;return true; }
void ViewRect(CefRect& rect) { rect=CefRect(0,0,1024,768); }
void Paint(const void*,int,int) {} // CPU OSR engine checkpoint; no UI presentation claim.
void StartOwnerMonitor(std::function<void()> stop) {
  const char* version=cef_version_full();
  if(!OwnerSend(@{@"kind":@"runtime",@"version":version?@(version):@"",@"crashReporting":@(CefCrashReportingEnabled())})) {owner_failed=true;stop();return;}
  monitor_running=true;
  owner_monitor=std::thread([stop]{
    while(monitor_running) {
      if(!Ready(owner,POLLIN,Clock::now()+std::chrono::milliseconds(50)))continue;
      std::string text;
      bool ok=Frame(owner,false,text);if(!ok || text!="{\"kind\":\"stop\"}")owner_failed=true;
      stop();return;
    }
  });
}
void PrepareOwnedShutdown() {
  monitor_running=false;if(owner_monitor.joinable())owner_monitor.join();
  if(!OwnerSend(@{@"kind":@"shutdown-start"})||!OwnerReply("{\"kind\":\"shutdown-start\"}",3))owner_failed=true;
}
int FinishOwnedShutdown() {
  if(owner_failed || !OwnerSend(@{@"kind":@"quiescent",@"broker":Identity(getpid()),@"group":Census(false),@"children":Census(true)})) return 75;
  return OwnerReply("{\"kind\":\"release\"}",15)?0:75;
}
}

namespace {
bool RegisterProcess(const char* kind,int argc,char** argv) {
  auto path=Get("DSH_WORK_CEF_OWNER_SOCKET");
  auto generation=Get("DSH_WORK_CEF_OWNER_GENERATION");
  uint32_t pid=0;uint64_t started=0;
  if(path.empty()||generation.empty()||!Number(Get("DSH_WORK_CEF_OWNER_PID"),pid)||!Number(Get("DSH_WORK_CEF_OWNER_STARTED"),started))return false;
  int fd=-1;dsh_ipc_peer peer{};if(dsh_ipc_connect(path.c_str(),-1,&fd,&peer)!=0)return false;
  int one=1;setsockopt(fd,SOL_SOCKET,SO_NOSIGPIPE,&one,sizeof(one));
  bool matched=peer.pid==pid&&peer.started==started;
  std::string type;
  for(int i=1;i<argc;i++){std::string value=argv[i];if(value.rfind("--type=",0)==0)type=value.substr(7);}
  // These switches are Chromium-owned identifiers, not arbitrary page values.
  if(type.find_first_not_of("abcdefghijklmnopqrstuvwxyz-0123456789")!=std::string::npos)matched=false;
  std::string request="{\"kind\":\""+std::string(kind)+"\",\"generation\":\""+generation+"\",\"type\":\""+type+"\"}",reply;
  bool result=matched&&Frame(fd,true,request)&&Frame(fd,false,reply)&&reply=="{\"kind\":\"registered\"}";
  close(fd);return result;
}
}
bool RegisterOwnedHelper(int argc,char** argv) {
  bool result=RegisterProcess("helper",argc,argv);
  for(const char* key:{"DSH_WORK_CEF_OWNER_SOCKET","DSH_WORK_CEF_OWNER_GENERATION","DSH_WORK_CEF_OWNER_PID","DSH_WORK_CEF_OWNER_STARTED"})unsetenv(key);
  return result;
}
