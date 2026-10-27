package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/storagepaths"
)

func purgeTestDirectories(t *testing.T) storagepaths.Directories {
	t.Helper()
	root := t.TempDir()
	cache := filepath.Join(root, "local", "dsh-work")
	return storagepaths.Directories{
		Root:         filepath.Join(root, "roaming", "dsh-work"),
		UserDataRoot: filepath.Join(root, ".dsh-work"),
		CacheRoot:    cache,
		LogRoot:      filepath.Join(cache, "logs"),
		ConfigFile:   filepath.Join(cache, "config.toml"),
	}
}

func TestDataPurgeResolvesDefaultStorageRoots(t *testing.T) {
	defaults := purgeTestDirectories(t)
	locations, err := storagepaths.Open(defaults.ConfigFile, defaults.Root, defaults.UserDataRoot)
	if err != nil {
		t.Fatal(err)
	}
	targets := dataPurgeTargets(defaults, locations.Snapshot().Current)
	got := make(map[string]bool, len(targets))
	for _, target := range targets {
		got[filepath.Clean(target.path)] = true
	}
	for _, want := range []string{defaults.Root, defaults.UserDataRoot, defaults.CacheRoot, defaults.LogRoot, defaults.ConfigFile} {
		if !got[filepath.Clean(want)] {
			t.Errorf("default managed path %q is missing from purge plan: %+v", want, targets)
		}
	}
	if len(got) != 5 {
		t.Fatalf("default purge paths = %d, want 5: %+v", len(got), targets)
	}
}

func TestInstallerMutexOnlyAllowsExplicitUninstallerPurge(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{name: "ordinary purge", args: []string{"data", "purge"}},
		{name: "ordinary confirmed purge", args: []string{"data", "purge", "--yes"}},
		{name: "uninstaller without confirmation", args: []string{"data", "purge", "--during-uninstall"}},
		{name: "confirmed uninstaller purge", args: []string{"data", "purge", "--yes", "--during-uninstall"}, want: true},
		{name: "uninstaller flags in either order", args: []string{"data", "purge", "--during-uninstall", "--yes"}, want: true},
		{name: "another command", args: []string{"stop", "--wait", "--during-uninstall"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isDataPurgeUninstallerCommand(test.args); got != test.want {
				t.Fatalf("isDataPurgeUninstallerCommand(%v) = %v, want %v", test.args, got, test.want)
			}
		})
	}
}

func TestDataPurgeDuringUninstallRequiresConfirmation(t *testing.T) {
	defaults := purgeTestDirectories(t)
	if err := runDataPurgeWith([]string{"--during-uninstall"}, &bytes.Buffer{}, defaults); err == nil || !strings.Contains(err.Error(), "--during-uninstall requires --yes") {
		t.Fatalf("unconfirmed uninstaller purge error = %v", err)
	}
}

