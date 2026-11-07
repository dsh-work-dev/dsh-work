// Standalone sandbox-enabled CEF broker. The Wails process never loads CEF.
#include "../../policy/native_policy.h"
#include "../../policy/architecture_probe.h"
#include "include/base/cef_callback.h"
#include "include/cef_app.h"
#include "include/cef_application_mac.h"
#include "include/cef_client.h"
#include "include/cef_command_line.h"
#include "include/cef_parser.h"
#include "include/wrapper/cef_closure_task.h"
#include "include/wrapper/cef_helpers.h"
#include "include/wrapper/cef_library_loader.h"
#include "osr_surface.h"
#import <Cocoa/Cocoa.h>
#include <array>
#include <atomic>
#include <cstring>
#include <iostream>
#include <poll.h>
#include <thread>
#include <vector>
@interface BrowserLabBrokerApplication : NSApplication <CefAppProtocol> {
  BOOL handling_;
}
@end
@implementation BrowserLabBrokerApplication
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
namespace {
std::array<CefRefPtr<CefBrowser>, 2> browsers;
std::array<lab_osr::Surface, 2> surfaces;
struct Geometry {
  int width = 890, height = 680, x = 0, y = 0;
  double scale = 1;
  bool hidden = false;
};
std::array<Geometry, 2> geometry;
std::vector<CefRefPtr<CefBrowser>> popups;
int closed_count = 0;
void Event(const std::string &type, int index, int id = 0) {
  auto d = CefDictionaryValue::Create();
  d->SetString("event", type);
  d->SetInt("index", index);
  d->SetInt("id", id);
  auto v = CefValue::Create();
  v->SetDictionary(d);
  std::cout << CefWriteJSON(v, JSON_WRITER_DEFAULT).ToString() << std::endl;
}
class Client final : public CefClient,
                     public CefLifeSpanHandler,
                     public CefRenderHandler {
  int index_;

public:
  BROWSER_LAB_POLICY_CLIENT
  explicit Client(int index) : index_(index) {}
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  CefRefPtr<CefRenderHandler> GetRenderHandler() override { return this; }
  void OnAfterCreated(CefRefPtr<CefBrowser> b) override {
    policy_->Register(b, index_);
    if (!browsers[index_]) {
      browsers[index_] = b;
      Event("created", index_, b->GetIdentifier());
    } else {
      popups.push_back(b);
      Event("popup", index_, b->GetIdentifier());
    }
  }
  bool DoClose(CefRefPtr<CefBrowser>) override { return false; }
  void OnBeforeClose(CefRefPtr<CefBrowser> b) override {
    policy_->Close(b);
    if (browsers[index_] && browsers[index_]->IsSame(b)) {
      browsers[index_] = nullptr;
      closed_count++;
      Event("closed", index_);
    } else {
      for (auto it = popups.begin(); it != popups.end(); ++it)
        if ((*it)->IsSame(b)) {
          popups.erase(it);
          break;
        }
    }
    if (closed_count == 2 && popups.empty())
      CefQuitMessageLoop();
  }
  void GetViewRect(CefRefPtr<CefBrowser>, CefRect &r) override {
    auto g = geometry[index_];
    r = CefRect(0, 0, g.width, g.height);
  }
  bool GetRootScreenRect(CefRefPtr<CefBrowser>, CefRect &r) override {
    auto g = geometry[index_];
    r = CefRect(g.x, g.y, g.width, g.height);
    return true;
  }
  bool GetScreenPoint(CefRefPtr<CefBrowser>, int x, int y, int &sx,
                      int &sy) override {
    auto g = geometry[index_];
    sx = g.x + x;
    sy = g.y + y;
    return true;
  }
  bool GetScreenInfo(CefRefPtr<CefBrowser>, CefScreenInfo &info) override {
    auto g = geometry[index_];
    info.device_scale_factor = g.scale;
    info.depth = 32;
    info.depth_per_component = 8;
    info.rect = CefRect(g.x, g.y, g.width, g.height);
    info.available_rect = info.rect;
    return true;
  }
  void OnPaint(CefRefPtr<CefBrowser> b, PaintElementType type, const RectList &,
               const void *buffer, int width, int height) override {
    if (type != PET_VIEW || !browsers[index_] || !browsers[index_]->IsSame(b) ||
        width < 1 || height < 1 || width > lab_osr::kMaxWidth ||
        height > lab_osr::kMaxHeight)
      return;
    auto &s = surfaces[index_];
    if (flock(s.fd, LOCK_EX))
      return;
    memcpy(s.pixels(), buffer, size_t(width) * height * 4);
    s.header()->width = width;
    s.header()->height = height;
    s.header()->sequence++;
    flock(s.fd, LOCK_UN);
  }
  void OnImeCompositionRangeChanged(CefRefPtr<CefBrowser>,
                                    const CefRange &range,
                                    const RectList &rects) override {
    auto d = CefDictionaryValue::Create();
    d->SetString("event", "ime");
    d->SetInt("index", index_);
    d->SetInt("from", range.from);
    d->SetInt("to", range.to);
    if (!rects.empty()) {
      auto r = rects.front();
      d->SetInt("x", r.x);
      d->SetInt("y", r.y);
      d->SetInt("width", r.width);
      d->SetInt("height", r.height);
    }
    auto v = CefValue::Create();
    v->SetDictionary(d);
    std::cout << CefWriteJSON(v, JSON_WRITER_DEFAULT).ToString() << std::endl;
  }

private:
  IMPLEMENT_REFCOUNTING(Client);
};
class App final : public CefApp, public CefBrowserProcessHandler {
public:
  void OnBeforeCommandLineProcessing(const CefString&, CefRefPtr<CefCommandLine> command) override {
    if (std::getenv("BROWSER_LAB_ARCH_SHARED")) command->AppendSwitch("process-per-site");
    if (std::getenv("BROWSER_LAB_ARCH_OOPIF")) command->AppendSwitch("site-per-process");
  }
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override {
    return this;
  }
  void OnContextInitialized() override {
    for (int i = 0; i < 2; i++) {
      CefWindowInfo info;
      info.SetAsWindowless(nullptr);
      info.runtime_style = CEF_RUNTIME_STYLE_ALLOY;
      CefBrowserSettings settings;
      settings.windowless_frame_rate = 30;
      CefBrowserHost::CreateBrowser(info, new Client(i),
                                    lab_arch::Enabled() ? lab_arch::URL(i) : "http://127.0.0.1:19478/interactions?tab=" +
                                        std::string(i ? "B" : "A"),
                                    settings, nullptr, lab_arch::Context(i));
    }
  }

private:
  IMPLEMENT_REFCOUNTING(App);
};
void Command(CefRefPtr<CefDictionaryValue> d) {
  CEF_REQUIRE_UI_THREAD();
  auto op = d->GetString("op").ToString();
  int i = d->GetInt("index");
  if (i < 0 || i > 1)
    return;
  if (op == "close") {
    auto extras = popups;
    for (auto b : extras)
      b->GetHost()->CloseBrowser(true);
    for (auto b : browsers)
      if (b)
        b->GetHost()->CloseBrowser(true);
    return;
  }
  auto b = browsers[i];
  auto h = b ? b->GetHost() : nullptr;
  if (op == "geometry") {
    auto &g = geometry[i];
    int w = d->GetInt("width"), height = d->GetInt("height");
    double scale = d->GetType("scale") == VTYPE_INT ? d->GetInt("scale")
                                                    : d->GetDouble("scale");
    if (w < 1 || height < 1 || scale < 1 || scale > 4 ||
        w * scale > lab_osr::kMaxWidth || height * scale > lab_osr::kMaxHeight)
      return;
    bool resize = w != g.width || height != g.height,
         screen =
             scale != g.scale || d->GetInt("x") != g.x || d->GetInt("y") != g.y;
    g.width = w;
    g.height = height;
    g.scale = scale;
    g.x = d->GetInt("x");
    g.y = d->GetInt("y");
    bool hidden = d->GetBool("hidden");
    bool visibility = hidden != g.hidden;
    g.hidden = hidden;
    if (h) {
      if (visibility)
        h->WasHidden(hidden);
      if (screen)
        h->NotifyScreenInfoChanged();
      if (resize)
        h->WasResized();
    }
    return;
  }
  if (!h)
    return;
  if (op == "focus") {
    h->SetFocus(d->GetBool("focused"));
    return;
  }
  if (op == "mouse") {
    CefMouseEvent e;
    e.x = d->GetInt("x");
    e.y = d->GetInt("y");
    e.modifiers = d->GetInt("modifiers");
    auto kind = d->GetString("kind");
    if (kind == "move")
      h->SendMouseMoveEvent(e, false);
    else if (kind == "wheel")
      h->SendMouseWheelEvent(e, d->GetInt("dx"), d->GetInt("dy"));
    else
      h->SendMouseClickEvent(e, d->GetInt("button") == 1 ? MBT_RIGHT : MBT_LEFT,
                             kind == "up", std::max(1, d->GetInt("count")));
    return;
  }
  if (op == "key") {
    CefKeyEvent e;
    e.type = static_cast<cef_key_event_type_t>(d->GetInt("type"));
    e.windows_key_code = d->GetInt("vk");
    e.native_key_code = d->GetInt("native");
    e.character = d->GetInt("character");
    e.unmodified_character = d->GetInt("unmodified");
    e.modifiers = d->GetInt("modifiers");
    h->SendKeyEvent(e);
    return;
  }
  if (op == "text") {
    h->ImeCommitText(d->GetString("text"), CefRange(-1, -1), 0);
    return;
  }
  if (op == "composition") {
    CefCompositionUnderline u;
    CefString text = d->GetString("text");
    u.range = CefRange(0, text.length());
    u.color = 0xff000000;
    std::vector<CefCompositionUnderline> underlines{u};
    h->ImeSetComposition(text, underlines, CefRange(-1, -1),
                         CefRange(d->GetInt("from"), d->GetInt("to")));
    return;
  }
  if (op == "cancel-composition")
    h->ImeCancelComposition();
}
} // namespace
int main(int argc, char **argv) {
  CefScopedLibraryLoader loader;
  if (!loader.LoadInMain())
    return 2;
  CefMainArgs args(argc, argv);
  int sub = CefExecuteProcess(args, nullptr, nullptr);
  if (sub >= 0)
    return sub;
  @autoreleasepool {
    [BrowserLabBrokerApplication sharedApplication];
    [NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
    auto c = CefCommandLine::CreateCommandLine();
    c->InitFromArgv(argc, argv);
    for (int i = 0; i < 2; i++)
      if (!surfaces[i].Open(c->GetSwitchValue(i ? "surface-b" : "surface-a")
                                .ToString()
                                .c_str(),
                            false))
        return 3;
    CefSettings settings;
    settings.no_sandbox = false;
    settings.windowless_rendering_enabled = true;
    settings.remote_debugging_port = 19479;
    CefString(&settings.cache_path) = c->GetSwitchValue("cache-path");
    CefString(&settings.root_cache_path) = c->GetSwitchValue("cache-path");
    CefString(&settings.browser_subprocess_path) =
        c->GetSwitchValue("helper-path");
    CefString(&settings.log_file) = c->GetSwitchValue("log-file");
    browser_lab::Rpc::Initialize();
    if (!CefInitialize(args, settings, new App(), nullptr))
      return 4;
    std::atomic<bool> reading{true};
    std::thread input([&] {
      std::string pending;
      while (reading) {
        pollfd fd{STDIN_FILENO, POLLIN, 0};
        if (poll(&fd, 1, 100) <= 0)
          continue;
        char bytes[4096];
        auto n = read(STDIN_FILENO, bytes, sizeof(bytes));
        if (n <= 0)
          break;
        pending.append(bytes, n);
        size_t end;
        while ((end = pending.find('\n')) != std::string::npos) {
          auto line = pending.substr(0, end);
          pending.erase(0, end + 1);
          if (line.size() > 65536)
            continue;
          auto v = CefParseJSON(line, JSON_PARSER_RFC);
          if (v && v->GetDictionary())
            CefPostTask(TID_UI, base::BindOnce(Command, v->GetDictionary()));
        }
        if (pending.size() > 65536)
          break;
      }
      if (reading) {
        auto close = CefDictionaryValue::Create();
        close->SetString("op", "close");
        close->SetInt("index", 0);
        CefPostTask(TID_UI, base::BindOnce(Command, close));
      }
    });
    CefRunMessageLoop();
    reading = false;
    input.join();
    CefShutdown();
    Event("shutdown", 0);
    for (auto &s : surfaces)
      s.Close();
    return closed_count == 2 ? 0 : 5;
  }
}
