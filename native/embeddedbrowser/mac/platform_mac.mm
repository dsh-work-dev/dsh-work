#define __STDC_WANT_LIB_EXT1__ 1
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
#include <cstdlib>
#include <cstdio>
#include <string.h>
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
uint32_t download_host_pid=0;
uint64_t download_host_started=0;
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
void Lifecycle(const char* event) {
  // Fixed state markers only; never write a URL, page data, token or error body.
  for (const char* allowed : {
    "startup-owner-carrier-failed","startup-owner-registration-failed",
    "startup-owner-registration-identity-failed","startup-owner-registration-write-failed",
    "startup-owner-registration-reply-failed",
    "startup-owner-descriptor-failed","startup-host-carrier-failed",
    "startup-host-hmac-failed","startup-owner-initialize-failed","download-writer-admission-failed",
    "shutdown-enter","before-close","before-close-tab-erased","before-close-return",
    "frame-destroyed-enter","frame-destroyed-return","loop-returned","owner-stop",
    "owner-read-failed","monitor-join-start","monitor-joined","shutdown-request",
    "shutdown-ack","shutdown-ack-failed","shutdown-returned","quiescent-sent",
    "quiescent-failed","release-received","release-failed"}) {
    if(std::strcmp(event,allowed)!=0)continue;
    char line[128];const int n=std::snprintf(line,sizeof(line),"DSH_CEF_EVENT %s\n",allowed);
    if(n>0&&n<static_cast<int>(sizeof(line))) (void)write(STDERR_FILENO,line,n);
    return;
  }
}

std::string TakeEnvironment(const char* name) { auto text=Get(name); unsetenv(name); return text; }
bool OpenTransport(const std::string& path) {
  const int owner_one=1;
  if(!Number(TakeEnvironment("DSH_WORK_CEF_OWNER_FD"),owner) ||
     fcntl(owner,F_SETFD,FD_CLOEXEC)<0 ||
     setsockopt(owner,SOL_SOCKET,SO_NOSIGPIPE,&owner_one,sizeof(owner_one))) {
    Lifecycle("startup-owner-descriptor-failed");
    return false;
  }
  if(!RegisterProcess("broker",0,nullptr))return false;
  uint32_t pid=0; uint64_t started=0;
  if(!Number(TakeEnvironment("DSH_WORK_CEF_HOST_PID"),pid) || !Number(TakeEnvironment("DSH_WORK_CEF_HOST_STARTED"),started)) return false;
  int result=EIO; dsh_ipc_peer peer{};
  std::thread io([&]{result=dsh_ipc_connect(path.c_str(),-1,&transport,&peer);}); io.join();
  if(result || peer.pid!=pid || peer.started!=started) {
    Lifecycle("startup-host-carrier-failed");
    return false;
  }
  download_host_pid=peer.pid;
  download_host_started=peer.started;
  int flags=fcntl(transport,F_GETFL),one=1;
  return flags>=0 && fcntl(transport,F_SETFL,flags&~O_NONBLOCK)==0 && setsockopt(transport,SOL_SOCKET,SO_NOSIGPIPE,&one,sizeof(one))==0;
}
int ReceiveDownloadWriter(const std::string& endpoint) {
  int fd=-1;
  if(dsh_ipc_receive_writer(endpoint.c_str(),-1,download_host_pid,download_host_started,&fd)){
    Lifecycle("download-writer-admission-failed");
    return -1;
  }
  return fd;
}
bool Transfer(bool write,void* data,uint32_t size) {
  auto* p=static_cast<char*>(data);
  while(size) { auto n=write?send(transport,p,size,0):recv(transport,p,size,0); if(n<0&&errno==EINTR)continue; if(n<=0)return false; p+=n;size-=n; }
  return true;
}
bool Authenticate(const std::string& token) {
  std::array<unsigned char,32> nonce{},actual{},expected{};
  auto end=Clock::now()+std::chrono::seconds(3);
  if(!Bytes(transport,false,nonce.data(),32,end)) {
    Lifecycle("startup-host-hmac-failed");
    return false;
  }
  auto mac=[&](const char* role) { std::string data=role; data.append(reinterpret_cast<char*>(nonce.data()),32); CCHmac(kCCHmacAlgSHA256,token.data(),token.size(),data.data(),data.size(),expected.data()); };
  mac("worker");
  if(!Bytes(transport,true,expected.data(),32,end)||!Bytes(transport,false,actual.data(),32,end)) {
    Lifecycle("startup-host-hmac-failed");
    return false;
  }
  mac("host"); unsigned char difference=0; for(size_t i=0;i<32;i++)difference|=actual[i]^expected[i];
  if(difference!=0) {
    Lifecycle("startup-host-hmac-failed");
    return false;
  }
  std::string reply;
  if(!OwnerSend(@{@"kind":@"initialize"}) || !Frame(owner,false,reply) || reply!="{\"kind\":\"initialize\"}") {
    Lifecycle("startup-owner-initialize-failed");
    return false;
  }
  return true;
}
void Erase(std::string& value) {
  // Darwin exposes non-elidable C11 Annex K erasure via <string.h> (10.9+).
  if (!value.empty() && memset_s(value.data(),value.size(),0,value.size()) != 0) std::abort();
  value.clear();
}
void CancelTransport() { Lifecycle("loop-returned"); if(transport>=0)shutdown(transport,SHUT_RDWR); }
void CloseTransport() { if(transport>=0)close(transport);transport=-1;if(owner>=0)close(owner);owner=-1; }
bool ConfigureWindow(CefWindowInfo& window) { window.SetAsWindowless(nullptr);window.runtime_style=CEF_RUNTIME_STYLE_ALLOY;return true; }
void StartOwnerMonitor(std::function<void()> stop) {
  const char* version=cef_version_full();
  if(!OwnerSend(@{@"kind":@"runtime",@"version":version?@(version):@"",@"crashReporting":@(CefCrashReportingEnabled())})) {owner_failed=true;stop();return;}
  monitor_running=true;
  owner_monitor=std::thread([stop]{
    while(monitor_running) {
      if(!Ready(owner,POLLIN,Clock::now()+std::chrono::milliseconds(50)))continue;
      std::string text;
      bool ok=Frame(owner,false,text);if(!ok || text!="{\"kind\":\"stop\"}")owner_failed=true;
      Lifecycle(ok?"owner-stop":"owner-read-failed");
      stop();return;
    }
  });
}
void PrepareOwnedShutdown() {
  Lifecycle("monitor-join-start");
  monitor_running=false;if(owner_monitor.joinable())owner_monitor.join();
  Lifecycle("monitor-joined");Lifecycle("shutdown-request");
  const bool ack=OwnerSend(@{@"kind":@"shutdown-start"})&&OwnerReply("{\"kind\":\"shutdown-start\"}",3);
  Lifecycle(ack?"shutdown-ack":"shutdown-ack-failed");if(!ack)owner_failed=true;
}
int FinishOwnedShutdown() {
  Lifecycle("shutdown-returned");
  if(owner_failed || !OwnerSend(@{@"kind":@"quiescent",@"broker":Identity(getpid()),@"group":Census(false),@"children":Census(true)})) {Lifecycle("quiescent-failed");return 75;}
  Lifecycle("quiescent-sent");
  const bool released=OwnerReply("{\"kind\":\"release\"}",15);
  Lifecycle(released?"release-received":"release-failed");return released?0:75;
}
}

