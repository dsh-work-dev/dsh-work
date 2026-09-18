package dshmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

// ProfileVersionAdapter owns the DSH-specific dependency manifest boundary.
// Package managers remain responsible for dependency resolution and installation.
type ProfileVersionAdapter interface {
	CheckVersionProfile(context.Context, string) error
	CaptureVersions(context.Context, string) (ProfileVersionInput, error)
	ApplyVersions(context.Context, string, ProfileVersionInput) error
	ResetVersions(context.Context, string) ([]string, error)
	ForceInstall(string, bool) ([]string, error)
	RemoveVersions(string, []string) ([]string, error)
}

type ProfileVersionInput struct {
	Bundles        []string        `json:"bundles"`
	Lock           string          `json:"lock"`
	Workspace      string          `json:"workspace"`
	Plugins        []VersionPlugin `json:"plugins"`
	PackageManager string          `json:"packageManager"`
	Unavailable    string          `json:"unavailable,omitempty"`
}

type VersionPlugin struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Source  string `json:"source"`
	Group   string `json:"group"`
}

type RestorePoint struct {
	ID             string          `json:"id"`
	Kind           string          `json:"kind"`
	Label          string          `json:"label"`
	CreatedAt      string          `json:"createdAt"`
	LastVerifiedAt string          `json:"lastVerifiedAt"`
	Target         RunContext      `json:"target"`
	DSHVersion     string          `json:"dshVersion"`
	NodeVersion    string          `json:"nodeVersion"`
	PackageManager string          `json:"packageManager"`
	Platform       string          `json:"platform"`
	Plugins        []VersionPlugin `json:"plugins"`
	Digest         string          `json:"digest"`
	Lockfile       string          `json:"lockfile,omitempty"`
	Unavailable    string          `json:"unavailable,omitempty"`
}

type storedRestorePoint struct {
	RestorePoint
	Input ProfileVersionInput `json:"input"`
}

