package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/lifecycle"
)

const (
	defaultPluginOfficialRegistry = "https://registry.npmjs.org/"
	defaultPluginMirrorRegistry   = "https://registry.npmmirror.com/"
)

// Config supplies discovered DSH data directories and runtimes to the
// manager. Workspace context is intentionally absent: it belongs to DSH's
// session surface and is resolved per Worker generation.
type Config struct {
	StatePath              string
	DataDirectories        []DataDirectoryInfo
	Runtimes               []RuntimeInfo
	DSHReleases            []DSHReleaseInfo
	Nodes                  []NodeInstallationInfo
	DefaultRunContext      RunContext
	CommandRunner          CommandRunner
	PluginCommands         PluginCommandBuilder
	RuntimeInstaller       RuntimeInstaller
	NodeCatalog            NodeReleaseCatalog
	NodeInstaller          NodeInstaller
	NodeResolver           NodeResolver
	DSHCatalog             DSHReleaseCatalog
	RuntimeVerifier        RuntimeVerifier
	StateStore             StateStore
	ProfileCatalog         ProfileCatalog
	ProfileReader          ProfileReader
	ThemeReader            ThemeReader
	PluginOfficialRegistry string
	PluginMirrorRegistry   string
}

// CommandRunner is the narrow seam for invoking the selected DSH public CLI.
// The concrete Windows .cmd handling lives in internal/platform/windows; the
// manager only supplies an executable, safe argument vector, environment and
// profile working directory.
type CommandRunner interface {
	Run(context.Context, string, []string, map[string]string, string) (CommandResult, error)
}

type CommandResult struct {
	Stdout string
	Stderr string
}

type RuntimeInstaller interface {
	Install(context.Context, string) (RuntimeInfo, error)
}

// NodeReleaseCatalog resolves the official latest stable exact release only
// when an explicit caller asks. Snapshot and startup never cross this seam.
type NodeReleaseCatalog interface {
	RefreshLatest(context.Context) (NodeReleaseInfo, error)
}

// DSHReleaseCatalog reads the public package view only for an explicit
// refresh action.
type DSHReleaseCatalog interface {
	Refresh(context.Context) ([]DSHReleaseInfo, error)
}

// NodeInstaller materializes one already-resolved exact Node release without
// changing the selected Run context.
type NodeInstaller interface {
	Install(context.Context, NodeReleaseInfo, RuntimeInstallObserver) (NodeInstallationInfo, error)
	Remove(context.Context, NodeInstallationInfo) error
}

// NodeResolver converts the persisted selection into the exact child-process
// environment used for one launch. It does not mutate process-wide PATH.
type NodeResolver interface {
	Resolve(context.Context, NodeSelection, []NodeInstallationInfo) (ResolvedNode, error)
}

// RuntimeInstallerWithProgress is the optional acquisition seam used by the
// trusted UI. Keeping Install on the base interface preserves the explicit
// offline/catalog test seam and lets older platform adapters remain usable.
type RuntimeInstallerWithProgress interface {
	RuntimeInstaller
	InstallWithProgress(context.Context, string, RuntimeInstallObserver) (RuntimeInfo, error)
}

// RuntimeInstallerWithNodes lets the platform installer use the immutable
// managed Node catalog when the system toolchain is absent. It may not acquire
// another Node as a side effect of installing DSH.
type RuntimeInstallerWithNodes interface {
	InstallWithNodes(context.Context, string, []NodeInstallationInfo, RuntimeInstallObserver) (RuntimeInfo, error)
}

// RuntimeInstallerNodeRequirement lets the platform report that an explicit
// DSH install needs a managed Node acquisition before any package-manager
// attempt begins. Manager owns that acquisition and catalog registration.
type RuntimeInstallerNodeRequirement interface {
	RequiresNodeAcquisition(context.Context, []NodeInstallationInfo) (bool, error)
}

// RuntimeDiscarder lets the manager quarantine a candidate that passed the
// installer boundary but failed the DSH adapter verification before catalog
// registration. It must only remove the candidate it just created.
type RuntimeDiscarder interface {
	Discard(context.Context, RuntimeInfo) error
}

// RuntimeFinalizer lets an installer release a preserved previous runtime
// only after the candidate has been registered successfully. This keeps a
// same-version replacement recoverable if the manager verifier or persistence
// boundary rejects it.
type RuntimeFinalizer interface {
	Finalize(context.Context, RuntimeInfo) error
}

// RuntimeRemover deletes a removed runtime's managed installation. An
// installation that is already absent is not an error.
type RuntimeRemover interface {
	Remove(context.Context, RuntimeInfo) error
}

// RuntimeVerifier is implemented by the selected DSH adapter. The manager
// owns catalog identity and presence checks; only the adapter can verify that
// an executable speaks the DSH contract expected by this dsh-work build.
type RuntimeVerifier interface {
	Verify(context.Context, string, string) error
}

// RuntimeVerifierWithEnvironment is the optional verifier boundary for a
// managed runtime whose launcher needs a child-only toolchain environment.
// Implementations must not mutate the parent process environment.
type RuntimeVerifierWithEnvironment interface {
	VerifyWithEnvironment(context.Context, string, string, map[string]string) error
}

// RuntimeProfileVerifier is an optional DSH-adapter seam for compatibility
// checks that depend on both the selected runtime and profile. The manager
// still validates the catalog identities itself; the adapter owns any
// version-specific pair rules.
type RuntimeProfileVerifier interface {
	VerifyProfile(context.Context, string, string, string, string) error
}

// PluginCommandBuilder keeps DSH CLI grammar behind the DSH adapter. The
// manager supplies an explicit ProfileRef and operation intent, but does not
// construct version-specific command lines itself.
type PluginCommandBuilder interface {
	Install(string, string) ([]string, error)
	InstallAt(string, string, string) ([]string, error)
	Prepare(string) ([]string, error)
	Remove(string, string) ([]string, error)
	List(string) ([]string, error)
	Outdated(string) ([]string, error)
	Update(string, string, string) ([]string, error)
}

// Manager owns the configured/current Run context state and performs
// cross-platform validation. It has no Wails or operating-system-specific
// process logic.
type Manager struct {
	mu                sync.RWMutex
	config            Config
	configured        *RunContext
	current           *RunContext
	knownGood         *RunContext
	versionRecovery   *VersionRecoveryState
	verifiedPoint     *storedRestorePoint
	restoreSaveError  string
	safeMode          *SafeModeState
	latestNode        *NodeReleaseInfo
	dshReleases       []DSHReleaseInfo
	pluginProvenance  []PluginProvenanceRecord
	pluginDisables    []PluginDisableRecord
	lastSwitchAttempt *SwitchAttempt
	switching         bool
	operationGate     chan struct{}
	store             StateStore
}

// RefreshDSHReleases fetches public metadata without holding the environment
// operation gate. Only the final persistence step is serialized with mutations.
func (m *Manager) RefreshDSHReleases(ctx context.Context) (Snapshot, error) {
	m.mu.RLock()
	catalog := m.config.DSHCatalog
	m.mu.RUnlock()
	if catalog == nil {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "DSH release discovery is unavailable", "this platform has no DSH release catalog")
	}
	releases, err := catalog.Refresh(ctx)
	if err != nil {
		return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "DSH release discovery failed", "the public release catalog could not be refreshed", true, false)
	}
	for _, release := range releases {
		if err := validateDSHRelease(release); err != nil {
			return Snapshot{}, err
		}
	}
	releaseOperation, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer releaseOperation()
	m.mu.RLock()
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	state.DSHReleases = cloneDSHReleases(releases)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.dshReleases = cloneDSHReleases(releases)
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

// RefreshLatestNode explicitly refreshes and caches the official latest stable
// Node release. It is never called by New or Snapshot.
func (m *Manager) RefreshLatestNode(ctx context.Context) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	catalog := m.config.NodeCatalog
	m.mu.RUnlock()
	if catalog == nil {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "Node release discovery is unavailable", "this platform has no Node release catalog")
	}
	latest, err := catalog.RefreshLatest(ctx)
	if err != nil {
		return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "Node release discovery failed", "the latest stable release could not be refreshed", true, false)
	}
	if err := validateNodeRelease(latest); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	state.LatestNode = cloneNodeRelease(&latest)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.latestNode = cloneNodeRelease(&latest)
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

// InstallLatestNode resolves latest stable at action time, installs the exact
// result and registers it without switching the Run context.
func (m *Manager) InstallLatestNode(ctx context.Context, observer RuntimeInstallObserver) (Snapshot, error) {
	releaseOperation, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer releaseOperation()
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	return m.installLatestNode(ctx, observer)
}

func (m *Manager) installLatestNode(ctx context.Context, observer RuntimeInstallObserver) (Snapshot, error) {
	m.mu.RLock()
	catalog := m.config.NodeCatalog
	installer := m.config.NodeInstaller
	m.mu.RUnlock()
	if catalog == nil || installer == nil {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "Node installation is unavailable", "this platform has no Node acquisition adapter")
	}
	latest, err := catalog.RefreshLatest(ctx)
	if err != nil {
		return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "Node release discovery failed", "the latest stable release could not be refreshed", true, false)
	}
	if err := validateNodeRelease(latest); err != nil {
		return Snapshot{}, err
	}
	installed, err := installer.Install(ctx, latest, observer)
	if err != nil {
		return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "Node installation failed", "the explicit Node acquisition did not complete", true, true)
	}
	m.mu.RLock()
	wasKnown := false
	for _, node := range m.config.Nodes {
		if node.ID == installed.ID {
			wasKnown = true
			break
		}
	}
	m.mu.RUnlock()
	cleanupCandidate := func(original error) error {
		if wasKnown {
			return original
		}
		if cleanupErr := installer.Remove(context.Background(), installed); cleanupErr != nil {
			return candidateCleanupFailure(original)
		}
		return original
	}
	if cancellation := contextError(ctx); cancellation != nil {
		return Snapshot{}, cleanupCandidate(cancellation)
	}
	if err := validateNodeInstallation(installed); err != nil {
		return Snapshot{}, cleanupCandidate(err)
	}
	if installed.Version != latest.Version || installed.Platform != latest.Platform || installed.Architecture != latest.Architecture || !strings.EqualFold(installed.SHA256, latest.SHA256) {
		return Snapshot{}, cleanupCandidate(failure(lifecycle.ErrorRuntimeInstallFailed, "The installed Node identity does not match the requested release", "the candidate was not added to the Node catalog"))
	}
	m.mu.RLock()
	nodes := cloneNodes(m.config.Nodes)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	updated := false
	for index := range nodes {
		if nodes[index].ID == installed.ID {
			nodes[index] = installed
			updated = true
			break
		}
	}
	if !updated {
		nodes = append(nodes, installed)
	}
	state.Nodes = cloneNodes(nodes)
	state.LatestNode = cloneNodeRelease(&latest)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, cleanupCandidate(err)
	}
	m.mu.Lock()
	m.config.Nodes = nodes
	m.latestNode = cloneNodeRelease(&latest)
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) RemoveNode(ctx context.Context, id string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	nodes := cloneNodes(m.config.Nodes)
	installer := m.config.NodeInstaller
	selected := false
	for _, target := range []*RunContext{m.configured, m.current, m.knownGood, m.safeModeReturnLocked()} {
		if target != nil && target.Node.Kind == NodeSelectionManaged && target.Node.InstallationID == id {
			selected = true
			break
		}
	}
	m.mu.RUnlock()
	if selected {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "Node installation is still selected", "choose another Node installation before removing it")
	}
	index := -1
	for candidate := range nodes {
		if nodes[candidate].ID == id {
			index = candidate
			break
		}
	}
	if index < 0 {
		return Snapshot{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "Node installation was not found", "the installation is not in the catalog")
	}
	node := nodes[index]
	if node.Ownership != NodeOwnershipManaged || !node.Removable {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "Node installation cannot be removed", "choose a removable managed installation")
	}
	if installer == nil {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "Node removal is unavailable", "this platform has no Node acquisition adapter")
	}
	if err := installer.Remove(ctx, node); err != nil {
		return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "Node removal failed", "the managed installation was not removed", true, false)
	}
	nodes = append(nodes[:index], nodes[index+1:]...)
	m.mu.RLock()
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	state.Nodes = cloneNodes(nodes)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.config.Nodes = nodes
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

