package dshmanager

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestManagerPersistsAnExplicitRunContext(t *testing.T) {
	root := t.TempDir()
	dataDirectoryPath := filepath.Join(root, "dsh-data")
	if err := os.MkdirAll(filepath.Join(dataDirectoryPath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}

	config := Config{
		StatePath: filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{
			ID:        "dsh-work",
			Name:      "dsh-work managed",
			Path:      dataDirectoryPath,
			Ownership: DataDirectoryOwnershipDSHWork,
		}},
		Runtimes: []RuntimeInfo{{
			ID:      "dsh-0.1.2-alpha.3",
			Version: "0.1.2-alpha.3",
			Path:    runtimePath,
			Source:  RuntimeSourceDevelopmentFixture,
		}},
		DefaultRunContext: RunContext{
			RuntimeID: "dsh-0.1.2-alpha.3",
			Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	}

	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	want := RunContext{
		RuntimeID: "dsh-0.1.2-alpha.3",
		Node:      NodeSelection{Kind: NodeSelectionSystem},
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	}
	if _, err := manager.SetConfigured(context.Background(), want); err != nil {
		t.Fatalf("SetConfigured() error = %v", err)
	}

	reloaded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Configured == nil || *snapshot.Configured != want {
		t.Fatalf("reloaded configured Run context = %#v, want %#v", snapshot.Configured, want)
	}
}

func TestManagerRequiresAProfileReferenceForTarget(t *testing.T) {
	manager := newTestManager(t)

	_, err := manager.SetConfigured(context.Background(), RunContext{
		RuntimeID: "dsh-test",
	})
	if err == nil {
		t.Fatal("SetConfigured() error = nil, want profile-required failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorProfileRequired)
}

func TestManagerRejectsUnknownProfileReference(t *testing.T) {
	manager := newTestManager(t)

	_, err := manager.Resolve(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "missing"},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want profile-not-found failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorProfileNotFound)
}

func TestManagerRejectsAnUnverifiedRuntimeBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "missing", Version: "0.1.2-alpha.3", Path: filepath.Join(root, "missing-dsh.cmd")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].Installed {
		t.Fatalf("runtime verification projection = %#v", snapshot.Runtimes)
	}
	_, err = manager.Resolve(context.Background(), LaunchRequest{
		RuntimeID: "missing", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	})
	assertFailureCode(t, err, lifecycle.ErrorDSHRuntimeNotFound)
}

func TestManagerDelegatesProfileCatalogAndRuntimeVerification(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	verifier := &recordingRuntimeVerifier{}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		ProfileCatalog: testProfileCatalog{definitions: []ProfileDefinition{{
			Name: "web", Kind: ProfileKindBuiltIn, AutoInitialize: true,
		}}},
		RuntimeVerifier: verifier,
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.ResolveLaunch(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Target.Profile.Name != "web" {
		t.Fatalf("resolved launch profile = %#v, want web", resolved.Target.Profile)
	}
	if verifier.path != runtimePath || verifier.version != "0.1.2-alpha.3" || verifier.calls != 1 || verifier.profileCalls != 1 || verifier.profileName != "web" || verifier.profileDataDirectory != homePath {
		t.Fatalf("runtime/profile verifier calls = runtime(%q, %q, %d) profile(%q, %q, %d)", verifier.path, verifier.version, verifier.calls, verifier.profileDataDirectory, verifier.profileName, verifier.profileCalls)
	}
}

func TestManagerPreservesAdapterRuntimeCompatibilityFailure(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		ProfileCatalog:  testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn}}},
		RuntimeVerifier: failingRuntimeVerifier{},
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.ResolveLaunch(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	})
	assertFailureCode(t, err, lifecycle.ErrorDSHUnsupportedVersion)
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.CorrelationID == "" {
		t.Fatalf("compatibility failure = %#v, want correlation id", err)
	}
}

func TestManagerKeepsCurrentWithoutInventingUnverifiedRecovery(t *testing.T) {
	manager := newTestManager(t)
	configured := RunContext{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	}
	if _, err := manager.SetConfigured(context.Background(), configured); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitCurrent(context.Background(), &configured); err != nil {
		t.Fatal(err)
	}

	changed := configured
	changed.Profile.Name = "web-clean"
	if _, err := manager.SetConfigured(context.Background(), changed); err == nil {
		t.Fatal("SetConfigured() error = nil, want active-context guard")
	} else {
		assertFailureCode(t, err, lifecycle.ErrorManagerOperationBusy)
	}

	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Configured == nil || snapshot.Configured.Profile.Name != "web" {
		t.Fatalf("configured = %#v, want web", snapshot.Configured)
	}
	if snapshot.Current == nil || snapshot.Current.Profile.Name != "web" {
		t.Fatalf("current = %#v, want web", snapshot.Current)
	}
	if snapshot.KnownGood != nil || snapshot.RestorePoints == nil || snapshot.RestorePoints.SaveError == "" {
		t.Fatalf("missing version adapter must retain current with a recording error, not invent recovery: %#v", snapshot)
	}
}

