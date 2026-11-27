#include "identity.h"
#include "include/base/cef_bind.h"
#include "include/base/cef_callback.h"
#include "include/cef_app.h"
#include "include/cef_application_mac.h"
#include "include/cef_client.h"
#include "include/cef_crash_util.h"
#include "include/cef_frame.h"
#include "include/cef_command_line.h"
#include "include/cef_task.h"
#include "include/cef_version.h"
#include "include/cef_version_info.h"
#include "include/wrapper/cef_closure_task.h"
#include "include/wrapper/cef_library_loader.h"
#import <Cocoa/Cocoa.h>
#include <array>
#include <atomic>
#include <chrono>
#include <fcntl.h>
#include <poll.h>
#include <signal.h>
#include <sys/wait.h>
#include <thread>

@interface OwnerProbeApplication : NSApplication <CefAppProtocol> { BOOL handling_; }
@end
@implementation OwnerProbeApplication
- (BOOL)isHandlingSendEvent { return handling_; }
- (void)setHandlingSendEvent:(BOOL)value { handling_=value; }
- (void)sendEvent:(NSEvent*)event { CefScopedSendingEvent scope; [super sendEvent:event]; }
@end

namespace {
std::array<CefRefPtr<CefBrowser>,2> browsers;
int closed=0;
bool closing=false; // UI thread only.
std::string page_url;
std::string directory,cut;
int lease=-1;
std::atomic<bool> lease_lost{false},startup_aborted{false},launch_held{false};
bool ObserveEOF() {
  char byte; const auto size=read(lease,&byte,1);
  if(size==0) {
    if(!lease_lost.exchange(true)) owner_probe::Emit("{\"event\":\"startup-lease-eof\"}");
    return true;
  }
  if(size<0 && errno!=EINTR && errno!=EAGAIN && errno!=EWOULDBLOCK) {
    owner_probe::Emit("{\"event\":\"lease-read-failed\",\"error\":"+std::to_string(errno)+"}");
    lease_lost=true;
  }
  return lease_lost;
}
int HoldRoot(int result) {
  const auto deadline=std::chrono::steady_clock::now()+std::chrono::seconds(30);
  while(std::chrono::steady_clock::now()<deadline) {
    for(;;) {
      int status=0; pid_t child=waitpid(-1,&status,WNOHANG);
      if(child>0) { owner_probe::Emit("{\"event\":\"child-reaped\",\"pid\":"+std::to_string(child)+",\"wait_status\":"+std::to_string(status)+"}"); continue; }
      if(child<0 && errno==EINTR) continue;
      if(child<0 && errno!=ECHILD) { owner_probe::Emit("{\"event\":\"reap-error\",\"error\":"+std::to_string(errno)+"}"); return 7; }
      break;
    }
    if(access((directory+"/release").c_str(),F_OK)==0) {
      owner_probe::Emit("{\"event\":\"root-release\",\"group\":"+owner_probe::Census(getpgrp())+"}"); return result;
    }
    std::this_thread::sleep_for(std::chrono::milliseconds(25));
  }
  owner_probe::Emit("{\"event\":\"root-release-timeout\"}"); return 6;
}
bool ArmLease() {
  lease=open((directory+"/lease").c_str(),O_RDONLY|O_NONBLOCK|O_CLOEXEC);
  if(lease<0) { owner_probe::Emit("{\"event\":\"lease-open-failed\"}"); return false; }
  bool connected=false;
  const auto deadline=std::chrono::steady_clock::now()+std::chrono::seconds(5);
  while(std::chrono::steady_clock::now()<deadline) {
    char byte; auto size=read(lease,&byte,1); const int error=size<0?errno:0;
    if(size==1 && byte=='R') { owner_probe::Emit("{\"event\":\"lease-armed\"}"); return true; }
    if(size==1) { owner_probe::Emit("{\"event\":\"lease-invalid-arm\"}"); return false; }
    if(size<0 && (error==EAGAIN || error==EWOULDBLOCK)) {
      if(!connected) { connected=true; owner_probe::Emit("{\"event\":\"lease-connected-unarmed\"}"); }
    } else if(size==0 && connected) {
      lease_lost=true; owner_probe::Emit("{\"event\":\"startup-lease-eof\"}"); return false;
    }
    else if(size<0 && error!=EINTR) { owner_probe::Emit("{\"event\":\"lease-read-failed\",\"error\":"+std::to_string(error)+"}"); return false; }
    // Before the first writer attaches, read==0 is ambiguous. This startup
    // handshake has its own finite bound; it never starts CEF without R.
    std::this_thread::sleep_for(std::chrono::milliseconds(25));
  }
  owner_probe::Emit("{\"event\":\"lease-arm-timeout\"}"); return false;
}
void CloseAll() {
  closing=true;
  owner_probe::Emit("{\"event\":\"close-requested\"}");
  for(auto browser:browsers) if(browser) browser->GetHost()->CloseBrowser(true);
}
class Client final : public CefClient,public CefLifeSpanHandler,public CefRenderHandler,public CefLoadHandler {
  int index_;
 public:
  explicit Client(int index):index_(index) {}
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  CefRefPtr<CefRenderHandler> GetRenderHandler() override { return this; }
  CefRefPtr<CefLoadHandler> GetLoadHandler() override { return this; }
  void GetViewRect(CefRefPtr<CefBrowser>,CefRect& rect) override { rect=CefRect(0,0,800,600); }
  void OnPaint(CefRefPtr<CefBrowser>,PaintElementType,const RectList&,const void*,int,int) override {}
  void OnAfterCreated(CefRefPtr<CefBrowser> browser) override {
    browsers[index_]=browser; owner_probe::Emit("{\"event\":\"created\",\"index\":"+std::to_string(index_)+"}");
    if(closing || lease_lost) browser->GetHost()->CloseBrowser(true);
  }
  void OnBeforeClose(CefRefPtr<CefBrowser>) override {
    browsers[index_]=nullptr; closed++;
    owner_probe::Emit("{\"event\":\"before-close\",\"index\":"+std::to_string(index_)+"}");
    if(closed==2) CefQuitMessageLoop();
  }
  void OnLoadEnd(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,int code) override {
    if(frame->IsMain()) owner_probe::Emit("{\"event\":\"loaded\",\"index\":"+std::to_string(index_)+",\"status\":"+std::to_string(code)+"}");
  }
  void OnLoadError(CefRefPtr<CefBrowser>,CefRefPtr<CefFrame> frame,ErrorCode code,const CefString&,const CefString&) override {
    if(frame->IsMain()) owner_probe::Emit("{\"event\":\"load-error\",\"index\":"+std::to_string(index_)+",\"code\":"+std::to_string(code)+"}");
  }
  IMPLEMENT_REFCOUNTING(Client);
};
class App final : public CefApp,public CefBrowserProcessHandler {
 public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override { return this; }
  void OnContextInitialized() override {
    const char* runtime_version=cef_version_full();
    owner_probe::Emit(std::string("{\"event\":\"initialized\",\"crash_reporting\":")+(CefCrashReportingEnabled()?"true":"false")+",\"version\":"+owner_probe::Quote(runtime_version?runtime_version:"")+",\"compiled_version\":"+owner_probe::Quote(CEF_VERSION)+"}");
    if(lease_lost || ObserveEOF()) {
      startup_aborted=true;
      owner_probe::Emit("{\"event\":\"startup-aborted\",\"stage\":\"context-initialized\"}");
      CefPostTask(TID_UI,base::BindOnce([]{CefQuitMessageLoop();})); return;
    }
    for(int i=0;i<2;i++) {
      CefWindowInfo window; window.SetAsWindowless(nullptr); window.runtime_style=CEF_RUNTIME_STYLE_ALLOY;
      CefBrowserSettings settings; settings.windowless_frame_rate=10;
      if(!CefBrowserHost::CreateBrowser(window,new Client(i),page_url+"?tab="+std::to_string(i),settings,nullptr,nullptr)) owner_probe::Emit("{\"event\":\"create-failed\"}");
    }
  }
  void OnBeforeChildProcessLaunch(CefRefPtr<CefCommandLine> command) override {
    owner_probe::Emit("{\"event\":\"launch-intent\",\"type\":"+owner_probe::Quote(command->GetSwitchValue("type").ToString())+",\"utility\":"+owner_probe::Quote(command->GetSwitchValue("utility-sub-type").ToString())+"}");
    if(cut=="first-launch") {
      if(!launch_held.exchange(true)) owner_probe::Emit("{\"event\":\"first-launch-held\",\"group\":"+owner_probe::Census(getpgrp())+"}");
      const auto deadline=std::chrono::steady_clock::now()+std::chrono::seconds(10);
      while(access((directory+"/launch-release").c_str(),F_OK)!=0) {
        if(std::chrono::steady_clock::now()>=deadline) { owner_probe::Emit("{\"event\":\"launch-hold-timeout\"}"); lease_lost=true; break; }
        std::this_thread::sleep_for(std::chrono::milliseconds(25));
      }
      ObserveEOF();
      owner_probe::Emit("{\"event\":\"launch-released\",\"owner_dead\":"+std::string(lease_lost?"true":"false")+"}");
    }
  }
  IMPLEMENT_REFCOUNTING(App);
};
}
int main(int argc,char** argv) {
  owner_probe::Self("broker-entry",argc,argv);
  directory=owner_probe::Argument(argc,argv,"probe-directory");
  cut=owner_probe::Argument(argc,argv,"probe-cut");
  page_url=owner_probe::Argument(argc,argv,"probe-url");
  if(directory.empty() || page_url.empty()) return 2;
  if(!ArmLease()) {
    if(lease>=0) close(lease);
    owner_probe::Emit("{\"event\":\"startup-aborted\",\"stage\":\"before-framework\"}");
    return HoldRoot(0);
  }
  CefScopedLibraryLoader loader; if(!loader.LoadInMain()) return 3;
  CefMainArgs args(argc,argv);
  @autoreleasepool {
    [OwnerProbeApplication sharedApplication]; [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
    CefSettings settings; settings.no_sandbox=false; settings.windowless_rendering_enabled=true; settings.remote_debugging_port=0;
    auto profile=owner_probe::Argument(argc,argv,"probe-profile"); if(profile.empty()) profile=directory+"/profile";
    CefString(&settings.root_cache_path)=profile; CefString(&settings.cache_path)=profile;
    owner_probe::Emit("{\"event\":\"profile-configured\",\"path\":"+owner_probe::Quote(profile)+"}");
    CefString(&settings.log_file)=directory+"/cef.log";
    if(!CefInitialize(args,settings,new App(),nullptr)) return 4;
    std::atomic<bool> reading{true};
    std::thread monitor([&]{
      const char* reason="pump-ended";
      while(reading) {
        if(access((directory+"/crash").c_str(),F_OK)==0) { owner_probe::Emit("{\"event\":\"broker-self-sigkill\"}"); raise(SIGKILL); }
        if(access((directory+"/close").c_str(),F_OK)==0) { reason="close-marker"; break; }
        pollfd p{lease,POLLIN|POLLHUP,0}; int result=poll(&p,1,50);
        if(result<0) {
          if(errno==EINTR) continue;
          owner_probe::Emit("{\"event\":\"lease-poll-failed\",\"error\":"+std::to_string(errno)+"}"); reason="poll-failed"; break;
        }
        // Read the nonblocking FIFO even if the readiness hint timed out. Only
        // read(2)==0 proves EOF; readiness is not the lease's lifetime contract.
        char byte; auto size=read(lease,&byte,1); const int error=size<0?errno:0;
        if(size==0) {
          lease_lost=true;
          owner_probe::Emit("{\"event\":\"lease-eof\",\"poll_result\":"+std::to_string(result)+",\"revents\":"+std::to_string(p.revents)+"}"); reason="lease-eof"; break;
        } else if(size<0 && error!=EINTR && error!=EAGAIN && error!=EWOULDBLOCK) {
          owner_probe::Emit("{\"event\":\"lease-read-failed\",\"error\":"+std::to_string(error)+"}"); reason="read-failed"; break;
        }
      }
      owner_probe::Emit("{\"event\":\"lease-monitor-ended\",\"reason\":"+owner_probe::Quote(reason)+"}");
      if(reading) CefPostTask(TID_UI,base::BindOnce(CloseAll));
    });
    CefRunMessageLoop(); owner_probe::Emit("{\"event\":\"pump-return\"}"); reading=false;
    monitor.join(); owner_probe::Emit("{\"event\":\"monitor-joined\"}");
    owner_probe::Emit("{\"event\":\"shutdown-enter\"}"); CefShutdown();
    close(lease); // Main owns the reader; callbacks/monitor only borrow it.
    const auto shutdown_at=owner_probe::Now();
    owner_probe::Emit("{\"event\":\"shutdown-return\",\"returned_at_ms\":"+std::to_string(shutdown_at)+",\"group\":"+owner_probe::Census(getpgrp())+",\"children\":"+owner_probe::Census(getpid(),true)+"}");
    // Keep the actual LaunchAgent root alive after CEF shutdown. Otherwise its
    // exit would trigger launchd's group fallback before the observer can prove
    // natural child quiescence. Only the observer may release this diagnostic
    // root after original helpers and the kernel group satisfy the joint gate.
    return HoldRoot(startup_aborted ? (closed==0?0:5) : (closed==2?0:5));
  }
}