// New creates a manager from the current catalog and restores only the
// persisted configured Run context. Current and known-good state are
// process-local and are never restored as if a DSH Worker were still alive.
func New(config Config) (*Manager, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		config:        normalized,
		dshReleases:   cloneDSHReleases(normalized.DSHReleases),
		store:         normalized.StateStore,
		operationGate: make(chan struct{}, 1),
	}
	state, err := manager.store.Load(context.Background(), normalized.StatePath)
	if err != nil {
		return nil, err
	}
	if state != nil {
		if err := validateState(*state); err != nil {
			return nil, failure(lifecycle.ErrorManagerStateInvalid, "manager state is invalid", "the persisted Run context has an unsupported shape")
		}
		discardInactiveSafeMode(state)
		if len(state.DataDirectories) > 0 {
			normalized.DataDirectories = mergeDataDirectories(normalized.DataDirectories, state.DataDirectories)
		}
		if len(state.Runtimes) > 0 {
			normalized.Runtimes = mergeRuntimes(normalized.Runtimes, state.Runtimes)
		}
		if len(state.Nodes) > 0 {
			normalized.Nodes = mergeNodes(normalized.Nodes, state.Nodes)
		}
		normalized, err = normalizeConfig(normalized)
		if err != nil {
			return nil, err
		}
	}
	manager.config = normalized
	removeSafeModeSessions(normalized.StatePath, normalized.DataDirectories)
	if state != nil {
		manager.versionRecovery = cloneVersionRecovery(state.VersionRecovery)
		if p := manager.versionRecovery.point(manager.versionRecovery.LastRunning); p != nil {
			manager.knownGood = cloneRunContext(&p.Target)
		}
		manager.safeMode = cloneSafeMode(state.SafeMode)
		manager.lastSwitchAttempt = cloneSwitchAttempt(state.LastSwitchAttempt)
		manager.latestNode = cloneNodeRelease(state.LatestNode)
		manager.dshReleases = cloneDSHReleases(state.DSHReleases)
		manager.pluginProvenance = append([]PluginProvenanceRecord(nil), state.PluginProvenance...)
		manager.pluginDisables = append([]PluginDisableRecord(nil), state.PluginDisables...)
	}
	if state != nil && state.Configured != nil {
		configured := *state.Configured
		manager.configured = &configured
	} else if normalized.DefaultRunContext.RuntimeID != "" {
		configured := normalized.DefaultRunContext
		manager.configured = &configured
	}
	return manager, nil
}

func (m *Manager) RegisterRuntime(ctx context.Context, runtime RuntimeInfo) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	return m.registerRuntime(ctx, runtime)
}

func (m *Manager) registerRuntime(ctx context.Context, runtime RuntimeInfo) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	normalizedRuntime, err := normalizeRuntime(runtime)
	if err != nil {
		return Snapshot{}, err
	}
	runtime = normalizedRuntime
	m.mu.RLock()
	runtimes := cloneRuntimes(m.config.Runtimes)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	updated := false
	for i := range runtimes {
		if runtimes[i].ID == runtime.ID {
			runtimes[i] = runtime
			updated = true
			break
		}
	}
	if !updated {
		runtimes = append(runtimes, runtime)
	}
	state.Runtimes = cloneRuntimes(runtimes)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.config.Runtimes = runtimes
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) InstallRuntime(ctx context.Context, version string) (Snapshot, error) {
	return m.InstallRuntimeWithProgress(ctx, version, nil)
}

// InstallRuntimeWithProgress performs an explicit, staged runtime acquisition
// and only registers a candidate after the DSH adapter verifies it. Normal
// startup never calls this method; the trusted Settings surface starts an
// explicit pull when the local runtime is unavailable.
func (m *Manager) InstallRuntimeWithProgress(ctx context.Context, version string, observer RuntimeInstallObserver) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	if !validRuntimeVersion(version) {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallFailed, "DSH runtime version is invalid", "use a semantic version such as 0.1.2-alpha.3")
	}
	if !m.catalogContainsDSHVersion(version) {
		return Snapshot{}, failure(lifecycle.ErrorDSHUnsupportedVersion, "The requested DSH runtime version is not in the refreshed catalog", "refresh releases and choose an exact published version")
	}
	runtimeID := "dsh-" + version
	m.mu.RLock()
	runtimeInUse := false
	for _, target := range []*RunContext{m.configured, m.current, m.knownGood, m.safeModeReturnLocked()} {
		if target == nil {
			continue
		}
		// A missing configured distribution can be acquired on first use.
		// Current and recovery environments remain protected.
		if target == m.configured {
			runtime, exists := findRuntime(m.config.Runtimes, target.RuntimeID)
			if !exists || !runtimeInstallationPresent(runtime) {
				continue
			}
		}
		if target.RuntimeID == runtimeID {
			runtimeInUse = true
			break
		}
		if runtime, exists := findRuntime(m.config.Runtimes, target.RuntimeID); exists && runtime.Version == version {
			runtimeInUse = true
			break
		}
	}
	m.mu.RUnlock()
	if runtimeInUse {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "The requested DSH runtime is in use", "stop the current workspace before replacing this runtime")
	}
	m.mu.RLock()
	installer := m.config.RuntimeInstaller
	verifier := m.config.RuntimeVerifier
	m.mu.RUnlock()
	if installer == nil {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "runtime installation is unavailable", "this platform has no native runtime installer")
	}
	var runtime RuntimeInfo
	if nodeAwareInstaller, ok := installer.(RuntimeInstallerWithNodes); ok {
		m.mu.RLock()
		nodes := cloneNodes(m.config.Nodes)
		m.mu.RUnlock()
		if requirement, supported := installer.(RuntimeInstallerNodeRequirement); supported {
			required, requirementErr := requirement.RequiresNodeAcquisition(ctx, nodes)
			if requirementErr != nil {
				return Snapshot{}, preserveFailure(requirementErr, lifecycle.ErrorRuntimeInstallUnavailable, "Node toolchain detection failed", "the explicit runtime install operation did not start", true, false)
			}
			if required {
				if _, acquisitionErr := m.installLatestNode(ctx, observer); acquisitionErr != nil {
					return Snapshot{}, acquisitionErr
				}
				m.mu.RLock()
				nodes = cloneNodes(m.config.Nodes)
				m.mu.RUnlock()
			}
		}
		runtime, err = nodeAwareInstaller.InstallWithNodes(ctx, version, nodes, observer)
	} else if progressInstaller, ok := installer.(RuntimeInstallerWithProgress); ok {
		runtime, err = progressInstaller.InstallWithProgress(ctx, version, observer)
	} else {
		runtime, err = installer.Install(ctx, version)
	}
	if err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return Snapshot{}, cancellation
		}
		return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "DSH runtime installation failed", "the explicit runtime install operation did not complete", true, true)
	}
	if runtime.ID == "" {
		runtime.ID = "dsh-" + version
	}
	if runtime.Version == "" {
		runtime.Version = version
	}
	if runtime.Version != version {
		installFailure := failure(lifecycle.ErrorDSHUnsupportedVersion, "The installed DSH runtime version does not match the request", "the candidate was not added to the runtime catalog")
		if cleanupErr := m.discardRuntimeCandidate(ctx, installer, runtime); cleanupErr != nil {
			return Snapshot{}, candidateCleanupFailure(installFailure)
		}
		return Snapshot{}, installFailure
	}
	if runtime.Source == "" {
		runtime.Source = RuntimeSourceManaged
	}
	runtime.Installed = true
	runtime.Removable = true
	if cancellation := contextError(ctx); cancellation != nil {
		if cleanupErr := m.discardRuntimeCandidate(ctx, installer, runtime); cleanupErr != nil {
			return Snapshot{}, candidateCleanupFailure(cancellation)
		}
		return Snapshot{}, cancellation
	}
	if err := verifyRuntime(ctx, verifier, runtime); err != nil {
		if cleanupErr := m.discardRuntimeCandidate(ctx, installer, runtime); cleanupErr != nil {
			return Snapshot{}, candidateCleanupFailure(err)
		}
		return Snapshot{}, err
	}
	if cancellation := contextError(ctx); cancellation != nil {
		if cleanupErr := m.discardRuntimeCandidate(ctx, installer, runtime); cleanupErr != nil {
			return Snapshot{}, candidateCleanupFailure(cancellation)
		}
		return Snapshot{}, cancellation
	}
	snapshot, err := m.registerRuntime(ctx, runtime)
	if err != nil {
		if cleanupErr := m.discardRuntimeCandidate(ctx, installer, runtime); cleanupErr != nil {
			return Snapshot{}, candidateCleanupFailure(err)
		}
		return Snapshot{}, err
	}
	if cleanupErr := m.finalizeRuntimeCandidate(ctx, installer, runtime); cleanupErr != nil {
		return snapshot, candidateCleanupFailure(nil)
	}
	return snapshot, nil
}

func (m *Manager) catalogContainsDSHVersion(version string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, release := range m.dshReleases {
		if release.Version == version {
			return true
		}
	}
	return false
}

func (m *Manager) discardRuntimeCandidate(ctx context.Context, installer RuntimeInstaller, runtime RuntimeInfo) error {
	discard, ok := installer.(RuntimeDiscarder)
	if !ok {
		return nil
	}
	return discard.Discard(ctx, runtime)
}

func (m *Manager) finalizeRuntimeCandidate(ctx context.Context, installer RuntimeInstaller, runtime RuntimeInfo) error {
	finalizer, ok := installer.(RuntimeFinalizer)
	if !ok {
		return nil
	}
	return finalizer.Finalize(ctx, runtime)
}

func candidateCleanupFailure(original error) error {
	const detail = "The runtime candidate could not be cleaned up; retry the operation after closing dsh-work."
	var failureValue lifecycle.Failure
	if errors.As(original, &failureValue) {
		failureValue.Detail = detail
		failureValue.Retryable = true
		failureValue.EffectOccurred = true
		if failureValue.CorrelationID == "" {
			failureValue.CorrelationID = lifecycle.NewCorrelationID()
		}
		return failureValue
	}
	return lifecycle.Failure{
		Code:           lifecycle.ErrorRuntimeInstallFailed,
		Summary:        "The DSH runtime was prepared but cleanup did not complete.",
		Detail:         detail,
		Retryable:      true,
		EffectOccurred: true,
		CorrelationID:  lifecycle.NewCorrelationID(),
	}
}

