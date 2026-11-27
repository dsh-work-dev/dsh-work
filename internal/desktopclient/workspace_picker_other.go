//go:build !darwin || !cgo || server

package desktopclient

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func WorkspaceDirectoryPicker(func() application.Window) func(context.Context) (string, error) {
	return nil
}
