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

const stateVersion = 2

// Config supplies discovered DSH data directories and runtimes to the
// manager. Workspace context is intentionally absent: it belongs to DSH's
// session surface and is resolved per Worker generation.
type Config struct {
	StatePath        string
	DataDirectories  []DataDirectoryInfo
	Runtimes         []RuntimeInfo
	DefaultTarget    LaunchTarget
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
	desired       *LaunchTarget
	active        *LaunchTarget
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
		if err := validateState(*state); err != nil {
			return nil, failure(lifecycle.ErrorManagerStateInvalid, "manager state is invalid", "the persisted launch target has an unsupported shape")
		}
		if len(state.DataDirectories) > 0 {
			normalized.DataDirectories = mergeDataDirectories(normalized.DataDirectories, state.DataDirectories)
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
		target := *state.Desired
		manager.desired = &target
	} else if normalized.DefaultTarget.RuntimeID != "" {
		target := normalized.DefaultTarget
		manager.desired = &target
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

func (m *Manager) RegisterDataDirectory(ctx context.Context, dataDirectory DataDirectoryInfo) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	normalizedDataDirectory, err := normalizeDataDirectory(dataDirectory)
	if err != nil {
		return Snapshot{}, err
	}
	dataDirectory = normalizedDataDirectory
	m.mu.Lock()
	updated := false
	for i := range m.config.DataDirectories {
		if m.config.DataDirectories[i].ID == dataDirectory.ID {
			m.config.DataDirectories[i] = dataDirectory
			updated = true
			break
		}
	}
	if !updated {
		m.config.DataDirectories = append(m.config.DataDirectories, dataDirectory)
	}
	state := m.stateLocked()
	statePath := m.config.StatePath
	m.mu.Unlock()
	if err := m.store.Save(ctx, statePath, state); err != nil {
		return Snapshot{}, err
	}
	return m.Snapshot(ctx)
}

func (m *Manager) RemoveDataDirectory(ctx context.Context, id string) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	m.mu.Lock()
	index := -1
	for i := range m.config.DataDirectories {
		if m.config.DataDirectories[i].ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH data directory was not found", "the data directory is not in the catalog")
	}
	if (m.desired != nil && m.desired.Profile.DataDirectoryID == id) || (m.active != nil && m.active.Profile.DataDirectoryID == id) {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "DSH data directory is still selected", "choose another profile before removing it")
	}
	if m.config.DataDirectories[index].Ownership == DataDirectoryOwnershipWork {
		m.mu.Unlock()
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "the Work DSH data directory cannot be removed here", "remove the data directory through an explicit data-management flow")
	}
	m.config.DataDirectories = append(m.config.DataDirectories[:index], m.config.DataDirectories[index+1:]...)
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
	desired := cloneTarget(m.desired)
	active := cloneTarget(m.active)
	m.mu.RUnlock()

	profiles := discoverProfiles(ctx, config.DataDirectories, config.ProfileCatalog, config.ProfileReader)
	return Snapshot{
		Runtimes:        refreshRuntimes(config.Runtimes),
		DataDirectories: cloneDataDirectories(config.DataDirectories),
		Profiles:        profiles,
		Desired:         desired,
		Active:          active,
		Theme:           selectedTheme(ctx, config.DataDirectories, active, desired, config.ThemeReader),
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
	active := cloneTarget(m.active)
	desired := cloneTarget(m.desired)
	m.mu.RUnlock()
	return selectedTheme(ctx, config.DataDirectories, active, desired, config.ThemeReader), nil
}