func (m *Manager) RemoveRuntime(ctx context.Context, id string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	index := -1
	for i := range m.config.Runtimes {
		if m.config.Runtimes[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime was not found", "the runtime is not in the catalog")
	}
	if (m.safeMode != nil && m.safeMode.ReturnTo.RuntimeID == id) || (m.configured != nil && m.configured.RuntimeID == id) || (m.current != nil && m.current.RuntimeID == id) || (m.knownGood != nil && m.knownGood.RuntimeID == id) {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "DSH runtime is still selected", "choose another runtime before removing it")
	}
	if !m.config.Runtimes[index].Removable {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "DSH runtime is managed by the development fixture", "the pinned development runtime cannot be removed")
	}
	runtimes := cloneRuntimes(m.config.Runtimes)
	state := m.stateLocked()
	statePath := m.config.StatePath
	remover, _ := m.config.RuntimeInstaller.(RuntimeRemover)
	m.mu.RUnlock()
	if remover != nil {
		if err := remover.Remove(ctx, runtimes[index]); err != nil {
			return Snapshot{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallFailed, "DSH runtime removal failed", "the runtime files could not be removed; close programs using them and retry", true, false)
		}
	}
	runtimes = append(runtimes[:index], runtimes[index+1:]...)
	state.Runtimes = cloneRuntimes(runtimes)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.config.Runtimes = runtimes
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) RegisterDataDirectory(ctx context.Context, dataDirectory DataDirectoryInfo) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	normalizedDataDirectory, err := normalizeDataDirectory(dataDirectory)
	if err != nil {
		return Snapshot{}, err
	}
	dataDirectory = normalizedDataDirectory
	m.mu.RLock()
	dataDirectories := cloneDataDirectories(m.config.DataDirectories)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	updated := false
	for i := range dataDirectories {
		if dataDirectories[i].ID == dataDirectory.ID {
			dataDirectories[i] = dataDirectory
			updated = true
			break
		}
	}
	if !updated {
		dataDirectories = append(dataDirectories, dataDirectory)
	}
	state.DataDirectories = cloneDataDirectories(dataDirectories)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.config.DataDirectories = dataDirectories
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) RemoveDataDirectory(ctx context.Context, id string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := m.ensureMutationAllowed(); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	index := -1
	for i := range m.config.DataDirectories {
		if m.config.DataDirectories[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the data directory is not in the catalog")
	}
	if (m.safeMode != nil && m.safeMode.ReturnTo.Profile.DataDirectoryID == id) || (m.configured != nil && m.configured.Profile.DataDirectoryID == id) || (m.current != nil && m.current.Profile.DataDirectoryID == id) || (m.knownGood != nil && m.knownGood.Profile.DataDirectoryID == id) {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "DSH data directory is still selected", "choose another profile before removing it")
	}
	if m.config.DataDirectories[index].Ownership == DataDirectoryOwnershipDSHWork {
		m.mu.RUnlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "the dsh-work DSH data directory cannot be removed here", "remove the data directory through an explicit data-management flow")
	}
	dataDirectories := cloneDataDirectories(m.config.DataDirectories)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	dataDirectories = append(dataDirectories[:index], dataDirectories[index+1:]...)
	state.DataDirectories = cloneDataDirectories(dataDirectories)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.config.DataDirectories = dataDirectories
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	configured := cloneRunContext(m.configured)
	current := cloneRunContext(m.current)
	knownGood := cloneRunContext(m.knownGood)
	latestNode := cloneNodeRelease(m.latestNode)
	dshReleases := cloneDSHReleases(m.dshReleases)
	lastSwitchAttempt := cloneSwitchAttempt(m.lastSwitchAttempt)
	safeMode := cloneSafeMode(m.safeMode)
	versionPoints := m.versionViewLocked()
	m.mu.RUnlock()

	profiles := discoverProfiles(ctx, config.DataDirectories, config.ProfileCatalog, config.ProfileReader, current, configured, knownGood, lastSwitchAttempt)
	for i := range profiles {
		m.markDisabledPlugins(profiles[i].Ref, profiles[i].Plugins)
	}
	if safeMode != nil {
		for i := range profiles {
			if profiles[i].Ref == safeMode.ReturnTo.Profile {
				profiles[i].Deletable = false
				profiles[i].Renamable = false
			}
		}
	}
	var systemNode *ResolvedNode
	if config.NodeResolver != nil {
		if resolved, err := config.NodeResolver.Resolve(ctx, NodeSelection{Kind: NodeSelectionSystem}, nil); err == nil && resolved.Version != "" {
			resolved.Selection = NodeSelection{Kind: NodeSelectionSystem}
			systemNode = &resolved
		}
	}
	return Snapshot{
		SafeMode:          safeMode,
		RestorePoints:     versionPoints,
		Runtimes:          refreshRuntimes(config.Runtimes),
		DSHReleases:       dshReleases,
		Nodes:             refreshNodes(config.Nodes),
		SystemNode:        systemNode,
		LatestNode:        latestNode,
		DataDirectories:   cloneDataDirectories(config.DataDirectories),
		Profiles:          profiles,
		Configured:        configured,
		Current:           current,
		KnownGood:         knownGood,
		LastSwitchAttempt: lastSwitchAttempt,
		Theme:             selectedTheme(ctx, config.DataDirectories, current, configured, config.ThemeReader),
	}, nil
}

// Theme returns the selected DSH data directory's appearance preference
// without discovering the runtime, data directory and profile catalogs.
func (m *Manager) Theme(ctx context.Context) (ThemePreference, error) {
	if err := contextError(ctx); err != nil {
		return ThemePreferenceSystem, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	current := cloneRunContext(m.current)
	configured := cloneRunContext(m.configured)
	m.mu.RUnlock()
	return selectedTheme(ctx, config.DataDirectories, current, configured, config.ThemeReader), nil
}

func (m *Manager) ListPlugins(ctx context.Context, request PluginListRequest) ([]PluginInfo, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	dataDirectory, runtime, environment, err := m.resolvePluginTarget(ctx, request.Target, false, true)
	if err != nil {
		return nil, err
	}
	profilePath := filepath.Join(dataDirectory.Path, "profiles", request.Target.Profile.Name)
	catalog := m.profileCatalog()
	if !profileExists(catalog, dataDirectory, request.Target.Profile.Name) {
		if isBuiltInProfile(catalog, request.Target.Profile.Name) {
			return []PluginInfo{}, nil
		}
		return nil, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "create or select an existing profile")
	}
	plugins, err := m.profilePlugins(ctx, profilePath)
	if err != nil {
		return nil, err
	}
	return m.markDisabledPlugins(request.Target.Profile, m.observePluginUpdates(ctx, dataDirectory, runtime, request.Target.Profile, plugins, environment)), nil
}

// InstallPlugin delegates profile composition to DSH's supported plugin
// command. dsh-work validates the target and package spec and never runs pnpm
// directly; the only manifest field it writes is the bundle list (plugin_disable.go).
func (m *Manager) InstallPlugin(ctx context.Context, request PluginInstallRequest) (PluginResult, error) {
	return m.runPluginCommand(ctx, request.Target, request.Package, "add")
}

func (m *Manager) RemovePlugin(ctx context.Context, request PluginRemoveRequest) (PluginResult, error) {
	return m.runPluginCommand(ctx, request.Target, request.Package, "remove")
}

func (m *Manager) UpgradePlugin(ctx context.Context, request PluginUpgradeRequest) (PluginResult, error) {
	return m.runPluginCommand(ctx, request.Target, request.Package, "update")
}

