//go:build !windows

package desktopapp

import (
	"context"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func askDownloadShutdownPlatform(ctx context.Context, _ *application.App, _ settings.Locale) (bool, error) {
	// Download coordinators currently exist only on Windows. Preserve the
	// other platforms' existing exit contract without a new UI prerequisite.
	return false, ctx.Err()
}
