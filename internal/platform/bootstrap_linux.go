//go:build linux

package platform

import linuxplatform "github.com/local/dsh-work/internal/platform/linux"

// RunBootstrap must precede command parsing in executables that use the Linux
// process owner. It accepts only the fixed private-FD bootstrap invocation.
func RunBootstrap() bool { return linuxplatform.RunBootstrap() }
