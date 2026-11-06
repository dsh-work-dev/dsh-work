// Cocoa native views. CEF shares this Wails process; broker-loss tests must
// observe that boundary rather than pretend there is a separate broker PID.
#include "include/cef_app.h"
#include "include/cef_application_mac.h"
#include "include/cef_client.h"
#include "include/wrapper/cef_helpers.h"
#include "include/wrapper/cef_library_loader.h"
#import <Cocoa/Cocoa.h>
#include <algorithm>
#include <array>
#include <atomic>
#include <csignal>
#include <sstream>
#include <string>
#include <unistd.h>
#include <vector>
@interface BrowserLabApplication : NSApplication <CefAppProtocol> {
  BOOL handling_;
}
@end
@implementation BrowserLabApplication
- (BOOL)isHandlingSendEvent {
  return handling_;
}
- (void)setHandlingSendEvent:(BOOL)v {
  handling_ = v;
}
- (void)sendEvent:(NSEvent *)event {
  CefScopedSendingEvent scope;
  [super sendEvent:event];
}
@end
#define LAB_EXPORT extern "C" __attribute__((visibility("default")))
static CefScopedLibraryLoader loader;
static NSWindow *main_window;
static NSWindow *float_window;
static std::array<NSView *, 2> slots{};
static std::array<CefRefPtr<CefBrowser>, 2> views;
static std::vector<CefRefPtr<CefBrowser>> popups;
static std::array<bool, 2> faults{};
static std::atomic<bool> running{false};
static std::atomic<uint64_t> pump_epoch{0};
static NSTimer *pump_timer;
static bool pumping = false;
static int selected = 0, closed_count = 0;
static bool covered = false, floating = false;
static std::string last_error;
static void PumpWork() {
  if (!running || pumping)
    return;
  pumping = true;
  CefDoMessageLoopWork();
  pumping = false;
}
static void Layout() {
  for (int i = 0; i < 2; i++) {
    NSWindow *owner = floating && selected == i ? float_window : main_window;
    if (!owner || !slots[i])
      continue;
    NSView *content = owner.contentView;
    if (slots[i].superview != content) {
      [slots[i] removeFromSuperview];
      [content addSubview:slots[i] positioned:NSWindowAbove relativeTo:nil];
    }
    CGFloat left = owner == main_window ? 240 : 20;
    slots[i].frame = NSMakeRect(
        left, 20, std::max<CGFloat>(100, content.bounds.size.width - left - 20),
        std::max<CGFloat>(100, content.bounds.size.height - 120));
    slots[i].hidden = i != selected || covered || faults[i];
    if (views[i])
      [(NSView *)views[i]->GetHost()->GetWindowHandle()
          setFrame:slots[i].bounds];
  }
}
class App final : public CefApp, public CefBrowserProcessHandler {
public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override {
    return this;
  }
  void OnScheduleMessagePumpWork(int64_t delay) override {
    auto epoch = ++pump_epoch;
    dispatch_after(
        dispatch_time(DISPATCH_TIME_NOW,
                      std::clamp<int64_t>(delay, 0, 33) * NSEC_PER_MSEC),
        dispatch_get_main_queue(), ^{
          if (epoch == pump_epoch)
            PumpWork();
        });
  }

private:
  IMPLEMENT_REFCOUNTING(App);
};
class Client final : public CefClient, public CefLifeSpanHandler {
  int index_;

public:
  explicit Client(int index) : index_(index) {}
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  void OnAfterCreated(CefRefPtr<CefBrowser> b) override {
    if (!views[index_]) {
      views[index_] = b;
      NSView *v = (NSView *)b->GetHost()->GetWindowHandle();
      v.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
    } else
      popups.push_back(b);
  }
  bool DoClose(CefRefPtr<CefBrowser> b) override {
    if (views[index_] && views[index_]->IsSame(b)) {
      [(NSView *)b->GetHost()->GetWindowHandle() removeFromSuperview];
      return true;
    }
    return false;
  }
  void OnBeforeClose(CefRefPtr<CefBrowser> b) override {
    if (views[index_] && views[index_]->IsSame(b)) {
      views[index_] = nullptr;
      closed_count++;
    } else
      for (auto i = popups.begin(); i != popups.end(); ++i)
        if ((*i)->IsSame(b)) {
          popups.erase(i);
          break;
        }
  }

private:
  IMPLEMENT_REFCOUNTING(Client);
};
LAB_EXPORT int lab_init(const char *cache, const char *helper,
                        const char *log) {
  if (!loader.LoadInMain()) {
    last_error = "CEF framework load failed";
    return 0;
  }
  [BrowserLabApplication sharedApplication];
  if (![NSApp isKindOfClass:[BrowserLabApplication class]]) {
    last_error = "NSApp protocol subclass was not installed first";
    return 0;
  }
  CefSettings settings;
  settings.no_sandbox = false;
  settings.remote_debugging_port = 19479;
  settings.external_message_pump = true;
  CefString(&settings.root_cache_path) = cache;
  CefString(&settings.cache_path) = cache;
  CefString(&settings.browser_subprocess_path) = helper;
  CefString(&settings.log_file) = log;
  char name[] = "browser-lab";
  char *argv[] = {name, nullptr};
  CefMainArgs args(1, argv);
  running = true;
  if (!CefInitialize(args, settings, new App(), nullptr)) {
    running = false;
    last_error = "CefInitialize failed";
    return 0;
  }
  pump_timer = [NSTimer timerWithTimeInterval:1.0 / 30.0
                                      repeats:YES
                                        block:^(NSTimer *) {
                                          PumpWork();
                                        }];
  [[NSRunLoop mainRunLoop] addTimer:pump_timer forMode:NSRunLoopCommonModes];
  return 1;
}
LAB_EXPORT int lab_create(void *native, const char *) {
  main_window = (NSWindow *)native;
  closed_count = 0;
  for (int i = 0; i < 2; i++) {
    slots[i] = [[NSView alloc] initWithFrame:NSMakeRect(240, 20, 700, 540)];
    [main_window.contentView addSubview:slots[i]
                             positioned:NSWindowAbove
                             relativeTo:nil];
    slots[i].hidden = i != selected;
    CefWindowInfo info;
    info.SetAsChild(slots[i], CefRect(0, 0, 700, 540));
    info.runtime_style = CEF_RUNTIME_STYLE_ALLOY;
    CefBrowserSettings settings;
    CefBrowserHost::CreateBrowser(info, new Client(i),
                                  "http://localhost:19478/interactions?tab=" +
                                      std::string(i ? "B" : "A"),
                                  settings, nullptr, nullptr);
  }
  Layout();
  return 1;
}
LAB_EXPORT int lab_float_parent(void *native) {
  float_window = (NSWindow *)native;
  return float_window != nil;
}
LAB_EXPORT int lab_command(const char *command, int index) {
  std::string a(command);
  if (a == "tab") {
    floating = false;
    selected = index;
  } else if (a == "float") {
    if (!float_window)
      return 0;
    floating = true;
  } else if (a == "dock")
    floating = false;
  else if (a == "cover")
    covered = true;
  else if (a == "uncover")
    covered = false;
  else if (a == "renderer-fault")
    faults[index] = true;
  else if (a == "renderer-recovered")
    faults[index] = false;
  else if (a == "crash-broker") {
    kill(getpid(), SIGKILL);
    return 0;
  } else if (a == "restart-broker") {
    last_error =
        "CEF shares the Wails process; cold restart requires a new host";
    return 0;
  } else if (a == "focus-guest") {
    if (covered || faults[selected] || !views[selected])
      return 0;
    NSWindow *w = floating ? float_window : main_window;
    [w makeKeyAndOrderFront:nil];
    views[selected]->GetHost()->SetFocus(true);
  } else if (a == "focus-host") {
    [main_window makeKeyAndOrderFront:nil];
  } else if (a == "native-key") {
    NSWindow *w = floating ? float_window : main_window;
    NSEvent *e =
        [NSEvent keyEventWithType:NSEventTypeKeyDown
                               location:NSZeroPoint
                          modifierFlags:0
                              timestamp:NSProcessInfo.processInfo.systemUptime
                           windowNumber:w.windowNumber
                                context:nil
                             characters:@"r"
            charactersIgnoringModifiers:@"r"
                              isARepeat:NO
                                keyCode:15];
    [w sendEvent:e];
  } else if (a != "layout")
    return 0;
  Layout();
  return 1;
}
LAB_EXPORT const char *lab_state() {
  static thread_local std::string result;
  Layout();
  std::ostringstream s;
  auto main_id = main_window ? main_window.windowNumber : 0,
       float_id = float_window ? float_window.windowNumber : 0;
  auto dpi = [](NSWindow *w) {
    return w ? int(w.backingScaleFactor * 96) : 96;
  };
  int count = (views[0] ? 1 : 0) + (views[1] ? 1 : 0);
  s << "{\"initialized\":" << (running ? "true" : "false")
    << ",\"created\":" << count << ",\"closed\":" << closed_count
    << ",\"parentAttached\":"
    << (views[0] && [(NSView *)views[0]->GetHost()->GetWindowHandle() window] ==
                        main_window
            ? "true"
            : "false")
    << ",\"windowId\":\"" << main_id << "\",\"main\":{\"hwnd\":" << main_id
    << ",\"windowId\":\"" << main_id << "\",\"dpi\":" << dpi(main_window)
    << "},\"float\":{\"hwnd\":" << float_id << ",\"windowId\":\"" << float_id
    << "\",\"dpi\":" << dpi(float_window) << ",\"visible\":"
    << (float_window && float_window.visible ? "true" : "false")
    << "},\"guestRunning\":" << (running && closed_count < 2 ? "true" : "false")
    << ",\"guestPID\":" << getpid() << ",\"selected\":\""
    << (selected ? "B" : "A")
    << "\",\"covered\":" << (covered ? "true" : "false")
    << ",\"floating\":" << (floating ? "true" : "false") << ",\"tabs\":[";
  for (int i = 0; i < 2; i++) {
    if (i)
      s << ",";
    NSView *v =
        views[i] ? (NSView *)views[i]->GetHost()->GetWindowHandle() : nil;
    NSWindow *w = v.window;
    s << "{\"name\":\"" << (i ? "B" : "A") << "\",\"hwnd\":" << (uintptr_t)v
      << ",\"parentHWND\":" << (w ? w.windowNumber : 0) << ",\"visible\":"
      << (v && w.visible && !v.hiddenOrHasHiddenAncestor ? "true" : "false")
      << ",\"dpi\":" << dpi(w)
      << ",\"fault\":" << (faults[i] ? "true" : "false") << "}";
  }
  s << "],\"guestTopLevelWindows\":[";
  for (size_t i = 0; i < popups.size(); i++) {
    if (i)
      s << ",";
    NSView *v = (NSView *)popups[i]->GetHost()->GetWindowHandle();
    NSWindow *w = v.window;
    s << "{\"hwnd\":" << (w ? w.windowNumber : 0)
      << ",\"parentHWND\":0,\"visible\":" << (w && w.visible ? "true" : "false")
      << ",\"title\":\"\"}";
  }
  NSWindow *active = floating ? float_window : main_window;
  NSResponder *responder = active.firstResponder;
  NSView *focused =
      [responder isKindOfClass:[NSView class]] ? (NSView *)responder : nil;
  bool in_guest =
      focused && slots[selected] && [focused isDescendantOf:slots[selected]];
  bool in_host =
      focused && [focused isDescendantOf:main_window.contentView] && !in_guest;
  s << "],\"nativeFocus\":{\"inGuest\":" << (in_guest ? "true" : "false")
    << ",\"inHost\":" << (in_host ? "true" : "false") << "}}";
  result = s.str();
  return result.c_str();
}
LAB_EXPORT void lab_close() {
  auto extras = popups;
  for (auto b : extras)
    b->GetHost()->CloseBrowser(true);
  for (auto b : views)
    if (b)
      b->GetHost()->CloseBrowser(true);
}
LAB_EXPORT int lab_closed() { return closed_count == 2 && popups.empty(); }
LAB_EXPORT void lab_shutdown() {
  running = false;
  [pump_timer invalidate];
  pump_timer = nil;
  CefShutdown();
}
LAB_EXPORT const char *lab_error() { return last_error.c_str(); }
