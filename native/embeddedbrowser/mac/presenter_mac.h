#pragma once
#include "include/cef_browser.h"
#include "include/cef_render_handler.h"
#include <functional>
namespace dsh_platform {
std::string PresentBrowser(CefRefPtr<CefBrowser>,CefRefPtr<CefDictionaryValue>,std::function<void()>);
std::string HideBrowser(CefRefPtr<CefBrowser>,CefRefPtr<CefDictionaryValue>);
bool BrowserVisible(CefRefPtr<CefBrowser>);
bool BrowserSuspendedInVisibleHost(CefRefPtr<CefBrowser>);
bool OpenBrowserFilePicker(CefRefPtr<CefBrowser>,const std::string&,const std::vector<CefString>&,bool,bool,
                           std::function<void(std::vector<CefString>)>);
void CancelBrowserFilePicker(int);
void RetireBrowser(int);
void BrowserViewRect(int,CefRect&);
bool BrowserScreenInfo(int,CefScreenInfo&);
void BrowserPaint(int,CefRenderHandler::PaintElementType,const void*,int,int);
void BrowserIME(int,const CefRange&,const CefRenderHandler::RectList&);
}
