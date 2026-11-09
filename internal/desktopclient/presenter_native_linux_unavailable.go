//go:build linux && (!cgo || !e67presenter || server)

package desktopclient

import (
	"context"
	"errors"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func presenterCapability(context.Context, *EmbeddedBrowserService) (bool, error) { return false, nil }

func presenterNativeWindow(application.Window) (uint64, float64, error) {
	return 0, 0, errors.New("CEF_X11_PRESENTER_UNAVAILABLE")
}
