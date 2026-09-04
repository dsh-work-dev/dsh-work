package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestManagerPersistsOnlyRuntimeDataDirectoryAndProfile(t *testing.T) {
	root := t.TempDir()
	dataDirectory := filepath.Join(root, "dsh-data")
	if err := os.MkdirAll(filepath.Join(dataDirectory, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}

	manager, err := New(Config{
		StatePath: filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{
			ID: "dsh-work", Name: "dsh-work managed", Path: dataDirectory, Ownership: DataDirectoryOwnershipDSHWork,
		}},
		Runtimes: []RuntimeInfo{{
			ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath,
		}},
		DefaultRunContext: RunContext{
			RuntimeID: "dsh-test",
			Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	target := RunContext{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
	}
	if _, err := manager.SetConfigured(context.Background(), target); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(root, "manager.json"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if strings.Contains(contents, "workspace") {
		t.Fatalf("persisted launch target contains workspace state: %s", contents)
	}
	if !strings.Contains(contents, "dataDirectoryId") || !strings.Contains(contents, "dataDirectories") {
		t.Fatalf("persisted launch target does not contain the new data-directory shape: %s", contents)
	}

	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Configured == nil || *snapshot.Configured != target {
		t.Fatalf("configured Run context = %#v, want %#v", snapshot.Configured, target)
	}
}

func TestFileStateStoreRejectsUnsupportedPreviousStateVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	for _, contents := range []string{
		`{"version":1,"homes":[],"desired":{"runtimeId":"dsh-test","profile":{"homeId":"dsh-work","name":"web"},"workspace":"project"}}`,
		`{"version":2,"dataDirectories":[{"id":"dsh-work","name":"dsh-work","path":"C:/dsh-work","ownership":"dsh-work"}],"runtimes":[],"desired":{"runtimeId":"dsh-test","profile":{"dataDirectoryId":"dsh-work","name":"web"}}}`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := (FileStateStore{}).Load(context.Background(), path)
		if err == nil {
			t.Fatal("Load() error = nil, want unsupported previous state version")
		}
		var failure lifecycle.Failure
		if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorManagerStateInvalid {
			t.Fatalf("Load() error = %#v, want manager-state-invalid failure", err)
		}
	}
}

func TestFileStateStoreRejectsAProfileWithoutDataDirectoryIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := os.WriteFile(path, []byte(`{"version":3,"configured":{"runtimeId":"dsh-test","profile":{"name":"web"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := (FileStateStore{}).Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load() error = nil, want malformed target rejection")
	}
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorManagerStateInvalid {
		t.Fatalf("Load() error = %v, want manager state invalid", err)
	}
}

func TestFileStateStoreRejectsWorkspaceFieldsInTheNewStateShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := os.WriteFile(path, []byte(`{"version":3,"dataDirectories":[],"workspace":"project"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := (FileStateStore{}).Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load() error = nil, want workspace field rejection")
	}
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorManagerStateInvalid {
		t.Fatalf("Load() error = %v, want manager state invalid", err)
	}
}
