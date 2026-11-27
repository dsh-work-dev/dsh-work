#import <AppKit/AppKit.h>
#include <unistd.h>

@interface ButtonFixture : NSObject
@property(nonatomic, copy) NSString *witness;
@property(nonatomic, strong) NSWindow *window;
@property(nonatomic) NSInteger clicks;
@end

@implementation ButtonFixture
- (void)record {
    NSString *value = [NSString stringWithFormat:@"%d %ld %ld\n", getpid(),
                      (long)self.window.windowNumber, (long)self.clicks];
    [value writeToFile:self.witness atomically:YES encoding:NSUTF8StringEncoding error:nil];
}
- (void)clicked:(NSButton *)button {
    self.clicks++;
    button.title = @"Clicked";
    [self record];
}
@end

int main(int argc, const char **argv) {
    if (argc != 2) return 2;
    @autoreleasepool {
        NSApplication *app = NSApplication.sharedApplication;
        [app setActivationPolicy:NSApplicationActivationPolicyAccessory];
        [app finishLaunching];
        ButtonFixture *fixture = [ButtonFixture new];
        fixture.witness = @(argv[1]);
        fixture.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(180, 180, 320, 200)
            styleMask:NSWindowStyleMaskTitled backing:NSBackingStoreBuffered defer:NO];
        fixture.window.animationBehavior = NSWindowAnimationBehaviorNone;
        fixture.window.title = @"DSH Work AUV button fixture";
        NSButton *button = [NSButton buttonWithTitle:@"Click me" target:fixture action:@selector(clicked:)];
        button.frame = NSMakeRect(60, 50, 200, 100);
        [fixture.window.contentView addSubview:button];
        [fixture.window orderFront:nil];
        [fixture record];
        [NSTimer scheduledTimerWithTimeInterval:0.25 repeats:YES block:^(__unused NSTimer *timer) {
            if ([[NSFileManager defaultManager] fileExistsAtPath:[fixture.witness stringByAppendingString:@".stop"]]) {
                [app terminate:nil];
            }
        }];
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 90 * NSEC_PER_SEC), dispatch_get_main_queue(), ^{
            [app terminate:nil];
        });
        [app run];
    }
    return 0;
}
