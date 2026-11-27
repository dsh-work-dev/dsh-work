#import <AppKit/AppKit.h>
#include <stdio.h>

static NSWindow *open_window(void) {
    NSWindow *window = [[NSWindow alloc]
        initWithContentRect:NSMakeRect(110, 110, 320, 200)
        styleMask:NSWindowStyleMaskTitled | NSWindowStyleMaskClosable
        backing:NSBackingStoreBuffered defer:NO];
    window.releasedWhenClosed = NO;
    window.animationBehavior = NSWindowAnimationBehaviorNone;
    window.title = @"DSH Work window identity fixture";
    [window orderFront:nil];
    return window;
}

int main(void) {
    @autoreleasepool {
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
        [app finishLaunching];
        __block NSWindow *window = open_window();
        printf("%ld\n", (long)window.windowNumber);
        fflush(stdout);
        dispatch_async(dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
            char command[16];
            while (fgets(command, sizeof(command), stdin)) {
                if (command[0] == 'c') {
                    dispatch_sync(dispatch_get_main_queue(), ^{
                        [window close];
                        puts("closed");
                        fflush(stdout);
                    });
                }
                if (command[0] == 'r') {
                    dispatch_sync(dispatch_get_main_queue(), ^{
                        [window close];
                        [window release];
                        window = open_window();
                        printf("%ld\n", (long)window.windowNumber);
                        fflush(stdout);
                    });
                }
            }
            dispatch_async(dispatch_get_main_queue(), ^{ [app terminate:nil]; });
        });
        [app run];
        [window release];
    }
    return 0;
}
