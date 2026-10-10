package dshmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewDiscardsSafeModeLeftAfterExit(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "manager.json")
	session := filepath.Join(root, "safe-mode", "session-1")
	if err := os.MkdirAll(filepath.Join(session, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "environment")
	if err := os.MkdirAll(filepath.Join(home, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	configured := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
	if err := (FileStateStore{}).Save(context.Background(), statePath, State{
		Configured: &configured,
		DataDirectories: []DataDirectoryInfo{
			{ID: "dsh-work", Name: "DSH Work", Path: home, Ownership: DataDirectoryOwnershipDSHWork},
			{ID: SafeModeDataDirectoryID, Name: "Safe mode", Path: session, Ownership: DataDirectoryOwnershipDSHWork},
		},
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       statePath,
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "DSH Work", Path: home, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == SafeModeDataDirectoryID {
			t.Fatalf("stale safe mode data directory listed: %#v", snapshot.DataDirectories)
		}
	}
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Fatalf("stale safe mode session retained: %v", err)
	}
}

func TestNewKeepsActiveSafeModeSession(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "manager.json")
	session := filepath.Join(root, "safe-mode", "session-1")
	orphan := filepath.Join(root, "safe-mode", "session-0")
	for _, directory := range []string{filepath.Join(session, "profiles", "web"), orphan} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(root, "environment")
	if err := os.MkdirAll(filepath.Join(home, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	returnTo := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}}
	rescue := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: SafeModeDataDirectoryID, Name: "web"}}
	if err := (FileStateStore{}).Save(context.Background(), statePath, State{
		Configured: &rescue,
		SafeMode:   &SafeModeState{Target: rescue, ReturnTo: returnTo},
		DataDirectories: []DataDirectoryInfo{
			{ID: "dsh-work", Name: "DSH Work", Path: home, Ownership: DataDirectoryOwnershipDSHWork},
			{ID: SafeModeDataDirectoryID, Name: "Safe mode", Path: session, Ownership: DataDirectoryOwnershipDSHWork},
		},
	}); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       statePath,
		DataDirectories: []DataDirectoryInfo{{ID: "dsh-work", Name: "DSH Work", Path: home, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SafeMode == nil {
		t.Fatal("active safe mode was discarded")
	}
	if _, err := os.Stat(session); err != nil {
		t.Fatalf("active session removed: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan session retained: %v", err)
	}
}

func TestSafeModePersistsModeFaultTargetAndIndependentReturnTarget(t *testing.T) {
	m := newTestManager(t)
	configured := *m.configured
	faultTarget := configured
	faultTarget.Profile.Name = "broken"

	target, err := m.PrepareSafeMode(context.Background(), SafeModeRequest{
		Mode:        SafeModeWithData,
		FaultTarget: &faultTarget,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.Profile.DataDirectoryID != SafeModeDataDirectoryID {
		t.Fatalf("safe target = %#v", target)
	}
	launch, err := m.ResolveLaunch(context.Background(), LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil || !launch.UserDataOverlay {
		t.Fatalf("with-data launch overlay = %v, %v", launch.UserDataOverlay, err)
	}

	reloaded, err := New(Config{StatePath: m.config.StatePath})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SafeMode == nil {
		t.Fatal("safe mode state was not restored")
	}
	if snapshot.SafeMode.Mode != SafeModeWithData {
		t.Fatalf("mode = %q", snapshot.SafeMode.Mode)
	}
	if snapshot.SafeMode.FaultTarget != faultTarget {
		t.Fatalf("fault target = %#v, want %#v", snapshot.SafeMode.FaultTarget, faultTarget)
	}
	if snapshot.SafeMode.ReturnTo != configured {
		t.Fatalf("return target = %#v, want %#v", snapshot.SafeMode.ReturnTo, configured)
	}
}

func TestSafeModeFaultTargetRemainsProtectedForRepair(t *testing.T) {
	m := newTestManager(t)
	m.config.ProfileCatalog = testProfileCatalog{}
	fault := *m.configured
	fault.Profile.Name = "broken"
	faultPath := filepath.Join(m.config.DataDirectories[0].Path, "profiles", fault.Profile.Name)
	if err := os.MkdirAll(faultPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(faultPath, "package.json"), []byte(`{"dependencies":{"@example/plugin":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PrepareSafeMode(context.Background(), SafeModeRequest{Mode: SafeModeWithData, FaultTarget: &fault}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref == fault.Profile {
			if profile.Deletable || profile.Renamable {
				t.Fatalf("fault target was exposed as mutable: %#v", profile)
			}
			if len(profile.Plugins) != 1 || profile.Plugins[0].Name != "@example/plugin" {
				t.Fatalf("fault target plugins = %#v", profile.Plugins)
			}
			return
		}
	}
	t.Fatal("fault target profile was not visible")
}

func TestDecodeLegacySafeModeDefaultsToEmptyDiagnostic(t *testing.T) {
	data := []byte(`{"safeMode":{"target":{"runtimeId":"dsh-test","profile":{"dataDirectoryId":"dsh-work-safe-mode","name":"web"}},"returnTo":{"runtimeId":"dsh-test","profile":{"dataDirectoryId":"dsh-work","name":"web"}}}}`)
	state, err := decodeState(data)
	if err != nil {
		t.Fatal(err)
	}
	if state.SafeMode.Mode != SafeModeDiagnostic {
		t.Fatalf("legacy mode = %q", state.SafeMode.Mode)
	}
	if state.SafeMode.FaultTarget != state.SafeMode.ReturnTo {
		t.Fatalf("legacy fault target = %#v, want return target %#v", state.SafeMode.FaultTarget, state.SafeMode.ReturnTo)
	}
}
