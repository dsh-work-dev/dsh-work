//go:build darwin && cgo && !server

package desktopclient

/*
#include "presenter_view_darwin.h"
*/
import "C"

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// WorkspaceDirectoryPicker is scoped to the workbench, independently of any
// embedded browser view or CEF publication.
func WorkspaceDirectoryPicker(workbench func() application.Window) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		window := workbench()
		if window == nil {
			return "", errors.New("workbench window is unavailable")
		}
		picker := newMacFilePickerResult()
		defer picker.handle.Delete()
		started := application.InvokeSyncWithResult(func() bool {
			return ctx.Err() == nil && C.dsh_mac_workspace_picker_begin(window.NativeWindow(), C.uintptr_t(picker.handle)) == 1
		})
		if !started {
			return "", errors.New("workspace directory picker is unavailable")
		}
		select {
		case <-ctx.Done():
			picker.cancel()
			<-picker.result
			return "", ctx.Err()
		case paths := <-picker.result:
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if len(paths) == 1 && filepath.IsAbs(paths[0]) {
				return paths[0], nil
			}
			return "", nil
		}
	}
}