namespace {
bool RegisterProcess(const char* kind,int argc,char** argv) {
  auto path=Get("DSH_WORK_CEF_OWNER_SOCKET");
  auto generation=Get("DSH_WORK_CEF_OWNER_GENERATION");
  uint32_t pid=0;uint64_t started=0;
  if(path.empty()||generation.empty()||!Number(Get("DSH_WORK_CEF_OWNER_PID"),pid)||!Number(Get("DSH_WORK_CEF_OWNER_STARTED"),started))return false;
  int fd=-1;dsh_ipc_peer peer{};
  if(dsh_ipc_connect(path.c_str(),-1,&fd,&peer)!=0){
    if(std::strcmp(kind,"broker")==0)dsh_platform::Lifecycle("startup-owner-carrier-failed");
    return false;
  }
  int one=1;setsockopt(fd,SOL_SOCKET,SO_NOSIGPIPE,&one,sizeof(one));
  bool matched=peer.pid==pid&&peer.started==started;
  std::string type, renderer_id;
  bool type_seen=false, renderer_id_seen=false;
  for(int i=1;i<argc;i++){
    std::string value=argv[i];
    if(value.rfind("--type=",0)==0){
      if(type_seen)matched=false;
      type_seen=true;
      type=value.substr(7);
    }
    if(value.rfind("--renderer-client-id=",0)==0){
      if(renderer_id_seen)matched=false;
      renderer_id_seen=true;
      renderer_id=value.substr(21);
    }
  }
  // These switches are Chromium-owned identifiers, not arbitrary page values.
  uint32_t child=0;
  if(type=="renderer" && (!Number(renderer_id,child)||child>0x7fffffff||renderer_id!=std::to_string(child)))matched=false;
  if(type!="renderer" && renderer_id_seen)matched=false;
  if(type.find_first_not_of("abcdefghijklmnopqrstuvwxyz-0123456789")!=std::string::npos)matched=false;
  std::string request="{\"kind\":\""+std::string(kind)+"\",\"generation\":\""+generation+"\",\"type\":\""+type+"\",\"rendererChild\":"+std::to_string(child)+"}",reply;
  const char* failure=nullptr;
  if(!matched)failure="startup-owner-registration-identity-failed";
  else if(!Frame(fd,true,request))failure="startup-owner-registration-write-failed";
  else if(!Frame(fd,false,reply)||reply!="{\"kind\":\"registered\"}")failure="startup-owner-registration-reply-failed";
  if(failure&&std::strcmp(kind,"broker")==0)dsh_platform::Lifecycle(failure);
  close(fd);return failure==nullptr;
}
}
bool RegisterOwnedHelper(int argc,char** argv) {
  bool result=RegisterProcess("helper",argc,argv);
  for(const char* key:{"DSH_WORK_CEF_OWNER_SOCKET","DSH_WORK_CEF_OWNER_GENERATION","DSH_WORK_CEF_OWNER_PID","DSH_WORK_CEF_OWNER_STARTED"})unsetenv(key);
  return result;
}
