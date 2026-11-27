#pragma once
// Same-process registration precedes the helper's first CEF call. It does not
// replace the official sandbox/framework/CefExecuteProcess initialization order.
bool RegisterOwnedHelper(int argc, char** argv);
