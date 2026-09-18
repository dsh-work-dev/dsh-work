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
