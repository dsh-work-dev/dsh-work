package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/storagepaths"
)

type purgeTarget struct {
	name string
	path string
}

func isDataPurgeCommand(args []string) bool {
	return len(args) >= 2 && args[0] == "data" && args[1] == "purge"
}

func isDataPurgeUninstallerCommand(args []string) bool {
	return isDataPurgeCommand(args) && hasFlag(args[2:], "--yes") && hasFlag(args[2:], "--during-uninstall")
}

func runDataPurge(args []string, stdout io.Writer) error {
	defaults, err := storagepaths.DefaultDirectories()
	if err != nil {
		return err
	}
	return runDataPurgeWith(args, stdout, defaults)
}

func runDataPurgeWith(args []string, stdout io.Writer, defaults storagepaths.Directories) error {
	set := flag.NewFlagSet("data purge", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	yes := set.Bool("yes", false, "delete the listed DSH Work data")
	duringUninstall := set.Bool("during-uninstall", false, "internal uninstaller handoff")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 {
		return fmt.Errorf("unexpected arguments for data purge: %s", strings.Join(set.Args(), " "))
	}
	if *duringUninstall && !*yes {
		return errors.New("--during-uninstall requires --yes")
	}

	locations, err := storagepaths.Open(defaults.ConfigFile, defaults.Root, defaults.UserDataRoot)
	if err != nil {
		return fmt.Errorf("read DSH Work storage locations: %w", err)
	}
	current := locations.Snapshot().Current
	targets := dataPurgeTargets(defaults, current)
	var protectedUserDirectories []string
	if *yes {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve dsh-work-cli executable: %w", err)
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve user home directory: %w", err)
		}
		if err := validatePurgeTargets(targets, home, executable); err != nil {
			return err
		}
		state, err := loadManagerState(current.Root)
		if err != nil {
			return err
		}
		protectedUserDirectories = registeredUserDSHDirectories(state)
		if err := validateProtectedUserDirectories(targets, protectedUserDirectories); err != nil {
			return err
		}
	}

	settingsPath := filepath.Join(current.Root, "settings.json")
	var lock *dshworkapp.ProcessLock
	if *yes {
		releaseBoundary, boundaryErr := maintenance.AcquireInstallerBoundary()
		if boundaryErr != nil {
			return fmt.Errorf("reserve DSH Work install boundary: %w", boundaryErr)
		}
		defer releaseBoundary()

		// Create/lock the boundary when it does not exist yet so a manager
		// cannot start between the running-process check and the file removal.
		lock, err = dshworkapp.AcquireExistingManagerProcessLock(settingsPath)
		if err == nil && lock == nil {
			lock, err = dshworkapp.AcquireManagerProcessLock(settingsPath)
		}
	} else {
		// A dry run must stay read-only: inspect an existing lock but do not
		// create the storage root or its lock file.
		lock, err = dshworkapp.AcquireExistingManagerProcessLock(settingsPath)
	}
	if err != nil {
		return purgeLockError(err)
	}
	if lock != nil {
		defer lock.Close()
	}

	confirmed, err := storagepaths.Open(defaults.ConfigFile, defaults.Root, defaults.UserDataRoot)
	if err != nil {
		return fmt.Errorf("recheck DSH Work storage locations: %w", err)
	}
	confirmedCurrent := confirmed.Snapshot().Current
	if !samePurgePath(confirmedCurrent.Root, current.Root) || !samePurgePath(confirmedCurrent.UserData, current.UserData) {
		return errors.New("DSH Work storage locations changed during purge; rerun the command")
	}
	current = confirmedCurrent
	state, err := loadManagerState(current.Root)
	if err != nil {
		return err
	}
	protectedUserDirectories = registeredUserDSHDirectories(state)
	targets = dataPurgeTargets(defaults, current)
	if *yes {
		if err := validateProtectedUserDirectories(targets, protectedUserDirectories); err != nil {
			return err
		}
	}

	fmt.Fprintln(stdout, "Managed DSH Work paths (registered user DSH directories are kept):")
	for _, target := range targets {
		fmt.Fprintf(stdout, "  %s\n", target.path)
	}
	for _, path := range protectedUserDirectories {
		fmt.Fprintf(stdout, "  kept user DSH directory: %s\n", path)
	}
	if !*yes {
		fmt.Fprintln(stdout, "No files were deleted. Pass --yes to purge these paths.")
		return nil
	}

	if err := removePurgeTargets(targets, current.Root, defaults.ConfigFile, lock, protectedUserDirectories); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "DSH Work data purged.")
	return nil
}

func purgeLockError(err error) error {
	var failure lifecycle.Failure
	if errors.As(err, &failure) && failure.Code == lifecycle.ErrorManagerOperationBusy {
		return lifecycle.Failure{
			Code:          lifecycle.ErrorManagerOperationBusy,
			Summary:       "DSH Work is already running.",
			Retryable:     true,
			CorrelationID: lifecycle.NewCorrelationID(),
			Detail:        "Stop the DSH Work background before purging user data.",
		}
	}
	return err
}

func loadManagerState(appRoot string) (*dshmanager.State, error) {
	state, err := (dshmanager.FileStateStore{}).Load(context.Background(), filepath.Join(appRoot, "state.json"))
	if err != nil {
		return nil, fmt.Errorf("read registered DSH data directories: %w", err)
	}
	return state, nil
}

