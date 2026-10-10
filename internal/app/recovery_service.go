package app

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

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
	return s.EnterSafeModeWithOptions(ctx, dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeDiagnostic})
}

func (s *ManagerService) EnterSafeModeWithOptions(ctx context.Context, request dshmanager.SafeModeRequest) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !s.runtimeSurfaceAuthorized(ctx) {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Safe mode is available in Settings or startup.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.host.EnterSafeModeWithOptions(ctx, request)
}

func (s *ManagerService) TrySafeModeTarget(ctx context.Context) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !s.runtimeSurfaceAuthorized(ctx) {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Safe mode recovery is available in Settings or startup.")
	}
	ctx, cancel := contextWithTimeout(ctx, runtimeOperationTimeout)
	defer cancel()
	return s.host.TrySafeModeTarget(ctx)
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
	return h.EnterSafeModeWithOptions(ctx, dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeDiagnostic})
}

func (h *Host) EnterSafeModeWithOptions(ctx context.Context, request dshmanager.SafeModeRequest) (dshmanager.Snapshot, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	manager, ok := h.deps.Manager.(interface {
		PrepareSafeMode(context.Context, ...dshmanager.SafeModeRequest) (dshmanager.RunContext, error)
		AbortPreparedSafeMode(context.Context) error
	})
	if !ok {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if request.Mode == "" {
		request.Mode = dshmanager.SafeModeDiagnostic
	}
	if request.FaultTarget == nil {
		snapshot, err := h.deps.Manager.Snapshot(ctx)
		if err != nil {
			return dshmanager.Snapshot{}, err
		}
		switch {
		case snapshot.Current != nil && snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID:
			request.FaultTarget = snapshot.Current
		case snapshot.SafeMode != nil:
			fault := snapshot.SafeMode.FaultTarget
			request.FaultTarget = &fault
		case snapshot.LastSwitchAttempt != nil:
			request.FaultTarget = &snapshot.LastSwitchAttempt.Target
		case snapshot.Configured != nil:
			request.FaultTarget = snapshot.Configured
		}
	}
	target, err := manager.PrepareSafeMode(ctx, request)
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

func (h *Host) TrySafeModeTarget(ctx context.Context) (dshmanager.Snapshot, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	snapshot, err := h.deps.Manager.Snapshot(ctx)
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	if snapshot.SafeMode == nil || (snapshot.Current == nil && (snapshot.Configured == nil || snapshot.Configured.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID)) {
		return snapshot, errors.New("safe mode is not active")
	}
	return h.applyRunContextLocked(ctx, snapshot.SafeMode.FaultTarget, nil, nil, false)
}

func (h *Host) RepairSafeModePlugin(ctx context.Context, packageName, operation string) (dshmanager.Snapshot, error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	if operation != "disable" && operation != "remove" {
		return dshmanager.Snapshot{}, errors.New("unsupported safe mode plugin operation")
	}
	snapshot, err := h.deps.Manager.Snapshot(ctx)
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	if snapshot.SafeMode == nil || snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || h.Status().State != lifecycle.StateReady {
		return snapshot, errors.New("safe mode must be running before its fault target can be repaired")
	}
	manager, ok := h.deps.Manager.(PluginFaultManager)
	if !ok {
		return snapshot, errors.New("safe mode plugin repair is unavailable")
	}
	target := snapshot.SafeMode.FaultTarget
	launch, err := h.deps.Manager.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		return snapshot, err
	}
	installed, err := manager.ProfilePluginPackages(ctx, launch)
	if err != nil {
		return snapshot, err
	}
	if !slices.Contains(installed, packageName) {
		return snapshot, errors.New("the plugin is not installed in the safe mode fault target")
	}
	var repairErr error
	next, switchErr := h.applyRunContextLocked(ctx, *snapshot.Current, func(operationCtx context.Context, _ dshmanager.ResolvedLaunch) error {
		if operation == "disable" {
			repairErr = manager.DisableFaultPlugin(operationCtx, launch, packageName)
		} else {
			_, repairErr = manager.ApplyPlugin(operationCtx, launch, packageName, "remove")
		}
		// Keep the safe Worker available even when the package operation fails.
		return nil
	}, nil, false)
	if repairErr != nil {
		return next, errors.Join(repairErr, switchErr)
	}
	return next, switchErr
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
