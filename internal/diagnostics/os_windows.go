//go:build windows

package diagnostics

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/windows"
)

func osDescription() string {
	v := windows.RtlGetVersion()
	return fmt.Sprintf("Windows %d.%d.%d %s", v.MajorVersion, v.MinorVersion, v.BuildNumber, runtime.GOARCH)
}
