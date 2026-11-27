#pragma once
#include "include/cef_browser.h"
#include "include/cef_render_handler.h"
#include <cstdint>
#include <functional>
#include <string>

namespace dsh_platform {
void Lifecycle(const char* event);
using Window = CefWindowHandle;
bool Transfer(bool write, void* bytes, uint32_t size);
std::string TakeEnvironment(const char* name);
bool OpenTransport(const std::string& path);
bool Authenticate(const std::string& token);
void Erase(std::string& value);
void CancelTransport();
void CloseTransport();
bool ConfigureWindow(CefWindowInfo&);
void StartOwnerMonitor(std::function<void()> onStop);
void PrepareOwnedShutdown();
int FinishOwnedShutdown();
}
#include "presenter_mac.h"
