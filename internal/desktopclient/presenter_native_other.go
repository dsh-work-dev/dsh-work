//go:build !linux

package desktopclient

import (
	"context"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func presenterCapability(context.Context, *EmbeddedBrowserService) (bool, error) { return true, nil }

func presenterNativeWindow(window application.Window) (uint64, float64, error) {
	return uint64(uintptr(window.NativeWindow())), window.GetZoom(), nil
}
