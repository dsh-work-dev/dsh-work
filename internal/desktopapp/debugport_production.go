//go:build production

package desktopapp

// webviewDebugArgs: release builds open no remote-debugging port.
func webviewDebugArgs() []string { return nil }
