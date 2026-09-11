//go:build !windows

package desktopprobe

func Processes(uint32, int, int) any { return map[string]any{"error": "unsupported platform"} }
