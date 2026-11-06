// Standalone CEF UI loop: no GTK4/Wails main-context ownership in this process.
#include "include/cef_app.h"
#include "include/cef_client.h"
#include "include/cef_command_line.h"
#include "include/cef_task.h"
#include <iostream>
#include <string>
#include <thread>

static CefRefPtr<CefBrowser> browser;
static bool saw_close = false;
class Client final : public CefClient, public CefLifeSpanHandler {
public:
  CefRefPtr<CefLifeSpanHandler> GetLifeSpanHandler() override { return this; }
  void OnAfterCreated(CefRefPtr<CefBrowser> value) override {
    browser = value;
    std::cout << "CREATED " << value->GetHost()->GetWindowHandle() << std::endl;
  }
  void OnBeforeClose(CefRefPtr<CefBrowser> value) override {
    browser = nullptr;
    saw_close = true;
    std::cout << "CLOSED" << std::endl;
    CefQuitMessageLoop();
  }

private:
  IMPLEMENT_REFCOUNTING(Client);
};
class App final : public CefApp, public CefBrowserProcessHandler {
public:
  CefRefPtr<CefBrowserProcessHandler> GetBrowserProcessHandler() override {
    return this;
  }
  void OnContextInitialized() override {
    auto command = CefCommandLine::GetGlobalCommandLine();
    CefWindowInfo info;
    info.SetAsChild(
        std::stoull(command->GetSwitchValue("parent-xid").ToString()),
        CefRect(240, 100, 700, 540));
    info.runtime_style = CEF_RUNTIME_STYLE_ALLOY;
    CefBrowserSettings settings;
    CefBrowserHost::CreateBrowser(info, new Client(),
                                  command->GetSwitchValue("url"), settings,
                                  nullptr, nullptr);
  }

private:
  IMPLEMENT_REFCOUNTING(App);
};
class CloseTask final : public CefTask {
public:
  void Execute() override {
    if (browser)
      browser->GetHost()->CloseBrowser(true);
  }

private:
  IMPLEMENT_REFCOUNTING(CloseTask);
};
int main(int argc, char **argv) {
  CefMainArgs args(argc, argv);
  int code = CefExecuteProcess(args, nullptr, nullptr);
  if (code >= 0)
    return code;
  auto command = CefCommandLine::CreateCommandLine();
  command->InitFromArgv(argc, argv);
  CefSettings settings;
  settings.no_sandbox = false;
  settings.remote_debugging_port = 19479;
  CefString(&settings.root_cache_path) = command->GetSwitchValue("cache-path");
  CefString(&settings.cache_path) = command->GetSwitchValue("cache-path");
  CefString(&settings.log_file) = command->GetSwitchValue("log-file");
  if (!CefInitialize(args, settings, new App(), nullptr))
    return 1;
  std::thread([] {
    std::string line;
    if (std::getline(std::cin, line) && line == "close")
      CefPostTask(TID_UI, new CloseTask());
  }).detach();
  CefRunMessageLoop();
  CefShutdown();
  if (saw_close)
    std::cout << "SHUTDOWN" << std::endl;
  return saw_close ? 0 : 2;
}
