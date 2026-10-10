package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/workspacecontext"
)

type safeModeFailingCommandRunner struct{}

func (safeModeFailingCommandRunner) Run(context.Context, string, []string, map[string]string, string) (dshmanager.CommandResult, error) {
	return dshmanager.CommandResult{}, errors.New("fixture plugin operation failed")
}

func TestSafeModeRejectedSwitchDiscardsReservation(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	before, _ := f.manager.Snapshot(context.Background())
	f.host.mu.Lock()
	f.host.shutdownRequested = true
	f.host.mu.Unlock()
	_, err := f.host.EnterSafeMode(context.Background())
	if err == nil {
		t.Fatal("shutdown accepted a new switch")
	}
	after, _ := f.manager.Snapshot(context.Background())
	if after.SafeMode != nil || after.Configured == nil || *after.Configured != *before.Configured || len(after.DataDirectories) != len(before.DataDirectories) {
		t.Fatalf("failed entry retained reservation: %#v", after)
	}
}

func TestSafeModeFailedReturnRestartsSafeWorker(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	snapshot, err := f.host.EnterSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || snapshot.SafeMode == nil {
		t.Fatalf("safe mode = %#v", snapshot)
	}
	f.supervisor.startErrors["alpha"] = errors.New("original plugins still broken")
	snapshot, err = f.host.ExitSafeMode(context.Background())
	if err == nil {
		t.Fatal("failed original environment accepted")
	}
	if snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || snapshot.SafeMode == nil || snapshot.Configured == nil || snapshot.Configured.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || f.host.Status().State != lifecycle.StateReady {
		t.Fatalf("failed return lost its safe-mode reservation: %#v", snapshot)
	}
	delete(f.supervisor.startErrors, "alpha")
	snapshot, err = f.host.ExitSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || snapshot.Current.Profile.Name != "alpha" || snapshot.SafeMode != nil {
		t.Fatalf("exit = %#v", snapshot)
	}
}

func TestSafeModeExitDiscardsRescueEnvironment(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	snapshot, err := f.host.EnterSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session := ""
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == dshmanager.SafeModeDataDirectoryID {
			session = directory.Path
		}
	}
	if session == "" {
		t.Fatalf("safe mode data directory missing: %#v", snapshot.DataDirectories)
	}
	snapshot, err = f.host.ExitSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == dshmanager.SafeModeDataDirectoryID {
			t.Fatalf("safe mode data directory retained after exit: %#v", snapshot.DataDirectories)
		}
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref.DataDirectoryID == dshmanager.SafeModeDataDirectoryID {
			t.Fatalf("safe mode profile retained after exit: %#v", profile)
		}
	}
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Fatalf("safe mode session directory retained: %v", err)
	}
}

func TestSafeModeRebindsSelectedWorkspaceToNewGeneration(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	workspacePath := t.TempDir()
	expectedWorkspacePath, pathErr := filepath.EvalSymlinks(workspacePath)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	requests := make([]workspacecontext.Request, 0, 2)
	f.host.deps.WorkspaceResolver = workspacecontext.ResolverFunc(func(_ context.Context, generation string, request workspacecontext.Request) (workspacecontext.Context, error) {
		requests = append(requests, request)
		if request.ID == "" {
			request = workspacecontext.Request{ID: "workspace-1", Path: workspacePath, Title: "Project"}
		}
		return workspacecontext.NewSelected(generation, request.ID, request.Path, request.Title)
	})
	f.startReady(t)
	_, err := f.host.EnterSafeModeWithOptions(context.Background(), dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeWithData})
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatalf("workspace resolve requests = %#v", requests)
	}
	if requests[1].ID != "workspace-1" || requests[1].Path != expectedWorkspacePath || requests[1].Title != "Project" {
		t.Fatalf("workspace was not rebound: %#v", requests[1])
	}
	snapshot, err := f.host.TrySafeModeTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.SafeMode != nil || snapshot.Current == nil || snapshot.Current.Profile.Name != "alpha" {
		t.Fatalf("successful normal attempt did not complete repair: %#v", snapshot)
	}
	if len(requests) != 3 || requests[2].ID != "workspace-1" || requests[2].Path != expectedWorkspacePath || requests[2].Title != "Project" {
		t.Fatalf("normal attempt did not rebind workspace: %#v", requests)
	}
}