func (m *Manager) ListPlugins(ctx context.Context, request PluginListRequest) ([]PluginInfo, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	dataDirectory, _, err := m.resolvePluginTarget(ctx, request.Target, false)
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

// RenameProfile changes the directory name that DSH uses as a custom
// profile's identity. DSH 0.1.2 exposes no profile-rename CLI command; its
// public contract resolves profiles directly from $DSH_HOME/profiles/<name>.
// The manager therefore keeps this narrow filesystem operation at the
// identity boundary: it never rewrites package.json, dsh.profile or patch
// layers, and it refuses built-in or active profiles.
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
	newName := strings.TrimSpace(request.NewName)
	if !validProfileName(newName) {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "new DSH profile name is invalid", "profile names cannot contain path separators")
	}
	if newName == request.Profile.Name {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "the DSH profile name is unchanged", "enter a different profile name")
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	desired := cloneTarget(m.desired)
	active := cloneTarget(m.active)
	m.mu.RUnlock()
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
	if active != nil && active.Profile == request.Profile {
		return Snapshot{}, failure(lifecycle.ErrorProfileInUse, "the active DSH profile cannot be renamed", "stop DSH before renaming the active profile")
	}
	oldPath := filepath.Join(dataDirectory.Path, "profiles", request.Profile.Name)
	oldInfo, err := os.Stat(oldPath)
	if err != nil || !oldInfo.IsDir() {
		return Snapshot{}, failure(lifecycle.ErrorProfileNotFound, "DSH profile was not found", "choose an existing custom profile")
	}
	if profileExists(config.ProfileCatalog, dataDirectory, newName) {
		return Snapshot{}, failure(lifecycle.ErrorProfileInvalid, "a DSH profile already uses that name", "choose a different profile name")
	}
	newPath := filepath.Join(dataDirectory.Path, "profiles", newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return Snapshot{}, failureWithMeta(lifecycle.ErrorProfileRenameFailed, "the DSH profile could not be renamed", "the profile directory was not changed", true, false)
	}

	desiredChanged := desired != nil && desired.Profile == request.Profile
	if desiredChanged {
		desired.Profile.Name = newName
		m.mu.Lock()
		m.desired = cloneTarget(desired)
		state := m.stateLocked()
		statePath := m.config.StatePath
		m.mu.Unlock()
		if err := m.store.Save(ctx, statePath, state); err != nil {
			rollbackErr := os.Rename(newPath, oldPath)
			m.mu.Lock()
			m.desired = cloneTarget(&LaunchTarget{
				RuntimeID: desired.RuntimeID,
				Profile:   request.Profile,
			})
			m.mu.Unlock()
			if rollbackErr != nil {
				return Snapshot{}, failureWithMeta(lifecycle.ErrorProfileRenameFailed, "the DSH profile rename is only partially complete", "the profile directory changed but the launch selection could not be saved", true, true)
			}
			return Snapshot{}, err
		}
	}
	return m.Snapshot(ctx)
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
	dataDirectory, runtime, err := m.resolvePluginTarget(ctx, target, operation == "add")
	if err != nil {
		return PluginResult{}, err
	}
	m.mu.RLock()
	runner := m.config.CommandRunner
	commands := m.config.PluginCommands
	active := cloneTarget(m.active)
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
	if _, err = runner.Run(ctx, runtime.Path, args, map[string]string{"DSH_HOME": dataDirectory.Path}, dataDirectory.Path); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return PluginResult{}, cancellation
		}
		return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginCommandFailed, "The DSH plugin operation failed", "DSH rejected the profile plugin operation", true, true)
	}
	plugins, err := m.profilePlugins(ctx, filepath.Join(dataDirectory.Path, "profiles", target.Profile.Name))
	if err != nil {
		return PluginResult{}, err
	}
	restartRequired := active != nil && active.Profile == target.Profile
	return PluginResult{Profile: target.Profile, Plugins: plugins, RestartRequired: restartRequired}, nil
}

func (m *Manager) resolvePluginTarget(ctx context.Context, target PluginTarget, allowCreate bool) (DataDirectoryInfo, RuntimeInfo, error) {
	if err := contextError(ctx); err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	desired := cloneTarget(m.desired)
	m.mu.RUnlock()
	dataDirectory, err := resolveDataDirectoryProfile(config, target.Profile, allowCreate)
	if err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, err
	}
	runtimeID := target.RuntimeID
	if runtimeID == "" && desired != nil {
		runtimeID = desired.RuntimeID
	}
	runtime, ok := findRuntime(config.Runtimes, runtimeID)
	if !ok {
		return DataDirectoryInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "DSH runtime was not found", "select a runtime for this plugin operation")
	}
	if !runtimeExecutablePresent(runtime.Path) {
		return DataDirectoryInfo{}, RuntimeInfo{}, failure(lifecycle.ErrorDSHRuntimeNotFound, "The selected DSH runtime is not available", "verify or install the selected runtime before changing plugins")
	}
	if err := verifyRuntime(ctx, config.RuntimeVerifier, runtime); err != nil {
		return DataDirectoryInfo{}, RuntimeInfo{}, err
	}
	runtime.Installed = true
	return dataDirectory, runtime, nil
}

