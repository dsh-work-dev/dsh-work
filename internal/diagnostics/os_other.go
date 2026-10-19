//go:build !windows

package diagnostics

import "runtime"

func osDescription() string {
	return runtime.GOOS + " " + runtime.GOARCH
}
