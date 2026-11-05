//go:build windows

package nativeui

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

// Physical coordinates match driver screenshots. Never activate a decoration
// window or modify the system cursor. The pointer is excluded from capture.
func ShowComputerOverlay(w *application.WebviewWindow, x, y int, pointer bool) {
	hwnd := uintptr(w.NativeWindow())
	if hwnd == 0 {
		return
	}
	dll := windows.NewLazySystemDLL("user32.dll")
	get := dll.NewProc("GetWindowLongPtrW")
	set := dll.NewProc("SetWindowLongPtrW")
	style, _, _ := get.Call(hwnd, ^uintptr(19))
	set.Call(hwnd, ^uintptr(19), style|0x08000000|0x80)
	// Wails owns the WebView controller's visibility as well as the HWND.
	// SetWindowPos alone shows an empty container after Hide() hid Chromium.
	// NOACTIVATE is installed before Show() so the decoration keeps user focus.
	w.Show()
	if pointer {
		dll.NewProc("SetWindowDisplayAffinity").Call(hwnd, 0x11)
	}
	// SWP_NOACTIVATE | SWP_SHOWWINDOW | SWP_NOSIZE. HWND_TOPMOST.
	dll.NewProc("SetWindowPos").Call(hwnd, ^uintptr(0), uintptr(x), uintptr(y), 0, 0, 0x10|0x40|0x1)
}

func ShowComputerPointer(w *application.WebviewWindow, x, y float64, target uint64, hotspotX, hotspotY float64) {
	dpi, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("GetDpiForWindow").Call(uintptr(target))
	if dpi == 0 {
		return
	}
	scale := float64(dpi) / 96
	ShowComputerOverlay(w, int(x-hotspotX*scale), int(y-hotspotY*scale), true)
}
