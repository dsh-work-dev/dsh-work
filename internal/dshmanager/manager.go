package dshmanager

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"unicode"

	"github.com/local/work/internal/lifecycle"
)

const stateVersion = 1

// Config supplies discovered homes and runtimes to the manager. Discovery
// and installation are separate concerns; this first slice keeps the catalog
// injectable so the same domain contract can be used by the GUI and CLI.
type Config struct {
	StatePath        string
	WorkspaceRoot    string
	Homes            []HomeInfo
	Runtimes         []RuntimeInfo
	DefaultSelection LaunchSelection
	CommandRunner    CommandRunner
	PluginCommands   PluginCommandBuilder
	RuntimeInstaller RuntimeInstaller
	RuntimeVerifier  RuntimeVerifier
	StateStore       StateStore
	ProfileCatalog   ProfileCatalog
	ProfileReader    ProfileReader
	ThemeReader      ThemeReader
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

// RuntimeVerifier is implemented by the selected DSH adapter. The manager
// owns catalog identity and presence checks; only the adapter can verify that
// an executable speaks the DSH contract expected by this Work build.
type RuntimeVerifier interface {
	Verify(context.Context, string, string) error
}

// PluginCommandBuilder keeps DSH CLI grammar behind the DSH adapter. The
// manager supplies an explicit ProfileRef and operation intent, but does not
// construct version-specific command lines itself.
type PluginCommandBuilder interface {
	Install(string, string) ([]string, error)
	Remove(string, string) ([]string, error)
}

// Manager owns desired/active selection state and performs cross-platform
// validation. It has no Wails or operating-system-specific process logic.
type Manager struct {
	mu            sync.RWMutex
	config        Config
	desired       *LaunchSelection
	active        *LaunchSelection
	operationGate chan struct{}
	store         StateStore
}

// New creates a manager from the current catalog and restores only the
// persisted desired selection. Active state is necessarily process-local and
// is never restored as if a DSH worker were still alive.
func New(config Config) (*Manager, error) {
	normalized, err := normalizeConfig(config)
	if err != nil {
		return nil, err
	}

	manager := &Manager{
		config:        normalized,
		store:         normalized.StateStore,
		operationGate: make(chan struct{}, 1),
	}
	state, err := manager.store.Load(context.Background(), normalized.StatePath)
	if err != nil {
		return nil, err
	}
	if state != nil {
		if len(state.Homes) > 0 {
			normalized.Homes = mergeHomes(normalized.Homes, state.Homes)
		}
		if len(state.Runtimes) > 0 {
			normalized.Runtimes = mergeRuntimes(normalized.Runtimes, state.Runtimes)
		}
		normalized, err = normalizeConfig(normalized)
		if err != nil {
			return nil, err
		}
	}
	manager.config = normalized
	if state != nil && state.Desired != nil {
		selection := *state.Desired
		manager.desired = &selection
	} else if normalized.DefaultSelection.RuntimeID != "" {
		selection := normalized.DefaultSelection
		manager.desired = &selection
	}
	return manager, nil
}

func (m *Manager) RegisterRuntime(ctx context.Context, runtime RuntimeInfo) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	return m.registerRuntime(ctx, runtime)
}

func (m *Manager) registerRuntime(ctx context.Context, runtime RuntimeInfo) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if err := validateRuntime(runtime); err != nil {
		return Snapshot{}, err
	}
	if runtime.Source == "" {
		runtime.Source = RuntimeSourceManaged
	}
	if runtime.Source == RuntimeSourceManaged && !runtime.Removable {
		runtime.Removable = true
	}
	m.mu.Lock()
	updated := false
	for i := range m.config.Runtimes {
		if m.config.Runtimes[i].ID == runtime.ID {
			m.config.Runtimes[i] = runtime
			updated = true
			break
		}
	}
	if !updated {
		m.config.Runtimes = append(m.config.Runtimes, runtime)
	}
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.Unlock()
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}

