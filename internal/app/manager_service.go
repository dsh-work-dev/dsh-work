package app

import (
	"context"
	"time"

	"github.com/local/work/internal/dshmanager"
	"github.com/local/work/internal/lifecycle"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ManagerService is the trusted Settings-window binding for the nested DSH
// manager. It exposes catalog, selection, runtime/home and profile-plugin
// operations; profile composition and plugin mutation still go through DSH's
// public CLI seam, never direct file edits.
type ManagerService struct {
	manager *dshmanager.Manager
}

const managerOperationTimeout = 2 * time.Minute

func NewManagerService(manager *dshmanager.Manager) *ManagerService {
	return &ManagerService{manager: manager}
}

func (s *ManagerService) GetSnapshot(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.Snapshot(ctx)
}

// GetTheme reads the selected DSH home's appearance preference. Settings uses
// it while both trusted windows are open so its surface follows changes made
// by DSH.
func (s *ManagerService) GetTheme(ctx context.Context) dshmanager.ThemePreference {
	if s == nil || s.manager == nil || !isTrustedWindow(ctx, "settings") {
		return dshmanager.ThemePreferenceSystem
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	theme, err := s.manager.Theme(ctx)
	if err != nil || !theme.Valid() {
		return dshmanager.ThemePreferenceSystem
	}
	return theme
}

func (s *ManagerService) SetDesiredSelection(ctx context.Context, selection dshmanager.LaunchSelection) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.SetDesired(ctx, selection)
}

func (s *ManagerService) InstallRuntime(ctx context.Context, version string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.InstallRuntime(ctx, version)
}

func (s *ManagerService) RemoveRuntime(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RemoveRuntime(ctx, id)
}

func (s *ManagerService) RegisterHome(ctx context.Context, home dshmanager.HomeInfo) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RegisterHome(ctx, home)
}

func (s *ManagerService) RemoveHome(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RemoveHome(ctx, id)
}

func (s *ManagerService) ResolveLaunch(ctx context.Context, request dshmanager.LaunchRequest) (dshmanager.ResolvedLaunch, error) {
	if s == nil || s.manager == nil {
		return dshmanager.ResolvedLaunch{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.ResolvedLaunch{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.ResolveLaunch(ctx, request)
}

func (s *ManagerService) ListPlugins(ctx context.Context, request dshmanager.PluginListRequest) ([]dshmanager.PluginInfo, error) {
	if s == nil || s.manager == nil {
		return nil, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return nil, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.ListPlugins(ctx, request)
}

func (s *ManagerService) InstallPlugin(ctx context.Context, request dshmanager.PluginInstallRequest) (dshmanager.PluginResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.PluginResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.PluginResult{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.InstallPlugin(ctx, request)
}

func (s *ManagerService) RemovePlugin(ctx context.Context, request dshmanager.PluginRemoveRequest) (dshmanager.PluginResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.PluginResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.PluginResult{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RemovePlugin(ctx, request)
}

func (s *ManagerService) RenameProfile(ctx context.Context, request dshmanager.ProfileRenameRequest) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("DSH management is available only in the Settings window.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RenameProfile(ctx, request)
}

func managerContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, managerOperationTimeout)
}

func isTrustedWindow(ctx context.Context, name string) bool {
	if ctx == nil {
		return false
	}
	window, ok := ctx.Value(application.WindowKey).(application.Window)
	return ok && window != nil && window.Name() == name
}

func trustedSurfaceRequired(detail string) error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorTrustedSurfaceRequired,
		Summary:       "This Work control is available only on its trusted surface.",
		Detail:        detail,
		CorrelationID: lifecycle.NewCorrelationID(),
	}
}

func managerUnavailable() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorManagerStateInvalid,
		Summary:       "The DSH manager in Work Settings is unavailable.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Restart Work and try again.",
	}
}
