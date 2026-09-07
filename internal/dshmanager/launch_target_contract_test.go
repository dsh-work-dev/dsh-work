package dshmanager

import (
	"context"
	"encoding/json"
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
		Node:      NodeSelection{Kind: NodeSelectionSystem},
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
	if !strings.Contains(contents, "dataDirectoryId") || !strings.Contains(contents, "dataDirectories") || !strings.Contains(contents, `"version": "0.1.2-alpha.3"`) {
		t.Fatalf("persisted launch target does not contain the new data-directory shape: %s", contents)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("persisted launch target is invalid JSON: %v", err)
	}
	if _, exists := persisted["version"]; exists {
		t.Fatalf("persisted launch target contains a state version: %s", contents)
	}

	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Configured == nil || *snapshot.Configured != target {
		t.Fatalf("configured Run context = %#v, want %#v", snapshot.Configured, target)
	}
}

func TestFileStateStoreReadsSelectionWithoutStateVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	contents := `{"dataDirectories":[{"id":"dsh-work","name":"dsh-work","path":"C:/dsh-work","ownership":"dsh-work","futureDirectoryField":true}],"runtimes":[],"configured":{"runtimeId":"dsh-test","profile":{"dataDirectoryId":"dsh-work","name":"web"},"futureContextField":{"anything":"goes"}},"futureTopLevelField":[1,2,3]}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := (FileStateStore{}).Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load() error = %v, want compatible state", err)
	}
	if state == nil || state.Configured == nil || state.Configured.Node.Kind != NodeSelectionSystem {
		t.Fatalf("loaded state = %#v, want selection and system Node default", state)
	}
}

func TestFileStateStoreReadsForwardStateFieldsNeededByTheCurrentCatalog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	contents := `{"dataDirectories":[{"id":"dsh-work","name":"dsh-work","path":"C:/dsh-work","ownership":"dsh-work"}],"runtimes":[{"id":"dsh-test","version":"0.1.2-alpha.3","path":"C:/dsh.cmd","source":"development-fixture","toolchain":"none","installSource":"none","installed":true,"removable":false}],"nodes":[],"dshReleases":[],"pluginProvenance":[],"configured":{"runtimeId":"dsh-test","node":{"kind":"system"},"profile":{"dataDirectoryId":"dsh-work","name":"web"}}}`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := (FileStateStore{}).Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load() error = %v, want current catalog fields to be readable", err)
	}
	if state == nil || len(state.Runtimes) != 1 || state.Runtimes[0].ID != "dsh-test" || state.Configured == nil || state.Configured.Node.Kind != NodeSelectionSystem {
		t.Fatalf("loaded state = %#v, want runtime and configured selection", state)
	}
}

func TestFileStateStoreRejectsAProfileWithoutDataDirectoryIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := os.WriteFile(path, []byte(`{"configured":{"runtimeId":"dsh-test","profile":{"name":"web"}}}`), 0o600); err != nil {
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

func TestFileStateStoreRejectsAConfiguredRuntimeWithoutRequiredIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := os.WriteFile(path, []byte(`{"runtimes":[{"id":"dsh-test"}],"configured":{"runtimeId":"dsh-test","node":{"kind":"system"},"profile":{"dataDirectoryId":"dsh-work","name":"web"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := (FileStateStore{}).Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load() error = nil, want malformed runtime rejection")
	}
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorManagerStateInvalid {
		t.Fatalf("Load() error = %v, want manager state invalid", err)
	}
}

func TestFileStateStoreIgnoresUnknownStateFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manager.json")
	if err := os.WriteFile(path, []byte(`{"version":4,"dataDirectories":[],"workspace":"project","future":{"state":"ignored"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	state, err := (FileStateStore{}).Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load() error = %v, want unknown fields to be ignored", err)
	}
	if state == nil {
		t.Fatalf("loaded state = %#v, want readable state", state)
	}
}
