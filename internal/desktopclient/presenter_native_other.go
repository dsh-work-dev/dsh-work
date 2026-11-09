//go:build !linux

package desktopclient

import "github.com/wailsapp/wails/v3/pkg/application"

func presenterNativeWindow(window application.Window) (uint64, float64, error) {
	return uint64(uintptr(window.NativeWindow())), window.GetZoom(), nil
}
