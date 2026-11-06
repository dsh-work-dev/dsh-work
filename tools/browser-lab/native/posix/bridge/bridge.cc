// Isolated research bridge, not a production browser provider.
#include "include/cef_app.h"
#include "include/cef_browser.h"
#include "include/cef_client.h"
#include "include/cef_task.h"
#include <atomic>
#include <algorithm>
#include <condition_variable>
#include <functional>
#include <mutex>
#include <sstream>
#if defined(__APPLE__)
#import <Cocoa/Cocoa.h>
#include "include/cef_application_mac.h"
#include "include/wrapper/cef_helpers.h"
#include "include/wrapper/cef_library_loader.h"
@interface BrowserLabApplication : NSApplication <CefAppProtocol> { BOOL handling_; }
@end
@implementation BrowserLabApplication
- (BOOL)isHandlingSendEvent { return handling_; }
- (void)setHandlingSendEvent:(BOOL)value { handling_ = value; }
- (void)sendEvent:(NSEvent*)event { CefScopedSendingEvent scope; [super sendEvent:event]; }
@end
static CefScopedLibraryLoader loader;
static NSWindow* host_window;
static NSView* slot;
static NSTimer* pump_timer;
static bool pumping=false;
static std::atomic<uint64_t> pump_epoch{0};
#else
#include <gtk/gtk.h>
#include <gdk/x11/gdkx.h>
#include <X11/Xlib.h>
static Window host_xid;
#endif
static std::atomic<bool> running{false};
static CefRefPtr<CefBrowser> browser;
static std::atomic<int> created{0}, closed{0};
static std::string last_error;
#define LAB_EXPORT extern "C" __attribute__((visibility("default")))
#if defined(__APPLE__)
static void PumpWork() {
  if(!running || pumping)return;
  pumping=true;CefDoMessageLoopWork();pumping=false;
}
#endif
class Task final : public CefTask {
 public:
  explicit Task(std::function<void()> work) : work_(std::move(work)) {}
  void Execute() override { work_(); }
 private:
  std::function<void()> work_;
  IMPLEMENT_REFCOUNTING(Task);
};
static void OnUI(std::function<void()> work) {
  if (CefCurrentlyOn(TID_UI)) { work(); return; }
  std::mutex mutex; std::condition_variable cv; bool done = false;
  CefPostTask(TID_UI, new Task([&] { work(); {std::lock_guard<std::mutex> lock(mutex);done=true;} cv.notify_one(); }));
  std::unique_lock<std::mutex> lock(mutex); cv.wait(lock, [&] {return done;});
}
class App final : public CefApp, public CefBrowserProcessHandler {
 public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override { return this; }
  void OnScheduleMessagePumpWork(int64_t delay_ms) override {
#if defined(__APPLE__)
    const auto epoch=++pump_epoch;
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, std::clamp<int64_t>(delay_ms,0,33)*NSEC_PER_MSEC), dispatch_get_main_queue(), ^{ if(epoch==pump_epoch)PumpWork(); });
#endif
  }
 private:
  IMPLEMENT_REFCOUNTING(App);
};
class Client final : public CefClient, public CefLifeSpanHandler {
 public:
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  void OnAfterCreated(CefRefPtr<CefBrowser> value) override { browser=value; created++; }
  bool DoClose(CefRefPtr<CefBrowser> value) override {
    // Destroy only the owned child, never send close to the Wails top-level.
#if defined(__APPLE__)
    NSView* view=(NSView*)value->GetHost()->GetWindowHandle(); [view removeFromSuperview];
#else
    // CEF's X11 CloseHostWindow closes its own child, not the native parent.
    return false;
#endif
    return true;
  }
  void OnBeforeClose(CefRefPtr<CefBrowser> value) override { browser=nullptr; closed++; }
 private:
  IMPLEMENT_REFCOUNTING(Client);
};
LAB_EXPORT int lab_init(const char* cache, const char* helper, const char* log) {
#if defined(__APPLE__)
  if(!loader.LoadInMain()){last_error="CEF framework load failed";return 0;}
  [BrowserLabApplication sharedApplication];
  if(![NSApp isKindOfClass:[BrowserLabApplication class]]){last_error="NSApp initialized before CEF protocol subclass";return 0;}
#else
  XInitThreads();
#endif
  CefSettings settings;
  settings.no_sandbox=false;
  settings.remote_debugging_port=19479;
  CefString(&settings.root_cache_path)=cache;
  CefString(&settings.cache_path)=cache;
  CefString(&settings.browser_subprocess_path)=helper;
  CefString(&settings.log_file)=log;
#if defined(__APPLE__)
  settings.external_message_pump=true;
#else
  settings.multi_threaded_message_loop=true;
#endif
  char arg0[]="browser-lab";char* argv[]={arg0,nullptr};CefMainArgs args(1,argv);
  running=true;
  if(!CefInitialize(args,settings,new App(),nullptr)){running=false;last_error="CefInitialize failed";return 0;}
#if defined(__APPLE__)
  // The official external-pump sample bounds idle work to 30Hz as well as
  // servicing schedule callbacks. Without this idle timer IPC can stall.
  pump_timer=[NSTimer timerWithTimeInterval:1.0/30.0 repeats:YES block:^(NSTimer*){PumpWork();}];
  [[NSRunLoop mainRunLoop] addTimer:pump_timer forMode:NSRunLoopCommonModes];
#endif
  return 1;
}
LAB_EXPORT int lab_create(void* native, const char* url) {
  CefWindowInfo info;
#if defined(__APPLE__)
  host_window=(NSWindow*)native;
  NSView* content=host_window.contentView;
  slot=[[NSView alloc] initWithFrame:NSMakeRect(240,20,700,540)];
  [content addSubview:slot positioned:NSWindowAbove relativeTo:nil];
  info.SetAsChild(slot,CefRect(0,0,700,540));
#else
  GdkSurface* surface=gtk_native_get_surface(GTK_NATIVE(native));
  if(!surface || !GDK_IS_X11_SURFACE(surface)){last_error="X11 surface required; Wayland is a separate assessment";return 0;}
  host_xid=gdk_x11_surface_get_xid(surface);
  info.SetAsChild(host_xid,CefRect(240,100,700,540));
#endif
  info.runtime_style=CEF_RUNTIME_STYLE_ALLOY;
  CefBrowserSettings settings;
  return CefBrowserHost::CreateBrowser(info,new Client(),url,settings,nullptr,nullptr);
}
LAB_EXPORT const char* lab_state() {
  static thread_local std::string result;
  bool attached=false;uintptr_t id=0,guest=0;std::string ancestry="[]";
  OnUI([&]{
    if(browser){
      guest=(uintptr_t)browser->GetHost()->GetWindowHandle();
#if defined(__APPLE__)
      id=host_window.windowNumber;attached=[(NSView*)guest isDescendantOf:slot] && [(NSView*)guest window]==host_window;
#else
      id=host_xid;Display* display=XOpenDisplay(nullptr);Window current=guest;
      std::ostringstream chain;chain<<"[";
      for(int depth=0;depth<16;depth++){
        Window root,parent,*children=nullptr;unsigned count=0;
        bool found=XQueryTree(display,current,&root,&parent,&children,&count);
        if(children) { XFree(children); }
        if(!found)break;
        if(depth) { chain<<","; }
        chain<<parent;
        if(parent==host_xid){attached=true;break;}
        if(parent==root || parent==0 || parent==current)break;
        current=parent;
      }
      chain<<"]";ancestry=chain.str();
      XCloseDisplay(display);
#endif
    }
  });
  std::ostringstream out;out<<"{\"initialized\":"<<(running?"true":"false")<<",\"created\":"<<created<<",\"closed\":"<<closed<<",\"parentAttached\":"<<(attached?"true":"false")<<",\"windowId\":\""<<id<<"\",\"guestId\":\""<<guest<<"\",\"guestAncestors\":"<<ancestry<<"}";
  result=out.str();return result.c_str();
}
LAB_EXPORT void lab_close() { OnUI([]{if(browser)browser->GetHost()->CloseBrowser(true);}); }
LAB_EXPORT int lab_closed() { return created>0 && created==closed; }
LAB_EXPORT void lab_shutdown() {
  running=false;
#if defined(__APPLE__)
  [pump_timer invalidate];pump_timer=nil;
#endif
  CefShutdown();
}
LAB_EXPORT const char* lab_error() {return last_error.c_str();}
