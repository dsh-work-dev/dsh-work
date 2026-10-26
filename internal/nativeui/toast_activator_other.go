//go:build !windows

package nativeui

// EnsureToastActivator requires no registration outside Windows.
func EnsureToastActivator(appName, displayName, clsid string) error { return nil }