// RenameProfile changes the directory name that DSH uses as a custom
// profile's identity. DSH 0.1.2 exposes no profile-rename CLI command; its
// public contract resolves profiles directly from $DSH_HOME/profiles/<name>.
// The manager therefore keeps this narrow filesystem operation at the
// identity boundary: it never rewrites package.json, dsh.profile or patch
// layers, and it refuses built-in or current profiles.
func (m *Manager) RenameProfile(ctx context.Context, request ProfileRenameRequest) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := validateProfileRef(request.Profile); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	protected := m.safeMode != nil && m.safeMode.ReturnTo.Profile == request.Profile
	m.mu.RUnlock()
	if protected {
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "profile is retained for leaving safe mode", "return to the previous environment before renaming it")
	}
	newName := strings.TrimSpace(request.NewName)
	if !validProfileName(newName) {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "new DSH profile name is invalid", "profile names cannot contain path separators")
	}
	if newName == request.Profile.Name {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "the DSH profile name is unchanged", "enter a different profile name")
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	configured := cloneRunContext(m.configured)
	current := cloneRunContext(m.current)
	knownGood := cloneRunContext(m.knownGood)
	switching := m.switching
	m.mu.RUnlock()
	if switching {
		return Snapshot{}, failure(lifecycle.ErrorManagerOperationBusy, "the Run context is switching", "wait for the current context switch to finish")
	}
	dataDirectory, ok := findDataDirectory(config.DataDirectories, request.Profile.DataDirectoryID)
	if !ok {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the profile data directory is not in the catalog")
	}
	if dataDirectory.Ownership == DataDirectoryOwnershipUser && !directoryExists(dataDirectory.Path) {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable", "register an existing DSH data directory")
	}
	if isBuiltInProfile(config.ProfileCatalog, request.Profile.Name) {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "built-in DSH profiles cannot be renamed", "choose a custom profile")
	}
	if current != nil && current.Profile == request.Profile {
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "the current DSH profile cannot be renamed", "switch to another Run context before renaming the current profile")
	}
	oldPath := filepath.Join(dataDirectory.Path, "profiles", request.Profile.Name)
	oldInfo, err := os.Stat(oldPath)
	if err != nil || !oldInfo.IsDir() {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "choose an existing custom profile")
	}
	if profileExists(config.ProfileCatalog, dataDirectory, newName) {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "a DSH profile already uses that name", "choose a different profile name")
	}
	if _, err := m.prepareProfileBackups(ctx, request.Profile); err != nil {
		return Snapshot{}, recoveryFailure(err, lifecycle.ErrorProfileRenameFailed, "Profile backups could not be moved with the profile.")
	}
	newPath := filepath.Join(dataDirectory.Path, "profiles", newName)
	m.mu.Lock()
	if err := os.Rename(oldPath, newPath); err != nil {
		m.mu.Unlock()
		return Snapshot{}, failureWithMeta(lifecycle.ErrorProfileRenameFailed, "the DSH profile could not be renamed", "the profile directory was not changed", true, false)
	}

	configuredChanged := configured != nil && configured.Profile == request.Profile
	knownGoodChanged := knownGood != nil && knownGood.Profile == request.Profile
	state := m.stateLocked()
	if configuredChanged {
		configured.Profile.Name = newName
		state.Configured = cloneRunContext(configured)
	}
	if state.VersionRecovery != nil {
		oldKey := profilePointKey(request.Profile)
		newRef := request.Profile
		newRef.Name = newName
		if id := state.VersionRecovery.LastByProfile[oldKey]; id != "" {
			delete(state.VersionRecovery.LastByProfile, oldKey)
			state.VersionRecovery.LastByProfile[profilePointKey(newRef)] = id
		}
		for i := range state.VersionRecovery.Points {
			if state.VersionRecovery.Points[i].Target.Profile == request.Profile {
				state.VersionRecovery.Points[i].Target.Profile = newRef
			}
		}
	}
	if err := m.store.Save(ctx, m.config.StatePath, state); err != nil {
		rollbackErr := os.Rename(newPath, oldPath)
		m.mu.Unlock()
		return Snapshot{}, errors.Join(err, rollbackErr)
	}
	m.versionRecovery = state.VersionRecovery
	if configuredChanged {
		m.configured = cloneRunContext(configured)
	}

	if knownGoodChanged {
		knownGood.Profile.Name = newName
		m.knownGood = cloneRunContext(knownGood)
	}
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) runPluginCommand(ctx context.Context, target PluginTarget, packageSpec, operation string) (PluginResult, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return PluginResult{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return PluginResult{}, err
	}
	if err := validatePluginSpec(packageSpec); err != nil {
		return PluginResult{}, err
	}
	dataDirectory, runtime, environment, err := m.resolvePluginTarget(ctx, target, operation == "add", true)
	if err != nil {
		return PluginResult{}, err
	}
	m.mu.RLock()
	runner := m.config.CommandRunner
	commands := m.config.PluginCommands
	current := cloneRunContext(m.current)
	m.mu.RUnlock()
	if runner == nil || commands == nil {
		return PluginResult{}, failure(lifecycle.ErrorPluginCommandUnavailable, "The DSH plugin command is unavailable", "run this operation on a platform with a native command adapter")
	}
	var args []string
	successfulRoute := RuntimeArtifactSourceNone
	profilePath := filepath.Join(dataDirectory.Path, "profiles", target.Profile.Name)
	customRegistry := configuredPluginRegistry(profilePath, pluginPackageName(packageSpec))
	if operation == "remove" {
		args, err = commands.Remove(target.Profile.Name, packageSpec)
		if err == nil {
			_, err = runner.Run(ctx, runtime.Path, args, cloneEnvironment(environment), dataDirectory.Path)
		}
	} else if customRegistry != "" {
		if operation == "add" {
			args, err = commands.InstallAt(target.Profile.Name, packageSpec, customRegistry)
		} else {
			args, err = commands.Update(target.Profile.Name, packageSpec, customRegistry)
		}
		if err == nil {
			_, err = runner.Run(ctx, runtime.Path, args, cloneEnvironment(environment), dataDirectory.Path)
		}
	} else if classifyRequestedPluginSource(packageSpec) == PluginSourcePublicRegistry {
		var route acquisition.Route
		route, _, err = m.runPublicPluginAttempt(ctx, dataDirectory, runtime, target.Profile, packageSpec, operation, environment)
		if route == acquisition.RouteMirror {
			successfulRoute = RuntimeArtifactSourceMirror
		} else if route == acquisition.RouteOfficial {
			successfulRoute = RuntimeArtifactSourceOfficial
		}
	} else {
		if operation == "add" {
			args, err = commands.Install(target.Profile.Name, packageSpec)
		} else {
			args, err = commands.Update(target.Profile.Name, packageSpec, "")
		}
		if err == nil {
			_, err = runner.Run(ctx, runtime.Path, args, cloneEnvironment(environment), dataDirectory.Path)
		}
	}
	if err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return PluginResult{}, cancellation
		}
		return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginCommandFailed, "The DSH plugin operation failed", "DSH rejected the profile plugin operation", true, true)
	}
	if operation == "add" || operation == "update" {
		packageName := pluginPackageName(packageSpec)
		sourceKind := classifyRequestedPluginSource(packageSpec)
		if customRegistry != "" {
			sourceKind = PluginSourcePrivateRegistry
		}
		if sourceKind != PluginSourceUnknown && validPackageName(packageName) {
			if err := m.recordPluginProvenance(ctx, PluginProvenanceRecord{Profile: target.Profile, Package: packageName, RequestedSpec: boundedPluginSpec(packageSpec), SourceKind: sourceKind, SuccessfulRoute: successfulRoute, RecordedAt: time.Now().UTC().Format(time.RFC3339)}); err != nil {
				return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginCommandFailed, "The plugin changed but its source record could not be saved", "refresh the current profile before another change", true, true)
			}
		}
	}
	if operation == "remove" {
		// An uninstalled plugin has nothing left to keep disabled.
		if err := m.forgetPluginDisable(ctx, target.Profile, pluginPackageName(packageSpec)); err != nil {
			return PluginResult{}, err
		}
	}
	plugins, err := m.profilePlugins(ctx, filepath.Join(dataDirectory.Path, "profiles", target.Profile.Name))
	if err != nil {
		return PluginResult{}, err
	}
	restartRequired := current != nil && current.Profile == target.Profile
	return PluginResult{Profile: target.Profile, Plugins: m.markDisabledPlugins(target.Profile, plugins), RestartRequired: restartRequired}, nil
}

func (m *Manager) recordPluginProvenance(ctx context.Context, record PluginProvenanceRecord) error {
	record.RequestedSpec = boundedPluginSpec(record.RequestedSpec)
	if record.SourceKind != PluginSourcePublicRegistry {
		record.RequestedSpec = ""
	}
	if record.SuccessfulRoute != RuntimeArtifactSourceOfficial && record.SuccessfulRoute != RuntimeArtifactSourceMirror {
		record.SuccessfulRoute = RuntimeArtifactSourceNone
	}
	m.mu.RLock()
	records := append([]PluginProvenanceRecord(nil), m.pluginProvenance...)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	filtered := records[:0]
	for _, existing := range records {
		if existing.Profile != record.Profile || existing.Package != record.Package {
			filtered = append(filtered, existing)
		}
	}
	records = append(filtered, record)
	if len(records) > 256 {
		records = append([]PluginProvenanceRecord(nil), records[len(records)-256:]...)
	}
	state.PluginProvenance = append([]PluginProvenanceRecord(nil), records...)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return err
	}
	m.mu.Lock()
	m.pluginProvenance = records
	m.mu.Unlock()
	return nil
}

func pluginPackageName(spec string) string {
	value := strings.TrimSpace(spec)
	separator := strings.LastIndex(value, "@")
	if separator > 0 {
		return value[:separator]
	}
	return value
}

func boundedPluginSpec(spec string) string {
	spec = strings.TrimSpace(spec)
	if classifyRequestedPluginSource(spec) == PluginSourcePublicRegistry && len(spec) <= 256 {
		return spec
	}
	return ""
}

func configuredPluginRegistry(profilePath, packageName string) string {
	data, err := os.ReadFile(filepath.Join(profilePath, ".npmrc"))
	if err != nil || len(data) > 64*1024 {
		return ""
	}
	scope := ""
	if strings.HasPrefix(packageName, "@") {
		if slash := strings.Index(packageName, "/"); slash > 1 {
			scope = strings.ToLower(packageName[:slash]) + ":registry"
		}
	}
	candidate := ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		if scope != "" && key == scope {
			candidate = strings.TrimSpace(parts[1])
			break
		}
		if key == "registry" {
			candidate = strings.TrimSpace(parts[1])
		}
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	normalized := strings.TrimRight(parsed.String(), "/") + "/"
	if normalized == defaultPluginOfficialRegistry || normalized == defaultPluginMirrorRegistry {
		return ""
	}
	return normalized
}

func (m *Manager) runPublicPluginAttempt(ctx context.Context, dataDirectory DataDirectoryInfo, runtime RuntimeInfo, profile ProfileRef, packageName, operation string, environment map[string]string) (acquisition.Route, string, error) {
	m.mu.RLock()
	runner, commands := m.config.CommandRunner, m.config.PluginCommands
	official, mirror := m.config.PluginOfficialRegistry, m.config.PluginMirrorRegistry
	m.mu.RUnlock()
	candidates := []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: official}}
	if mirror != "" {
		candidates = append(candidates, acquisition.SourceCandidate{Route: acquisition.RouteMirror, Location: mirror})
	}
	result, err := acquisition.Acquire(ctx, acquisition.Request{
		OperationID: "plugin-" + operation, Identity: acquisition.ArtifactIdentity{Kind: acquisition.ArtifactPlugin, Name: packageName},
		Candidates: candidates, StagingRoot: os.TempDir(), StagePrefix: ".dsh-work-plugin-",
	}, pluginAttemptAdapter{runner: runner, commands: commands, runtime: runtime, dataDirectory: dataDirectory, profile: profile, packageName: packageName, operation: operation, environment: environment}, nil)
	payload := ""
	if err == nil && result.PayloadPath != "" {
		data, readErr := os.ReadFile(result.PayloadPath)
		if readErr != nil {
			err = readErr
		} else {
			payload = string(data)
		}
	}
	if result.StagingPath != "" {
		_ = os.RemoveAll(result.StagingPath)
	}
	return result.Route, payload, err
}

type pluginAttemptAdapter struct {
	environment   map[string]string
	runner        CommandRunner
	commands      PluginCommandBuilder
	runtime       RuntimeInfo
	dataDirectory DataDirectoryInfo
	profile       ProfileRef
	packageName   string
	operation     string
}

func (a pluginAttemptAdapter) Attempt(ctx context.Context, request acquisition.AttemptRequest) (acquisition.AttemptResult, error) {
	var args []string
	var err error
	if a.operation == "add" {
		args, err = a.commands.InstallAt(a.profile.Name, a.packageName, request.Candidate.Location)
	} else if a.operation == "update" {
		args, err = a.commands.Update(a.profile.Name, a.packageName, request.Candidate.Location)
	} else {
		args, err = a.commands.Outdated(a.profile.Name)
	}
	if err != nil {
		return acquisition.AttemptResult{}, acquisition.Failure{Kind: acquisition.FailureSemantic, Summary: "plugin command is invalid", Cause: err}
	}
	environment := cloneEnvironment(a.environment)
	if a.operation == "outdated" {
		// pnpm outdated rejects --registry but reads the npm config environment.
		environment["npm_config_registry"] = request.Candidate.Location
	}
	result, err := a.runner.Run(ctx, a.runtime.Path, args, environment, a.dataDirectory.Path)
	// pnpm outdated exits non-zero whenever a package is outdated; its JSON
	// report on stdout is still the result.
	if err != nil && !(a.operation == "outdated" && isOutdatedReport(result.Stdout)) {
		return acquisition.AttemptResult{}, classifyPluginAttemptFailure(result, err)
	}
	payload := ""
	if a.operation == "outdated" {
		payload = filepath.Join(request.StagingPath, "result.json")
		if err := os.WriteFile(payload, []byte(result.Stdout), 0o600); err != nil {
			return acquisition.AttemptResult{}, acquisition.Failure{Kind: acquisition.FailureLocalIO, Summary: "plugin result staging failed", Cause: err}
		}
	}
	return acquisition.AttemptResult{ResolvedIdentity: request.Identity, PayloadPath: payload}, nil
}

func isOutdatedReport(stdout string) bool {
	var report map[string]json.RawMessage
	return json.Unmarshal([]byte(strings.TrimSpace(stdout)), &report) == nil
}

func classifyPluginAttemptFailure(result CommandResult, err error) acquisition.Failure {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr + "\n" + err.Error())
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return acquisition.Failure{Kind: acquisition.FailureCanceled, Summary: "plugin operation was canceled", Cause: err}
	}
	for _, marker := range []string{"econnreset", "econnrefused", "enotfound", "etimedout", "network", "socket hang up", "fetch failed"} {
		if strings.Contains(text, marker) {
			return acquisition.Failure{Kind: acquisition.FailureReachability, Summary: "plugin registry could not be reached", Cause: err}
		}
	}
	if strings.Contains(text, "401") {
		return acquisition.Failure{Kind: acquisition.FailureAuthentication, Summary: "plugin registry authentication failed", Cause: err}
	}
	if strings.Contains(text, "403") {
		return acquisition.Failure{Kind: acquisition.FailureAuthorization, Summary: "plugin registry authorization failed", Cause: err}
	}
	if strings.Contains(text, "404") || strings.Contains(text, "not found") {
		return acquisition.Failure{Kind: acquisition.FailureNotFound, Summary: "plugin package was not found", Cause: err}
	}
	return acquisition.Failure{Kind: acquisition.FailureSemantic, Summary: "plugin command failed", Cause: err}
}

