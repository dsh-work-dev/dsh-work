// Standalone sandboxed CEF owner; GTK4 and CEF never share a main context.
#include "../../policy/native_policy.h"
#include "../../policy/architecture_probe.h"
#include "include/cef_app.h"
#include "include/cef_client.h"
#include "include/cef_command_line.h"
#include "include/cef_task.h"
#include "include/views/cef_display.h"
#include <array>
#include <atomic>
#include <iostream>
#include <poll.h>
#include <string>
#include <thread>
#include <unistd.h>
#include <vector>
#include <xcb/xcb.h>
static std::array<CefRefPtr<CefBrowser>, 2> views;
static std::vector<CefRefPtr<CefBrowser>> popups;
static int closed_count = 0;
// CEF's default DoClose notifies the top-level parent. That parent belongs
// to another process here and may already be gone. Complete only our own
// native hierarchy, as required by CefLifeSpanHandler's custom-close contract.
class DestroyChild final : public CefTask {
  xcb_window_t child_;
public:
  explicit DestroyChild(xcb_window_t child) : child_(child) {}
  void Execute() override {
    auto* connection = xcb_connect(nullptr, nullptr);
    if (xcb_connection_has_error(connection)) {
      std::fprintf(stderr, "ARCH_OWN_CHILD_DESTROY connection failed\n");
      xcb_disconnect(connection); return;
    }
    auto* error = xcb_request_check(connection, xcb_destroy_window_checked(connection, child_));
    std::fprintf(stderr, "ARCH_OWN_CHILD_DESTROY id=%u error=%d\n", child_, error ? error->error_code : 0);
    std::free(error); xcb_flush(connection); xcb_disconnect(connection);
  }
private:
  IMPLEMENT_REFCOUNTING(DestroyChild);
};
class Client final : public CefClient, public CefLifeSpanHandler {
  int index_;

public:
  BROWSER_LAB_POLICY_CLIENT
  explicit Client(int index) : index_(index) {}
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  bool DoClose(CefRefPtr<CefBrowser> b) override {
    CefPostTask(TID_UI, new DestroyChild(static_cast<xcb_window_t>(b->GetHost()->GetWindowHandle())));
    return true;
  }
  void OnAfterCreated(CefRefPtr<CefBrowser> b) override {
    policy_->Register(b, index_);
    if (!views[index_]) {
      views[index_] = b;
      auto display = CefDisplay::GetPrimaryDisplay();
      std::cout << "CREATED " << index_ << " "
                << b->GetHost()->GetWindowHandle() << " "
                << (display ? display->GetDeviceScaleFactor() : 0) << std::endl;
    } else {
      popups.push_back(b);
      std::cout << "POPUP " << b->GetHost()->GetWindowHandle() << std::endl;
    }
  }
  void OnBeforeClose(CefRefPtr<CefBrowser> b) override {
    policy_->Close(b);
    if (views[index_] && views[index_]->IsSame(b)) {
      views[index_] = nullptr;
      closed_count++;
      std::cout << "CLOSED " << index_ << std::endl;
    } else
      for (auto it = popups.begin(); it != popups.end(); ++it)
        if ((*it)->IsSame(b)) {
          popups.erase(it);
          break;
        }
    if (closed_count == 2 && popups.empty())
      CefQuitMessageLoop();
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
    auto c = CefCommandLine::GetGlobalCommandLine();
    for (int i = 0; i < 2; i++) {
      CefWindowInfo info;
      info.SetAsChild(
          std::stoull(
              c->GetSwitchValue(i ? "parent-b" : "parent-a").ToString()),
          CefRect(0, 0, 700, 540));
      info.runtime_style = CEF_RUNTIME_STYLE_ALLOY;
      CefBrowserSettings settings;
      CefBrowserHost::CreateBrowser(info, new Client(i),
                                    lab_arch::Enabled() ? lab_arch::URL(i) : "http://127.0.0.1:19478/interactions?tab=" +
                                        std::string(i ? "B" : "A"),
                                    settings, nullptr, lab_arch::Context(i));
    }
  }

private:
  IMPLEMENT_REFCOUNTING(App);
};
class CloseTask final : public CefTask {
public:
  void Execute() override {
    if (std::getenv("BROWSER_LAB_ARCH_PROBE")) std::fprintf(stderr, "ARCH_CLOSE_TASK begin\n");
    auto extras = popups;
    for (auto b : extras)
      b->GetHost()->CloseBrowser(true);
    for (auto b : views)
      if (b) {
        if (std::getenv("BROWSER_LAB_ARCH_PROBE")) std::fprintf(stderr, "ARCH_CLOSE_BROWSER id=%d\n", b->GetIdentifier());
        b->GetHost()->CloseBrowser(true);
      }
  }

private:
  IMPLEMENT_REFCOUNTING(CloseTask);
};
int main(int argc, char **argv) {
  CefMainArgs args(argc, argv);
  int code = CefExecuteProcess(args, nullptr, nullptr);
  if (code >= 0)
    return code;
  auto c = CefCommandLine::CreateCommandLine();
  c->InitFromArgv(argc, argv);
  CefSettings settings;
  settings.no_sandbox = false;
  settings.remote_debugging_port = 19479;
  CefString(&settings.root_cache_path) = c->GetSwitchValue("cache-path");
  CefString(&settings.cache_path) = c->GetSwitchValue("cache-path");
  CefString(&settings.log_file) = c->GetSwitchValue("log-file");
  browser_lab::Rpc::Initialize();
  if (!CefInitialize(args, settings, new App(), nullptr))
    return 1;
  std::atomic<bool> running{true};
  std::thread input([&running] {
    while (running) {
      pollfd fd{STDIN_FILENO, POLLIN, 0};
      if (poll(&fd, 1, 100) > 0) {
        std::string line;
        // This demo's UI owns the pipe. EOF means that owner died: close the
        // disposable browsers instead of leaking a live profile singleton.
        // A daemon-owned multi-UI broker needs a different ownership protocol.
        const bool eof = !std::getline(std::cin, line);
        if (std::getenv("BROWSER_LAB_ARCH_PROBE")) std::fprintf(stderr, "ARCH_INPUT eof=%d close=%d revents=%d\n", eof ? 1 : 0, line == "close" ? 1 : 0, fd.revents);
        if (eof || line == "close")
          CefPostTask(TID_UI, new CloseTask());
        break;
      }
    }
  });
  CefRunMessageLoop();
  running = false;
  input.join();
  CefShutdown();
  if (closed_count == 2)
    std::cout << "SHUTDOWN" << std::endl;
  return closed_count == 2 ? 0 : 2;
}
