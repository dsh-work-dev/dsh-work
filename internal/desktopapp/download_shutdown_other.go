//go:build !windows && (!darwin || !cgo || server)

package desktopapp

import (
	"context"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func askDownloadShutdownPlatform(ctx context.Context, _ *application.App, _ settings.Locale) (bool, error) {
	// Platforms without a native coordinator retain their exit contract.
	return false, ctx.Err()
}
