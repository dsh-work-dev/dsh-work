//go:build linux

package desktopapp

import (
	"context"
	"os"
	"path/filepath"
	"time"

	linuxplatform "github.com/local/dsh-work/internal/platform/linux"
)

func embeddedPlatformAvailable() bool {
	if embeddedDevelopmentRoot() == "" {
		return false
	}
	exe := os.Getenv("DSH_WORK_CEF_EXECUTABLE")
	if !filepath.IsAbs(exe) {
		return false
	}
	info, err := os.Stat(exe)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return linuxplatform.Available(ctx) == nil
}
