package app

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

// SetBackupFileActions binds native dialogs at composition time, outside the
// Wails method surface. File paths come from the system picker, not web content.
func SetBackupFileActions(s *ManagerService, choose func() (string, error), open func(string) error) {
	s.chooseBackup = choose
	s.openBackups = open
}

func (s *ManagerService) ListProfileBackups(ctx context.Context, ref dshmanager.ProfileRef) ([]dshmanager.ProfileBackupResult, error) {
	if s == nil || s.manager == nil {
		return nil, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return nil, trustedSurfaceRequired("Profile backups are available in Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.ListProfileBackups(ctx, ref)
}

func (s *ManagerService) DeleteProfileBackup(ctx context.Context, ref dshmanager.ProfileRef, fileName string) error {
	if s == nil || s.manager == nil {
		return managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return trustedSurfaceRequired("Profile backups are available in Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.DeleteProfileBackup(ctx, ref, fileName)
}

func (s *ManagerService) RestoreProfileBackup(ctx context.Context, request dshmanager.ProfileRestoreRequest) (dshmanager.ProfileCloneResult, error) {
	if s == nil || s.manager == nil {
		return dshmanager.ProfileCloneResult{}, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.ProfileCloneResult{}, trustedSurfaceRequired("Profile backups are available in Settings.")
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	return s.manager.RestoreProfileBackup(ctx, request)
}

func (s *ManagerService) ImportProfileBackup(ctx context.Context) (*dshmanager.ProfileCloneResult, error) {
	if s == nil || s.manager == nil || s.chooseBackup == nil {
		return nil, managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return nil, trustedSurfaceRequired("Profile backups are available in Settings.")
	}
	snapshot, err := s.manager.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	target := snapshot.Current
	if target == nil {
		target = snapshot.Configured
	}
	if target == nil {
		return nil, lifecycle.Failure{Code: lifecycle.ErrorProfileRequired, Summary: "Select a current environment before importing a profile."}
	}
	dataDirectoryID := target.Profile.DataDirectoryID
	path, err := s.chooseBackup()
	if err != nil || path == "" {
		return nil, err
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	result, err := s.manager.ImportProfileBackup(ctx, dataDirectoryID, path)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *ManagerService) OpenProfileBackups(ctx context.Context, ref dshmanager.ProfileRef) error {
	if s == nil || s.manager == nil || s.openBackups == nil {
		return managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return trustedSurfaceRequired("Profile backups are available in Settings.")
	}
	path, err := s.manager.ProfileBackupDirectory(ctx, ref)
	if err != nil {
		return err
	}
	return s.openBackups(path)
}

func (s *ManagerService) EnterSafeMode(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !s.runtimeSurfaceAuthorized(ctx) {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Safe mode is available in Settings or startup.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.host.EnterSafeMode(ctx)
}

func (s *ManagerService) ExitSafeMode(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !s.runtimeSurfaceAuthorized(ctx) {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Safe mode is available in Settings or startup.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.host.ExitSafeMode(ctx)
}

func (h *Host) EnterSafeMode(ctx context.Context) (dshmanager.Snapshot, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	manager, ok := h.deps.Manager.(interface {
		PrepareSafeMode(context.Context) (dshmanager.RunContext, error)
		AbortPreparedSafeMode(context.Context) error
	})
	if !ok {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	target, err := manager.PrepareSafeMode(ctx)
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	snapshot, err := h.applyRunContextLocked(ctx, target, nil, nil, false)
	if err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), h.config.ShutdownTimeout)
		defer cancel()
		err = errors.Join(err, manager.AbortPreparedSafeMode(cleanupCtx))
		snapshot, _ = h.deps.Manager.Snapshot(context.Background())
	}
	return snapshot, err
}

func (h *Host) ExitSafeMode(ctx context.Context) (dshmanager.Snapshot, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	manager, ok := h.deps.Manager.(interface {
		SafeModeReturnTarget(context.Context) (dshmanager.RunContext, error)
	})
	if !ok {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	target, err := manager.SafeModeReturnTarget(ctx)
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	snapshot, err := h.deps.Manager.Snapshot(ctx)
	if err != nil {
		return snapshot, err
	}
	selected := snapshot.Current
	if selected == nil {
		selected = snapshot.Configured
	}
	if selected == nil || selected.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID {
		return snapshot, errors.New("safe mode is not active")
	}
	return h.applyRunContextLocked(ctx, target, nil, nil, false)
}

func SetProfileExportAction(s *ManagerService, choose func(string) (string, error)) {
	s.chooseExport = choose
}

func (s *ManagerService) ExportProfile(ctx context.Context, ref dshmanager.ProfileRef) (string, error) {
	if s == nil || s.manager == nil || s.chooseExport == nil {
		return "", managerUnavailable()
	}
	if !isTrustedWindow(ctx, "settings") {
		return "", trustedSurfaceRequired("Profile export is available in Settings.")
	}
	path, err := s.chooseExport(ref.Name + ".zip")
	if err != nil || path == "" {
		return "", err
	}
	ctx, cancel := managerContext(ctx)
	defer cancel()
	if err := s.manager.ExportProfile(ctx, ref, path); err != nil {
		return "", err
	}
	return filepath.Base(path), nil
}
