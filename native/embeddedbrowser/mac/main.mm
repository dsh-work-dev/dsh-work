#include "include/cef_app.h"
#include "include/cef_application_mac.h"
#include "include/wrapper/cef_library_loader.h"
#include "include/cef_version_info.h"
#include <cstring>
#import <Cocoa/Cocoa.h>

@interface DSHOwnedCEFApplication : NSApplication<CefAppProtocol> { BOOL handling_; }
@end
@implementation DSHOwnedCEFApplication
- (BOOL)isHandlingSendEvent { return handling_; }
- (void)setHandlingSendEvent:(BOOL)value { handling_=value; }
- (void)sendEvent:(NSEvent*)event { CefScopedSendingEvent scope;[super sendEvent:event]; }
@end
int RunBroker(CefMainArgs,void*);
int main(int argc,char** argv) {
  CefScopedLibraryLoader loader;if(!loader.LoadInMain())return 70;
  const char* version=cef_version_full();
  if(!version||std::strcmp(version,"156.0.15+gcf671c0+chromium-156.0.8078.80")!=0)return 76;
  @autoreleasepool {
    [DSHOwnedCEFApplication sharedApplication];[NSApp setActivationPolicy:NSApplicationActivationPolicyProhibited];
    return RunBroker(CefMainArgs(argc,argv),nullptr);
  }
}