func (m *Manager) observePluginUpdates(ctx context.Context, dataDirectory DataDirectoryInfo, runtime RuntimeInfo, profile ProfileRef, plugins []PluginInfo, environment map[string]string) []PluginInfo {
	m.mu.RLock()
	runner := m.config.CommandRunner
	commands := m.config.PluginCommands
	provenance := append([]PluginProvenanceRecord(nil), m.pluginProvenance...)
	m.mu.RUnlock()
	for index := range plugins {
		plugins[index].SourceKind = classifyPluginSource(plugins[index].Spec)
		plugins[index].CurrentVersion = plugins[index].Version
		plugins[index].UpdateCheck = PluginUpdateUnknown
		for provenanceIndex := len(provenance) - 1; provenanceIndex >= 0; provenanceIndex-- {
			record := provenance[provenanceIndex]
			if record.Profile == profile && record.Package == plugins[index].Package {
				plugins[index].SourceKind = record.SourceKind
				plugins[index].SuccessfulRoute = record.SuccessfulRoute
				break
			}
		}
		if configuredPluginRegistry(filepath.Join(dataDirectory.Path, "profiles", profile.Name), plugins[index].Package) != "" {
			plugins[index].SourceKind = PluginSourcePrivateRegistry
			plugins[index].SuccessfulRoute = RuntimeArtifactSourceNone
		}
	}
	if runner == nil || commands == nil {
		return plugins
	}
	listArgs, err := commands.List(profile.Name)
	if err == nil {
		if result, runErr := runner.Run(ctx, runtime.Path, listArgs, cloneEnvironment(environment), dataDirectory.Path); runErr == nil {
			applyPluginListJSON(plugins, result.Stdout)
		}
	}
	route, result, err := m.runPublicPluginAttempt(ctx, dataDirectory, runtime, profile, "public-profile-plugins", "outdated", environment)
	if err != nil {
		return plugins
	}
	for index := range plugins {
		if plugins[index].SourceKind != PluginSourcePublicRegistry {
			continue
		}
		if route == acquisition.RouteMirror {
			plugins[index].SuccessfulRoute = RuntimeArtifactSourceMirror
		} else {
			plugins[index].SuccessfulRoute = RuntimeArtifactSourceOfficial
		}
	}
	applyPluginOutdatedJSON(plugins, result)
	return plugins
}

func applyPluginListJSON(plugins []PluginInfo, data string) {
	var document struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if json.Unmarshal([]byte(data), &document) != nil {
		return
	}
	for index := range plugins {
		if dependency, ok := document.Dependencies[plugins[index].Package]; ok && validRuntimeVersion(dependency.Version) {
			plugins[index].Version = dependency.Version
			plugins[index].CurrentVersion = dependency.Version
		}
	}
}

func applyPluginOutdatedJSON(plugins []PluginInfo, data string) {
	var outdated map[string]struct {
		Current string `json:"current"`
		Latest  string `json:"latest"`
	}
	if json.Unmarshal([]byte(data), &outdated) != nil {
		return
	}
	for index := range plugins {
		if plugins[index].SourceKind != PluginSourcePublicRegistry {
			continue
		}
		plugins[index].UpdateCheck = PluginUpdateCurrent
		if item, ok := outdated[plugins[index].Package]; ok {
			if item.Current != "" {
				plugins[index].CurrentVersion = item.Current
				plugins[index].Version = item.Current
			}
			if item.Latest != "" && item.Latest != item.Current {
				plugins[index].AvailableVersion = item.Latest
				plugins[index].UpdateCheck = PluginUpdateAvailable
			}
		}
	}
}

var publicPluginRangePattern = regexp.MustCompile(`^[~^<>=*0-9xX.v|+\-]+$`)
var publicPluginTagPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

func classifyPluginSource(spec string) PluginSourceKind {
	value := strings.ToLower(strings.TrimSpace(spec))
	switch {
	case value == "":
		return PluginSourceUnknown
	case strings.HasPrefix(value, "file:") || strings.HasPrefix(value, "link:") || strings.HasPrefix(value, "workspace:") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || filepath.IsAbs(spec):
		return PluginSourceLocal
	case strings.HasPrefix(value, "git+") || strings.HasPrefix(value, "git://") || strings.HasPrefix(value, "github:") || strings.HasPrefix(value, "gitlab:") || strings.HasPrefix(value, "bitbucket:"):
		return PluginSourceGit
	case strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://"):
		return PluginSourceURL
	case strings.HasSuffix(value, ".tgz") || strings.HasSuffix(value, ".tar.gz"):
		return PluginSourceURL
	case strings.HasPrefix(value, "registry:"):
		return PluginSourcePrivateRegistry
	case strings.HasPrefix(value, "npm:") || validRuntimeVersion(strings.TrimLeft(value, "~^<>=v")) || publicPluginRangePattern.MatchString(value) || publicPluginTagPattern.MatchString(value):
		return PluginSourcePublicRegistry
	default:
		return PluginSourceUnknown
	}
}

func classifyRequestedPluginSource(spec string) PluginSourceKind {
	if kind := classifyPluginSource(spec); kind != PluginSourceUnknown {
		return kind
	}
	value := strings.TrimSpace(spec)
	separator := strings.LastIndex(value, "@")
	if separator > 0 {
		if kind := classifyPluginSource(value[separator+1:]); kind != PluginSourceUnknown {
			return kind
		}
	}
	if validPackageName(value) {
		return PluginSourcePublicRegistry
	}
	return PluginSourceUnknown
}

func (m *Manager) resolvePluginTarget(ctx context.Context, target PluginTarget, allowCreate, mutation bool) (DataDirectoryInfo, RuntimeInfo, map[string]string, error) {
	if launch, ok := ctx.Value(pluginLaunchKey{}).(ResolvedLaunch); ok && mutation && launch.Target.Profile == target.Profile {
		env := cloneEnvironment(launch.Node.ChildEnvironment)
		env["DSH_HOME"] = launch.DataDirectory.Path
		return launch.DataDirectory, launch.Runtime, env, nil
	}

	if err := contextError(ctx); err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	current := cloneRunContext(m.current)
	switching := m.switching
	m.mu.RUnlock()
	dataDirectory, err := resolveDataDirectoryProfile(config, target.Profile, allowCreate)
	if err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, err
	}
	if !mutation {
		return dataDirectory, RuntimeInfo{}, nil, nil
	}
	if switching || current == nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, failure(lifecycle.ErrorManagerOperationBusy, "profile changes require a current Ready Run context", "start or restore a Run context before changing profile plugins")
	}
	if current.Profile != target.Profile {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, failure(lifecycle.ErrorManagerOperationBusy, "only the current profile can be changed", "switch the Run context to this profile before changing its plugins")
	}
	runtime, ok := findRuntime(config.Runtimes, current.RuntimeID)
	if !ok {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, failure(lifecycle.ErrorDSHRuntimeNotFound, "the current DSH runtime was not found", "restore a valid Run context before changing profile plugins")
	}
	if !runtimeInstallationPresent(runtime) {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, failure(lifecycle.ErrorDSHRuntimeNotFound, "the current DSH runtime is not available", "restore a valid Run context before changing profile plugins")
	}
	node, err := resolveNode(ctx, config.NodeResolver, current.Node, config.Nodes)
	if err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, err
	}
	environment := cloneEnvironment(node.ChildEnvironment)
	environment["DSH_HOME"] = dataDirectory.Path
	if err := verifyRuntimeWithEnvironment(ctx, config.RuntimeVerifier, runtime, environment); err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, err
	}
	if err := verifyRuntimeProfile(ctx, config.RuntimeVerifier, runtime, dataDirectory, current.Profile); err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, nil, err
	}
	runtime.Installed = true
	return dataDirectory, runtime, environment, nil
}

// Resolve validates a complete runtime + data-directory + profile pairing. It
// returns one immutable Run context suitable for the Host launch boundary.
func (m *Manager) Resolve(ctx context.Context, request LaunchRequest) (RunContext, error) {
	resolved, err := m.ResolveLaunch(ctx, request)
	if err != nil {
		return RunContext{}, err
	}
	return resolved.Target, nil
}