func (m *Manager) InstallRuntime(ctx context.Context, version string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	if !validRuntimeVersion(version) {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallFailed, "DSH runtime version is invalid", "use a semantic version such as 0.1.2-alpha.3")
	}
	m.mu.RLock()
	installer := m.config.RuntimeInstaller
	m.mu.RUnlock()
	if installer == nil {
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInstallUnavailable, "runtime installation is unavailable", "this platform has no native runtime installer")
	}
	runtime, err := installer.Install(ctx, version)
	if err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return Snapshot{}, cancellation
		}
		return Snapshot{}, failureWithMeta(lifecycle.ErrorRuntimeInstallFailed, "DSH runtime installation failed", "the explicit runtime install operation did not complete", true, true)
	}
	if runtime.ID == "" {
		runtime.ID = "dsh-" + version
	}
	if runtime.Version == "" {
		runtime.Version = version
	}
	if runtime.Source == "" {
		runtime.Source = RuntimeSourceManaged
	}
	runtime.Installed = true
	runtime.Removable = true
	return m.registerRuntime(ctx, runtime)
}

func (m *Manager) RemoveRuntime(ctx context.Context, id string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	m.mu.Lock()
	index := -1
	for i := range m.config.Runtimes {
		if m.config.Runtimes[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime was not found", "the runtime is not in the catalog")
	}
	if (m.desired != nil && m.desired.RuntimeID == id) || (m.active != nil && m.active.RuntimeID == id) {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "DSH runtime is still selected", "choose another runtime before removing it")
	}
	if !m.config.Runtimes[index].Removable {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorRuntimeInUse, "DSH runtime is managed by the development fixture", "the pinned development runtime cannot be removed")
	}
	m.config.Runtimes = append(m.config.Runtimes[:index], m.config.Runtimes[index+1:]...)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.Unlock()
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}

func (m *Manager) RegisterHome(ctx context.Context, home HomeInfo) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	normalizedHome, err := normalizeHome(home)
	if err != nil {
		return Snapshot{}, err
	}
	home = normalizedHome
	m.mu.Lock()
	updated := false
	for i := range m.config.Homes {
		if m.config.Homes[i].ID == home.ID {
			m.config.Homes[i] = home
			updated = true
			break
		}
	}
	if !updated {
		m.config.Homes = append(m.config.Homes, home)
	}
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.Unlock()
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}

func (m *Manager) RemoveHome(ctx context.Context, id string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	m.mu.Lock()
	index := -1
	for i := range m.config.Homes {
		if m.config.Homes[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH home was not found", "the home is not in the catalog")
	}
	if (m.desired != nil && m.desired.Profile.HomeID == id) || (m.active != nil && m.active.Profile.HomeID == id) {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "DSH home is still selected", "choose another profile before removing it")
	}
	if m.config.Homes[index].Ownership == HomeOwnershipWork {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "the Work DSH home cannot be removed here", "remove the home through an explicit data-management flow")
	}
	m.config.Homes = append(m.config.Homes[:index], m.config.Homes[index+1:]...)
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.Unlock()
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}

func (m *Manager) Snapshot(ctx context.Context) (Snapshot, error) {
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	desired := cloneSelection(m.desired)
	active := cloneSelection(m.active)
	m.mu.RUnlock()

	profiles := discoverProfiles(ctx, config.Homes, config.ProfileCatalog, config.ProfileReader)
	return Snapshot{
		Runtimes: refreshRuntimes(config.Runtimes),
		Homes:    cloneHomes(config.Homes),
		Profiles: profiles,
		Desired:  desired,
		Active:   active,
		Theme:    selectedTheme(ctx, config.Homes, active, desired, config.ThemeReader),
	}, nil
}

// Theme returns the selected DSH home's appearance preference without
// discovering the runtime, home and profile catalogs.
func (m *Manager) Theme(ctx context.Context) (ThemePreference, error) {
	if err := contextError(ctx); err != nil {
		return ThemePreferenceSystem, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	active := cloneSelection(m.active)
	desired := cloneSelection(m.desired)
	m.mu.RUnlock()
	return selectedTheme(ctx, config.Homes, active, desired, config.ThemeReader), nil
}

func (m *Manager) ListPlugins(ctx context.Context, request PluginListRequest) ([]PluginInfo, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	home, _, err := m.resolvePluginTarget(ctx, request.Target, false)
	if err != nil {
		return nil, err
	}
	profilePath := filepath.Join(home.Path, "profiles", request.Target.Profile.Name)
	catalog := m.profileCatalog()
	if !profileExists(catalog, home, request.Target.Profile.Name) {
		if isBuiltInProfile(catalog, request.Target.Profile.Name) {
			return []PluginInfo{}, nil
		}
		return nil, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "create or select an existing profile")
	}
	plugins, err := m.profilePlugins(ctx, profilePath)
	if err != nil {
		return nil, err
	}
	return plugins, nil
}

