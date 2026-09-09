//go:build !windows

package nativeui

import (
	"context"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Other native overlays remain capability-gated; retain normal pointer input.
func StartPetPointer(context.Context, application.Window, func(float64, float64) bool) {}
