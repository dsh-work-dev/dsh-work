//go:build !windows

package desktopprobe

func probeProcesses(uint32) any { return map[string]any{"error": "unsupported platform"} }
