package app

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/dshmanager"
)

type versionRecoveryManager interface {
	RecoveryPointID(dshmanager.RunContext, bool) string
	PreviewRestorePoint(context.Context, string) (dshmanager.RestorePoint, error)
	RecoverVersionPoint(context.Context, string) (dshmanager.ResolvedLaunch, error)
	FailVersionRecovery(context.Context)
	PendingVersionRecovery() *dshmanager.RecoveryOperation
}
type restorePointKey struct{}

func (h *Host) RestoreVersionPoint(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	m, ok := h.deps.Manager.(versionRecoveryManager)
	if !ok {
		return dshmanager.Snapshot{}, errors.New("version snapshots are unavailable")
	}
	p, err := m.PreviewRestorePoint(ctx, id)
	if err != nil {
		return dshmanager.Snapshot{}, err
	}
	return h.applyRunContext(context.WithValue(ctx, restorePointKey{}, id), p.Target, nil, nil, true)
}

func (s *ManagerService) SaveRestorePoint(ctx context.Context, label string) (dshmanager.Snapshot, error) {
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Snapshots can be saved in Settings.")
	}
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	return s.manager.SaveRestorePoint(ctx, label)
}
func (s *ManagerService) RenameRestorePoint(ctx context.Context, id, label string) (dshmanager.Snapshot, error) {
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Snapshots can be edited in Settings.")
	}
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	return s.manager.EditRestorePoint(ctx, id, label, false)
}
func (s *ManagerService) DeleteRestorePoint(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if !isTrustedWindow(ctx, "settings") {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Snapshots can be edited in Settings.")
	}
	if s == nil || s.manager == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	return s.manager.EditRestorePoint(ctx, id, "", true)
}
func (s *ManagerService) PreviewRestorePoint(ctx context.Context, id string) (dshmanager.RestorePoint, error) {
	if s == nil || s.manager == nil {
		return dshmanager.RestorePoint{}, managerUnavailable()
	}
	if !s.runtimeSurfaceAuthorized(ctx) {
		return dshmanager.RestorePoint{}, trustedSurfaceRequired("Snapshot recovery is available in Settings or startup.")
	}
	return s.manager.PreviewRestorePoint(ctx, id)
}

// Cancellation is per service, with the Worker stop boundary still owned by Host.
type restoreCancellation struct {
	mu     sync.Mutex
	cancel context.CancelFunc
}

func (s *ManagerService) RestorePoint(ctx context.Context, id string) (dshmanager.Snapshot, error) {
	if s == nil || s.manager == nil || s.host == nil {
		return dshmanager.Snapshot{}, managerUnavailable()
	}
	if !s.runtimeSurfaceAuthorized(ctx) {
		return dshmanager.Snapshot{}, trustedSurfaceRequired("Snapshot recovery is available in Settings or startup.")
	}
	s.restoreCancel.mu.Lock()
	if s.restoreCancel.cancel != nil {
		s.restoreCancel.mu.Unlock()
		return dshmanager.Snapshot{}, errors.New("a recovery is already running")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	s.restoreCancel.cancel = cancel
	s.restoreCancel.mu.Unlock()
	defer func() { cancel(); s.restoreCancel.mu.Lock(); s.restoreCancel.cancel = nil; s.restoreCancel.mu.Unlock() }()
	return s.host.RestoreVersionPoint(ctx, id)
}
func (s *ManagerService) CancelRestore(ctx context.Context) error {
	if s == nil || !s.runtimeSurfaceAuthorized(ctx) {
		return trustedSurfaceRequired("Recovery cancellation is available in Settings or startup.")
	}
	if s.host != nil {
		s.host.cancelVersionOperation()
	}
	s.restoreCancel.mu.Lock()
	defer s.restoreCancel.mu.Unlock()
	if s.restoreCancel.cancel != nil {
		s.restoreCancel.cancel()
	}
	return nil
}