func registeredUserDSHDirectories(state *dshmanager.State) []string {
	if state == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var paths []string
	for _, directory := range state.DataDirectories {
		path := strings.TrimSpace(directory.Path)
		if directory.Ownership != dshmanager.DataDirectoryOwnershipUser || path == "" {
			continue
		}
		path = filepath.Clean(path)
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func dataPurgeTargets(defaults storagepaths.Directories, current storagepaths.Locations) []purgeTarget {
	all := []purgeTarget{
		{name: "application data", path: current.Root},
		{name: "managed DSH user data", path: current.UserData},
		{name: "cache", path: defaults.CacheRoot},
		{name: "logs", path: defaults.LogRoot},
		{name: "configuration", path: defaults.ConfigFile},
	}
	seen := make(map[string]struct{}, len(all))
	targets := make([]purgeTarget, 0, len(all))
	for _, target := range all {
		if strings.TrimSpace(target.path) == "" {
			continue
		}
		target.path = filepath.Clean(target.path)
		key := target.path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, target)
	}
	// Remove children first. In the default Windows layout, logs and config are
	// inside the cache root; custom app and DSH data roots can also be nested.
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].name == "configuration" {
			return false
		}
		if targets[j].name == "configuration" {
			return true
		}
		return len(targets[i].path) > len(targets[j].path)
	})
	return targets
}

func removePurgeTargets(targets []purgeTarget, appRoot, configFile string, lock *dshworkapp.ProcessLock, protectedUserDirectories []string) error {
	protected := []string{configFile}
	lockPath := filepath.Join(appRoot, "state.lock")
	if lock != nil {
		protected = append(protected, lockPath)
	}
	protected = append(protected, protectedUserDirectories...)
	for _, target := range targets {
		if samePurgePath(target.path, configFile) {
			continue
		}
		if err := removePurgePath(target.path, protected); err != nil {
			return fmt.Errorf("remove %s (%s): %w", target.name, target.path, err)
		}
	}

	// Keep state.lock until the manager boundary is released, then remove it.
	// config.toml is the final file removed so no future invocation can resolve
	// a location that has only been partially purged.
	if lock != nil {
		if err := lock.Close(); err != nil {
			return fmt.Errorf("release manager lock: %w", err)
		}
		if !isPurgePathWithinAny(lockPath, protectedUserDirectories) {
			if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove manager lock %q: %w", lockPath, err)
			}
		}
	}
	if !isPurgePathWithinAny(configFile, protectedUserDirectories) {
		if err := os.Remove(configFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove configuration %q: %w", configFile, err)
		}
	}
	// The protected files are gone; remove the now-empty roots. All contents
	// were removed in the first pass, while paths with symlinked protected
	// ancestors were rejected rather than followed.
	for _, target := range targets {
		if samePurgePath(target.path, configFile) {
			continue
		}
		if err := removePurgePath(target.path, protectedUserDirectories); err != nil {
			return fmt.Errorf("remove %s (%s): %w", target.name, target.path, err)
		}
	}
	return nil
}

func removePurgePath(path string, protected []string) error {
	for _, candidate := range protected {
		if samePurgePath(path, candidate) || containsPurgePath(candidate, path) {
			return nil
		}
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	containsProtected := false
	for _, candidate := range protected {
		if containsPurgePath(path, candidate) {
			containsProtected = true
			break
		}
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if containsProtected {
			return fmt.Errorf("protected file is below non-directory path %q", path)
		}
		return os.Remove(path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		isProtected := false
		protectsDescendant := false
		for _, candidate := range protected {
			if samePurgePath(child, candidate) || containsPurgePath(candidate, child) {
				isProtected = true
				break
			}
			if containsPurgePath(child, candidate) {
				protectsDescendant = true
			}
		}
		if isProtected {
			continue
		}
		if protectsDescendant {
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("protected file is below symlink %q", child)
			}
			if err := removePurgePath(child, protected); err != nil {
				return err
			}
			continue
		}
		if err := os.RemoveAll(child); err != nil {
			return err
		}
	}
	if containsProtected {
		return nil
	}
	return os.Remove(path)
}

func isPurgePathWithinAny(path string, protected []string) bool {
	for _, candidate := range protected {
		if samePurgePath(path, candidate) || containsPurgePath(candidate, path) {
			return true
		}
	}
	return false
}

func validateProtectedUserDirectories(targets []purgeTarget, protected []string) error {
	for _, keep := range protected {
		for _, target := range targets {
			// A user directory nested in a managed root can be preserved while
			// the rest of that root is removed. If it is the root or an ancestor,
			// the requested purge would delete user-owned data, so refuse it.
			if samePurgePath(keep, target.path) || containsPurgePath(keep, target.path) {
				return fmt.Errorf("refusing to purge %s path %q because registered user DSH directory %q overlaps it", target.name, target.path, keep)
			}
		}
	}
	return nil
}

func validatePurgeTargets(targets []purgeTarget, home, executable string) error {
	executableDir := filepath.Dir(filepath.Clean(executable))
	for _, target := range targets {
		path := filepath.Clean(target.path)
		if !filepath.IsAbs(path) {
			return fmt.Errorf("refusing to purge non-absolute %s path %q", target.name, target.path)
		}
		if filepath.Dir(path) == path {
			return fmt.Errorf("refusing to purge filesystem root from %s path %q", target.name, path)
		}
		if samePurgePath(path, home) || containsPurgePath(path, home) {
			return fmt.Errorf("refusing to purge the user profile or one of its parent directories from %s path %q", target.name, path)
		}
		if executableDir != "" && (samePurgePath(path, executableDir) || containsPurgePath(path, executableDir)) {
			return fmt.Errorf("refusing to purge the installed program directory from %s path %q", target.name, path)
		}
	}
	return nil
}

func samePurgePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func containsPurgePath(parent, child string) bool {
	parent, child = filepath.Clean(parent), filepath.Clean(child)
	if runtime.GOOS == "windows" {
		parent, child = strings.ToLower(parent), strings.ToLower(child)
	}
	relative, err := filepath.Rel(parent, child)
	if err != nil || relative == "." || relative == ".." {
		return false
	}
	return !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
