package daemon

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/presence"
	"github.com/local/dsh-work/internal/settings"
)

// SurfaceFrom returns the workspace surface selected by the /control caller.
func SurfaceFrom(ctx context.Context) string { return app.LocalClientSurface(ctx) }

func isWorkspaceSurface(surface string) bool {
	if surface == "workspace" {
		return true
	}
	if !strings.HasPrefix(surface, "workspace-") {
		return false
	}
	value := strings.TrimPrefix(surface, "workspace-")
	number, err := strconv.ParseUint(value, 10, 64)
	return err == nil && number >= 2 && strconv.FormatUint(number, 10) == value
}

// WindowService owns per-window focus and routes window-specific persistence
// through the caller surface stamped by Server.invoke.
type WindowService struct {
	Settings *settings.Manager
	Presence *presence.Tracker
}

func (s *WindowService) SetFocus(ctx context.Context, focused bool) error {
	surface := SurfaceFrom(ctx)
	if !isWorkspaceSurface(surface) {
		return errors.New("workspace surface required")
	}
	if s.Presence != nil {
		s.Presence.SetFocused(surface, focused)
	}
	return nil
}

func (s *WindowService) Closed(ctx context.Context) error {
	surface := SurfaceFrom(ctx)
	if !isWorkspaceSurface(surface) {
		return errors.New("workspace surface required")
	}
	if s.Presence != nil {
		s.Presence.Clear(surface)
	}
	return nil
}

func (s *WindowService) SaveZoom(ctx context.Context, zoom float64) error {
	if !isWorkspaceSurface(SurfaceFrom(ctx)) {
		return errors.New("workspace surface required")
	}
	if s.Settings == nil {
		return errors.New("settings are unavailable")
	}
	return s.Settings.SetWorkspaceZoom(ctx, zoom)
}

func (s *WindowService) SaveGeometry(ctx context.Context, geometry settings.WindowGeometry) error {
	surface := SurfaceFrom(ctx)
	if !isWorkspaceSurface(surface) {
		return errors.New("workspace surface required")
	}
	if surface != "workspace" {
		return nil
	}
	if s.Settings == nil {
		return errors.New("settings are unavailable")
	}
	return s.Settings.SetWindowGeometry(ctx, surface, geometry)
}

// HostService combines the existing host controls with the daemon-owned update
// action, keeping updater implementation details outside internal/app.
type HostService struct {
	*app.HostService
	UpdateState   func() UpdateSnapshot
	UpdateCommand func(context.Context, UpdateAction) error
}

func (s *HostService) UpdateAction(ctx context.Context, action UpdateAction) (UpdateSnapshot, error) {
	if !isWorkspaceSurface(SurfaceFrom(ctx)) {
		return UpdateSnapshot{}, errors.New("workspace surface required")
	}
	if action != UpdateActionCheck && action != UpdateActionDownload && action != UpdateActionInstall {
		return UpdateSnapshot{}, errors.New("invalid update action")
	}
	if s.UpdateCommand == nil {
		return UpdateSnapshot{}, errors.New("update controls unavailable")
	}
	if err := s.UpdateCommand(ctx, action); err != nil {
		return UpdateSnapshot{}, err
	}
	if s.UpdateState != nil {
		return s.UpdateState(), nil
	}
	return UpdateSnapshot{}, nil
}