// ResolveLaunch validates and returns the complete immutable launch tuple.
// Keeping the catalog records in this result prevents callers from resolving
// a target and then accidentally pairing it with a different data directory
// or runtime.
func (m *Manager) ResolveLaunch(ctx context.Context, request LaunchRequest) (ResolvedLaunch, error) {
	if err := contextError(ctx); err != nil {
		return ResolvedLaunch{}, err
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()

	selection, err := normalizeNodeSelection(request.Node)
	if err != nil {
		return ResolvedLaunch{}, err
	}
	reportLaunchPhase(ctx, lifecycle.PhaseNode)
	resolvedNode, err := resolveNode(ctx, config.NodeResolver, selection, config.Nodes)
	if err != nil {
		return ResolvedLaunch{}, err
	}
	reportLaunchPhase(ctx, lifecycle.PhaseRuntime)
	if request.RuntimeID == "" {
		return ResolvedLaunch{}, failureWithMeta(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime is required", "select an installed DSH runtime", true, false)
	}
	runtime, ok := findRuntime(config.Runtimes, request.RuntimeID)
	if !ok {
		return ResolvedLaunch{}, failureWithMeta(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime was not found", "the selected runtime is not in the catalog", true, false)
	}
	if !runtimeInstallationPresent(runtime) {
		return ResolvedLaunch{}, failureWithMeta(lifecycle.ErrorDSHRuntimeNotFound, "The selected DSH runtime is not available", "verify or install the selected runtime before starting dsh-work", true, false)
	}
	if err := verifyRuntimeWithEnvironment(ctx, config.RuntimeVerifier, runtime, resolvedNode.ChildEnvironment); err != nil {
		return ResolvedLaunch{}, err
	}
	runtime.Installed = true
	reportLaunchPhase(ctx, lifecycle.PhaseProfile)
	dataDirectory, err := resolveDataDirectoryProfile(config, request.Profile, false)
	if err != nil {
		return ResolvedLaunch{}, err
	}
	if err := verifyRuntimeProfile(ctx, config.RuntimeVerifier, runtime, dataDirectory, request.Profile); err != nil {
		return ResolvedLaunch{}, err
	}

	return ResolvedLaunch{
		Target: RunContext{
			RuntimeID: request.RuntimeID,
			Node:      selection,
			Profile:   request.Profile,
		},
		Runtime:       runtime,
		Node:          resolvedNode,
		DataDirectory: dataDirectory,
	}, nil
}

// SetConfigured validates and persists the configured Run context. A running
// Host must use its serialized switch boundary instead; changing this value
// directly while a Worker is alive would create a deferred selection.
func (m *Manager) SetConfigured(ctx context.Context, target RunContext) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	m.mu.RLock()
	busy := m.switching || m.current != nil
	m.mu.RUnlock()
	if busy {
		return Snapshot{}, failure(lifecycle.ErrorManagerOperationBusy, "the Run context is already active", "use the managed context switch while dsh-work is running")
	}
	resolved, err := m.ResolveLaunch(ctx, LaunchRequest{
		RuntimeID: target.RuntimeID,
		Node:      target.Node,
		Profile:   target.Profile,
	})
	if err != nil {
		return Snapshot{}, err
	}

	runContext := resolved.Target
	m.mu.Lock()
	state := m.stateLocked()
	state.Configured = cloneRunContext(&runContext)
	statePath := m.config.StatePath
	m.mu.Unlock()

	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.configured = cloneRunContext(&runContext)
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

// CommitCurrent publishes a Run context only after its Worker has reached
// readiness. The configured value is persisted in the same operation, so a
// failed candidate can never become the configured Run context.
func (m *Manager) CommitCurrent(ctx context.Context, target *RunContext) (Snapshot, error) {
	if target == nil {
		return Snapshot{}, errors.New("a Ready Run context is required")
	}
	resolved, err := m.ResolveLaunch(ctx, LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		return Snapshot{}, err
	}
	resolved = m.CaptureLaunchVersions(ctx, resolved)
	return m.CommitHealthy(ctx, resolved)
}

// CommitHealthy uses the exact launch that passed readiness, including pinned
// recovery executables. It never re-resolves a floating system Node at commit.
func (m *Manager) CommitHealthy(ctx context.Context, resolved ResolvedLaunch) (Snapshot, error) {
	return m.commitVersionHealthy(ctx, resolved)
}

// ClearCurrent removes the process-local current context after a Worker
// generation enters its stop boundary. The configured and known-good context
// remain available for restart or rollback.
func (m *Manager) ClearCurrent(ctx context.Context) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.current = nil
	m.mu.Unlock()
	return m.Snapshot(ctx)
}

// BeginRunContextSwitch blocks profile and configuration mutations while the
// Host performs its stop/candidate/rollback transaction.
func (m *Manager) BeginRunContextSwitch(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	if m.switching {
		m.mu.Unlock()
		return failure(lifecycle.ErrorManagerOperationBusy, "a Run context switch is already in progress", "wait for the current context switch to finish")
	}
	m.switching = true
	m.mu.Unlock()
	release, err := m.acquireOperation(ctx)
	if err != nil {
		m.mu.Lock()
		m.switching = false
		m.mu.Unlock()
		return err
	}
	release()
	return nil
}

// EndRunContextSwitch releases the mutation guard after a successful switch,
// a rollback, or a terminal recovery failure.
func (m *Manager) EndRunContextSwitch(ctx context.Context) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	m.switching = false
	m.mu.Unlock()
	return nil
}

// RecordSwitchAttempt publishes one bounded terminal result. configureTarget
// is used only after the previous Worker stopped and automatic rollback was
// disabled; it never marks the failed target current or known-good.
func (m *Manager) RecordSwitchAttempt(ctx context.Context, attempt SwitchAttempt, configureTarget bool) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	selection, err := normalizeNodeSelection(attempt.Target.Node)
	if err != nil {
		return Snapshot{}, err
	}
	attempt.Target.Node = selection
	m.mu.RLock()
	state := m.stateLocked()
	statePath := m.config.StatePath
	current := cloneRunContext(m.current)
	m.mu.RUnlock()
	if configureTarget {
		if current != nil {
			return Snapshot{}, failure(lifecycle.ErrorManagerOperationBusy, "The failed target cannot be configured while a context is current", "stop the current Worker before retaining the target")
		}
		state.Configured = cloneRunContext(&attempt.Target)
	}
	state.LastSwitchAttempt = cloneSwitchAttempt(&attempt)
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	if configureTarget {
		m.configured = cloneRunContext(&attempt.Target)
	}
	m.lastSwitchAttempt = cloneSwitchAttempt(&attempt)
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) ClearSwitchAttempt(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.RUnlock()
	state.LastSwitchAttempt = nil
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	m.mu.Lock()
	m.lastSwitchAttempt = nil
	m.mu.Unlock()
	return m.Snapshot(context.Background())
}

func (m *Manager) ensureMutationAllowed() error {
	m.mu.RLock()
	switching := m.switching
	m.mu.RUnlock()
	if switching {
		return failure(lifecycle.ErrorManagerOperationBusy, "the Run context is switching", "wait for the current context switch to finish")
	}
	return nil
}

func normalizeConfig(config Config) (Config, error) {
	if config.StateStore == nil {
		config.StateStore = FileStateStore{}
	}
	if config.ProfileReader == nil {
		config.ProfileReader = FileProfileReader{}
	}
	if config.ThemeReader == nil {
		config.ThemeReader = FileThemeReader{}
	}
	if strings.TrimSpace(config.PluginOfficialRegistry) == "" {
		config.PluginOfficialRegistry = defaultPluginOfficialRegistry
	}
	if config.PluginMirrorRegistry == "" {
		config.PluginMirrorRegistry = defaultPluginMirrorRegistry
	}
	for _, registry := range []string{config.PluginOfficialRegistry, config.PluginMirrorRegistry} {
		if registry != "" && (!strings.HasPrefix(registry, "https://") || strings.ContainsAny(registry, " \t\r\n")) {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "plugin registry configuration is invalid", "registry sources must use HTTPS")
		}
	}
	if config.StatePath == "" {
		configRoot, err := os.UserConfigDir()
		if err != nil || configRoot == "" {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "manager state directory is unavailable", "the operating system did not provide a user application-data directory")
		}
		config.StatePath = filepath.Join(configRoot, "dsh-work", "manager.json")
	}
	statePath, err := filepath.Abs(config.StatePath)
	if err != nil {
		return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "manager state path is invalid", "the state path could not be normalized")
	}
	config.StatePath = filepath.Clean(statePath)

	config.DataDirectories = cloneDataDirectories(config.DataDirectories)
	seenDataDirectories := make(map[string]struct{}, len(config.DataDirectories))
	for i := range config.DataDirectories {
		dataDirectory := &config.DataDirectories[i]
		if dataDirectory.ID == "" || dataDirectory.Path == "" {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory catalog is invalid", "every data directory needs an id and path")
		}
		if _, exists := seenDataDirectories[dataDirectory.ID]; exists {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory catalog is invalid", "data-directory ids must be unique")
		}
		seenDataDirectories[dataDirectory.ID] = struct{}{}
		dataDirectoryPath, err := filepath.Abs(dataDirectory.Path)
		if err != nil {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory catalog is invalid", "a data-directory path could not be normalized")
		}
		dataDirectory.Path = filepath.Clean(dataDirectoryPath)
		if dataDirectory.Ownership == "" {
			dataDirectory.Ownership = DataDirectoryOwnershipUser
		}
		if dataDirectory.Ownership != DataDirectoryOwnershipDSHWork && dataDirectory.Ownership != DataDirectoryOwnershipUser {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory catalog is invalid", "use dsh-work or user ownership")
		}
	}

	config.Runtimes = cloneRuntimes(config.Runtimes)
	seenRuntimes := make(map[string]struct{}, len(config.Runtimes))
	for i := range config.Runtimes {
		runtime, err := normalizeRuntime(config.Runtimes[i])
		if err != nil {
			return Config{}, err
		}
		config.Runtimes[i] = runtime
		if _, exists := seenRuntimes[runtime.ID]; exists {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime catalog is invalid", "runtime ids must be unique")
		}
		seenRuntimes[runtime.ID] = struct{}{}
	}
	config.DSHReleases = cloneDSHReleases(config.DSHReleases)
	seenDSHReleases := make(map[string]struct{}, len(config.DSHReleases))
	for _, release := range config.DSHReleases {
		if err := validateDSHRelease(release); err != nil {
			return Config{}, err
		}
		if _, exists := seenDSHReleases[release.Version]; exists {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH release catalog is invalid", "release versions must be unique")
		}
		seenDSHReleases[release.Version] = struct{}{}
	}
	config.Nodes = cloneNodes(config.Nodes)
	seenNodes := make(map[string]struct{}, len(config.Nodes))
	for index := range config.Nodes {
		node, err := normalizeNodeInstallation(config.Nodes[index])
		if err != nil {
			return Config{}, err
		}
		config.Nodes[index] = node
		if _, exists := seenNodes[node.ID]; exists {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "Node installation catalog is invalid", "installation ids must be unique")
		}
		seenNodes[node.ID] = struct{}{}
	}
	if config.DefaultRunContext.RuntimeID != "" {
		selection, err := normalizeNodeSelection(config.DefaultRunContext.Node)
		if err != nil {
			return Config{}, err
		}
		config.DefaultRunContext.Node = selection
	}
	return config, nil
}

func validateDataDirectory(dataDirectory DataDirectoryInfo) error {
	if dataDirectory.ID == "" || dataDirectory.Path == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH data directory is invalid", "a data directory needs an id and path")
	}
	if dataDirectory.Ownership != "" && dataDirectory.Ownership != DataDirectoryOwnershipDSHWork && dataDirectory.Ownership != DataDirectoryOwnershipUser {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory ownership is invalid", "use dsh-work or user ownership")
	}
	return nil
}

func normalizeDataDirectory(dataDirectory DataDirectoryInfo) (DataDirectoryInfo, error) {
	if err := validateDataDirectory(dataDirectory); err != nil {
		return DataDirectoryInfo{}, err
	}
	path, err := filepath.Abs(dataDirectory.Path)
	if err != nil {
		return DataDirectoryInfo{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory path is invalid", "the data-directory path could not be normalized")
	}
	dataDirectory.Path = filepath.Clean(path)
	if dataDirectory.Ownership == "" {
		dataDirectory.Ownership = DataDirectoryOwnershipUser
	}
	if dataDirectory.Ownership == DataDirectoryOwnershipUser {
		info, statErr := os.Stat(dataDirectory.Path)
		if statErr != nil || !info.IsDir() {
			return DataDirectoryInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "register an existing DSH data directory")
		}
	}
	return dataDirectory, nil
}

func validateRuntime(runtime RuntimeInfo) error {
	if runtime.ID == "" || runtime.Version == "" || runtime.Path == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime is invalid", "a runtime needs an id, version and path")
	}
	if runtime.Source != "" && runtime.Source != RuntimeSourceManaged && runtime.Source != RuntimeSourceDevelopmentFixture && runtime.Source != RuntimeSourceSystem {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime source is invalid", "use managed, development-fixture or system")
	}
	if !runtime.Toolchain.Valid() {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime toolchain is invalid", "use system-pnpm, system-npm, managed-node-npm or none")
	}
	if !runtime.InstallSource.Valid() {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime install source is invalid", "use local, official, mirror or none")
	}
	return nil
}

