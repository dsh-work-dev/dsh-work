package dshmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	copyfs "github.com/otiai10/copy"
)

// HealthySnapshot owns independent files, never hard links into a mutable
// installation. Launch retains the logical selection and the pinned executables.
type HealthySnapshot struct {
	Directory string         `json:"directory"`
	Launch    ResolvedLaunch `json:"launch"`
}

var healthHomeFiles = []string{"settings.yaml", ".credentials.yaml", "cordis.patch.yml"}

// copyHealthTree uses the maintained copy library for permissions, flushing and
// file copying. Internal dependency links are rebased; external links are copied
// into the snapshot, with ancestor cycles rejected instead of retaining an alias
// to mutable files. No hard links are created.
func copyHealthTree(ctx context.Context, source, destination string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	// A regular file is materialized directly. Windows version-manager paths
	// can be readable/executable even when EvalSymlinks cannot normalize an
	// ancestor; no link rebasing is needed for the file's independent bytes.
	if info.Mode().IsRegular() {
		return copyfs.Copy(source, destination, copyfs.Options{Sync: true})
	}
	return copyHealthTreeSeen(ctx, source, destination, nil)
}

func copyHealthTreeSeen(ctx context.Context, source, destination string, ancestors []string) error {
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve snapshot source %s: %w", source, err)
	}
	source = resolvedSource
	for _, ancestor := range ancestors {
		if source == ancestor {
			return fmt.Errorf("cyclic dependency link: %s", source)
		}
	}
	ancestors = append(append([]string(nil), ancestors...), source)
	return copyfs.Copy(source, destination, copyfs.Options{Sync: true, NumOfWorkers: 8,
		Skip: func(info os.FileInfo, src, dst string) (bool, error) {
			if err := contextError(ctx); err != nil {
				return true, err
			}
			if info.Mode()&os.ModeSymlink == 0 {
				return false, nil
			}
			resolved, err := filepath.EvalSymlinks(src)
			if err != nil {
				return true, fmt.Errorf("resolve dependency link %s: %w", src, err)
			}
			rel, err := filepath.Rel(source, resolved)
			if err != nil {
				// A local plugin may live on a different Windows volume.
				return true, copyHealthTreeSeen(ctx, resolved, dst, ancestors)
			}
			if rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel) {
				link, err := filepath.Rel(filepath.Dir(dst), filepath.Join(destination, rel))
				if err != nil {
					return true, err
				}
				if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
					return true, err
				}
				return true, os.Symlink(link, dst)
			}
			return true, copyHealthTreeSeen(ctx, resolved, dst, ancestors)
		},
	})
}

