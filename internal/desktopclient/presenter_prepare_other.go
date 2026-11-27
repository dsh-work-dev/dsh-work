//go:build !darwin || !cgo || server

package desktopclient

import (
	"context"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func preparePresenterWindow(context.Context, *EmbeddedBrowserService, application.Window) error {
	return nil
}
