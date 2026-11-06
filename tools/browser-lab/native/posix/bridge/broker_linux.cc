// Standalone sandboxed CEF owner; GTK4 and CEF never share a main context.
#include "include/cef_app.h"
#include "include/cef_client.h"
#include "include/cef_command_line.h"
#include "include/cef_process_message.h"
#include "include/cef_render_process_handler.h"
#include "include/cef_task.h"
#include "include/views/cef_display.h"
#include <array>
#include <csignal>
#include <iostream>
#include <string>
#include <thread>
#include <unistd.h>
#include <vector>
static std::array<CefRefPtr<CefBrowser>, 2> views;
static std::array<int, 2> renderer_pids{};
static std::vector<CefRefPtr<CefBrowser>> popups;
static int closed_count = 0;
class Client final : public CefClient, public CefLifeSpanHandler {
  int index_;

public:
  explicit Client(int index) : index_(index) {}
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  bool OnProcessMessageReceived(CefRefPtr<CefBrowser> b, CefRefPtr<CefFrame>,
                                CefProcessId source,
                                CefRefPtr<CefProcessMessage> message) override {
    if (source != PID_RENDERER ||
        message->GetName() != "BROWSER_LAB_RENDERER_PID" || !views[index_] ||
        !views[index_]->IsSame(b))
      return false;
    renderer_pids[index_] = message->GetArgumentList()->GetInt(0);
    std::cout << "RENDERER " << index_ << " " << renderer_pids[index_]
              << std::endl;
    return true;
  }
  void OnAfterCreated(CefRefPtr<CefBrowser> b) override {
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
class App final : public CefApp,
                  public CefBrowserProcessHandler,
                  public CefRenderProcessHandler {
public:
  CefRefPtr<CefRenderProcessHandler> GetRenderProcessHandler() override {
    return this;
  }
  void OnContextCreated(CefRefPtr<CefBrowser>, CefRefPtr<CefFrame> frame,
                        CefRefPtr<CefV8Context>) override {
    if (!frame->IsMain()) return;
    auto message = CefProcessMessage::Create("BROWSER_LAB_RENDERER_PID");
    message->GetArgumentList()->SetInt(0, getpid());
    frame->SendProcessMessage(PID_BROWSER, message);
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
                                    "http://127.0.0.1:19478/interactions?tab=" +
                                        std::string(i ? "B" : "A"),
                                    settings, nullptr, nullptr);
    }
  }

private:
  IMPLEMENT_REFCOUNTING(App);
};
class CloseTask final : public CefTask {
public:
  void Execute() override {
    auto extras = popups;
    for (auto b : extras)
      b->GetHost()->CloseBrowser(true);
    for (auto b : views)
      if (b)
        b->GetHost()->CloseBrowser(true);
  }

private:
  IMPLEMENT_REFCOUNTING(CloseTask);
};
class CrashRendererTask final : public CefTask {
  int index_;

public:
  explicit CrashRendererTask(int index) : index_(index) {}
  void Execute() override {
    if (renderer_pids[index_] > 1 && views[index_])
      kill(renderer_pids[index_], SIGKILL);
  }

private:
  IMPLEMENT_REFCOUNTING(CrashRendererTask);
};
int main(int argc, char **argv) {
  CefMainArgs args(argc, argv);
  int code = CefExecuteProcess(args, new App(), nullptr);
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
  if (!CefInitialize(args, settings, new App(), nullptr))
    return 1;
  std::thread([] {
    std::string line;
    while (std::getline(std::cin, line)) {
      if (line == "close") {
        CefPostTask(TID_UI, new CloseTask());
        break;
      }
      if (line == "crash-renderer-A" || line == "crash-renderer-B")
        CefPostTask(TID_UI, new CrashRendererTask(line.back() == 'B' ? 1 : 0));
    }
  }).detach();
  CefRunMessageLoop();
  CefShutdown();
  if (closed_count == 2)
    std::cout << "SHUTDOWN" << std::endl;
  return closed_count == 2 ? 0 : 2;
}
