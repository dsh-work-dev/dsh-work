//go:build !windows

package platform

// AllowForegroundActivation is a no-op where the operating system has no
// Windows foreground activation lock.
func AllowForegroundActivation() {}
