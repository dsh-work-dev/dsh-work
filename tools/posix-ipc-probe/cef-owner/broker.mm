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
std::string page_url;
void CloseAll() {
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
  void OnAfterCreated(CefRefPtr<CefBrowser> browser) override { browsers[index_]=browser; owner_probe::Emit("{\"event\":\"created\",\"index\":"+std::to_string(index_)+"}"); }
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
    for(int i=0;i<2;i++) {
      CefWindowInfo window; window.SetAsWindowless(nullptr); window.runtime_style=CEF_RUNTIME_STYLE_ALLOY;
      CefBrowserSettings settings; settings.windowless_frame_rate=10;
      if(!CefBrowserHost::CreateBrowser(window,new Client(i),page_url+"?tab="+std::to_string(i),settings,nullptr,nullptr)) owner_probe::Emit("{\"event\":\"create-failed\"}");
    }
  }
  void OnBeforeChildProcessLaunch(CefRefPtr<CefCommandLine> command) override {
    owner_probe::Emit("{\"event\":\"launch-intent\",\"type\":"+owner_probe::Quote(command->GetSwitchValue("type").ToString())+",\"utility\":"+owner_probe::Quote(command->GetSwitchValue("utility-sub-type").ToString())+"}");
  }
  IMPLEMENT_REFCOUNTING(App);
};
}
int main(int argc,char** argv) {
  owner_probe::Self("broker-entry",argc,argv);
  const std::string directory=owner_probe::Argument(argc,argv,"probe-directory");
  page_url=owner_probe::Argument(argc,argv,"probe-url");
  if(directory.empty() || page_url.empty()) return 2;
  CefScopedLibraryLoader loader; if(!loader.LoadInMain()) return 3;
  CefMainArgs args(argc,argv);
  @autoreleasepool {
    [OwnerProbeApplication sharedApplication]; [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
    CefSettings settings; settings.no_sandbox=false; settings.windowless_rendering_enabled=true; settings.remote_debugging_port=0;
    CefString(&settings.root_cache_path)=directory+"/profile"; CefString(&settings.cache_path)=directory+"/profile";
    CefString(&settings.log_file)=directory+"/cef.log";
    if(!CefInitialize(args,settings,new App(),nullptr)) return 4;
    std::atomic<bool> reading{true};
    std::thread monitor([&]{
      int lease=open((directory+"/lease").c_str(),O_RDONLY|O_NONBLOCK|O_CLOEXEC);
      if(lease<0) { owner_probe::Emit("{\"event\":\"lease-open-failed\"}"); CefPostTask(TID_UI,base::BindOnce(CloseAll)); return; }
      bool armed=false;
      while(reading) {
        if(access((directory+"/crash").c_str(),F_OK)==0) { owner_probe::Emit("{\"event\":\"broker-self-sigkill\"}"); raise(SIGKILL); }
        if(access((directory+"/close").c_str(),F_OK)==0) break;
        pollfd p{lease,POLLIN|POLLHUP,0}; int result=poll(&p,1,50);
        if(result>0) { char byte; auto size=read(lease,&byte,1); if(size>0 && byte=='R') { armed=true; owner_probe::Emit("{\"event\":\"lease-armed\"}"); } else if(size==0 && armed) { owner_probe::Emit("{\"event\":\"lease-eof\"}"); break; } }
        if(!armed) std::this_thread::sleep_for(std::chrono::milliseconds(25));
      }
      close(lease);
      if(reading) CefPostTask(TID_UI,base::BindOnce(CloseAll));
    });
    CefRunMessageLoop(); reading=false; monitor.join(); CefShutdown();
    const auto shutdown_at=owner_probe::Now();
    owner_probe::Emit("{\"event\":\"shutdown-return\",\"returned_at_ms\":"+std::to_string(shutdown_at)+",\"group\":"+owner_probe::Census(getpgrp())+"}");
    // Keep the actual LaunchAgent root alive after CEF shutdown. Otherwise its
    // exit would trigger launchd's group fallback before the observer can prove
    // natural child quiescence. Only the observer may release this diagnostic
    // root after original helpers and the kernel group satisfy the joint gate.
    const auto deadline=std::chrono::steady_clock::now()+std::chrono::seconds(30);
    while(std::chrono::steady_clock::now()<deadline) {
      for(;;) {
        int status=0; pid_t child=waitpid(-1,&status,WNOHANG);
        if(child>0) {
          owner_probe::Emit("{\"event\":\"child-reaped\",\"pid\":"+std::to_string(child)+",\"wait_status\":"+std::to_string(status)+"}");
          continue;
        }
        if(child<0 && errno==EINTR) continue;
        if(child<0 && errno!=ECHILD) { owner_probe::Emit("{\"event\":\"reap-error\",\"error\":"+std::to_string(errno)+"}"); return 7; }
        break;
      }
      if(access((directory+"/release").c_str(),F_OK)==0) {
        owner_probe::Emit("{\"event\":\"root-release\",\"group\":"+owner_probe::Census(getpgrp())+"}");
        return closed==2?0:5;
      }
      std::this_thread::sleep_for(std::chrono::milliseconds(25));
    }
    owner_probe::Emit("{\"event\":\"root-release-timeout\"}");
    return 6;
  }
}
