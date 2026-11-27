// Official helper order, with identity diagnostics before sandbox initialization.
// This is an observed entry marker, not an application pre-exec ownership gate.
#include "identity.h"
#include "include/cef_app.h"
#include "include/cef_sandbox_mac.h"
#include "include/wrapper/cef_library_loader.h"

int main(int argc,char** argv) {
  owner_probe::Self("helper-entry",argc,argv);
  CefScopedSandboxContext sandbox;
  if(!sandbox.Initialize(argc,argv)) { owner_probe::Emit("{\"event\":\"helper-sandbox-failed\"}"); return 1; }
  CefScopedLibraryLoader loader;
  if(!loader.LoadInHelper()) { owner_probe::Emit("{\"event\":\"helper-framework-failed\"}"); return 2; }
  CefMainArgs args(argc,argv);
  return CefExecuteProcess(args,nullptr,nullptr);
}
