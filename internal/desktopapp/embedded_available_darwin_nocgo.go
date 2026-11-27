//go:build darwin && !cgo

package desktopapp

func embeddedPlatformAvailable() bool { return false }
