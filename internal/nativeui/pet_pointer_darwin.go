//go:build darwin && cgo && !server

package nativeui

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa
#import <Cocoa/Cocoa.h>

enum { DSH_PET_INACTIVE, DSH_PET_MOVED, DSH_PET_HELD };

static int dshPetPointer(void *native, bool fixture, bool held, double sampleX, double sampleY, double *x, double *y) {
    NSWindow *window = (NSWindow *)native;
    NSView *content = window.contentView;
    if (!content || !window.visible || window.miniaturized || NSApp.hidden) return DSH_PET_INACTIVE;
    // Keep the existing capture policy while a native drag is in progress.
    if (fixture ? held : ([NSEvent pressedMouseButtons] & 1) != 0) return DSH_PET_HELD;
    NSPoint windowPoint;
    if (fixture) {
        NSRect frame = [content convertRect:content.bounds toView:nil];
        windowPoint = NSMakePoint(NSMinX(frame) + sampleX*frame.size.width, NSMaxY(frame) - sampleY*frame.size.height);
    } else {
        windowPoint = window.mouseLocationOutsideOfEventStream;
    }
    NSPoint point = [content convertPoint:windowPoint fromView:nil];
    NSRect rect = content.bounds;
    if (rect.size.width <= 0 || rect.size.height <= 0) return DSH_PET_INACTIVE;
    *x = (point.x - NSMinX(rect)) / rect.size.width;
    *y = (content.flipped ? point.y - NSMinY(rect) : NSMaxY(rect) - point.y) / rect.size.height;
    return DSH_PET_MOVED;
}

*/
import "C"

import (
	"context"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type macPetPointerSample struct {
	x, y float64
	held bool
}

type macPetPointer struct {
	window  application.Window
	hitTest func(float64, float64) bool
	ignored bool
}

func (p *macPetPointer) update(sample *macPetPointerSample) {
	var input macPetPointerSample
	if sample != nil {
		input = *sample
	}
	var x, y C.double
	status := application.InvokeSyncWithResult(func() C.int {
		return C.dshPetPointer(p.window.NativeWindow(), C.bool(sample != nil), C.bool(input.held), C.double(input.x), C.double(input.y), &x, &y)
	})
	if status == C.DSH_PET_HELD {
		return
	}
	ignore := status == C.DSH_PET_MOVED && !p.hitTest(float64(x), float64(y))
	if ignore != p.ignored {
		p.window.SetIgnoreMouseEvents(ignore)
		p.ignored = ignore
	}
}

func (p *macPetPointer) restore() {
	if p.ignored {
		p.window.SetIgnoreMouseEvents(false)
		p.ignored = false
	}
}

func StartPetPointer(ctx context.Context, w application.Window, hitTest func(float64, float64) bool) {
	go func() {
		ticker := time.NewTicker(32 * time.Millisecond)
		defer ticker.Stop()
		pointer := macPetPointer{window: w, hitTest: hitTest}
		defer pointer.restore()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pointer.update(nil)
		}
	}()
}