// Resolve validates a complete runtime + data-directory + profile pairing. It
// returns a launch target suitable for the Host launch boundary.
func (m *Manager) Resolve(ctx context.Context, request LaunchRequest) (LaunchTarget, error) {
	resolved, err := m.ResolveLaunch(ctx, request)
	if err != nil {
		return LaunchTarget{}, err
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
	dataDirectory, err := resolveDataDirectoryProfile(config, request.Profile, false)
	if err != nil {
		return ResolvedLaunch{}, err
	}

	return ResolvedLaunch{
		Target: LaunchTarget{
			RuntimeID: request.RuntimeID,
			Profile:   request.Profile,
		},
		Runtime:       runtime,
		DataDirectory: dataDirectory,
	}, nil
}

// SetDesired validates and persists the next launch selection. It does not
// start or restart DSH; applying it to a running Host is an explicit caller
// decision and is reported separately by the lifecycle layer.
func (m *Manager) SetDesired(ctx context.Context, target LaunchTarget) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	resolved, err := m.Resolve(ctx, LaunchRequest{
		RuntimeID: target.RuntimeID,
		Profile:   target.Profile,
	})
	if err != nil {
		return Snapshot{}, err
	}

	m.mu.Lock()
	m.desired = &resolved
	state := m.stateLocked()
	state.Desired = cloneTarget(&resolved)
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
func (m *Manager) MarkActive(ctx context.Context, target *LaunchTarget) (Snapshot, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return Snapshot{}, err
	}
	var active *LaunchTarget
	if target != nil {
		resolved, err := m.Resolve(ctx, LaunchRequest{
			RuntimeID: target.RuntimeID,
			Profile:   target.Profile,
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
	if config.StatePath == "" {
		configRoot, err := os.UserConfigDir()
		if err != nil || configRoot == "" {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "manager state directory is unavailable", "the operating system did not provide a user application-data directory")
		}
		config.StatePath = filepath.Join(configRoot, "Work", "dsh-work", "manager.json")
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
		if dataDirectory.Ownership != DataDirectoryOwnershipWork && dataDirectory.Ownership != DataDirectoryOwnershipUser {
			return Config{}, failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory catalog is invalid", "use work or user ownership")
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

func validateDataDirectory(dataDirectory DataDirectoryInfo) error {
	if dataDirectory.ID == "" || dataDirectory.Path == "" {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH data directory is invalid", "a data directory needs an id and path")
	}
	if dataDirectory.Ownership != "" && dataDirectory.Ownership != DataDirectoryOwnershipWork && dataDirectory.Ownership != DataDirectoryOwnershipUser {
		return failure(lifecycle.ErrorManagerStateInvalid, "DSH data-directory ownership is invalid", "use work or user ownership")
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
		Version:         stateVersion,
		DataDirectories: cloneDataDirectories(m.config.DataDirectories),
		Runtimes:        cloneRuntimes(m.config.Runtimes),
		Desired:         cloneTarget(m.desired),
	}
}

func (m *Manager) configSnapshotLocked() Config {
	config := m.config
	config.DataDirectories = cloneDataDirectories(m.config.DataDirectories)
	config.Runtimes = cloneRuntimes(m.config.Runtimes)
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

func discoverProfiles(ctx context.Context, dataDirectories []DataDirectoryInfo, catalog ProfileCatalog, reader ProfileReader) []ProfileInfo {
	profiles := make([]ProfileInfo, 0)
	definitions := profileDefinitions(catalog)
	for _, dataDirectory := range dataDirectories {
		profileRoot := filepath.Join(dataDirectory.Path, "profiles")
		entries, err := os.ReadDir(profileRoot)
		if err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || !validProfileName(entry.Name()) {
					continue
				}
				profiles = append(profiles, profileInfo(ctx, dataDirectory, entry.Name(), true, definitions, reader))
			}
		}
		for _, definition := range definitions {
			if !validProfileName(definition.Name) {
				continue
			}
			if !containsProfile(profiles, dataDirectory.ID, definition.Name) {
				profiles = append(profiles, profileInfo(ctx, dataDirectory, definition.Name, false, definitions, reader))
			}
		}
	}
	return profiles
}

func profileInfo(ctx context.Context, dataDirectory DataDirectoryInfo, name string, exists bool, definitions []ProfileDefinition, reader ProfileReader) ProfileInfo {
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
		if projected, err := reader.Read(ctx, filepath.Join(dataDirectory.Path, "profiles", name)); err == nil {
			plugins = projected
		}
	}
	return ProfileInfo{
		Ref:            ProfileRef{DataDirectoryID: dataDirectory.ID, Name: name},
		Path:           filepath.Join(dataDirectory.Path, "profiles", name),
		Exists:         exists,
		Kind:           kind,
		Launchable:     true,
		Renamable:      exists && kind == ProfileKindCustom,
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

func cloneTarget(target *LaunchTarget) *LaunchTarget {
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
