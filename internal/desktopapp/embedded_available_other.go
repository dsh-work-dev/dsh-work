//go:build !linux && !darwin

package desktopapp

func embeddedPlatformAvailable() bool { return true }
