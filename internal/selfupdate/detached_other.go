//go:build !windows

package selfupdate

import "fmt"

// startDetachedProcess is only implemented for the Windows release unit. A
// non-Windows build refuses the installer handoff before it reaches this
// function, so reaching it is an implementation error.
func startDetachedProcess(string, []string) (*detachedProcess, error) {
	return nil, fmt.Errorf("%w: installer handoff is only defined on Windows", ErrUnsupportedPlatform)
}

// detachedProcess exists so the Windows and non-Windows handoff code share one
// shape. Nothing can construct one on this platform.
type detachedProcess struct {
	handle uintptr
	PID    int
}
