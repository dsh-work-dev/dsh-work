//go:build windows

package platform

import "golang.org/x/sys/windows"

var allowSetForegroundWindow = windows.NewLazySystemDLL("user32.dll").NewProc("AllowSetForegroundWindow")

// AllowForegroundActivation lets the UI process honor the foreground request
// that follows a user action in the daemon process.
func AllowForegroundActivation() {
	_, _, _ = allowSetForegroundWindow.Call(uintptr(^uint32(0)))
}