func (m *Manager) captureHealthy(ctx context.Context, launch ResolvedLaunch) (*HealthySnapshot, error) {
	root := filepath.Join(filepath.Dir(m.config.StatePath), "healthy-snapshots")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	directory, err := os.MkdirTemp(root, "health-")
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			_ = os.RemoveAll(directory)
		}
	}()
	profile := filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name)
	if err := copyHealthProfile(ctx, profile, filepath.Join(directory, "profile")); err != nil {
		return nil, err
	}
	for _, name := range healthHomeFiles {
		if err := copyHealthTree(ctx, filepath.Join(launch.DataDirectory.Path, name), filepath.Join(directory, "home", name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	// npm/pnpm installations use a relocatable node_modules/.bin shim and need
	// the complete installation, including transitive and native dependencies.
	runtimeRoot := launch.Runtime.Path
	for dir := filepath.Dir(launch.Runtime.Path); filepath.Dir(dir) != dir; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "node_modules" {
			runtimeRoot = filepath.Dir(dir)
		}
	}
	if runtimeRoot == launch.Runtime.Path {
		// npm's global Windows shim lives beside node_modules rather than in .bin.
		parent := filepath.Dir(launch.Runtime.Path)
		if _, err := os.Stat(filepath.Join(parent, "node_modules", "@deepseek-ai", "dsh", "package.json")); err == nil {
			runtimeRoot = parent
		}
	}
	if runtimeRoot == launch.Runtime.Path && launch.Node.NodePath != "" {
		switch strings.ToLower(filepath.Ext(launch.Runtime.Path)) {
		case ".cmd", ".bat", ".ps1", ".js", ".mjs", ".sh":
			return nil, errors.New("DSH launcher has no self-contained installation; select a managed DSH installation")
		}
	}
	runtimeDestination := filepath.Join(directory, "runtime")
	if runtimeRoot == launch.Runtime.Path {
		runtimeDestination = filepath.Join(runtimeDestination, filepath.Base(runtimeRoot))
	}
	if err := copyHealthTree(ctx, runtimeRoot, runtimeDestination); err != nil {
		return nil, err
	}
	if runtimeRoot == launch.Runtime.Path {
		launch.Runtime.Path = runtimeDestination
	} else {
		rel, _ := filepath.Rel(runtimeRoot, launch.Runtime.Path)
		launch.Runtime.Path = filepath.Join(runtimeDestination, rel)
	}
	if launch.Node.NodePath != "" {
		nodeDestination := filepath.Join(directory, "node", filepath.Base(launch.Node.NodePath))
		if err := copyHealthTree(ctx, launch.Node.NodePath, nodeDestination); err != nil {
			return nil, err
		}
		launch.Node.NodePath = nodeDestination
		launch.Node.ChildEnvironment = childEnvironmentForNode(nodeDestination)
	}
	complete = true
	return &HealthySnapshot{Directory: directory, Launch: launch}, nil
}

// RestoreHealthy is replayable after an interrupted restore. Pending is durable
// before the first write and cleared only by the next successful health commit.
// DSH_HOME itself is never replaced: sessions, storages and workspaces stay put.
func (m *Manager) RestoreHealthy(ctx context.Context) (ResolvedLaunch, error) {
	if m.config.DisableHealthSnapshots {
		m.mu.RLock()
		target := cloneRunContext(m.knownGood)
		m.mu.RUnlock()
		if target == nil {
			return ResolvedLaunch{}, errors.New("no previous run context is available")
		}
		return m.ResolveLaunch(ctx, LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return ResolvedLaunch{}, err
	}
	defer release()
	m.mu.RLock()
	healthy := m.healthy
	state := m.stateLocked()
	live := m.current != nil
	m.mu.RUnlock()
	if live {
		return ResolvedLaunch{}, errors.New("stop the current Worker before restoring its environment")
	}
	if healthy == nil {
		return ResolvedLaunch{}, errors.New("no persistent healthy snapshot is available")
	}
	root := filepath.Join(filepath.Dir(m.config.StatePath), "healthy-snapshots")
	if filepath.Dir(healthy.Directory) != root || !pathWithin(healthy.Directory, healthy.Launch.Runtime.Path) ||
		(healthy.Launch.Node.NodePath != "" && !pathWithin(healthy.Directory, healthy.Launch.Node.NodePath)) {
		return ResolvedLaunch{}, errors.New("healthy snapshot files are outside their managed directory")
	}
	state.RecoveryPending = true
	if err := m.store.Save(ctx, m.config.StatePath, state); err != nil {
		return ResolvedLaunch{}, err
	}
	m.mu.Lock()
	m.recoveryPending = true
	m.mu.Unlock()
	launch := healthy.Launch
	profile := filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name)
	// Only environment entries move. Persistent data directories remain at
	// their original paths even if a plugin stored them inside the profile.
	if info, err := os.Lstat(profile); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return ResolvedLaunch{}, errors.New("cannot restore through a linked profile directory")
	}
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return ResolvedLaunch{}, err
	}
	entries, err := os.ReadDir(profile)
	if err != nil {
		return ResolvedLaunch{}, err
	}
	failedRoot := filepath.Join(launch.DataDirectory.Path, ".dsh-work-recovery")
	if err := os.MkdirAll(failedRoot, 0o700); err != nil {
		return ResolvedLaunch{}, err
	}
	failed, err := os.MkdirTemp(failedRoot, "failed-profile-")
	if err != nil {
		return ResolvedLaunch{}, err
	}
	for _, entry := range entries {
		if persistentProfileData(entry.Name()) {
			continue
		}
		if err := os.Rename(filepath.Join(profile, entry.Name()), filepath.Join(failed, entry.Name())); err != nil {
			return ResolvedLaunch{}, err
		}
	}
	if err := copyHealthProfile(ctx, filepath.Join(healthy.Directory, "profile"), profile); err != nil {
		return ResolvedLaunch{}, err
	}
	for _, name := range healthHomeFiles {
		src := filepath.Join(healthy.Directory, "home", name)
		dst := filepath.Join(launch.DataDirectory.Path, name)
		if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
			if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
				return ResolvedLaunch{}, err
			}
		} else if err != nil {
			return ResolvedLaunch{}, err
		} else if err := restoreHealthFile(ctx, src, dst); err != nil {
			return ResolvedLaunch{}, err
		}
	}
	return launch, nil
}

// Replace the directory entry atomically, including when a failed candidate
// turned the configuration file into a link to a workspace file.
func restoreHealthFile(ctx context.Context, source, destination string) error {
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".health-config-*")
	if err != nil {
		return err
	}
	path := temporary.Name()
	defer os.Remove(path)
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := copyHealthTree(ctx, source, path); err != nil {
		return err
	}
	return os.Rename(path, destination)
}

// ApplyPlugin is available only inside the Host's stopped-worker transaction.
// The context value grants this one command its already-resolved launch.
type pluginLaunchKey struct{}

func (m *Manager) ApplyPlugin(ctx context.Context, launch ResolvedLaunch, packageSpec, operation string) (PluginResult, error) {
	m.mu.RLock()
	allowed := m.switching && m.current == nil
	m.mu.RUnlock()
	if !allowed {
		return PluginResult{}, errors.New("plugin apply requires a stopped Run context transaction")
	}
	return m.runPluginCommand(context.WithValue(ctx, pluginLaunchKey{}, launch), PluginTarget{Profile: launch.Target.Profile}, packageSpec, operation)
}

func (m *Manager) ResumeRecovery(ctx context.Context) (*ResolvedLaunch, error) {
	m.mu.RLock()
	pending := m.recoveryPending && !m.config.DisableHealthSnapshots
	m.mu.RUnlock()
	if !pending {
		return nil, nil
	}
	launch, err := m.RestoreHealthy(ctx)
	return &launch, err
}

// Retain the durable recovery point and any snapshot supplying the live
// executables. Interrupted, unpublished copies can be reclaimed after commit.
func (m *Manager) pruneHealthySnapshots(healthy *HealthySnapshot, live ResolvedLaunch) {
	root := filepath.Dir(healthy.Directory)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasPrefix(entry.Name(), "health-") {
			continue
		}
		path := filepath.Join(root, entry.Name())
		if path == healthy.Directory || pathWithin(path, live.Runtime.Path) || pathWithin(path, live.Node.NodePath) {
			continue
		}
		_ = os.RemoveAll(path)
	}
}

func pathWithin(root, path string) bool {
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func persistentProfileData(name string) bool {
	switch strings.ToLower(name) {
	case "sessions", "storages", "workspaces", "worktrees", "attachments", "rewind-snapshots":
		return true
	default:
		return false
	}
}
func copyHealthProfile(ctx context.Context, source, destination string) error {
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return err
	}
	for _, entry := range entries {
		if persistentProfileData(entry.Name()) {
			continue
		}
		if err := copyHealthTree(ctx, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