func TestManagerCommitKeepsCurrentAfterCallerCancellationDuringSnapshot(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	commitContext, cancel := context.WithCancel(context.Background())
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		StateStore:      cancelAfterSaveStore{cancel: cancel},
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := RunContext{RuntimeID: "dsh-test", Node: NodeSelection{Kind: NodeSelectionSystem}, Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
	if _, err := manager.CommitCurrent(commitContext, &target); err != nil {
		t.Fatalf("CommitCurrent() error = %v, want committed snapshot despite post-save cancellation", err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || *snapshot.Current != target {
		t.Fatalf("current after canceled commit = %#v, want %#v", snapshot.Current, target)
	}
}

func TestManagerCatalogCommitSucceedsAfterPersistenceCancelsCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager, err := New(Config{
		StatePath:  filepath.Join(t.TempDir(), "manager.json"),
		StateStore: cancelAfterSaveStore{cancel: cancel},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.RegisterRuntime(ctx, RuntimeInfo{
		ID: "dsh-test", Version: "1.2.3", Path: filepath.Join(t.TempDir(), "dsh.cmd"),
	})
	if err != nil {
		t.Fatalf("RegisterRuntime() error = %v, want committed snapshot despite post-save cancellation", err)
	}
	if _, exists := findRuntime(snapshot.Runtimes, "dsh-test"); !exists {
		t.Fatalf("committed runtime missing from snapshot: %#v", snapshot.Runtimes)
	}
}

func TestManagerRenamesCustomProfileAndUpdatesConfiguredContext(t *testing.T) {
	manager := newTestManager(t)
	oldPath := filepath.Join(manager.config.DataDirectories[0].Path, "profiles", "web-clean")
	manifest := []byte(`{"name":"dsh-profile-web-clean","private":true,"dependencies":{"@example/plugin":"1.0.0"}}`)
	patch := []byte("- id: example\n  value: true\n")
	if err := os.WriteFile(filepath.Join(oldPath, "package.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldPath, "cordis.patch.yml"), patch, 0o600); err != nil {
		t.Fatal(err)
	}
	target := RunContext{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web-clean"},
	}
	if _, err := manager.SetConfigured(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	snapshot, err := manager.RenameProfile(context.Background(), ProfileRenameRequest{
		Profile: target.Profile,
		NewName: "coding",
	})
	if err != nil {
		t.Fatalf("RenameProfile() error = %v", err)
	}
	if snapshot.Configured == nil || snapshot.Configured.Profile.Name != "coding" {
		t.Fatalf("configured after rename = %#v, want coding", snapshot.Configured)
	}

	profiles := make(map[string]ProfileInfo, len(snapshot.Profiles))
	for _, profile := range snapshot.Profiles {
		profiles[profile.Ref.Name] = profile
	}
	if _, ok := profiles["web-clean"]; ok {
		t.Fatal("old custom profile name is still present")
	}
	if profile, ok := profiles["coding"]; !ok || !profile.Renamable {
		t.Fatalf("renamed profile = %#v, want a renamable coding profile", profile)
	} else {
		if _, err := os.Stat(profile.Path); err != nil {
			t.Fatalf("renamed profile path %q is unavailable: %v", profile.Path, err)
		}
		if got, err := os.ReadFile(filepath.Join(profile.Path, "package.json")); err != nil || string(got) != string(manifest) {
			t.Fatalf("renamed manifest = %q, error = %v, want original manifest", got, err)
		}
		if got, err := os.ReadFile(filepath.Join(profile.Path, "cordis.patch.yml")); err != nil || string(got) != string(patch) {
			t.Fatalf("renamed patch = %q, error = %v, want original patch", got, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(profile.Path), "web-clean")); !os.IsNotExist(err) {
			t.Fatalf("old profile path still exists, stat error = %v", err)
		}
	}
	reloaded, err := New(manager.config)
	if err != nil {
		t.Fatal(err)
	}
	reloadedSnapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if reloadedSnapshot.Configured == nil || reloadedSnapshot.Configured.Profile.Name != "coding" {
		t.Fatalf("reloaded configured after rename = %#v, want coding", reloadedSnapshot.Configured)
	}
}

func TestManagerDoesNotRenameBuiltInOrCurrentProfile(t *testing.T) {
	manager := newTestManager(t)
	manager.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn}}}
	_, err := manager.RenameProfile(context.Background(), ProfileRenameRequest{
		Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		NewName: "web-copy",
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)

	target := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web-clean"}}
	if _, err := manager.CommitCurrent(context.Background(), &target); err != nil {
		t.Fatal(err)
	}
	_, err = manager.RenameProfile(context.Background(), ProfileRenameRequest{
		Profile: target.Profile,
		NewName: "coding",
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileInUse)
}

func TestManagerClonesProfileWithUniqueNameAndRebuildableContentsOmitted(t *testing.T) {
	manager := newTestManager(t)
	source := filepath.Join(manager.config.DataDirectories[0].Path, "profiles", "web")
	if err := os.WriteFile(filepath.Join(source, "package.json"), []byte(`{"dependencies":{"@example/plugin":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "node_modules", "@example"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "node_modules", "@example", "plugin.js"), []byte("plugin"), 0o600); err != nil {
		t.Fatal(err)
	}

	first, err := manager.CloneProfile(context.Background(), ProfileCloneRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	if err != nil {
		t.Fatalf("CloneProfile() error = %v", err)
	}
	if first.Profile.Name != "web 1" {
		t.Fatalf("clone name = %q, want web 1", first.Profile.Name)
	}
	clonedManifest, err := os.ReadFile(filepath.Join(source, "..", "web 1", "package.json"))
	if err != nil || string(clonedManifest) != `{"dependencies":{"@example/plugin":"1.0.0"}}` {
		t.Fatalf("cloned manifest = %q, error = %v", clonedManifest, err)
	}
	if _, err := os.Stat(filepath.Join(source, "..", "web 1", "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("clone retained node_modules, stat error = %v", err)
	}
	second, err := manager.CloneProfile(context.Background(), ProfileCloneRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	if err != nil {
		t.Fatalf("second CloneProfile() error = %v", err)
	}
	if second.Profile.Name != "web 2" {
		t.Fatalf("second clone name = %q, want web 2", second.Profile.Name)
	}
}

func TestManagerAllowsCloningBuiltInProfile(t *testing.T) {
	manager := newTestManager(t)
	manager.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn}}}
	if err := os.WriteFile(filepath.Join(manager.config.DataDirectories[0].Path, "profiles", "web", "user.yml"), []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := manager.CloneProfile(context.Background(), ProfileCloneRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	if err != nil {
		t.Fatalf("CloneProfile() built-in error = %v", err)
	}
	if result.Profile.Name != "web 1" {
		t.Fatalf("built-in clone name = %q, want web 1", result.Profile.Name)
	}
	if _, err := os.Stat(filepath.Join(manager.config.DataDirectories[0].Path, "profiles", "web 1", "user.yml")); err != nil {
		t.Fatalf("built-in clone did not preserve user file: %v", err)
	}
}

func TestManagerInitializesMissingBuiltInBeforeClone(t *testing.T) {
	manager := newTestManager(t)
	manager.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn, AutoInitialize: true}}}
	profileRoot := filepath.Join(manager.config.DataDirectories[0].Path, "profiles")
	if err := os.RemoveAll(filepath.Join(profileRoot, "web")); err != nil {
		t.Fatal(err)
	}
	runner := &materializingRunner{}
	manager.config.CommandRunner = runner
	manager.config.PluginCommands = testPluginCommands{}
	result, err := manager.CloneProfile(context.Background(), ProfileCloneRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	if err != nil {
		t.Fatalf("CloneProfile() missing built-in error = %v", err)
	}
	if result.Profile.Name != "web 1" || runner.calls != 1 {
		t.Fatalf("materialized built-in clone = %#v, runner calls = %d", result.Profile, runner.calls)
	}
}

func TestManagerDeletesCustomProfileAndProtectsBuiltInAndSelectedProfiles(t *testing.T) {
	manager := newTestManager(t)
	custom := ProfileRef{DataDirectoryID: "dsh-work", Name: "web-clean"}
	if _, err := manager.DeleteProfile(context.Background(), ProfileDeleteRequest{Profile: custom}); err != nil {
		t.Fatalf("DeleteProfile() custom error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(manager.config.DataDirectories[0].Path, "profiles", custom.Name)); !os.IsNotExist(err) {
		t.Fatalf("deleted profile still exists, stat error = %v", err)
	}

	manager = newTestManager(t)
	manager.config.ProfileCatalog = testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn}}}
	_, err := manager.DeleteProfile(context.Background(), ProfileDeleteRequest{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)

	manager = newTestManager(t)
	selected := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web-clean"}}
	if _, err := manager.CommitCurrent(context.Background(), &selected); err != nil {
		t.Fatal(err)
	}
	_, err = manager.DeleteProfile(context.Background(), ProfileDeleteRequest{Profile: selected.Profile})
	assertFailureCode(t, err, lifecycle.ErrorProfileInUse)

	manager = newTestManager(t)
	failedTarget := RunContext{RuntimeID: "dsh-test", Profile: custom}
	if _, err := manager.RecordSwitchAttempt(context.Background(), SwitchAttempt{Target: failedTarget, Stage: SwitchAttemptCandidate, Failure: lifecycle.Failure{Code: lifecycle.ErrorDSHStartFailed, Summary: "candidate failed"}, Rollback: RollbackRestored}, false); err != nil {
		t.Fatal(err)
	}
	_, err = manager.DeleteProfile(context.Background(), ProfileDeleteRequest{Profile: custom})
	assertFailureCode(t, err, lifecycle.ErrorProfileInUse)
}

func TestManagerPreparesMissingProfileDependenciesBeforeSwitch(t *testing.T) {
	manager := newTestManager(t)
	runner := &recordingRunner{}
	manager.config.CommandRunner = runner
	manager.config.PluginCommands = testPluginCommands{}
	profilePath := filepath.Join(manager.config.DataDirectories[0].Path, "profiles", "web-clean")
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(`{"dependencies":{"@example/plugin":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	launch, err := manager.ResolveLaunch(context.Background(), LaunchRequest{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web-clean"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.PrepareRunContext(context.Background(), launch); err != nil {
		t.Fatalf("PrepareRunContext() error = %v", err)
	}
	if got := strings.Join(runner.args, " "); got != "plugin --profile web-clean install" {
		t.Fatalf("preparation args = %q", got)
	}
	if runner.env["DSH_HOME"] != manager.config.DataDirectories[0].Path {
		t.Fatalf("preparation DSH_HOME = %q", runner.env["DSH_HOME"])
	}
}

func TestDevelopmentLauncherWithoutDSHPackageIsNotInstalled(t *testing.T) {
	m := newTestManager(t)
	launcher := filepath.Join(t.TempDir(), "run-dsh.cmd")
	if err := os.WriteFile(launcher, []byte("@node missing.js"), 0600); err != nil {
		t.Fatal(err)
	}
	m.config.Runtimes[0].Path = launcher
	m.config.Runtimes[0].Source = RuntimeSourceDevelopmentFixture
	snapshot, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Runtimes[0].Installed {
		t.Fatal("launcher without its DSH package was advertised as installed")
	}
	_, err = m.ResolveLaunch(context.Background(), LaunchRequest{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}})
	assertFailureCode(t, err, lifecycle.ErrorDSHRuntimeNotFound)
}

func TestManagerBacksUpProfileWithoutGeneratedDependencyTrees(t *testing.T) {
	manager := newTestManager(t)
	profile := ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}
	profilePath := filepath.Join(manager.config.DataDirectories[0].Path, "profiles", profile.Name)
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(`{"name":"web"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "cordis.patch.yml"), []byte("patch"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"node_modules", ".cache"} {
		if err := os.MkdirAll(filepath.Join(profilePath, directory), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(profilePath, directory, "generated.txt"), []byte("generated"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	backup, err := manager.BackupProfile(context.Background(), ProfileBackupRequest{Profile: profile})
	if err != nil {
		t.Fatalf("BackupProfile() error = %v", err)
	}
	backupPath := filepath.Join(manager.config.DataDirectories[0].Path, "profiles", backup.Profile.Name, profileBackupFolder, backup.FileName)
	archive, err := zip.OpenReader(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	entries := make(map[string]bool, len(archive.File))
	for _, entry := range archive.File {
		entries[entry.Name] = true
	}
	for _, name := range []string{"_dsh-work/manifest.json", "package.json", "cordis.patch.yml"} {
		if !entries[name] {
			t.Fatalf("backup is missing %q: %#v", name, entries)
		}
	}
	for _, name := range []string{"node_modules/generated.txt", ".cache/generated.txt"} {
		if entries[name] {
			t.Fatalf("backup contains generated file %q: %#v", name, entries)
		}
	}
}

func TestManagerKeepsConfiguredProfileWhenRenamePersistenceFails(t *testing.T) {
	manager := newTestManager(t)
	profileRoot := filepath.Join(manager.config.DataDirectories[0].Path, "profiles")
	manager.store = failingStateStore{}
	if _, err := manager.RenameProfile(context.Background(), ProfileRenameRequest{
		Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		NewName: "coding",
	}); err == nil {
		t.Fatal("RenameProfile() error = nil, want persistence failure")
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Configured == nil || snapshot.Configured.Profile.Name != "web" {
		t.Fatalf("configured profile after failed rename = %#v", snapshot.Configured)
	}
	if _, err := os.Stat(filepath.Join(profileRoot, "web")); err != nil {
		t.Fatalf("original profile was not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(profileRoot, "coding")); !os.IsNotExist(err) {
		t.Fatalf("renamed profile remained after rollback: %v", err)
	}
}

func TestManagerDelegatesPluginOperationsToDSHForExplicitProfile(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	for _, profile := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(homePath, "profiles", profile), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "profiles", "alpha", "package.json"), []byte(`{"dependencies":{"@example/alpha":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "profiles", "beta", "package.json"), []byte(`{"dependencies":{"@example/beta":"2.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		CommandRunner:   runner,
		PluginCommands:  testPluginCommands{},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
		DefaultRunContext: RunContext{
			RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}}
	if _, err := manager.CommitCurrent(context.Background(), &want); err != nil {
		t.Fatal(err)
	}
	result, err := manager.InstallPlugin(context.Background(), PluginInstallRequest{
		Target:  PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}},
		Package: "@example/new-plugin@3.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RestartRequired || result.Profile.Name != "alpha" {
		t.Fatalf("plugin result = %#v, want alpha and restart required", result)
	}
	if runner.path != filepath.Join(root, "dsh.cmd") || runner.cwd != homePath || runner.env["DSH_HOME"] != homePath {
		t.Fatalf("DSH command target = path %q cwd %q env %#v", runner.path, runner.cwd, runner.env)
	}
	if got := strings.Join(runner.args, " "); got != "plugin --profile alpha add @example/new-plugin@3.0.0 --registry https://registry.npmjs.org/" {
		t.Fatalf("DSH plugin args = %q", got)
	}
	state, err := (FileStateStore{}).Load(context.Background(), filepath.Join(root, "manager.json"))
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || len(state.PluginProvenance) != 1 || state.PluginProvenance[0].Package != "@example/new-plugin" || state.PluginProvenance[0].SourceKind != PluginSourcePublicRegistry || state.PluginProvenance[0].SuccessfulRoute != RuntimeArtifactSourceOfficial {
		t.Fatalf("plugin provenance = %#v", state)
	}
	upgrade, err := manager.UpgradePlugin(context.Background(), PluginUpgradeRequest{
		Target: PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}}, Package: "@example/alpha",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !upgrade.RestartRequired || strings.Join(runner.args, " ") != "plugin --profile alpha update @example/alpha --registry https://registry.npmjs.org/" {
		t.Fatalf("plugin upgrade = result %#v args %#v", upgrade, runner.args)
	}
	alpha, err := manager.ListPlugins(context.Background(), PluginListRequest{Target: PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.ListPlugins(context.Background(), PluginListRequest{Target: PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "beta"}}})
	assertFailureCode(t, err, lifecycle.ErrorManagerOperationBusy)
	if len(alpha) != 1 || alpha[0].Name != "@example/alpha" {
		t.Fatalf("current profile plugin list = %#v", alpha)
	}
}

func TestManagerDoesNotExposeProfileDependencyDirectory(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: filepath.Join(root, "dsh.cmd")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref.Name == "node_modules" {
			t.Fatal("dependency directory was exposed as a DSH profile")
		}
	}
}

func TestManagerRejectsPluginMutationForNonCurrentProfile(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	for _, profile := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(homePath, "profiles", profile), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		CommandRunner:   runner,
		PluginCommands:  testPluginCommands{},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	current := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}}
	if _, err := manager.CommitCurrent(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	_, err = manager.InstallPlugin(context.Background(), PluginInstallRequest{
		Target:  PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "beta"}},
		Package: "@example/not-allowed",
	})
	assertFailureCode(t, err, lifecycle.ErrorManagerOperationBusy)
	if runner.path != "" || len(runner.args) != 0 {
		t.Fatalf("non-current mutation reached command runner: %#v", runner)
	}
	if _, err := manager.ListPlugins(context.Background(), PluginListRequest{
		Target: PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "beta"}},
	}); err == nil {
		t.Fatal("non-current profile inspection succeeded")
	}
}

func TestPluginObservationKeepsInstalledRowsWhenOutdatedFails(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	profilePath := filepath.Join(homePath, "profiles", "alpha")
	if err := os.MkdirAll(filepath.Join(profilePath, "node_modules", "@example", "public"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(`{"dependencies":{"@example/public":"^1.0.0","git-plugin":"git+https://example.test/plugin.git"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "node_modules", "@example", "public", "package.json"), []byte(`{"version":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &pluginObservationRunner{}
	manager, err := New(Config{StatePath: filepath.Join(root, "manager.json"), DataDirectories: []DataDirectoryInfo{{ID: "home", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}}, Runtimes: []RuntimeInfo{{ID: "dsh", Version: "1.0.0", Path: runtimePath}}, CommandRunner: runner, PluginCommands: testPluginCommands{}})
	if err != nil {
		t.Fatal(err)
	}
	target := RunContext{RuntimeID: "dsh", Profile: ProfileRef{DataDirectoryID: "home", Name: "alpha"}}
	if _, err := manager.CommitCurrent(context.Background(), &target); err != nil {
		t.Fatal(err)
	}
	plugins, err := manager.ListPlugins(context.Background(), PluginListRequest{Target: PluginTarget{Profile: target.Profile}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 2 {
		t.Fatalf("plugins = %#v", plugins)
	}
	byName := map[string]PluginInfo{}
	for _, plugin := range plugins {
		byName[plugin.Name] = plugin
	}
	public := byName["@example/public"]
	if public.CurrentVersion != "1.2.3" || public.SourceKind != PluginSourcePublicRegistry || public.UpdateCheck != PluginUpdateUnknown || public.AvailableVersion != "" {
		t.Fatalf("public plugin after failed outdated = %#v", public)
	}
	git := byName["git-plugin"]
	if git.SourceKind != PluginSourceGit || git.UpdateCheck != PluginUpdateUnknown {
		t.Fatalf("git plugin = %#v", git)
	}
}

func TestPluginOutdatedMarksAvailableVersionWithoutChangingCurrentVersion(t *testing.T) {
	plugins := []PluginInfo{{
		Name: "@example/public", Package: "@example/public", Version: "1.2.3", CurrentVersion: "1.2.3",
		SourceKind: PluginSourcePublicRegistry, UpdateCheck: PluginUpdateUnknown,
	}}
	applyPluginOutdatedJSON(plugins, `{"@example/public":{"current":"1.2.3","latest":"2.0.0"}}`)
	if plugins[0].CurrentVersion != "1.2.3" || plugins[0].AvailableVersion != "2.0.0" || plugins[0].UpdateCheck != PluginUpdateAvailable {
		t.Fatalf("outdated projection = %#v", plugins[0])
	}
}

func TestPublicPluginAttemptFallsBackOnlyAfterReachabilityFailure(t *testing.T) {
	runner := &pluginRouteRunner{failures: []error{errors.New("ECONNRESET")}}
	manager, err := New(Config{StatePath: filepath.Join(t.TempDir(), "manager.json"), CommandRunner: runner, PluginCommands: testPluginCommands{}})
	if err != nil {
		t.Fatal(err)
	}
	dataDirectory := DataDirectoryInfo{ID: "home", Path: t.TempDir()}
	runtime := RuntimeInfo{Path: filepath.Join(t.TempDir(), "dsh.cmd")}
	route, _, err := manager.runPublicPluginAttempt(context.Background(), dataDirectory, runtime, ProfileRef{DataDirectoryID: "home", Name: "web"}, "@example/plugin@1.2.3", "add", map[string]string{"DSH_HOME": dataDirectory.Path})
	if err != nil {
		t.Fatal(err)
	}
	if route != "mirror" || len(runner.calls) != 2 || argumentAfter(runner.calls[0], "--registry") != defaultPluginOfficialRegistry || argumentAfter(runner.calls[1], "--registry") != defaultPluginMirrorRegistry {
		t.Fatalf("route=%q calls=%#v", route, runner.calls)
	}

	runner.calls = nil
	runner.failures = []error{errors.New("engine requirement failed")}
	if _, _, err := manager.runPublicPluginAttempt(context.Background(), dataDirectory, runtime, ProfileRef{DataDirectoryID: "home", Name: "web"}, "@example/plugin@1.2.3", "add", map[string]string{"DSH_HOME": dataDirectory.Path}); err == nil {
		t.Fatal("semantic failure = nil")
	}
	if len(runner.calls) != 1 {
		t.Fatalf("semantic failure used mirror: %#v", runner.calls)
	}
}

func TestPluginSourceClassificationDoesNotInferPublicUpdatesForNonRegistrySpecs(t *testing.T) {
	tests := map[string]PluginSourceKind{
		"^1.2.3": PluginSourcePublicRegistry, "git+https://example.test/p.git": PluginSourceGit,
		"https://example.test/p.tgz": PluginSourceURL, "file:../p": PluginSourceLocal,
		"plugin.tgz": PluginSourceURL, "latest": PluginSourcePublicRegistry, "next": PluginSourcePublicRegistry,
		"npm:@example/replacement@1.2.3":           PluginSourcePublicRegistry,
		"registry:https://packages.example.test/p": PluginSourcePrivateRegistry, "workspace:*": PluginSourceLocal,
	}
	for spec, want := range tests {
		if got := classifyPluginSource(spec); got != want {
			t.Errorf("classifyPluginSource(%q)=%q want %q", spec, got, want)
		}
	}
}

func TestScopedPackageNameIsARegistryRequest(t *testing.T) {
	if got := classifyRequestedPluginSource("@example/plugin"); got != PluginSourcePublicRegistry {
		t.Fatalf("classifyRequestedPluginSource() = %q, want %q", got, PluginSourcePublicRegistry)
	}
}

func TestConfiguredPluginRegistryStaysPrivateAndDropsCredentials(t *testing.T) {
	profilePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(profilePath, ".npmrc"), []byte("@example:registry=https://user:secret@packages.example.test/npm?token=hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := configuredPluginRegistry(profilePath, "@example/plugin"), "https://packages.example.test/npm/"; got != want {
		t.Fatalf("registry = %q, want %q", got, want)
	}
	if got := configuredPluginRegistry(profilePath, "other-plugin"); got != "" {
		t.Fatalf("unscoped registry = %q", got)
	}
}

func TestManagerBlocksMutationsDuringRunContextSwitch(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.BeginRunContextSwitch(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.EndRunContextSwitch(context.Background()); err != nil {
			t.Fatal(err)
		}
	}()

	_, err := manager.RegisterRuntime(context.Background(), RuntimeInfo{
		ID: "dsh-other", Version: "0.1.2-alpha.3", Path: filepath.Join(t.TempDir(), "dsh.cmd"),
	})
	assertFailureCode(t, err, lifecycle.ErrorManagerOperationBusy)
}

func TestManagerPersistsCatalogEntriesProducedByExplicitRuntimeInstall(t *testing.T) {
	root := t.TempDir()
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")}}
	config := Config{
		StatePath:        filepath.Join(root, "manager.json"),
		RuntimeInstaller: installer,
		DSHReleases:      testDSHReleases("1.2.3"),
	}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.InstallRuntime(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if installer.version != "1.2.3" || len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].ID != "dsh-1.2.3" {
		t.Fatalf("install result = %#v, installer version = %q", snapshot.Runtimes, installer.version)
	}
	reloaded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	reloadedSnapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedSnapshot.Runtimes) != 1 || reloadedSnapshot.Runtimes[0].Version != "1.2.3" {
		t.Fatalf("reloaded runtime catalog = %#v", reloadedSnapshot.Runtimes)
	}
}

func TestManagerRefreshesAndPersistsDSHReleaseCatalogOnlyExplicitly(t *testing.T) {
	root := t.TempDir()
	catalog := &recordingDSHCatalog{releases: testDSHReleases("2.0.0", "1.2.3")}
	config := Config{StatePath: filepath.Join(root, "manager.json"), DSHCatalog: catalog}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 0 || len(snapshot.DSHReleases) != 0 {
		t.Fatalf("startup refreshed releases: calls=%d snapshot=%#v", catalog.calls, snapshot.DSHReleases)
	}
	snapshot, err = manager.RefreshDSHReleases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 1 || len(snapshot.DSHReleases) != 2 {
		t.Fatalf("refresh = calls=%d releases=%#v", catalog.calls, snapshot.DSHReleases)
	}
	reloaded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err = reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.calls != 1 || len(snapshot.DSHReleases) != 2 {
		t.Fatalf("reload performed I/O or lost cache: calls=%d releases=%#v", catalog.calls, snapshot.DSHReleases)
	}
}

func TestManagerVerifiesRuntimeBeforeRegisteringIt(t *testing.T) {
	root := t.TempDir()
	toolchainPath := filepath.Join(root, "node")
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd"), ToolchainPath: toolchainPath}}
	verifier := &recordingRuntimeVerifier{}
	manager, err := New(Config{
		StatePath:        filepath.Join(root, "manager.json"),
		RuntimeInstaller: installer,
		DSHReleases:      testDSHReleases("1.2.3"),
		RuntimeVerifier:  verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.InstallRuntime(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("InstallRuntime() error = %v", err)
	}
	if verifier.calls != 1 || verifier.path != filepath.Join(root, "dsh.cmd") || verifier.version != "1.2.3" || !strings.HasPrefix(verifier.env["PATH"], toolchainPath+string(os.PathListSeparator)) {
		t.Fatalf("runtime verifier call = %#v", verifier)
	}
	if len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].Version != "1.2.3" {
		t.Fatalf("registered runtimes = %#v", snapshot.Runtimes)
	}
}

func TestManagerCanAcquireMissingConfiguredRuntimeOnFirstUse(t *testing.T) {
	root := t.TempDir()
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")}}
	if err := os.WriteFile(installer.runtime.Path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath: filepath.Join(root, "manager.json"), RuntimeInstaller: installer,
		DSHReleases: testDSHReleases("1.2.3"), RuntimeVerifier: &recordingRuntimeVerifier{},
		Runtimes:          []RuntimeInfo{{ID: "dsh-1.2.3", Version: "1.2.3", Path: filepath.Join(root, "missing.cmd")}},
		DefaultRunContext: RunContext{RuntimeID: "dsh-1.2.3", Node: NodeSelection{Kind: NodeSelectionSystem}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.InstallRuntime(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runtimes) != 1 || !snapshot.Runtimes[0].Installed || snapshot.Configured.RuntimeID != "dsh-1.2.3" {
		t.Fatalf("first-use install = %+v", snapshot)
	}
	// Once installed, the selected environment is protected from replacement.
	_, err = manager.InstallRuntime(context.Background(), "1.2.3")
	assertFailureCode(t, err, lifecycle.ErrorRuntimeInUse)
}

func TestManagerDoesNotRegisterRuntimeRejectedByVerifier(t *testing.T) {
	root := t.TempDir()
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")}}
	manager, err := New(Config{
		StatePath:        filepath.Join(root, "manager.json"),
		RuntimeInstaller: installer,
		DSHReleases:      testDSHReleases("1.2.3"),
		RuntimeVerifier:  failingRuntimeVerifier{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.InstallRuntime(context.Background(), "1.2.3")
	assertFailureCode(t, err, lifecycle.ErrorDSHUnsupportedVersion)
	snapshot, snapshotErr := manager.Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if len(snapshot.Runtimes) != 0 {
		t.Fatalf("rejected runtime was registered: %#v", snapshot.Runtimes)
	}
}

func TestManagerDiscardsRuntimeWhenInstallIsCancelledBeforeVerification(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	installer := &cancelingInstaller{
		runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")},
		cancel:  cancel,
	}
	verifier := &recordingRuntimeVerifier{}
	manager, err := New(Config{
		StatePath:        filepath.Join(root, "manager.json"),
		RuntimeInstaller: installer,
		DSHReleases:      testDSHReleases("1.2.3"),
		RuntimeVerifier:  verifier,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = manager.InstallRuntime(ctx, "1.2.3")
	assertFailureCode(t, err, lifecycle.ErrorCancelled)
	if !installer.discarded {
		t.Fatal("cancelled runtime candidate was not discarded")
	}
	if verifier.calls != 0 {
		t.Fatalf("cancelled runtime was verified: %d calls", verifier.calls)
	}
	snapshot, snapshotErr := manager.Snapshot(context.Background())
	if snapshotErr != nil {
		t.Fatal(snapshotErr)
	}
	if len(snapshot.Runtimes) != 0 {
		t.Fatalf("cancelled runtime was registered: %#v", snapshot.Runtimes)
	}
}

func TestManagerDoesNotReplaceRuntimeUsedByCurrentOrKnownGoodContext(t *testing.T) {
	root := t.TempDir()
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataPath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(dataPath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "replacement", "dsh.cmd")}}
	manager, err := New(Config{
		StatePath:        filepath.Join(root, "manager.json"),
		RuntimeInstaller: installer,
		DSHReleases:      testDSHReleases("1.2.3"),
		DataDirectories:  []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: dataPath, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes: []RuntimeInfo{{
			ID: "dsh-1.2.3", Version: "1.2.3", Path: runtimePath,
		}},
		DefaultRunContext: RunContext{RuntimeID: "dsh-1.2.3", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CommitCurrent(context.Background(), &RunContext{
		RuntimeID: "dsh-1.2.3",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	}); err != nil {
		t.Fatal(err)
	}

	_, err = manager.InstallRuntime(context.Background(), "1.2.3")
	assertFailureCode(t, err, lifecycle.ErrorRuntimeInUse)
	if installer.version != "" {
		t.Fatalf("runtime installer was called for an in-use version: %q", installer.version)
	}
}

func TestManagerRejectsRuntimeVersionOutsideRefreshedCatalog(t *testing.T) {
	root := t.TempDir()
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")}}
	manager, err := New(Config{
		StatePath:        filepath.Join(root, "manager.json"),
		DSHReleases:      testDSHReleases("1.2.3"),
		RuntimeInstaller: installer,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.InstallRuntime(context.Background(), "9.9.9")
	assertFailureCode(t, err, lifecycle.ErrorDSHUnsupportedVersion)
	if installer.version != "" {
		t.Fatalf("installer was called for an unapproved version: %q", installer.version)
	}
}

func TestManagerRegistersOnlyExistingUserDSHDataDirectories(t *testing.T) {
	manager := newTestManager(t)
	existing := filepath.Join(t.TempDir(), "personal-dsh")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.RegisterDataDirectory(context.Background(), DataDirectoryInfo{
		ID:        "personal",
		Name:      "Personal DSH",
		Path:      existing,
		Ownership: DataDirectoryOwnershipUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.DataDirectories) != 2 || snapshot.DataDirectories[1].ID != "personal" {
		t.Fatalf("registered data directories = %#v", snapshot.DataDirectories)
	}

	_, err = manager.RegisterDataDirectory(context.Background(), DataDirectoryInfo{
		ID:        "missing",
		Name:      "Missing DSH",
		Path:      filepath.Join(t.TempDir(), "does-not-exist"),
		Ownership: DataDirectoryOwnershipUser,
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileNotFound)
}

func TestManagerKeepsCatalogUnchangedWhenPersistenceFails(t *testing.T) {
	t.Run("register runtime", func(t *testing.T) {
		manager := newTestManager(t)
		manager.store = failingStateStore{}
		_, err := manager.RegisterRuntime(context.Background(), RuntimeInfo{
			ID: "dsh-other", Version: "1.2.3", Path: filepath.Join(t.TempDir(), "dsh.cmd"),
		})
		if err == nil {
			t.Fatal("RegisterRuntime() error = nil, want persistence failure")
		}
		snapshot, snapshotErr := manager.Snapshot(context.Background())
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if _, exists := findRuntime(snapshot.Runtimes, "dsh-other"); exists {
			t.Fatalf("failed registration changed runtime catalog: %#v", snapshot.Runtimes)
		}
	})

	t.Run("remove runtime", func(t *testing.T) {
		manager := newTestManager(t)
		if _, err := manager.RegisterRuntime(context.Background(), RuntimeInfo{
			ID: "dsh-other", Version: "1.2.3", Path: filepath.Join(t.TempDir(), "dsh.cmd"),
		}); err != nil {
			t.Fatal(err)
		}
		manager.store = failingStateStore{}
		if _, err := manager.RemoveRuntime(context.Background(), "dsh-other"); err == nil {
			t.Fatal("RemoveRuntime() error = nil, want persistence failure")
		}
		snapshot, err := manager.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := findRuntime(snapshot.Runtimes, "dsh-other"); !exists {
			t.Fatalf("failed removal changed runtime catalog: %#v", snapshot.Runtimes)
		}
	})

	t.Run("register data directory", func(t *testing.T) {
		manager := newTestManager(t)
		directory := t.TempDir()
		manager.store = failingStateStore{}
		_, err := manager.RegisterDataDirectory(context.Background(), DataDirectoryInfo{
			ID: "personal", Path: directory, Ownership: DataDirectoryOwnershipUser,
		})
		if err == nil {
			t.Fatal("RegisterDataDirectory() error = nil, want persistence failure")
		}
		snapshot, snapshotErr := manager.Snapshot(context.Background())
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if _, exists := findDataDirectory(snapshot.DataDirectories, "personal"); exists {
			t.Fatalf("failed registration changed data-directory catalog: %#v", snapshot.DataDirectories)
		}
	})

	t.Run("remove data directory", func(t *testing.T) {
		manager := newTestManager(t)
		directory := t.TempDir()
		if _, err := manager.RegisterDataDirectory(context.Background(), DataDirectoryInfo{
			ID: "personal", Path: directory, Ownership: DataDirectoryOwnershipUser,
		}); err != nil {
			t.Fatal(err)
		}
		manager.store = failingStateStore{}
		if _, err := manager.RemoveDataDirectory(context.Background(), "personal"); err == nil {
			t.Fatal("RemoveDataDirectory() error = nil, want persistence failure")
		}
		snapshot, err := manager.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, exists := findDataDirectory(snapshot.DataDirectories, "personal"); !exists {
			t.Fatalf("failed removal changed data-directory catalog: %#v", snapshot.DataDirectories)
		}
	})
}

func TestManagerNormalizesAndValidatesRuntimeCatalogEntries(t *testing.T) {
	relativePath := filepath.Join("testdata", "dsh.cmd")
	manager, err := New(Config{
		StatePath: filepath.Join(t.TempDir(), "manager.json"),
		Runtimes:  []RuntimeInfo{{ID: "dsh-test", Version: "1.2.3", Path: relativePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	wantPath, err := filepath.Abs(relativePath)
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Runtimes[0]; got.Path != filepath.Clean(wantPath) || got.Source != RuntimeSourceManaged || !got.Removable {
		t.Fatalf("normalized runtime = %#v", got)
	}

	_, err = New(Config{
		StatePath: filepath.Join(t.TempDir(), "manager.json"),
		Runtimes:  []RuntimeInfo{{ID: "dsh-test", Version: "1.2.3", Path: relativePath, Source: RuntimeSource("unknown")}},
	})
	assertFailureCode(t, err, lifecycle.ErrorManagerStateInvalid)
}

func TestFileProfileReaderReportsInvalidManifest(t *testing.T) {
	profilePath := t.TempDir()
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileProfileReader{}).Read(context.Background(), profilePath); err == nil {
		t.Fatal("Read() error = nil, want invalid JSON error")
	}
}

func TestManagerReportsProfileReaderFailures(t *testing.T) {
	manager := newTestManager(t)
	target := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
	if _, err := manager.CommitCurrent(context.Background(), &target); err != nil {
		t.Fatal(err)
	}
	manager.config.ProfileReader = failingProfileReader{}
	_, err := manager.ListPlugins(context.Background(), PluginListRequest{
		Target: PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}},
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)
}

type recordingRunner struct {
	path string
	args []string
	env  map[string]string
	cwd  string
}

type materializingRunner struct {
	calls int
}

func (r *materializingRunner) Run(_ context.Context, _ string, _ []string, _ map[string]string, cwd string) (CommandResult, error) {
	r.calls++
	profilePath := filepath.Join(cwd, "profiles", "web")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return CommandResult{}, err
	}
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(`{"name":"dsh-profile-web"}`), 0o600); err != nil {
		return CommandResult{}, err
	}
	return CommandResult{}, nil
}

type pluginObservationRunner struct{}

func (*pluginObservationRunner) Run(_ context.Context, _ string, args []string, _ map[string]string, _ string) (CommandResult, error) {
	joined := strings.Join(args, " ")
	if strings.Contains(joined, " list ") {
		return CommandResult{Stdout: `{"dependencies":{"@example/public":{"version":"1.2.3"}}}`}, nil
	}
	if strings.Contains(joined, " outdated ") {
		return CommandResult{}, errors.New("registry unreachable")
	}
	return CommandResult{}, nil
}

type pluginRouteRunner struct {
	calls    [][]string
	failures []error
}

func (r *pluginRouteRunner) Run(_ context.Context, _ string, args []string, _ map[string]string, _ string) (CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	index := len(r.calls) - 1
	if index < len(r.failures) && r.failures[index] != nil {
		return CommandResult{Stderr: r.failures[index].Error()}, r.failures[index]
	}
	return CommandResult{}, nil
}

func argumentAfter(args []string, name string) string {
	for index := range args {
		if args[index] == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

type failingStateStore struct{}

func (failingStateStore) Load(context.Context, string) (*State, error) {
	return nil, nil
}

func (failingStateStore) Save(context.Context, string, State) error {
	return errors.New("save failed")
}

type failingProfileReader struct{}

func (failingProfileReader) Read(context.Context, string) ([]PluginInfo, error) {
	return nil, errors.New("read failed")
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingRunner) Run(ctx context.Context, _ string, _ []string, _ map[string]string, _ string) (CommandResult, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.release:
		return CommandResult{}, nil
	case <-ctx.Done():
		return CommandResult{}, ctx.Err()
	}
}

type testPluginCommands struct{}

func (testPluginCommands) Install(profile, packageSpec string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "add", packageSpec}, nil
}

func (testPluginCommands) InstallAt(profile, packageSpec, registry string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "add", packageSpec, "--registry", registry}, nil
}

func (testPluginCommands) Prepare(profile string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "install"}, nil
}

func (testPluginCommands) List(profile string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "list", "--depth", "0", "--json"}, nil
}

func (testPluginCommands) Outdated(profile, registry string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "outdated", "--format", "json", "--registry", registry}, nil
}

func (testPluginCommands) Update(profile, packageName, registry string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "update", packageName, "--registry", registry}, nil
}

func (testPluginCommands) Remove(profile, packageName string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "remove", packageName}, nil
}

type recordingInstaller struct {
	runtime RuntimeInfo
	version string
}

type recordingDSHCatalog struct {
	releases []DSHReleaseInfo
	calls    int
}

func (c *recordingDSHCatalog) Refresh(context.Context) ([]DSHReleaseInfo, error) {
	c.calls++
	return cloneDSHReleases(c.releases), nil
}

func testDSHReleases(versions ...string) []DSHReleaseInfo {
	releases := make([]DSHReleaseInfo, len(versions))
	for index, version := range versions {
		releases[index] = DSHReleaseInfo{Version: version, Source: RuntimeArtifactSourceOfficial, ObservedAt: "2026-09-06T00:00:00Z"}
	}
	return releases
}

func (i *recordingInstaller) Install(_ context.Context, version string) (RuntimeInfo, error) {
	i.version = version
	return i.runtime, nil
}

type cancelingInstaller struct {
	runtime   RuntimeInfo
	cancel    context.CancelFunc
	discarded bool
}

func (i *cancelingInstaller) Install(_ context.Context, _ string) (RuntimeInfo, error) {
	i.cancel()
	return i.runtime, nil
}

func (i *cancelingInstaller) Discard(context.Context, RuntimeInfo) error {
	i.discarded = true
	return nil
}

func (r *recordingRunner) Run(_ context.Context, path string, args []string, env map[string]string, cwd string) (CommandResult, error) {
	r.path = path
	r.args = append([]string(nil), args...)
	r.env = env
	r.cwd = cwd
	return CommandResult{}, nil
}

func TestManagerSerializesExternalPluginOperations(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "alpha"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		CommandRunner:   runner,
		PluginCommands:  testPluginCommands{},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	current := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}}
	if _, err := manager.CommitCurrent(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	target := PluginTarget{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "alpha"}}
	firstDone := make(chan error, 1)
	go func() {
		_, firstErr := manager.InstallPlugin(context.Background(), PluginInstallRequest{Target: target, Package: "@example/first"})
		firstDone <- firstErr
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first plugin operation did not reach the command runner")
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, secondErr := manager.InstallPlugin(secondCtx, PluginInstallRequest{Target: target, Package: "@example/second"})
	assertFailureCode(t, secondErr, lifecycle.ErrorCancelled)
	close(runner.release)
	select {
	case firstErr := <-firstDone:
		if firstErr != nil {
			t.Fatalf("first plugin operation error = %v", firstErr)
		}
	case <-time.After(time.Second):
		t.Fatal("first plugin operation did not release")
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	for _, profile := range []string{"web", "web-clean"} {
		if err := os.MkdirAll(filepath.Join(homePath, "profiles", profile), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work managed", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
		DefaultRunContext: RunContext{
			RuntimeID: "dsh-test",
			Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func assertFailureCode(t *testing.T, err error, want lifecycle.ErrorCode) {
	t.Helper()
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a lifecycle failure", err)
	}
	if failure.Code != want {
		t.Fatalf("failure code = %s, want %s", failure.Code, want)
	}
}

type testProfileCatalog struct {
	definitions []ProfileDefinition
}

func (c testProfileCatalog) BuiltInProfiles() []ProfileDefinition {
	return c.definitions
}

type recordingRuntimeVerifier struct {
	path                 string
	version              string
	calls                int
	env                  map[string]string
	profileDataDirectory string
	profileName          string
	profileCalls         int
}

func (v *recordingRuntimeVerifier) Verify(_ context.Context, path, version string) error {
	v.path = path
	v.version = version
	v.calls++
	return nil
}

func (v *recordingRuntimeVerifier) VerifyWithEnvironment(ctx context.Context, path, version string, env map[string]string) error {
	v.env = env
	return v.Verify(ctx, path, version)
}

func (v *recordingRuntimeVerifier) VerifyProfile(_ context.Context, _, _ string, dataDirectoryPath, profileName string) error {
	v.profileDataDirectory = dataDirectoryPath
	v.profileName = profileName
	v.profileCalls++
	return nil
}

type failingRuntimeVerifier struct{}

func (failingRuntimeVerifier) Verify(context.Context, string, string) error {
	return lifecycle.Failure{
		Code:    lifecycle.ErrorDSHUnsupportedVersion,
		Summary: "the test adapter rejected this runtime",
	}
}

type cancelAfterSaveStore struct {
	cancel context.CancelFunc
}

func (s cancelAfterSaveStore) Load(ctx context.Context, path string) (*State, error) {
	return (FileStateStore{}).Load(ctx, path)
}

func (s cancelAfterSaveStore) Save(ctx context.Context, path string, state State) error {
	err := (FileStateStore{}).Save(ctx, path, state)
	if err == nil && s.cancel != nil {
		s.cancel()
	}
	return err
}

func TestHealthyCommitResolvesOnlyMatchingFailure(t *testing.T) {
	for _, failedProfile := range []string{"web", "web-clean"} {
		t.Run(failedProfile, func(t *testing.T) {
			manager := newTestManager(t)
			target := RunContext{RuntimeID: "dsh-test", Node: NodeSelection{Kind: NodeSelectionSystem}, Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
			failed := target
			failed.Profile.Name = failedProfile
			if _, err := manager.RecordSwitchAttempt(context.Background(), SwitchAttempt{Target: failed, Failure: lifecycle.Failure{Code: lifecycle.ErrorProfilePreparationFailed}}, false); err != nil {
				t.Fatal(err)
			}
			snapshot, err := manager.CommitCurrent(context.Background(), &target)
			if err != nil {
				t.Fatal(err)
			}
			wantFailure := failedProfile != "web"
			if (snapshot.LastSwitchAttempt != nil) != wantFailure {
				t.Fatalf("failure after healthy commit = %+v", snapshot.LastSwitchAttempt)
			}
			reopened, err := New(manager.config)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err = reopened.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if (snapshot.LastSwitchAttempt != nil) != wantFailure {
				t.Fatalf("persisted failure = %+v", snapshot.LastSwitchAttempt)
			}
		})
	}
}
