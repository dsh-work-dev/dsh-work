#include "owner_mac.h"
#include "include/cef_app.h"
#include "include/cef_sandbox_mac.h"
#include "include/wrapper/cef_library_loader.h"

int main(int argc,char** argv) {
  if(!RegisterOwnedHelper(argc,argv))return 74;
  CefScopedSandboxContext sandbox;if(!sandbox.Initialize(argc,argv))return 71;
  CefScopedLibraryLoader loader;if(!loader.LoadInHelper())return 70;
  return CefExecuteProcess(CefMainArgs(argc,argv),nullptr,nullptr);
}
