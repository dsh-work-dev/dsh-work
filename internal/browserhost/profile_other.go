//go:build !windows

package browserhost

import (
	"context"
	"github.com/local/dsh-work/internal/settings"
)

type profileLauncher struct{}

func (profileLauncher) Resolve(string, string, string) (settings.BrowserLaunchHint, error) {
	return settings.BrowserLaunchHint{}, ErrUnsupported
}
func (profileLauncher) Open(context.Context, string, settings.BrowserLaunchHint) error {
	return ErrUnsupported
}
