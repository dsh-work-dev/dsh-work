package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
)

const SafeModeDataDirectoryID = "dsh-work-safe-mode"

type SafeModeState struct {
	Target   RunContext `json:"target"`
	ReturnTo RunContext `json:"returnTo"`
}

func cloneSafeMode(state *SafeModeState) *SafeModeState {
	if state == nil {
		return nil
	}
	copy := *state
	return &copy
}

// PrepareSafeMode reserves a fresh home and persists the return target before
// Host starts the normal serialized switch. DSH initializes its built-in web
// profile on launch; nothing is copied from the failed profile or home patches.
func (m *Manager) PrepareSafeMode(ctx context.Context) (result RunContext, resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorManagerStateInvalid, "Safe mode could not be prepared.")
	}()
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return RunContext{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return RunContext{}, err
	}
	m.mu.RLock()
	state := m.stateLocked()
	target := cloneRunContext(m.current)
	if target == nil {
		target = cloneRunContext(m.configured)
	}
	statePath := m.config.StatePath
	m.mu.RUnlock()
	if target == nil {
		return RunContext{}, errors.New("select a local Node and DSH runtime first")
	}
	if target.Profile.DataDirectoryID == SafeModeDataDirectoryID && state.SafeMode != nil {
		return state.SafeMode.Target, nil
	}
	root := filepath.Join(filepath.Dir(statePath), "safe-mode")
	if err := os.MkdirAll(root, 0700); err != nil {
		return RunContext{}, err
	}
	directory, err := os.MkdirTemp(root, "session-")
	if err != nil {
		return RunContext{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(directory)
		}
	}()
	if err := os.MkdirAll(filepath.Join(directory, "profiles", "web"), 0700); err != nil {
		return RunContext{}, err
	}
	entry := DataDirectoryInfo{ID: SafeModeDataDirectoryID, Name: "Safe mode", Path: directory, Ownership: DataDirectoryOwnershipDSHWork}
	updated := false
	for i := range state.DataDirectories {
		if state.DataDirectories[i].ID == entry.ID {
			state.DataDirectories[i] = entry
			updated = true
		}
	}
	if !updated {
		state.DataDirectories = append(state.DataDirectories, entry)
	}
	rescue := *target
	rescue.Profile = ProfileRef{DataDirectoryID: entry.ID, Name: "web"}
	state.SafeMode = &SafeModeState{Target: rescue, ReturnTo: *target}
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return RunContext{}, err
	}
	m.mu.Lock()
	m.config.DataDirectories = cloneDataDirectories(state.DataDirectories)
	m.safeMode = cloneSafeMode(state.SafeMode)
	m.mu.Unlock()
	keep = true
	return rescue, nil
}

// AbortPreparedSafeMode discards an unused rescue reservation after a failed
// Host switch. A running rescue or a pending Worker cleanup retains ownership.
func (m *Manager) AbortPreparedSafeMode(ctx context.Context) (resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorManagerStateInvalid, "Safe mode cleanup could not be completed.")
	}()
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return err
	}
	m.mu.RLock()
	state := m.stateLocked()
	active := m.current != nil && m.current.Profile.DataDirectoryID == SafeModeDataDirectoryID
	statePath := m.config.StatePath
	m.mu.RUnlock()
	if active {
		return nil
	}
	if state.Configured != nil && state.Configured.Profile.DataDirectoryID == SafeModeDataDirectoryID {
		if state.SafeMode == nil {
			return nil
		}
		state.Configured = cloneRunContext(&state.SafeMode.ReturnTo)
	}
	var discarded string
	kept := state.DataDirectories[:0]
	for _, directory := range state.DataDirectories {
		if directory.ID == SafeModeDataDirectoryID {
			discarded = directory.Path
		} else {
			kept = append(kept, directory)
		}
	}
	state.DataDirectories = kept
	state.SafeMode = nil
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return err
	}
	m.mu.Lock()
	m.config.DataDirectories = cloneDataDirectories(state.DataDirectories)
	m.configured = cloneRunContext(state.Configured)
	m.safeMode = nil
	m.mu.Unlock()
	if discarded == "" {
		return nil
	}
	// Only remove an app-created session directly beneath this manager's root.
	root := filepath.Join(filepath.Dir(statePath), "safe-mode")
	if filepath.Dir(discarded) != root || !strings.HasPrefix(filepath.Base(discarded), "session-") {
		return errors.New("invalid safe mode cleanup path")
	}
	return os.RemoveAll(discarded)
}

func (m *Manager) SafeModeReturnTarget(ctx context.Context) (result RunContext, resultErr error) {
	defer func() {
		resultErr = recoveryFailure(resultErr, lifecycle.ErrorManagerStateInvalid, "The previous environment is unavailable.")
	}()
	if err := contextError(ctx); err != nil {
		return RunContext{}, err
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.safeMode == nil {
		return RunContext{}, errors.New("no previous environment is available")
	}
	return m.safeMode.ReturnTo, nil
}

// Called only while the manager mutex is held.
func (m *Manager) safeModeReturnLocked() *RunContext {
	if m.safeMode == nil {
		return nil
	}
	return &m.safeMode.ReturnTo
}
