package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/work/internal/lifecycle"
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
			ID: "work", Name: "Work managed", Path: dataDirectory, Ownership: DataDirectoryOwnershipWork,
		}},
		Runtimes: []RuntimeInfo{{
			ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath,
		}},
		DefaultRunContext: RunContext{
			RuntimeID: "dsh-test",
			Profile:   ProfileRef{DataDirectoryID: "work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	target := RunContext{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{DataDirectoryID: "work", Name: "web"},
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

func TestFileStateStoreRejectsThePreviousStateVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"homes":[],"desired":{"runtimeId":"dsh-test","profile":{"homeId":"work","name":"web"},"workspace":"project"}}`), 0o600); err != nil {
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

func TestFileStateStoreMigratesLegacyConfiguredSelection(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "manager.json")
	legacy := `{"version":2,"dataDirectories":[{"id":"work","name":"Work","path":"` + filepath.ToSlash(filepath.Join(root, "dsh-data")) + `","ownership":"work"}],"runtimes":[{"id":"dsh-test","version":"0.1.2-alpha.3","path":"dsh.cmd"}],"desired":{"runtimeId":"dsh-test","profile":{"dataDirectoryId":"work","name":"web"}}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := (FileStateStore{}).Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Version != stateVersion || state.Configured == nil || state.Configured.Profile.Name != "web" {
		t.Fatalf("migrated state = %#v, want configured v%d state", state, stateVersion)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	contents := string(data)
	if strings.Contains(contents, `"desired"`) || !strings.Contains(contents, `"configured"`) {
		t.Fatalf("migrated state still exposes legacy selection: %s", contents)
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