// InstallPlugin delegates profile composition to DSH's supported plugin
// command. Work validates the target and package spec, but never edits DSH
// manifests or runs pnpm directly.
func (m *Manager) InstallPlugin(ctx context.Context, request PluginInstallRequest) (PluginResult, error) {
	return m.runPluginCommand(ctx, request.Target, request.Package, "add")
}

func (m *Manager) RemovePlugin(ctx context.Context, request PluginRemoveRequest) (PluginResult, error) {
	return m.runPluginCommand(ctx, request.Target, request.Package, "remove")
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
	home, runtime, err := m.resolvePluginTarget(ctx, target, operation == "add")
	if err != nil {
		return PluginResult{}, err
	}
	m.mu.RLock()
	runner := m.config.CommandRunner
	commands := m.config.PluginCommands
	active := cloneSelection(m.active)
	m.mu.RUnlock()
	if runner == nil || commands == nil {
		return PluginResult{}, failure(lifecycle.ErrorPluginCommandUnavailable, "The DSH plugin command is unavailable", "run this operation on a platform with a native command adapter")
	}
	var args []string
	if operation == "add" {
		args, err = commands.Install(target.Profile.Name, packageSpec)
	} else {
		args, err = commands.Remove(target.Profile.Name, packageSpec)
	}
	if err != nil {
		return PluginResult{}, failure(lifecycle.ErrorPluginSpecInvalid, "plugin package spec is invalid", "use one package name or versioned package spec")
	}
	if _, err = runner.Run(ctx, runtime.Path, args, map[string]string{"DSH_HOME": home.Path}, home.Path); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return PluginResult{}, cancellation
		}
		return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginCommandFailed, "The DSH plugin operation failed", "DSH rejected the profile plugin operation", true, true)
	}
	plugins, err := m.profilePlugins(ctx, filepath.Join(home.Path, "profiles", target.Profile.Name))
	if err != nil {
		return PluginResult{}, err
	}
	restartRequired := active != nil && active.Profile == target.Profile
	return PluginResult{Profile: target.Profile, Plugins: plugins, RestartRequired: restartRequired}, nil
}

func (m *Manager) resolvePluginTarget(ctx context.Context, target PluginTarget, allowCreate bool) (HomeInfo, RuntimeInfo, error) {
	if err := contextError(ctx); err != nil {
		return HomeInfo{}, RuntimeInfo{}, err
	}
	if err := validateProfileRef(target.Profile); err != nil {
		return HomeInfo{}, RuntimeInfo{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	desired := cloneSelection(m.desired)
	m.mu.RUnlock()
	home, ok := findHome(config.Homes, target.Profile.HomeID)
	if !ok {
		return HomeInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH home was not found", "the profile home is not in the catalog")
	}
	if home.Ownership == HomeOwnershipUser && !directoryExists(home.Path) {
		return HomeInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH home is unavailable", "register an existing DSH home directory")
	}
	if !allowCreate && !profileExists(config.ProfileCatalog, home, target.Profile.Name) && !isBuiltInProfile(config.ProfileCatalog, target.Profile.Name) {
		return HomeInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "create or select an existing profile")
	}
	runtimeID := target.RuntimeID
	if runtimeID == "" && desired != nil {
		runtimeID = desired.RuntimeID
	}
	runtime, ok := findRuntime(config.Runtimes, runtimeID)
	if !ok {
		return HomeInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime was not found", "select a runtime for this plugin operation")
	}
	if !runtimeExecutablePresent(runtime.Path) {
		return HomeInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "The selected DSH runtime is not available", "verify or install the selected runtime before changing plugins")
	}
	if err := verifyRuntime(ctx, config.RuntimeVerifier, runtime); err != nil {
		return HomeInfo{}, RuntimeInfo{}, err
	}
	runtime.Installed = true
	return home, runtime, nil
}

