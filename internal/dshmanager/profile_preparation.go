package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/local/dsh-work/internal/lifecycle"
)

const maxProfileManifestBytes = 1 << 20

// PrepareRunContext is the switch-time seam for rebuilding generated profile
// state. It is intentionally separate from ResolveLaunch: resolving a target
// remains a read-only catalog operation, while a Host switch prepares the
// candidate after stopping the current Worker.
func (m *Manager) PrepareRunContext(ctx context.Context, launch ResolvedLaunch) error {
	// A clean rescue home boots the shipped web composition directly. Running
	// the plugin install command here would make rescue depend on provisioning.
	if launch.Target.Profile.DataDirectoryID == SafeModeDataDirectoryID {
		return contextError(ctx)
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := contextError(ctx); err != nil {
		return err
	}

	m.mu.RLock()
	config := m.configSnapshotLocked()
	runner := config.CommandRunner
	commands := config.PluginCommands
	m.mu.RUnlock()
	profilePath := filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name)
	if !profileNeedsPreparation(profilePath, isBuiltInProfile(config.ProfileCatalog, launch.Target.Profile.Name)) {
		return nil
	}
	if runner == nil || commands == nil {
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH profile needs preparation", "the DSH plugin command is unavailable", true, false)
	}
	args, err := commands.Prepare(launch.Target.Profile.Name)
	if err != nil {
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH profile could not be prepared", "the DSH plugin command could not be constructed", true, false)
	}
	if err := prepareProfileWorkingDirectory(launch.DataDirectory); err != nil {
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH data directory could not be prepared", err.Error(), true, false)
	}
	env := cloneEnvironment(launch.Node.ChildEnvironment)
	env["DSH_HOME"] = launch.DataDirectory.Path
	if _, err := runner.Run(ctx, launch.Runtime.Path, args, env, launch.DataDirectory.Path); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return cancellation
		}
		return failureWithMeta(lifecycle.ErrorProfilePreparationFailed, "the DSH profile could not be prepared", "DSH could not reconcile the profile dependencies", true, true)
	}
	return nil
}

func (m *Manager) materializeBuiltInProfile(ctx context.Context, config Config, dataDirectory DataDirectoryInfo, profile string) error {
	m.mu.RLock()
	current := cloneRunContext(m.current)
	configured := cloneRunContext(m.configured)
	m.mu.RUnlock()
	runtime, ok := profilePreparationRuntime(config, current, configured)
	if !ok || config.CommandRunner == nil || config.PluginCommands == nil {
		return failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the built-in profile is not initialized", "the DSH command needed to initialize this profile is unavailable", true, false)
	}
	args, err := config.PluginCommands.Prepare(profile)
	if err != nil {
		return failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the built-in profile could not be initialized", "the DSH command could not be constructed", true, false)
	}
	if err := prepareProfileWorkingDirectory(dataDirectory); err != nil {
		return failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the DSH data directory could not be prepared", err.Error(), true, false)
	}
	env := map[string]string{"DSH_HOME": dataDirectory.Path}
	if selected := profilePreparationNode(current, configured); selected != nil {
		if resolved, resolveErr := resolveNode(ctx, config.NodeResolver, *selected, config.Nodes); resolveErr == nil {
			env = cloneEnvironment(resolved.ChildEnvironment)
			env["DSH_HOME"] = dataDirectory.Path
		}
	}
	if _, err := config.CommandRunner.Run(ctx, runtime.Path, args, env, dataDirectory.Path); err != nil {
		if cancellation := contextError(ctx); cancellation != nil {
			return cancellation
		}
		return failureWithMeta(lifecycle.ErrorProfileCloneFailed, "the built-in profile could not be initialized", "DSH could not create the profile state", true, true)
	}
	return nil
}

// DSH can initialize a missing profile, but the process must first have an
// existing working directory. Only app-owned homes may be created here.
func prepareProfileWorkingDirectory(directory DataDirectoryInfo) error {
	info, err := os.Stat(directory.Path)
	if errors.Is(err, os.ErrNotExist) && directory.Ownership == DataDirectoryOwnershipDSHWork {
		if err := os.MkdirAll(directory.Path, 0o700); err != nil {
			return fmt.Errorf("create DSH data directory %q: %w", directory.Path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("open DSH data directory %q: %w", directory.Path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("DSH data directory %q is not a directory", directory.Path)
	}
	return nil
}

func profilePreparationRuntime(config Config, current, configured *RunContext) (RuntimeInfo, bool) {
	for _, context := range []*RunContext{current, configured} {
		if context == nil {
			continue
		}
		if runtime, ok := findRuntime(config.Runtimes, context.RuntimeID); ok && runtimeInstallationPresent(runtime) {
			return runtime, true
		}
	}
	for _, runtime := range config.Runtimes {
		if runtimeInstallationPresent(runtime) {
			return runtime, true
		}
	}
	return RuntimeInfo{}, false
}

func profilePreparationNode(current, configured *RunContext) *NodeSelection {
	for _, context := range []*RunContext{current, configured} {
		if context != nil {
			selection, err := normalizeNodeSelection(context.Node)
			if err == nil {
				return &selection
			}
		}
	}
	return nil
}

// profileNeedsPreparation is deliberately local and conservative. DSH owns
// bundle reconciliation; this probe only avoids invoking its install command
// when a profile has no declared package dependencies and no missing manifest.
func profileNeedsPreparation(profilePath string, builtIn bool) bool {
	manifestPath := filepath.Join(profilePath, "package.json")
	info, err := os.Stat(manifestPath)
	if errors.Is(err, os.ErrNotExist) {
		// A custom profile without a package manifest is already handled by
		// DSH's normal profile boot path. Only an auto-initialized built-in
		// needs the explicit CLI seam when its manifest is absent.
		return builtIn
	}
	if err != nil {
		return true
	}
	if !info.Mode().IsRegular() || info.Size() > maxProfileManifestBytes {
		return true
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return true
	}
	var manifest struct {
		Dependencies         map[string]json.RawMessage `json:"dependencies"`
		DevDependencies      map[string]json.RawMessage `json:"devDependencies"`
		OptionalDependencies map[string]json.RawMessage `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return true
	}
	declared := make(map[string]struct{}, len(manifest.Dependencies)+len(manifest.DevDependencies)+len(manifest.OptionalDependencies))
	for name := range manifest.Dependencies {
		declared[name] = struct{}{}
	}
	for name := range manifest.DevDependencies {
		declared[name] = struct{}{}
	}
	for name := range manifest.OptionalDependencies {
		declared[name] = struct{}{}
	}
	for name := range declared {
		if !profileDependencyInstalled(profilePath, name) {
			return true
		}
	}
	return false
}

func profileDependencyInstalled(profilePath, packageName string) bool {
	if packageName == "" {
		return true
	}
	if !validPackageName(packageName) {
		return false
	}
	return directoryExists(filepath.Join(profilePath, "node_modules", filepath.FromSlash(packageName)))
}

func cloneEnvironment(source map[string]string) map[string]string {
	if len(source) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(source)+1)
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