func normalizeRuntime(runtime RuntimeInfo) (RuntimeInfo, error) {
	if runtime.Toolchain == "" {
		runtime.Toolchain = RuntimeToolchainNone
	}
	if runtime.InstallSource == "" {
		runtime.InstallSource = RuntimeArtifactSourceNone
	}
	if err := validateRuntime(runtime); err != nil {
		return RuntimeInfo{}, err
	}
	path, err := filepath.Abs(runtime.Path)
	if err != nil {
		return RuntimeInfo{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime path is invalid", "the runtime path could not be normalized")
	}
	runtime.Path = filepath.Clean(path)
	if runtime.ToolchainPath != "" {
		toolchainPath, err := filepath.Abs(runtime.ToolchainPath)
		if err != nil {
			return RuntimeInfo{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime toolchain path is invalid", "the toolchain path could not be normalized")
		}
		runtime.ToolchainPath = filepath.Clean(toolchainPath)
	}
	if runtime.Source == "" {
		runtime.Source = RuntimeSourceManaged
	}
	if runtime.Source == RuntimeSourceManaged {
		runtime.Removable = true
	}
	return runtime, nil
}

func validateNodeRelease(release NodeReleaseInfo) error {
	if release.Version == "" || release.Platform == "" || release.Architecture == "" || release.Filename == "" || release.SHA256 == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "Node release metadata is invalid", "the exact version, platform, archive and checksum are required")
	}
	if release.Source != RuntimeArtifactSourceOfficial && release.Source != RuntimeArtifactSourceMirror {
		return failure(lifecycle.ErrorManagerStateInvalid, "Node release source is invalid", "use official or mirror")
	}
	return nil
}

func validateDSHRelease(release DSHReleaseInfo) error {
	if !validRuntimeVersion(release.Version) || strings.TrimSpace(release.ObservedAt) == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH release metadata is invalid", "an exact version and observation time are required")
	}
	if release.Source != RuntimeArtifactSourceOfficial && release.Source != RuntimeArtifactSourceMirror {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH release source is invalid", "use official or mirror")
	}
	for _, tag := range release.Tags {
		if strings.TrimSpace(tag) == "" {
			return failure(lifecycle.ErrorManagerStateInvalid, "DSH release tag is invalid", "release tags cannot be empty")
		}
	}
	return nil
}

func validateNodeInstallation(node NodeInstallationInfo) error {
	if node.ID == "" || node.Version == "" || node.Platform == "" || node.Architecture == "" || node.NodePath == "" || node.NPMPath == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "Node installation is invalid", "the installation identity and executable paths are required")
	}
	if node.Ownership != NodeOwnershipSystem && node.Ownership != NodeOwnershipManaged {
		return failure(lifecycle.ErrorManagerStateInvalid, "Node installation ownership is invalid", "use system or managed")
	}
	if !node.InstallSource.Valid() {
		return failure(lifecycle.ErrorManagerStateInvalid, "Node installation source is invalid", "use system, local, official or mirror")
	}
	if node.Ownership == NodeOwnershipManaged && node.SHA256 == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "Managed Node integrity is missing", "a managed installation requires its SHA256 identity")
	}
	return nil
}

func normalizeNodeSelection(selection NodeSelection) (NodeSelection, error) {
	if selection.Kind == "" {
		selection.Kind = NodeSelectionSystem
	}
	switch selection.Kind {
	case NodeSelectionSystem:
		if selection.InstallationID != "" {
			return NodeSelection{}, failure(lifecycle.ErrorManagerStateInvalid, "System Node selection is invalid", "a system selection cannot name a managed installation")
		}
	case NodeSelectionManaged:
		if selection.InstallationID == "" {
			return NodeSelection{}, failure(lifecycle.ErrorManagerStateInvalid, "Managed Node selection is incomplete", "select a managed Node installation")
		}
	default:
		return NodeSelection{}, failure(lifecycle.ErrorManagerStateInvalid, "Node selection is invalid", "use system or managed")
	}
	return selection, nil
}

func resolveNode(ctx context.Context, resolver NodeResolver, selection NodeSelection, nodes []NodeInstallationInfo) (ResolvedNode, error) {
	if resolver != nil {
		resolved, err := resolver.Resolve(ctx, selection, cloneNodes(nodes))
		if err != nil {
			return ResolvedNode{}, preserveFailure(err, lifecycle.ErrorRuntimeInstallUnavailable, "The selected Node installation is unavailable", "choose an available Node installation", true, false)
		}
		resolved.Selection = selection
		return resolved, nil
	}
	if selection.Kind == NodeSelectionSystem {
		return ResolvedNode{Selection: selection}, nil
	}
	for _, node := range nodes {
		if node.ID != selection.InstallationID {
			continue
		}
		if !runtimeExecutablePresent(node.NodePath) || !runtimeExecutablePresent(node.NPMPath) {
			break
		}
		return ResolvedNode{
			Selection: selection, Version: node.Version, NodePath: node.NodePath,
			NPMPath: node.NPMPath, PNPMPath: node.PNPMPath,
			ChildEnvironment: childEnvironmentForNode(node.NodePath),
		}, nil
	}
	return ResolvedNode{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "The selected Node installation is unavailable", "choose an installed Node version")
}

func childEnvironmentForNode(nodePath string) map[string]string {
	if strings.TrimSpace(nodePath) == "" {
		return nil
	}
	directory := filepath.Dir(nodePath)
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		return map[string]string{"PATH": directory}
	}
	return map[string]string{"PATH": directory + string(os.PathListSeparator) + pathValue}
}

func normalizeNodeInstallation(node NodeInstallationInfo) (NodeInstallationInfo, error) {
	if err := validateNodeInstallation(node); err != nil {
		return NodeInstallationInfo{}, err
	}
	for _, value := range []*string{&node.NodePath, &node.NPMPath} {
		absolute, err := filepath.Abs(*value)
		if err != nil {
			return NodeInstallationInfo{}, failure(lifecycle.ErrorManagerStateInvalid, "Node installation path is invalid", "an executable path could not be normalized")
		}
		*value = filepath.Clean(absolute)
	}
	if node.PNPMPath != "" {
		absolute, err := filepath.Abs(node.PNPMPath)
		if err != nil {
			return NodeInstallationInfo{}, failure(lifecycle.ErrorManagerStateInvalid, "Node installation path is invalid", "the pnpm path could not be normalized")
		}
		node.PNPMPath = filepath.Clean(absolute)
	}
	return node, nil
}

var runtimeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

func validRuntimeVersion(version string) bool {
	return runtimeVersionPattern.MatchString(version)
}

func (m *Manager) acquireOperation(ctx context.Context) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	m.mu.Lock()
	gate := m.operationGate
	if gate == nil {
		gate = make(chan struct{}, 1)
		m.operationGate = gate
	}
	m.mu.Unlock()
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, contextError(ctx)
	}
}

func (m *Manager) stateLocked() State {
	return State{
		SafeMode:          cloneSafeMode(m.safeMode),
		VersionRecovery:   cloneVersionRecovery(m.versionRecovery),
		LastSwitchAttempt: cloneSwitchAttempt(m.lastSwitchAttempt),
		DataDirectories:   cloneDataDirectories(m.config.DataDirectories),
		Runtimes:          cloneRuntimes(m.config.Runtimes),
		Nodes:             cloneNodes(m.config.Nodes),
		LatestNode:        cloneNodeRelease(m.latestNode),
		DSHReleases:       cloneDSHReleases(m.dshReleases),
		PluginProvenance:  append([]PluginProvenanceRecord(nil), m.pluginProvenance...),
		PluginDisables:    append([]PluginDisableRecord(nil), m.pluginDisables...),
		Configured:        cloneRunContext(m.configured),
	}
}

func mergeNodes(base, persisted []NodeInstallationInfo) []NodeInstallationInfo {
	merged := cloneNodes(base)
	for _, node := range persisted {
		found := false
		for index := range merged {
			if merged[index].ID == node.ID {
				merged[index] = node
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, node)
		}
	}
	return merged
}

func (m *Manager) configSnapshotLocked() Config {
	config := m.config
	config.DataDirectories = cloneDataDirectories(m.config.DataDirectories)
	config.Runtimes = cloneRuntimes(m.config.Runtimes)
	config.DSHReleases = cloneDSHReleases(m.dshReleases)
	config.Nodes = cloneNodes(m.config.Nodes)
	return config
}

func mergeDataDirectories(base, persisted []DataDirectoryInfo) []DataDirectoryInfo {
	merged := cloneDataDirectories(base)
	for _, dataDirectory := range persisted {
		found := false
		for i := range merged {
			if merged[i].ID == dataDirectory.ID {
				merged[i] = dataDirectory
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, dataDirectory)
		}
	}
	return merged
}

func mergeRuntimes(base, persisted []RuntimeInfo) []RuntimeInfo {
	merged := cloneRuntimes(base)
	for _, runtime := range persisted {
		found := false
		for i := range merged {
			if merged[i].ID == runtime.ID {
				merged[i] = runtime
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, runtime)
		}
	}
	return merged
}

func discoverProfiles(ctx context.Context, dataDirectories []DataDirectoryInfo, catalog ProfileCatalog, reader ProfileReader, current, configured, knownGood *RunContext, lastSwitchAttempt *SwitchAttempt) []ProfileInfo {
	profiles := make([]ProfileInfo, 0)
	definitions := profileDefinitions(catalog)
	for _, dataDirectory := range dataDirectories {
		profileRoot := filepath.Join(dataDirectory.Path, "profiles")
		entries, err := os.ReadDir(profileRoot)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || !validProfileName(entry.Name()) || strings.EqualFold(entry.Name(), "node_modules") {
					continue
				}
				if definition, ok := findProfileDefinition(definitions, entry.Name()); ok && definition.DesktopUnsupported {
					continue
				}
				profiles = append(profiles, profileInfo(ctx, dataDirectory, entry.Name(), true, definitions, reader, current, configured, knownGood, lastSwitchAttempt))
			}
		}
		for _, definition := range definitions {
			if !validProfileName(definition.Name) || definition.DesktopUnsupported {
				continue
			}
			if !containsProfile(profiles, dataDirectory.ID, definition.Name) {
				profiles = append(profiles, profileInfo(ctx, dataDirectory, definition.Name, false, definitions, reader, current, configured, knownGood, lastSwitchAttempt))
			}
		}
	}
	return profiles
}

func profileInfo(ctx context.Context, dataDirectory DataDirectoryInfo, name string, exists bool, definitions []ProfileDefinition, reader ProfileReader, current, configured, knownGood *RunContext, lastSwitchAttempt *SwitchAttempt) ProfileInfo {
	kind := ProfileKindCustom
	autoInitialize := false
	desktopUnsupported := false
	if definition, ok := findProfileDefinition(definitions, name); ok {
		kind = definition.Kind
		if kind == "" {
			kind = ProfileKindBuiltIn
		}
		autoInitialize = definition.AutoInitialize
		desktopUnsupported = definition.DesktopUnsupported
	}
	plugins := []PluginInfo(nil)
	isCurrent := current != nil && current.Profile == (ProfileRef{DataDirectoryID: dataDirectory.ID, Name: name})
	profileRef := ProfileRef{DataDirectoryID: dataDirectory.ID, Name: name}
	isFailedTarget := lastSwitchAttempt != nil && lastSwitchAttempt.Target.Profile == profileRef
	isProtected := isCurrent || profileRefMatches(configured, profileRef) || profileRefMatches(knownGood, profileRef) || isFailedTarget
	if exists && isCurrent && reader != nil {
		if projected, err := reader.Read(ctx, filepath.Join(dataDirectory.Path, "profiles", name)); err == nil {
			plugins = projected
		}
	}
	return ProfileInfo{
		Ref:            ProfileRef{DataDirectoryID: dataDirectory.ID, Name: name},
		Path:           filepath.Join(dataDirectory.Path, "profiles", name),
		Exists:         exists,
		Kind:           kind,
		Launchable:     !desktopUnsupported,
		Renamable:      exists && kind == ProfileKindCustom,
		Deletable:      exists && kind == ProfileKindCustom && !isProtected,
		AutoInitialize: autoInitialize,
		PluginCount:    len(plugins),
		Plugins:        plugins,
	}
}

