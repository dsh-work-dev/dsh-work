#pragma once
#include "include/cef_browser.h"
#include "include/cef_render_handler.h"
#include <cstdint>
#include <functional>
#include <string>

namespace dsh_platform {
using Window = CefWindowHandle;
bool Transfer(bool write, void* bytes, uint32_t size);
std::string TakeEnvironment(const char* name);
bool OpenTransport(const std::string& path);
bool Authenticate(const std::string& token);
void Erase(std::string& value);
void CancelTransport();
void CloseTransport();
bool Visible(Window);
std::string Hide(Window);
std::string Present(Window, CefRefPtr<CefDictionaryValue>);
bool ConfigureWindow(CefWindowInfo&);
void ViewRect(CefRect&);
void Paint(const void* buffer, int width, int height);
void StartOwnerMonitor(std::function<void()> onStop);
void PrepareOwnedShutdown();
int FinishOwnedShutdown();
}