func TestSafeModeRepairDisablesPluginInStoredFaultTarget(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	before, err := f.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var betaHome string
	for _, directory := range before.DataDirectories {
		if directory.ID == "beta-home" {
			betaHome = directory.Path
		}
	}
	manifest := filepath.Join(betaHome, "profiles", "beta", "package.json")
	if err := os.WriteFile(manifest, []byte(`{"dependencies":{"@example/plugin":"1.0.0"},"dsh":{"profile":{"bundles":["@example/plugin"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(betaHome, "profiles", "beta", "node_modules", "@example", "plugin")
	if err := os.MkdirAll(packagePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "package.json"), []byte(`{"name":"@example/plugin","version":"1.0.0","dsh":{"bundle":{"patch":"./patch.json"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	faultTarget := f.target("beta")
	if _, err := f.host.EnterSafeModeWithOptions(context.Background(), dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeWithData, FaultTarget: &faultTarget}); err != nil {
		t.Fatal(err)
	}
	service := NewHostService(f.host, nil)
	if _, err := service.RepairSafeModePlugin(context.Background(), "@example/plugin", "disable"); err == nil {
		t.Fatal("safe-mode plugin repair accepted an untrusted surface")
	} else if failure, ok := err.(lifecycle.Failure); !ok || failure.Code != lifecycle.ErrorTrustedSurfaceRequired {
		t.Fatalf("untrusted repair error = %#v", err)
	}
	snapshot, err := service.RepairSafeModePlugin(LocalClientContext(context.Background(), "settings"), "@example/plugin", "disable")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || f.host.Status().State != lifecycle.StateReady {
		t.Fatalf("repair did not return to safe mode: %#v / %+v", snapshot, f.host.Status())
	}
	updated, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Dependencies map[string]string `json:"dependencies"`
		DSH          struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(updated, &state); err != nil {
		t.Fatal(err)
	}
	if _, remainsInstalled := state.Dependencies["@example/plugin"]; !remainsInstalled || len(state.DSH.Profile.Bundles) != 0 {
		t.Fatalf("fault target manifest after disable = %s", updated)
	}
	if snapshot.SafeMode == nil || snapshot.SafeMode.FaultTarget != faultTarget || snapshot.SafeMode.ReturnTo.Profile.Name != "alpha" {
		t.Fatalf("repair changed recovery targets: %#v", snapshot.SafeMode)
	}
}

func TestSafeModeFailedNormalAttemptRestartsSafeWorker(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	faultTarget := f.target("beta")
	if _, err := f.host.EnterSafeModeWithOptions(context.Background(), dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeWithData, FaultTarget: &faultTarget}); err != nil {
		t.Fatal(err)
	}
	f.supervisor.startErrors["beta"] = errors.New("fault target is still broken")
	snapshot, err := f.host.TrySafeModeTarget(context.Background())
	if err == nil {
		t.Fatal("failed normal attempt was reported as repaired")
	}
	if snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || snapshot.SafeMode == nil || f.host.Status().State != lifecycle.StateReady {
		t.Fatalf("failed normal attempt lost safe worker: %#v / %+v", snapshot, f.host.Status())
	}
	if snapshot.SafeMode.FaultTarget != faultTarget || snapshot.SafeMode.ReturnTo.Profile.Name != "alpha" {
		t.Fatalf("failed attempt changed recovery targets: %#v", snapshot.SafeMode)
	}
}

func TestSafeModeSuccessfulNormalAttemptUsesFaultTarget(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	faultTarget := f.target("beta")
	if _, err := f.host.EnterSafeModeWithOptions(context.Background(), dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeWithData, FaultTarget: &faultTarget}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.host.TrySafeModeTarget(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || snapshot.Current.Profile != faultTarget.Profile || snapshot.Configured == nil || snapshot.Configured.Profile != faultTarget.Profile || snapshot.SafeMode != nil {
		t.Fatalf("successful repair did not select its fault target: %#v", snapshot)
	}
}

func TestSafeModePluginOperationFailureStillRestartsSafeWorker(t *testing.T) {
	f := newRunContextSwitchFixture(t, func(config *dshmanager.Config) {
		config.CommandRunner = safeModeFailingCommandRunner{}
	})
	defer f.close()
	f.startReady(t)
	snapshot, err := f.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var betaHome string
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == "beta-home" {
			betaHome = directory.Path
		}
	}
	manifest := filepath.Join(betaHome, "profiles", "beta", "package.json")
	if err := os.WriteFile(manifest, []byte(`{"dependencies":{"@example/plugin":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	faultTarget := f.target("beta")
	if _, err := f.host.EnterSafeModeWithOptions(context.Background(), dshmanager.SafeModeRequest{Mode: dshmanager.SafeModeWithData, FaultTarget: &faultTarget}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = f.host.RepairSafeModePlugin(context.Background(), "@example/plugin", "remove")
	if err == nil {
		t.Fatal("failed plugin removal was reported as successful")
	}
	if snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || snapshot.SafeMode == nil || f.host.Status().State != lifecycle.StateReady {
		t.Fatalf("plugin failure did not restore safe mode: %#v / %+v", snapshot, f.host.Status())
	}
}
