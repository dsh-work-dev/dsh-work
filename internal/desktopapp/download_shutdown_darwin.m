//go:build darwin && cgo && !server

#import <Cocoa/Cocoa.h>
#include <stdint.h>

extern int goMacDownloadShutdownCanceled(uintptr_t handle);

int dsh_mac_download_shutdown(uintptr_t handle, const char* title,
                              const char* wait, const char* cancel, const char* back,
                              int fixtureChoice) {
    NSApplication* app = NSApplication.sharedApplication;
    if (app.modalWindow || goMacDownloadShutdownCanceled(handle)) {
        return 0;
    }
    NSAlert* alert = [[NSAlert alloc] init];
    alert.messageText = [NSString stringWithUTF8String:title];
    alert.alertStyle = NSAlertStyleWarning;
    NSButton* waitButton = [alert addButtonWithTitle:[NSString stringWithUTF8String:wait]];
    [alert addButtonWithTitle:[NSString stringWithUTF8String:cancel]];
    NSButton* backButton = [alert addButtonWithTitle:[NSString stringWithUTF8String:back]];
    waitButton.keyEquivalent = @"\r";
    backButton.keyEquivalent = @"\033";
    NSWindow* window = alert.window;
#ifdef DSH_MAC_PREVIEW_SHUTDOWN
    __block BOOL chose = NO;
#endif
    NSTimer* timer = [NSTimer timerWithTimeInterval:0.1 repeats:YES block:^(NSTimer* current) {
        if (goMacDownloadShutdownCanceled(handle) && app.modalWindow == window) {
            [app abortModal];
            [window orderOut:nil];
            return;
        }
#ifdef DSH_MAC_PREVIEW_SHUTDOWN
        if (!chose && fixtureChoice >= 1 && fixtureChoice <= 3 && app.modalWindow == window) {
            NSButton* button = alert.buttons[(NSUInteger)(fixtureChoice - 1)];
            if (button.enabled) {
                chose = YES;
                [button performClick:nil];
            }
        }
#endif
    }];
    [[NSRunLoop currentRunLoop] addTimer:timer forMode:NSModalPanelRunLoopMode];
    NSModalResponse response = [alert runModal];
    [timer invalidate];
    [alert release];
    if (response == NSAlertFirstButtonReturn) {
        return 1;
    }
    if (response == NSAlertSecondButtonReturn) {
        return 2;
    }
    return response == NSAlertThirdButtonReturn ? 3 : 0;
}