type RecoveryOperation struct {
	PointID     string `json:"pointId"`
	Stage       string `json:"stage"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	ResumeCount int    `json:"resumeCount"`
}

// All metadata is committed with the existing atomic manager state writer.
// Inputs are bounded and contain no installed package trees.
type VersionRecoveryState struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Points        []storedRestorePoint `json:"points"`
	LastByProfile map[string]string    `json:"lastByProfile"`
	LastRunning   string               `json:"lastRunning"`
	Pending       *RecoveryOperation   `json:"pending,omitempty"`
}

type RestorePointsView struct {
	Points        []RestorePoint     `json:"points"`
	LastByProfile map[string]string  `json:"lastByProfile"`
	LastRunning   string             `json:"lastRunning"`
	SaveError     string             `json:"saveError,omitempty"`
	Operation     *RecoveryOperation `json:"operation,omitempty"`
	CanSave       bool               `json:"canSave"`
}

type RuntimeForceInstaller interface {
	ForceInstall(context.Context, string, ResolvedNode) (RuntimeInfo, error)
}

type resumedVersionRecoveryKey struct{}

func profilePointKey(ref ProfileRef) string { return ref.DataDirectoryID + "/" + ref.Name }
func cloneVersionRecovery(s *VersionRecoveryState) *VersionRecoveryState {
	if s == nil {
		return &VersionRecoveryState{SchemaVersion: 1, LastByProfile: map[string]string{}, Points: []storedRestorePoint{}}
	}
	data, _ := json.Marshal(s)
	var out VersionRecoveryState
	_ = json.Unmarshal(data, &out)
	if out.LastByProfile == nil {
		out.LastByProfile = map[string]string{}
	}
	return &out
}
func (s *VersionRecoveryState) point(id string) *storedRestorePoint {
	if s != nil {
		for i := range s.Points {
			if s.Points[i].ID == id {
				return &s.Points[i]
			}
		}
	}
	return nil
}
func (m *Manager) versionViewLocked() *RestorePointsView {
	s := cloneVersionRecovery(m.versionRecovery)
	v := &RestorePointsView{Points: []RestorePoint{}, LastByProfile: s.LastByProfile, LastRunning: s.LastRunning, SaveError: m.restoreSaveError, Operation: s.Pending, CanSave: m.current != nil && m.current.Profile.DataDirectoryID != SafeModeDataDirectoryID && m.verifiedPoint != nil && !m.switching}
	for _, p := range s.Points {
		v.Points = append(v.Points, p.RestorePoint)
	}
	sort.Slice(v.Points, func(i, j int) bool { return v.Points[i].LastVerifiedAt > v.Points[j].LastVerifiedAt })
	return v
}

// CaptureLaunchVersions runs before starting the Worker; errors affect the
// recorder, not the ability to start an otherwise usable environment.
func (m *Manager) CaptureLaunchVersions(ctx context.Context, launch ResolvedLaunch) ResolvedLaunch {
	if launch.Target.Profile.DataDirectoryID == SafeModeDataDirectoryID {
		return launch
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		launch.VersionError = err.Error()
		return launch
	}
	defer release()
	p, err := m.readVersionPoint(ctx, launch)
	if err != nil {
		launch.VersionError = err.Error()
		launch.VersionUninitialized = errors.Is(err, os.ErrNotExist)
	} else {
		launch.VersionSeed = p
	}
	return launch
}
func (m *Manager) readVersionPoint(ctx context.Context, launch ResolvedLaunch) (*storedRestorePoint, error) {
	a, ok := m.config.PluginCommands.(ProfileVersionAdapter)
	if !ok {
		return nil, errors.New("version snapshot adapter is unavailable")
	}
	input, err := a.CaptureVersions(ctx, filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name))
	if err != nil {
		return nil, err
	}
	p := &storedRestorePoint{RestorePoint: RestorePoint{Target: launch.Target, DSHVersion: launch.Runtime.Version, NodeVersion: launch.Node.Version, PackageManager: input.PackageManager, Platform: runtime.GOOS + "/" + runtime.GOARCH, Plugins: input.Plugins, Unavailable: input.Unavailable}, Input: input}
	if input.Lock != "" {
		p.Lockfile = "pnpm-lock.yaml"
	}
	if !validRuntimeVersion(p.DSHVersion) {
		p.Unavailable = "DSH has no exact installable version"
	}
	return finishVersionPoint(p), nil
}
func finishVersionPoint(p *storedRestorePoint) *storedRestorePoint {
	input := p.Input
	data, _ := json.Marshal(struct {
		DSH, Node, PM, Platform string
		Input                   ProfileVersionInput
	}{p.DSHVersion, p.NodeVersion, p.PackageManager, p.Platform, input})
	digest := sha256.Sum256(data)
	p.Digest = hex.EncodeToString(digest[:])
	return p
}

// commitVersionHealthy replaces the old file-tree snapshot path. A failed
// recorder never tears down a Worker that passed readiness.
func (m *Manager) commitVersionHealthy(ctx context.Context, launch ResolvedLaunch) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	m.mu.RLock()
	if m.current != nil && *m.current != launch.Target {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorManagerOperationBusy, "another Run context is current", "stop the current Worker before committing a different context")
	}
	state := m.stateLocked()
	m.mu.RUnlock()
	s := cloneVersionRecovery(state.VersionRecovery)
	// A newly initialized built-in profile has no third-party dependencies to
	// drift while DSH creates its initial manifest on first boot.
	if launch.VersionSeed == nil && launch.VersionUninitialized {
		if p, e := m.readVersionPoint(ctx, launch); e == nil && len(p.Plugins) == 0 {
			launch.VersionSeed = p
			launch.VersionError = ""
		}
	}
	saveErr := launch.VersionError
	var verified *storedRestorePoint
	if launch.Target.Profile.DataDirectoryID != SafeModeDataDirectoryID {
		if launch.VersionSeed != nil {
			actual, e := m.readVersionPoint(ctx, launch)
			if e != nil {
				saveErr = e.Error()
			} else if actual.Digest != launch.VersionSeed.Digest {
				saveErr = "Dependencies changed during startup; restart to record this environment"
			} else {
				now := time.Now().UTC().Format(time.RFC3339Nano)
				verified = actual
				id := s.LastByProfile[profilePointKey(launch.Target.Profile)]
				if s.Pending != nil && s.Pending.Status == "running" {
					id = s.Pending.PointID
				}
				existing := s.point(id)
				if existing != nil && existing.Digest == actual.Digest {
					existing.LastVerifiedAt = now
					existing.Target = launch.Target
					verified = existing
				} else {
					actual.ID = lifecycle.NewCorrelationID()
					actual.Kind = "automatic"
					actual.Label = ""
					actual.CreatedAt = now
					actual.LastVerifiedAt = now
					s.Points = append(s.Points, *actual)
				}
				s.LastByProfile[profilePointKey(launch.Target.Profile)] = verified.ID
				s.LastRunning = verified.ID
			}
		} else if saveErr == "" {
			saveErr = "No verified startup version record is available"
		}
		if s.Pending != nil && verified != nil {
			s.Pending.Status = "completed"
			s.Pending.Stage = "ready"
			s.Pending.Error = ""
		}
		if verified != nil {
			copy := *verified
			verified = &copy
		}
		pruneVersionPoints(s)
	}
	state.Configured = cloneRunContext(&launch.Target)
	state.VersionRecovery = s
	if launch.Target.Profile.DataDirectoryID != SafeModeDataDirectoryID {
		state.SafeMode = nil
		discardInactiveSafeMode(&state)
	}
	if state.LastSwitchAttempt != nil && state.LastSwitchAttempt.Target == launch.Target {
		state.LastSwitchAttempt = nil
	}
	if err = m.store.Save(ctx, m.config.StatePath, state); err != nil {
		saveErr = "The version record could not be saved"
		verified = nil
	}
	m.mu.Lock()
	m.current = cloneRunContext(&launch.Target)
	m.configured = cloneRunContext(&launch.Target)
	m.restoreSaveError = saveErr
	m.verifiedPoint = verified
	if err == nil {
		m.versionRecovery = s
		m.safeMode = cloneSafeMode(state.SafeMode)
		m.lastSwitchAttempt = cloneSwitchAttempt(state.LastSwitchAttempt)
		m.config.DataDirectories = cloneDataDirectories(state.DataDirectories)
	}
	if launch.Target.Profile.DataDirectoryID != SafeModeDataDirectoryID && verified != nil {
		m.knownGood = cloneRunContext(&launch.Target)
	}
	m.mu.Unlock()
	if err == nil {
		removeSafeModeSessions(m.config.StatePath, state.DataDirectories)
	}
	return m.Snapshot(context.Background())
}
func pruneVersionPoints(s *VersionRecoveryState) {
	counts := map[string]int{}
	kept := []storedRestorePoint{}
	sort.SliceStable(s.Points, func(i, j int) bool { return s.Points[i].LastVerifiedAt > s.Points[j].LastVerifiedAt })
	for _, p := range s.Points {
		k := profilePointKey(p.Target.Profile)
		keep := p.Kind == "manual" || counts[k] < 2 || p.ID == s.LastRunning || p.ID == s.LastByProfile[k] || s.Pending != nil && s.Pending.Status != "completed" && p.ID == s.Pending.PointID
		if p.Kind == "automatic" {
			counts[k]++
		}
		if keep {
			kept = append(kept, p)
		}
	}
	s.Points = kept
	if s.Pending != nil && s.Pending.Status == "completed" && s.point(s.Pending.PointID) == nil {
		s.Pending = nil
	}
}
func (m *Manager) saveVersionState(ctx context.Context, s *VersionRecoveryState) error {
	m.mu.RLock()
	state := m.stateLocked()
	m.mu.RUnlock()
	state.VersionRecovery = s
	if err := m.store.Save(ctx, m.config.StatePath, state); err != nil {
		return err
	}
	m.mu.Lock()
	m.versionRecovery = s
	m.mu.Unlock()
	return nil
}
func (m *Manager) SaveRestorePoint(ctx context.Context, label string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err = m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	p := m.verifiedPoint
	current := cloneRunContext(m.current)
	s := cloneVersionRecovery(m.versionRecovery)
	m.mu.RUnlock()
	if p == nil || current == nil || current.Profile.DataDirectoryID == SafeModeDataDirectoryID || *current != p.Target {
		return Snapshot{}, errors.New("start the normal environment before saving a snapshot")
	}
	launch, err := m.ResolveLaunch(ctx, LaunchRequest{RuntimeID: current.RuntimeID, Node: current.Node, Profile: current.Profile})
	if err != nil {
		return Snapshot{}, err
	}
	actual, err := m.readVersionPoint(ctx, launch)
	if err != nil {
		return Snapshot{}, err
	}
	if actual.Digest != p.Digest {
		return Snapshot{}, errors.New("apply changes and restart before saving a snapshot")
	}
	if len(s.Points) >= 128 {
		return Snapshot{}, errors.New("delete an older manual snapshot first")
	}
	actual.ID = lifecycle.NewCorrelationID()
	actual.Kind = "manual"
	actual.Label = strings.TrimSpace(label)
	if len(actual.Label) > 120 {
		return Snapshot{}, errors.New("snapshot name is too long")
	}
	actual.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	actual.LastVerifiedAt = p.LastVerifiedAt
	s.Points = append(s.Points, *actual)
	if err = m.saveVersionState(ctx, s); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}
func (m *Manager) EditRestorePoint(ctx context.Context, id, label string, remove bool) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err = m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	s := cloneVersionRecovery(m.versionRecovery)
	m.mu.RUnlock()
	p := s.point(id)
	if p == nil || p.Kind != "manual" {
		return Snapshot{}, errors.New("manual snapshot not found")
	}
	if remove {
		if s.LastRunning == id || s.LastByProfile[profilePointKey(p.Target.Profile)] == id || s.Pending != nil && s.Pending.PointID == id && s.Pending.Status != "completed" {
			return Snapshot{}, errors.New("snapshot is in use")
		}
		for i := range s.Points {
			if s.Points[i].ID == id {
				s.Points = append(s.Points[:i], s.Points[i+1:]...)
				if s.Pending != nil && s.Pending.PointID == id {
					s.Pending = nil
				}
				break
			}
		}
	} else {
		if len(label) > 120 {
			return Snapshot{}, errors.New("snapshot name is too long")
		}
		p.Label = strings.TrimSpace(label)
	}
	if err = m.saveVersionState(ctx, s); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}
func (m *Manager) RecoveryPointID(target RunContext, startup bool) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.versionRecovery
	if s == nil {
		return ""
	}
	if startup {
		return s.LastByProfile[profilePointKey(target.Profile)]
	}
	return s.LastRunning
}
func (m *Manager) PreviewRestorePoint(ctx context.Context, id string) (RestorePoint, error) {
	m.mu.RLock()
	s := cloneVersionRecovery(m.versionRecovery)
	config := m.configSnapshotLocked()
	m.mu.RUnlock()
	p := s.point(id)
	if p == nil {
		return RestorePoint{}, errors.New("snapshot not found")
	}
	if validateRunContext(p.Target) != nil || !validRuntimeVersion(p.DSHVersion) {
		return p.RestorePoint, errors.New("invalid snapshot target")
	}
	copy := *p
	if finishVersionPoint(&copy).Digest != p.Digest {
		return p.RestorePoint, errors.New("snapshot contents failed integrity verification")
	}
	if _, ok := config.PluginCommands.(ProfileVersionAdapter); !ok || config.CommandRunner == nil {
		return p.RestorePoint, errors.New("snapshot installation adapter is unavailable")
	}
	if p.Unavailable != "" {
		return p.RestorePoint, errors.New(p.Unavailable)
	}
	if s.SchemaVersion != 1 || p.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return p.RestorePoint, errors.New("snapshot format or platform is incompatible")
	}
	if _, ok := config.RuntimeInstaller.(RuntimeForceInstaller); !ok {
		return p.RestorePoint, errors.New("forced DSH installation is unavailable on this platform")
	}
	node, err := resolveNode(ctx, config.NodeResolver, p.Target.Node, config.Nodes)
	if err != nil {
		return p.RestorePoint, err
	}
	if node.Version != p.NodeVersion {
		return p.RestorePoint, errors.New("select the Node version recorded in the snapshot before restoring")
	}
	found := false
	for _, d := range config.DataDirectories {
		if d.ID == p.Target.Profile.DataDirectoryID {
			if err := config.PluginCommands.(ProfileVersionAdapter).CheckVersionProfile(ctx, filepath.Join(d.Path, "profiles", p.Target.Profile.Name)); err != nil {
				return p.RestorePoint, errors.New("snapshot profile manifest is unavailable")
			}
			found = true
		}
	}
	if !found {
		return p.RestorePoint, errors.New("snapshot data directory is unavailable")
	}
	return p.RestorePoint, nil
}
func (m *Manager) RecoverVersionPoint(ctx context.Context, id string) (launch ResolvedLaunch, resultErr error) {
	if _, err := m.PreviewRestorePoint(ctx, id); err != nil {
		return launch, err
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return launch, err
	}
	defer release()
	m.mu.RLock()
	live := m.current != nil
	s := cloneVersionRecovery(m.versionRecovery)
	config := m.configSnapshotLocked()
	m.mu.RUnlock()
	if live {
		return launch, errors.New("stop the Worker before restoring")
	}
	p := s.point(id)
	if p == nil {
		return launch, errors.New("snapshot not found")
	}
	count := 0
	if resumed, _ := ctx.Value(resumedVersionRecoveryKey{}).(bool); resumed && s.Pending != nil && s.Pending.PointID == id {
		count = s.Pending.ResumeCount
	}
	s.Pending = &RecoveryOperation{PointID: id, Stage: "install-dsh", Status: "running", ResumeCount: count}
	if err = m.saveVersionState(ctx, s); err != nil {
		return launch, err
	}
	defer func() {
		if resultErr != nil {
			m.setRecoveryStage(context.WithoutCancel(ctx), "failed", "failed", resultErr.Error())
		}
	}()
	node, err := resolveNode(ctx, config.NodeResolver, p.Target.Node, config.Nodes)
	if err != nil {
		return launch, err
	}
	installed, err := config.RuntimeInstaller.(RuntimeForceInstaller).ForceInstall(ctx, p.DSHVersion, node)
	if err != nil {
		return launch, err
	}
	if installed.Version != p.DSHVersion {
		return launch, errors.New("installed DSH version does not match the snapshot")
	}
	m.mu.RLock()
	state := m.stateLocked()
	m.mu.RUnlock()
	state.Runtimes = mergeRuntimes(state.Runtimes, []RuntimeInfo{installed})
	if err = m.store.Save(ctx, m.config.StatePath, state); err != nil {
		return launch, err
	}
	m.mu.Lock()
	m.config.Runtimes = state.Runtimes
	m.mu.Unlock()
	target := p.Target
	target.RuntimeID = installed.ID
	for _, d := range config.DataDirectories {
		if d.ID == target.Profile.DataDirectoryID {
			launch.DataDirectory = d
		}
	}
	launch.Target = target
	launch.Runtime = installed
	launch.Node = node
	profile := filepath.Join(launch.DataDirectory.Path, "profiles", target.Profile.Name)
	adapter := config.PluginCommands.(ProfileVersionAdapter)
	env := cloneEnvironment(node.ChildEnvironment)
	env["DSH_HOME"] = launch.DataDirectory.Path
	if err = m.setRecoveryStage(ctx, "install-plugins", "running", ""); err != nil {
		return launch, err
	}
	// Some pnpm hoisted versions skip same-version damaged files under --force.
	// Reconcile removals through pnpm first; the Host never deletes node_modules.
	names, err := adapter.ResetVersions(ctx, profile)
	if err != nil {
		return launch, err
	}
	if len(names) > 0 {
		args, e := adapter.RemoveVersions(target.Profile.Name, names)
		if e != nil {
			return launch, e
		}
		if _, e = config.CommandRunner.Run(ctx, installed.Path, args, env, launch.DataDirectory.Path); e != nil {
			return launch, fmt.Errorf("reset plugin dependencies: %w", e)
		}
	}
	if err = adapter.ApplyVersions(ctx, profile, p.Input); err != nil {
		return launch, err
	}
	// A pristine profile without plugins or a lock has nothing to install.
	// Running pnpm here would introduce a new lock absent from the record.
	if len(p.Input.Plugins) > 0 || p.Input.Lock != "" {
		args, err := adapter.ForceInstall(target.Profile.Name, p.Input.Lock != "")
		if err != nil {
			return launch, err
		}
		if _, err = config.CommandRunner.Run(ctx, installed.Path, args, env, launch.DataDirectory.Path); err != nil {
			return launch, fmt.Errorf("install snapshot dependencies: %w", err)
		}
	}
	actual, err := m.readVersionPoint(ctx, launch)
	if err != nil {
		return launch, err
	}
	if actual.Digest != p.Digest {
		return launch, errors.New("installed dependency versions or lockfile do not match the snapshot")
	}
	if err = m.setRecoveryStage(ctx, "starting", "running", ""); err != nil {
		return launch, err
	}
	return launch, nil
}
func (m *Manager) setRecoveryStage(ctx context.Context, stage, status, detail string) error {
	m.mu.RLock()
	s := cloneVersionRecovery(m.versionRecovery)
	m.mu.RUnlock()
	if s.Pending == nil {
		return nil
	}
	if len(detail) > 1000 {
		detail = detail[:1000]
	}
	s.Pending.Stage = stage
	s.Pending.Status = status
	s.Pending.Error = detail
	return m.saveVersionState(ctx, s)
}
func (m *Manager) FailVersionRecovery(ctx context.Context) {
	release, e := m.acquireOperation(ctx)
	if e != nil {
		return
	}
	defer release()
	// Installation already recorded its specific error. Do not replace it with
	// a startup message when Host completes the failed recovery transaction.
	if operation := m.PendingVersionRecovery(); operation != nil && operation.Status == "failed" {
		return
	}
	_ = m.setRecoveryStage(ctx, "failed", "failed", "The restored environment could not start")
}
func (m *Manager) PendingVersionRecovery() *RecoveryOperation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cloneVersionRecovery(m.versionRecovery).Pending
}
func (m *Manager) ResumeVersionRecovery(ctx context.Context, automatic bool) (*ResolvedLaunch, error) {
	m.mu.RLock()
	safe := m.configured != nil && m.configured.Profile.DataDirectoryID == SafeModeDataDirectoryID
	m.mu.RUnlock()
	if safe {
		return nil, nil
	}
	p := m.PendingVersionRecovery()
	if p == nil || p.Status == "completed" {
		return nil, nil
	}
	if !automatic || p.ResumeCount >= 1 || p.Status == "failed" {
		return nil, errors.New("the previous recovery is unfinished; choose a snapshot or enter safe mode")
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	s := cloneVersionRecovery(m.versionRecovery)
	m.mu.RUnlock()
	if s.Pending == nil || s.Pending.Status == "completed" {
		release()
		return nil, nil
	}
	s.Pending.ResumeCount++
	err = m.saveVersionState(ctx, s)
	release()
	if err != nil {
		return nil, err
	}
	launch, err := m.RecoverVersionPoint(context.WithValue(ctx, resumedVersionRecoveryKey{}, true), p.PointID)
	return &launch, err
}
