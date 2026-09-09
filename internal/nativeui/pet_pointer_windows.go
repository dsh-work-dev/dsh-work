//go:build windows

package nativeui

import (
	"context"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// x/sys supplies the platform loader; Wails owns window mutations. A bounded
// sampler is sufficient for hit testing and requires no global input hook.
func StartPetPointer(ctx context.Context, w application.Window, hitTest func(float64, float64) bool) {
	user32 := windows.NewLazySystemDLL("user32.dll")
	cursor := user32.NewProc("GetCursorPos")
	toClient := user32.NewProc("ScreenToClient")
	clientRect := user32.NewProc("GetClientRect")
	key := user32.NewProc("GetAsyncKeyState")
	go func() {
		ticker := time.NewTicker(32 * time.Millisecond)
		defer ticker.Stop()
		ignored := false
		defer func() {
			if ignored {
				w.SetIgnoreMouseEvents(false)
			}
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if !w.IsVisible() {
				if ignored {
					w.SetIgnoreMouseEvents(false)
					ignored = false
				}
				continue
			}
			hwnd := uintptr(w.NativeWindow())
			if hwnd == 0 {
				continue
			}
			var point struct{ X, Y int32 }
			var rect struct{ Left, Top, Right, Bottom int32 }
			ok, _, _ := cursor.Call(uintptr(unsafe.Pointer(&point)))
			if ok == 0 {
				continue
			}
			ok, _, _ = toClient.Call(hwnd, uintptr(unsafe.Pointer(&point)))
			if ok == 0 {
				continue
			}
			ok, _, _ = clientRect.Call(hwnd, uintptr(unsafe.Pointer(&rect)))
			if ok == 0 || rect.Right <= 0 || rect.Bottom <= 0 {
				continue
			}
			// Keep the current capture policy while a mouse button is held: native drag
			// loops swallow pointerup, and crossing the hit edge must not release a drag.
			pressed, _, _ := key.Call(1)
			if pressed&0x8000 != 0 {
				continue
			}
			ignore := !hitTest(float64(point.X)/float64(rect.Right), float64(point.Y)/float64(rect.Bottom))
			if ignore != ignored {
				w.SetIgnoreMouseEvents(ignore)
				ignored = ignore
			}
		}
	}()
}