// Resolve validates a complete runtime + home + profile pairing. It returns a
// normalized selection suitable for the Host launch boundary.
func (m *Manager) Resolve(ctx context.Context, request LaunchRequest) (LaunchSelection, error) {
	resolved, err := m.ResolveLaunch(ctx, request)
	if err != nil {
		return LaunchSelection{}, err
	}
	return resolved.Selection, nil
}

// ResolveLaunch validates and returns the complete immutable launch tuple.
// Keeping the catalog records in this result prevents callers from resolving
// a selection and then accidentally pairing it with a different home/runtime.
func (m *Manager) ResolveLaunch(ctx context.Context, request LaunchRequest) (ResolvedLaunch, error) {
	if err := contextError(ctx); err != nil {
		return ResolvedLaunch{}, err
	}
	if err := validateProfileRef(request.Profile); err != nil {
		return ResolvedLaunch{}, err
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()

	if request.RuntimeID == "" {
		return ResolvedLaunch{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime is required", "select an installed DSH runtime")
	}
	runtime, ok := findRuntime(config.Runtimes, request.RuntimeID)
	if !ok {
		return ResolvedLaunch{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime was not found", "the selected runtime is not in the catalog")
	}
	if !runtimeExecutablePresent(runtime.Path) {
		return ResolvedLaunch{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "The selected DSH runtime is not available", "verify or install the selected runtime before starting Work")
	}
	if err := verifyRuntime(ctx, config.RuntimeVerifier, runtime); err != nil {
		return ResolvedLaunch{}, err
	}
	runtime.Installed = true
	home, ok := findHome(config.Homes, request.Profile.HomeID)
	if !ok {
		return ResolvedLaunch{}, failure(lifecycle.ErrorProfileNotFound, "DSH home was not found", "the profile home is not in the catalog")
	}
	if home.Ownership == HomeOwnershipUser && !directoryExists(home.Path) {
		return ResolvedLaunch{}, failure(lifecycle.ErrorProfileNotFound, "The selected user DSH home is unavailable", "register an existing DSH home directory")
	}
	if !profileExists(config.ProfileCatalog, home, request.Profile.Name) {
		return ResolvedLaunch{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "create or select an existing profile")
	}

	workspace := request.Workspace
	if workspace == "" {
		workspace = config.WorkspaceRoot
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return ResolvedLaunch{}, failure(lifecycle.ErrorManagerStateInvalid, "workspace path is invalid", "the workspace path could not be normalized")
	}
	return ResolvedLaunch{
		Selection: LaunchSelection{
			RuntimeID: request.RuntimeID,
			Profile:   request.Profile,
			Workspace: filepath.Clean(workspace),
		},
		Runtime: runtime,
		Home:    home,
	}, nil
}

// SetDesired validates and persists the next launch selection. It does not
// start or restart DSH; applying it to a running Host is an explicit caller
// decision and is reported separately by the lifecycle layer.
func (m *Manager) SetDesired(ctx context.Context, selection LaunchSelection) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	resolved, err := m.Resolve(ctx, LaunchRequest{
		RuntimeID: selection.RuntimeID,
		Profile:   selection.Profile,
		Workspace: selection.Workspace,
	})
	if err != nil {
		return Snapshot{}, err
	}

	m.mu.Lock()
	m.desired = &resolved
	state := m.stateLocked()
	state.Desired = cloneSelection(&resolved)
	statePath := m.config.StatePath
	m.mu.Unlock()

	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}

// MarkActive records the selection that the Host actually launched. It is
// intentionally not persisted: a process restart must never report an old
// worker as active.
func (m *Manager) MarkActive(ctx context.Context, selection *LaunchSelection) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	var active *LaunchSelection
	if selection != nil {
		resolved, err := m.Resolve(ctx, LaunchRequest{
			RuntimeID: selection.RuntimeID,
			Profile:   selection.Profile,
			Workspace: selection.Workspace,
		})
		if err != nil {
			return Snapshot{}, err
		}
		active = &resolved
	}
	m.mu.Lock()
	m.active = active
	m.mu.Unlock()
	return m.Snapshot(ctx)
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
	if config.WorkspaceRoot == "" {
		workspace, err := os.Getwd()
		if err != nil {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "workspace root is unavailable", "the current directory could not be determined")
		}
		config.WorkspaceRoot = workspace
	}
	workspace, err := filepath.Abs(config.WorkspaceRoot)
	if err != nil {
		return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "workspace root is invalid", "the workspace root could not be normalized")
	}
	config.WorkspaceRoot = filepath.Clean(workspace)

	if config.StatePath == "" {
		configRoot, err := os.UserConfigDir()
		if err != nil || configRoot == "" {
			configRoot = config.WorkspaceRoot
		}
		config.StatePath = filepath.Join(configRoot, "Work", "dsh-work", "manager.json")
	}
	statePath, err := filepath.Abs(config.StatePath)
	if err != nil {
		return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "manager state path is invalid", "the state path could not be normalized")
	}
	config.StatePath = filepath.Clean(statePath)

	config.Homes = cloneHomes(config.Homes)
	seenHomes := make(map[string]struct{}, len(config.Homes))
	for i := range config.Homes {
		home := &config.Homes[i]
		if home.ID == "" || home.Path == "" {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH home catalog is invalid", "every home needs an id and path")
		}
		if _, exists := seenHomes[home.ID]; exists {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH home catalog is invalid", "home ids must be unique")
		}
		seenHomes[home.ID] = struct{}{}
		homePath, err := filepath.Abs(home.Path)
		if err != nil {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH home catalog is invalid", "a home path could not be normalized")
		}
		home.Path = filepath.Clean(homePath)
		if home.Ownership == "" {
			home.Ownership = HomeOwnershipUser
		}
		if home.Ownership != HomeOwnershipWork && home.Ownership != HomeOwnershipUser {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH home catalog is invalid", "use work or user ownership")
		}
	}

	config.Runtimes = cloneRuntimes(config.Runtimes)
	seenRuntimes := make(map[string]struct{}, len(config.Runtimes))
	for i := range config.Runtimes {
		runtime := &config.Runtimes[i]
		if runtime.ID == "" || runtime.Version == "" || runtime.Path == "" {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime catalog is invalid", "every runtime needs an id, version and path")
		}
		if _, exists := seenRuntimes[runtime.ID]; exists {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime catalog is invalid", "runtime ids must be unique")
		}
		seenRuntimes[runtime.ID] = struct{}{}
		if runtime.Source == "" {
			runtime.Source = RuntimeSourceManaged
		}
	}
	return config, nil
}

