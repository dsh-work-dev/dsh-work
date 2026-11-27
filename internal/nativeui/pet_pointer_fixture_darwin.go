//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package nativeui

/*
#cgo CFLAGS: -x objective-c
#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#import <objc/runtime.h>

static bool dshPreviewPetIgnoresMouse(void *native) {
    return ((NSWindow *)native).ignoresMouseEvents;
}

static NSPoint dshPreviewPetScreenPoint(NSWindow *window, double x, double y) {
    NSRect bounds = [window.contentView convertRect:window.contentView.bounds toView:nil];
    return [window convertPointToScreen:NSMakePoint(NSMinX(bounds) + x * NSWidth(bounds),
                                                   NSMaxY(bounds) - y * NSHeight(bounds))];
}

static void *dshPreviewPetPlaceTarget(void *native, double x, double y) {
    NSWindow *window = (NSWindow *)native;
    NSPoint point = dshPreviewPetScreenPoint(window, x, y);
    NSRect frame = NSMakeRect(point.x - 8, point.y - 8, 16, 16);
    NSWindow *target = [[NSWindow alloc] initWithContentRect:frame styleMask:NSWindowStyleMaskBorderless
                                                    backing:NSBackingStoreBuffered defer:NO];
    target.releasedWhenClosed = NO;
    target.hasShadow = NO;
    target.backgroundColor = NSColor.windowBackgroundColor;
    target.level = window.level;
    [target orderFrontRegardless];
    [window orderFrontRegardless];
    return target;
}

static bool dshPreviewPetRoutesMouse(void *native, void *backdrop, double x, double y, bool passThrough, long *hitNumber, long *expectedNumber) {
    NSWindow *window = (NSWindow *)native;
    NSWindow *target = (NSWindow *)backdrop;
    NSPoint point = dshPreviewPetScreenPoint(window, x, y);
    NSInteger expected = passThrough ? target.windowNumber : window.windowNumber;
    NSInteger hit = [NSWindow windowNumberAtPoint:point belowWindowWithWindowNumber:0];
    *hitNumber = hit;
    *expectedNumber = expected;
    return expected > 0 && hit == expected;
}

static void dshPreviewPetCloseTarget(void *backdrop) {
    NSWindow *target = (NSWindow *)backdrop;
    [target close];
    [target release];
}

static char dshPreviewPetClickKey;

static WKWebView *dshPreviewPetWebView(NSView *view) {
    if ([view isKindOfClass:[WKWebView class]]) return (WKWebView *)view;
    for (NSView *child in view.subviews) {
        WKWebView *webView = dshPreviewPetWebView(child);
        if (webView) return webView;
    }
    return nil;
}

static int dshPreviewPetClick(void *native) {
    NSWindow *window = (NSWindow *)native;
    if (!window) return -1;
    NSNumber *state = objc_getAssociatedObject(window, &dshPreviewPetClickKey);
    if (state) return state.intValue;
    WKWebView *webView = dshPreviewPetWebView(window.contentView);
    if (!webView) return -1;
    objc_setAssociatedObject(window, &dshPreviewPetClickKey, @0, OBJC_ASSOCIATION_RETAIN_NONATOMIC);
    NSString *observe = @"(() => {"
        "const touch = document.querySelector('#pet-touch:not([hidden])');"
        "if (!touch) return false;"
        "touch.addEventListener('click', event => { globalThis.__dshPreviewTrustedPetClick = event.isTrusted; }, {once: true});"
        "return true;"
    "})()";
    [webView evaluateJavaScript:observe completionHandler:^(id result, NSError *error) {
        if (error || ![result boolValue] || !window.isVisible || window.ignoresMouseEvents) {
            objc_setAssociatedObject(window, &dshPreviewPetClickKey, @(-1), OBJC_ASSOCIATION_RETAIN_NONATOMIC);
            return;
        }
        NSRect bounds = webView.bounds;
        CGFloat y = webView.isFlipped ? 0.66 : 0.34;
        NSPoint location = [webView convertPoint:NSMakePoint(NSWidth(bounds) * 0.50, NSHeight(bounds) * y) toView:nil];
        for (NSEventType type = NSEventTypeLeftMouseDown; type <= NSEventTypeLeftMouseUp; ++type) {
            NSEvent *event = [NSEvent mouseEventWithType:type location:location modifierFlags:0
                timestamp:NSProcessInfo.processInfo.systemUptime windowNumber:window.windowNumber
                context:nil eventNumber:1 clickCount:1 pressure:type == NSEventTypeLeftMouseDown ? 1 : 0];
            [window sendEvent:event];
        }
        dispatch_after(dispatch_time(DISPATCH_TIME_NOW, 500 * NSEC_PER_MSEC), dispatch_get_main_queue(), ^{
            [webView evaluateJavaScript:@"globalThis.__dshPreviewTrustedPetClick === true" completionHandler:^(id clicked, NSError *clickError) {
                int result = !clickError && [clicked boolValue] ? 1 : -1;
                objc_setAssociatedObject(window, &dshPreviewPetClickKey, @(result), OBJC_ASSOCIATION_RETAIN_NONATOMIC);
            }];
        });
    }];
    return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// CheckMacPreviewPetPointer drives the real sampler using only local points
// in the isolated Pet window. The hit map comes from its running WebView.
func CheckMacPreviewPetPointer(window application.Window, hitTest func(float64, float64) bool) (map[string]bool, error) {
	margin := macPetPointerSample{x: 0.04, y: 0.05}
	body := macPetPointerSample{x: 0.50, y: 0.66}
	handle := macPetPointerSample{x: 0.90, y: 0.86}
	if hitTest(margin.x, margin.y) || !hitTest(body.x, body.y) || !hitTest(handle.x, handle.y) {
		return nil, errors.New("Pet WebView hit map is not ready")
	}
	pointer := macPetPointer{window: window, hitTest: hitTest}
	defer pointer.restore()
	target := application.InvokeSyncWithResult(func() unsafe.Pointer {
		return C.dshPreviewPetPlaceTarget(window.NativeWindow(), C.double(margin.x), C.double(margin.y))
	})
	defer application.InvokeSync(func() { C.dshPreviewPetCloseTarget(target) })
	ignoresMouse := func() bool {
		return application.InvokeSyncWithResult(func() bool {
			return bool(C.dshPreviewPetIgnoresMouse(window.NativeWindow()))
		})
	}
	samples := []struct {
		name   string
		point  macPetPointerSample
		ignore bool
		route  string
	}{
		{"transparentMargin", margin, true, "systemRoutesMarginToWindowBehindPet"},
		{"outsidePressRetainsPassThrough", macPetPointerSample{x: body.x, y: body.y, held: true}, true, ""},
		{"bodyReceivesInput", body, false, "systemRoutesBodyToPet"},
		{"dragHandleReceivesInput", handle, false, ""},
		{"dragKeepsCaptureOutsideHandle", macPetPointerSample{x: margin.x, y: margin.y, held: true}, false, ""},
		{"releaseRestoresPassThrough", margin, true, ""},
	}
	evidence := make(map[string]bool, len(samples)+3)
	for _, sample := range samples {
		pointer.update(&sample.point)
		evidence[sample.name] = ignoresMouse() == sample.ignore
		if sample.route != "" {
			var hit, expected C.long
			deadline := time.Now().Add(500 * time.Millisecond)
			for {
				if application.InvokeSyncWithResult(func() bool {
					return bool(C.dshPreviewPetRoutesMouse(window.NativeWindow(), target, C.double(sample.point.x), C.double(sample.point.y), C.bool(sample.ignore), &hit, &expected))
				}) {
					evidence[sample.route] = true
					break
				}
				if time.Now().After(deadline) {
					return nil, fmt.Errorf("%s: system hit %d, expected %d", sample.route, hit, expected)
				}
				time.Sleep(20 * time.Millisecond)
			}
		}
	}
	pointer.restore()
	evidence["cleanupRestoresInput"] = !ignoresMouse()
	return evidence, nil
}

// CheckMacPreviewPetClick sends the mouse sequence only to the fixture window.
func CheckMacPreviewPetClick(window application.Window) (bool, error) {
	state := application.InvokeSyncWithResult(func() int {
		return int(C.dshPreviewPetClick(window.NativeWindow()))
	})
	if state < 0 {
		return false, errors.New("owned Pet window did not receive a trusted WebView click")
	}
	return state == 1, nil
}