func TestDataPurgeDryRunListsPathsWithoutDeleting(t *testing.T) {
	defaults := purgeTestDirectories(t)
	if err := os.MkdirAll(filepath.Dir(defaults.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaults.ConfigFile, []byte("[storage]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{defaults.Root, defaults.UserDataRoot, defaults.CacheRoot, defaults.LogRoot} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var output bytes.Buffer
	if err := runDataPurgeWith(nil, &output, defaults); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "No files were deleted. Pass --yes") {
		t.Fatalf("dry-run output = %q", output.String())
	}
	for _, path := range []string{
		defaults.ConfigFile,
		filepath.Join(defaults.Root, "keep.txt"),
		filepath.Join(defaults.UserDataRoot, "keep.txt"),
		filepath.Join(defaults.CacheRoot, "keep.txt"),
		filepath.Join(defaults.LogRoot, "keep.txt"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("dry run changed %q: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(defaults.Root, "state.lock")); !os.IsNotExist(err) {
		t.Errorf("dry run created process lock %q (stat error %v)", filepath.Join(defaults.Root, "state.lock"), err)
	}
}

func TestDataPurgeUsesConfiguredRootsAndLeavesExternalDSHData(t *testing.T) {
	defaults := purgeTestDirectories(t)
	customApp := filepath.Join(t.TempDir(), "custom-app")
	customUser := filepath.Join(t.TempDir(), "custom-user")
	external := filepath.Join(customApp, "external-dsh")
	if err := os.MkdirAll(filepath.Dir(defaults.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("[storage]\napp = %q\ndata = %q\n", filepath.ToSlash(customApp), filepath.ToSlash(customUser))
	if err := os.WriteFile(defaults.ConfigFile, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{customApp, customUser, defaults.CacheRoot, defaults.LogRoot, external} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registered := fmt.Sprintf(`{"dataDirectories":[{"id":"external","name":"External DSH","path":%q,"ownership":"user"}]}`, external)
	if err := os.WriteFile(filepath.Join(customApp, "state.json"), []byte(registered), 0o600); err != nil {
		t.Fatal(err)
	}
	managedMarker := filepath.Join(customApp, "managed-state.txt")
	if err := os.WriteFile(managedMarker, []byte("remove managed app data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(customApp, "state.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	externalMarker := filepath.Join(external, "keep.txt")
	if err := os.WriteFile(externalMarker, []byte("external user data"), 0o600); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	if err := runDataPurgeWith([]string{"--yes"}, &output, defaults); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{customUser, defaults.CacheRoot, defaults.ConfigFile, managedMarker, filepath.Join(customApp, "state.json"), filepath.Join(customApp, "state.lock")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("managed path %q remains after purge (stat error %v)", path, err)
		}
	}
	if _, err := os.Stat(externalMarker); err != nil {
		t.Fatalf("registered external DSH data was removed: %v", err)
	}
	if !strings.Contains(output.String(), "kept user DSH directory: "+external) {
		t.Errorf("dry-run/purge output does not identify the retained user DSH directory: %q", output.String())
	}
}

func TestDataPurgeRefusesWhenUserDirectoryContainsManagedRoot(t *testing.T) {
	root := t.TempDir()
	targets := []purgeTarget{{name: "application data", path: filepath.Join(root, "app")}}
	userDirectory := root
	if err := validateProtectedUserDirectories(targets, []string{userDirectory}); err == nil {
		t.Fatalf("purge accepted managed path %q inside registered user directory %q", targets[0].path, userDirectory)
	}
}

func TestDataPurgeRefusesWhileManagerLockIsHeld(t *testing.T) {
	defaults := purgeTestDirectories(t)
	if err := os.MkdirAll(defaults.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(defaults.Root, "keep.txt")
	if err := os.WriteFile(marker, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}

	control := t.TempDir()
	readyPath := filepath.Join(control, "ready")
	releasePath := filepath.Join(control, "release")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHoldManagerProcessLock$")
	cmd.Env = append(os.Environ(),
		"DSH_TEST_MANAGER_LOCK_PATH="+filepath.Join(defaults.Root, "settings.json"),
		"DSH_TEST_MANAGER_LOCK_READY="+readyPath,
		"DSH_TEST_MANAGER_LOCK_RELEASE="+releasePath,
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	finished := false
	defer func() {
		if !finished {
			_ = os.WriteFile(releasePath, nil, 0o600)
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manager lock helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(defaults.Root, "state.lock")); err != nil {
		t.Fatalf("manager lock helper reported ready without creating %q: %v", filepath.Join(defaults.Root, "state.lock"), err)
	}
	if err := runDataPurgeWith([]string{"--yes"}, &bytes.Buffer{}, defaults); err == nil || !strings.Contains(err.Error(), "Stop the DSH Work background before purging user data") {
		t.Fatalf("purge while locked error = %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("running manager data was changed: %v", err)
	}
	if err := os.WriteFile(releasePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("manager lock helper: %v", err)
	}
	finished = true
}

func TestHoldManagerProcessLock(t *testing.T) {
	lockPath := os.Getenv("DSH_TEST_MANAGER_LOCK_PATH")
	if lockPath == "" {
		return
	}
	lock, err := dshworkapp.AcquireManagerProcessLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := os.WriteFile(os.Getenv("DSH_TEST_MANAGER_LOCK_READY"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	releasePath := os.Getenv("DSH_TEST_MANAGER_LOCK_RELEASE")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(releasePath); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manager lock helper timed out waiting for release")
}

func TestValidatePurgeTargetsRejectsProfileAndInstallAncestors(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{name: "profile", path: filepath.Join(t.TempDir(), "profile")},
		{name: "install directory", path: filepath.Join(t.TempDir(), "programs")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := test.path
			executable := filepath.Join(test.path, "programs", "dsh-work-cli.exe")
			if test.name == "install directory" {
				home = filepath.Join(test.path, "profile")
			}
			if err := validatePurgeTargets([]purgeTarget{{name: "test", path: test.path}}, home, executable); err == nil {
				t.Fatalf("unsafe purge path %q was accepted", test.path)
			}
		})
	}
}