func validateHome(home HomeInfo) error {
	if home.ID == "" || home.Path == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH home is invalid", "a home needs an id and path")
	}
	if home.Ownership != "" && home.Ownership != HomeOwnershipWork && home.Ownership != HomeOwnershipUser {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH home ownership is invalid", "use work or user ownership")
	}
	return nil
}

func normalizeHome(home HomeInfo) (HomeInfo, error) {
	if err := validateHome(home); err != nil {
		return HomeInfo{}, err
	}
	path, err := filepath.Abs(home.Path)
	if err != nil {
		return HomeInfo{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH home path is invalid", "the home path could not be normalized")
	}
	home.Path = filepath.Clean(path)
	if home.Ownership == "" {
		home.Ownership = HomeOwnershipUser
	}
	if home.Ownership == HomeOwnershipUser {
		info, statErr := os.Stat(home.Path)
		if statErr != nil || !info.IsDir() {
			return HomeInfo{}, failure(lifecycle.ErrorProfileNotFound, "DSH home was not found", "register an existing DSH home directory")
		}
	}
	return home, nil
}

func validateRuntime(runtime RuntimeInfo) error {
	if runtime.ID == "" || runtime.Version == "" || runtime.Path == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH runtime is invalid", "a runtime needs an id, version and path")
	}
	return nil
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
		Version:  stateVersion,
		Homes:    cloneHomes(m.config.Homes),
		Runtimes: cloneRuntimes(m.config.Runtimes),
		Desired:  cloneSelection(m.desired),
	}
}