func (m *Manager) profilePlugins(ctx context.Context, profilePath string) ([]PluginInfo, error) {
	m.mu.RLock()
	reader := m.config.ProfileReader
	m.mu.RUnlock()
	if reader == nil {
		return []PluginInfo{}, nil
	}
	plugins, err := reader.Read(ctx, profilePath)
	if err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return nil, cancellation
		}
		return nil, failure(lifecycle.ErrorProfileInvalid, "DSH profile could not be read", "the profile package manifest is unavailable or invalid")
	}
	return plugins, nil
}

func validPackageName(name string) bool {
	if name == "" || strings.ContainsRune(name, '\x00') || filepath.IsAbs(name) {
		return false
	}
	for _, part := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return !strings.ContainsAny(name, "\r\n\t")
}

func validatePluginSpec(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" || len(spec) > 256 || strings.HasPrefix(spec, "-") || strings.ContainsRune(spec, '\x00') || strings.IndexFunc(spec, unicode.IsSpace) >= 0 {
		return failure(lifecycle.ErrorPluginSpecInvalid, "plugin package spec is invalid", "use one package name or versioned package spec")
	}
	return nil
}

func profileExists(catalog ProfileCatalog, dataDirectory DataDirectoryInfo, name string) bool {
	if isBuiltInProfile(catalog, name) {
		return true
	}
	info, err := os.Stat(filepath.Join(dataDirectory.Path, "profiles", name))
	return err == nil && info.IsDir()
}

func directoryExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func isBuiltInProfile(catalog ProfileCatalog, name string) bool {
	_, ok := findProfileDefinition(profileDefinitions(catalog), name)
	return ok
}

func profileDefinitions(catalog ProfileCatalog) []ProfileDefinition {
	if catalog == nil {
		return nil
	}
	definitions := catalog.BuiltInProfiles()
	return append([]ProfileDefinition(nil), definitions...)
}

func findProfileDefinition(definitions []ProfileDefinition, name string) (ProfileDefinition, bool) {
	for _, definition := range definitions {
		if definition.Name == name {
			return definition, true
		}
	}
	return ProfileDefinition{}, false
}

func (m *Manager) profileCatalog() ProfileCatalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.config.ProfileCatalog
}

func containsProfile(profiles []ProfileInfo, dataDirectoryID, name string) bool {
	for _, profile := range profiles {
		if profile.Ref.DataDirectoryID == dataDirectoryID && profile.Ref.Name == name {
			return true
		}
	}
	return false
}

func validateProfileRef(ref ProfileRef) error {
	if ref.DataDirectoryID == "" || ref.Name == "" {
		return failure(lifecycle.ErrorProfileRequired, "a DSH profile is required", "select a DSH data directory and profile")
	}
	if !validProfileName(ref.Name) {
		return failure(lifecycle.ErrorProfileInvalid, "DSH profile name is invalid", "profile names cannot contain path separators")
	}
	return nil
}

func validProfileName(name string) bool {
	if name == "" || name == "." || name == ".." || name == "node_modules" || strings.IndexByte(name, 0) >= 0 {
		return false
	}
	return filepath.Base(name) == name && !strings.ContainsAny(name, `/\\`)
}

func findDataDirectory(dataDirectories []DataDirectoryInfo, id string) (DataDirectoryInfo, bool) {
	for _, dataDirectory := range dataDirectories {
		if dataDirectory.ID == id {
			return dataDirectory, true
		}
	}
	return DataDirectoryInfo{}, false
}

func resolveDataDirectoryProfile(config Config, ref ProfileRef, allowCreate bool) (DataDirectoryInfo, error) {
	if err := validateProfileRef(ref); err != nil {
		return DataDirectoryInfo{}, err
	}
	dataDirectory, ok := findDataDirectory(config.DataDirectories, ref.DataDirectoryID)
	if !ok {
		return DataDirectoryInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the profile data directory is not in the catalog")
	}
	if dataDirectory.Ownership == DataDirectoryOwnershipUser && !directoryExists(dataDirectory.Path) {
		return DataDirectoryInfo{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable", "register an existing DSH data directory")
	}
	if !allowCreate && !profileExists(config.ProfileCatalog, dataDirectory, ref.Name) && !isBuiltInProfile(config.ProfileCatalog, ref.Name) {
		return DataDirectoryInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "create or select an existing profile")
	}
	return dataDirectory, nil
}

func findRuntime(runtimes []RuntimeInfo, id string) (RuntimeInfo, bool) {
	for _, runtime := range runtimes {
		if runtime.ID == id {
			return runtime, true
		}
	}
	return RuntimeInfo{}, false
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return failure(lifecycle.ErrorCancelled, "manager operation was cancelled", "the operation did not complete")
	}
	return nil
}

func failure(code lifecycle.ErrorCode, summary, detail string) error {
	return failureWithMeta(code, summary, detail, false, false)
}

func failureWithMeta(code lifecycle.ErrorCode, summary, detail string, retryable, effectOccurred bool) error {
	return lifecycle.Failure{
		Code:           code,
		Summary:        summary,
		Detail:         detail,
		Retryable:      retryable,
		EffectOccurred: effectOccurred,
		CorrelationID:  lifecycle.NewCorrelationID(),
	}
}

func verifyRuntime(ctx context.Context, verifier RuntimeVerifier, runtime RuntimeInfo) error {
	return verifyRuntimeWithEnvironment(ctx, verifier, runtime, runtimeChildEnvironment(runtime))
}

func verifyRuntimeWithEnvironment(ctx context.Context, verifier RuntimeVerifier, runtime RuntimeInfo, environment map[string]string) error {
	if verifier == nil {
		return nil
	}
	var err error
	if environmentVerifier, ok := verifier.(RuntimeVerifierWithEnvironment); ok {
		err = environmentVerifier.VerifyWithEnvironment(ctx, runtime.Path, runtime.Version, environment)
	} else {
		err = verifier.Verify(ctx, runtime.Path, runtime.Version)
	}
	if err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return cancellation
		}
		return preserveFailure(err, lifecycle.ErrorDSHVersionCheckFailed, "The selected DSH runtime could not be verified.", "the DSH adapter rejected the selected runtime", false, false)
	}
	return nil
}

func runtimeChildEnvironment(runtime RuntimeInfo) map[string]string {
	if strings.TrimSpace(runtime.ToolchainPath) == "" {
		return nil
	}
	pathValue := os.Getenv("PATH")
	if pathValue == "" {
		return map[string]string{"PATH": runtime.ToolchainPath}
	}
	return map[string]string{"PATH": runtime.ToolchainPath + string(os.PathListSeparator) + pathValue}
}

func verifyRuntimeProfile(ctx context.Context, verifier RuntimeVerifier, runtime RuntimeInfo, dataDirectory DataDirectoryInfo, profile ProfileRef) error {
	pairVerifier, ok := verifier.(RuntimeProfileVerifier)
	if !ok {
		return nil
	}
	if err := pairVerifier.VerifyProfile(ctx, runtime.Path, runtime.Version, dataDirectory.Path, profile.Name); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return cancellation
		}
		return preserveFailure(err, lifecycle.ErrorRuntimeProfileIncompatible, "The selected DSH runtime and profile are incompatible.", "the DSH adapter rejected this Run context pairing", false, false)
	}
	return nil
}

func preserveFailure(err error, fallbackCode lifecycle.ErrorCode, fallbackSummary, fallbackDetail string, retryable, effectOccurred bool) error {
	var failureValue lifecycle.Failure
	if errors.As(err, &failureValue) {
		if failureValue.CorrelationID == "" {
			failureValue.CorrelationID = lifecycle.NewCorrelationID()
		}
		return failureValue
	}
	var failurePointer *lifecycle.Failure
	if errors.As(err, &failurePointer) && failurePointer != nil {
		value := *failurePointer
		if value.CorrelationID == "" {
			value.CorrelationID = lifecycle.NewCorrelationID()
		}
		return value
	}
	return failureWithMeta(fallbackCode, fallbackSummary, fallbackDetail, retryable, effectOccurred)
}

func cloneRunContext(target *RunContext) *RunContext {
	if target == nil {
		return nil
	}
	copy := *target
	return &copy
}

func cloneDataDirectories(dataDirectories []DataDirectoryInfo) []DataDirectoryInfo {
	if dataDirectories == nil {
		return nil
	}
	return append([]DataDirectoryInfo(nil), dataDirectories...)
}

func cloneRuntimes(runtimes []RuntimeInfo) []RuntimeInfo {
	if runtimes == nil {
		return nil
	}
	return append([]RuntimeInfo(nil), runtimes...)
}

func cloneNodes(nodes []NodeInstallationInfo) []NodeInstallationInfo {
	if nodes == nil {
		return nil
	}
	return append([]NodeInstallationInfo(nil), nodes...)
}

func cloneNodeRelease(release *NodeReleaseInfo) *NodeReleaseInfo {
	if release == nil {
		return nil
	}
	copy := *release
	return &copy
}

func cloneDSHReleases(releases []DSHReleaseInfo) []DSHReleaseInfo {
	if releases == nil {
		return nil
	}
	cloned := make([]DSHReleaseInfo, len(releases))
	for index, release := range releases {
		cloned[index] = release
		cloned[index].Tags = append([]string(nil), release.Tags...)
	}
	return cloned
}

func cloneSwitchAttempt(attempt *SwitchAttempt) *SwitchAttempt {
	if attempt == nil {
		return nil
	}
	copy := *attempt
	if attempt.RollbackFailure != nil {
		failure := *attempt.RollbackFailure
		copy.RollbackFailure = &failure
	}
	return &copy
}

func refreshNodes(nodes []NodeInstallationInfo) []NodeInstallationInfo {
	refreshed := cloneNodes(nodes)
	for index := range refreshed {
		refreshed[index].Installed = runtimeExecutablePresent(refreshed[index].NodePath) && runtimeExecutablePresent(refreshed[index].NPMPath)
		refreshed[index].Verified = refreshed[index].Verified && refreshed[index].Installed
	}
	return refreshed
}

func refreshRuntimes(runtimes []RuntimeInfo) []RuntimeInfo {
	refreshed := cloneRuntimes(runtimes)
	for i := range refreshed {
		refreshed[i].Installed = runtimeInstallationPresent(refreshed[i])
	}
	return refreshed
}

func runtimeExecutablePresent(path string) bool {
	if path == "" {
		return false
	}
	if info, err := os.Stat(path); err == nil {
		return !info.IsDir()
	}
	_, err := exec.LookPath(path)
	return err == nil
}

func runtimeInstallationPresent(runtime RuntimeInfo) bool {
	if !runtimeExecutablePresent(runtime.Path) {
		return false
	}
	// The tracked development shim is present in a fresh worktree even when
	// its untracked npm installation has never been prepared.
	if runtime.Source == RuntimeSourceDevelopmentFixture && strings.EqualFold(filepath.Base(runtime.Path), "run-dsh.cmd") {
		return runtimeExecutablePresent(filepath.Join(filepath.Dir(runtime.Path), "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"))
	}
	return true
}