func (m *Manager) configSnapshotLocked() Config {
	config := m.config
	config.Homes = cloneHomes(m.config.Homes)
	config.Runtimes = cloneRuntimes(m.config.Runtimes)
	return config
}

func mergeHomes(base, persisted []HomeInfo) []HomeInfo {
	merged := cloneHomes(base)
	for _, home := range persisted {
		found := false
		for i := range merged {
			if merged[i].ID == home.ID {
				merged[i] = home
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, home)
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

func discoverProfiles(ctx context.Context, homes []HomeInfo, catalog ProfileCatalog, reader ProfileReader) []ProfileInfo {
	profiles := make([]ProfileInfo, 0)
	definitions := profileDefinitions(catalog)
	for _, home := range homes {
		profileRoot := filepath.Join(home.Path, "profiles")
		entries, err := os.ReadDir(profileRoot)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || !validProfileName(entry.Name()) {
					continue
				}
				profiles = append(profiles, profileInfo(ctx, home, entry.Name(), true, definitions, reader))
			}
		}
		for _, definition := range definitions {
			if !validProfileName(definition.Name) {
				continue
			}
			if !containsProfile(profiles, home.ID, definition.Name) {
				profiles = append(profiles, profileInfo(ctx, home, definition.Name, false, definitions, reader))
			}
		}
	}
	return profiles
}

func profileInfo(ctx context.Context, home HomeInfo, name string, exists bool, definitions []ProfileDefinition, reader ProfileReader) ProfileInfo {
	kind := ProfileKindCustom
	autoInitialize := false
	if definition, ok := findProfileDefinition(definitions, name); ok {
		kind = definition.Kind
		if kind == "" {
			kind = ProfileKindBuiltIn
		}
		autoInitialize = definition.AutoInitialize
	}
	plugins := []PluginInfo(nil)
	if exists && reader != nil {
		if projected, err := reader.Read(ctx, filepath.Join(home.Path, "profiles", name)); err == nil {
			plugins = projected
		}
	}
	return ProfileInfo{
		Ref:            ProfileRef{HomeID: home.ID, Name: name},
		Path:           filepath.Join(home.Path, "profiles", name),
		Exists:         exists,
		Kind:           kind,
		Launchable:     true,
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
		return []PluginInfo{}, nil
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

func profileExists(catalog ProfileCatalog, home HomeInfo, name string) bool {
	if isBuiltInProfile(catalog, name) {
		return true
	}
	info, err := os.Stat(filepath.Join(home.Path, "profiles", name))
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

func containsProfile(profiles []ProfileInfo, homeID, name string) bool {
	for _, profile := range profiles {
		if profile.Ref.HomeID == homeID && profile.Ref.Name == name {
			return true
		}
	}
	return false
}

func validateProfileRef(ref ProfileRef) error {
	if ref.HomeID == "" || ref.Name == "" {
		return failure(lifecycle.ErrorProfileRequired, "a DSH profile is required", "select a DSH home and profile")
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

func findHome(homes []HomeInfo, id string) (HomeInfo, bool) {
	for _, home := range homes {
		if home.ID == id {
			return home, true
		}
	}
	return HomeInfo{}, false
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
	if verifier == nil {
		return nil
	}
	if err := verifier.Verify(ctx, runtime.Path, runtime.Version); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return cancellation
		}
		return preserveFailure(err, lifecycle.ErrorDSHVersionCheckFailed, "The selected DSH runtime could not be verified.", "the DSH adapter rejected the selected runtime", false, false)
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

func cloneSelection(selection *LaunchSelection) *LaunchSelection {
	if selection == nil {
		return nil
	}
	copy := *selection
	return &copy
}

func cloneHomes(homes []HomeInfo) []HomeInfo {
	if homes == nil {
		return nil
	}
	return append([]HomeInfo(nil), homes...)
}

func cloneRuntimes(runtimes []RuntimeInfo) []RuntimeInfo {
	if runtimes == nil {
		return nil
	}
	return append([]RuntimeInfo(nil), runtimes...)
}

func refreshRuntimes(runtimes []RuntimeInfo) []RuntimeInfo {
	refreshed := cloneRuntimes(runtimes)
	for i := range refreshed {
		refreshed[i].Installed = runtimeExecutablePresent(refreshed[i].Path)
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
