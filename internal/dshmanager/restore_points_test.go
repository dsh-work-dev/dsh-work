package dshmanager_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

type versionFailStore struct {
	dshmanager.FileStateStore
	fail bool
}

type versionUnavailableInstaller struct{ calls int }

func (i *versionUnavailableInstaller) Install(context.Context, string) (dshmanager.RuntimeInfo, error) {
	return dshmanager.RuntimeInfo{}, errors.New("download unavailable")
}
func (i *versionUnavailableInstaller) ForceInstall(context.Context, string, dshmanager.ResolvedNode) (dshmanager.RuntimeInfo, error) {
	i.calls++
	return dshmanager.RuntimeInfo{}, errors.New("download unavailable")
}

type versionNoCommand struct{}

func (versionNoCommand) Run(context.Context, string, []string, map[string]string, string) (dshmanager.CommandResult, error) {
	return dshmanager.CommandResult{}, errors.New("unexpected plugin command")
}

func (s *versionFailStore) Save(ctx context.Context, path string, state dshmanager.State) error {
	if s.fail {
		return errors.New("disk unavailable")
	}
	return s.FileStateStore.Save(ctx, path, state)
}

func TestVersionPointPersistenceManualRetentionAndFailedSave(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	profile := filepath.Join(home, "profiles", "web")
	write := func(name, value string) {
		t.Helper()
		path := filepath.Join(profile, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"dependencies":{"plugin":"1.0.0"},"dsh":{"profile":{"bundles":["plugin"]}}}`)
	write("node_modules/plugin/package.json", `{"name":"plugin","version":"1.0.0"}`)
	write("node_modules/.modules.yaml", "packageManager: pnpm@11.19.0\n")
	write("pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	runtime := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtime, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	store := &versionFailStore{}
	config := dshmanager.Config{StatePath: filepath.Join(root, "manager.json"), StateStore: store, PluginCommands: dshadapter.NewPluginCommands(), Runtimes: []dshmanager.RuntimeInfo{{ID: "dsh", Version: "1.0.0", Path: runtime}}, DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "home", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}}}
	m, err := dshmanager.New(config)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := m.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: "dsh", Profile: dshmanager.ProfileRef{DataDirectoryID: "home", Name: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	commit := func() dshmanager.Snapshot {
		t.Helper()
		snapshot, e := m.CommitHealthy(ctx, m.CaptureLaunchVersions(ctx, launch))
		if e != nil {
			t.Fatal(e)
		}
		return snapshot
	}
	first := commit()
	if first.RestorePoints == nil || len(first.RestorePoints.Points) != 1 {
		t.Fatalf("automatic point: %+v", first.RestorePoints)
	}
	id := first.RestorePoints.LastRunning
	if s := commit(); len(s.RestorePoints.Points) != 1 || s.RestorePoints.LastRunning != id {
		t.Fatal("repeat startup duplicated snapshot")
	}
	manual, err := m.SaveRestorePoint(ctx, "Before update")
	if err != nil {
		t.Fatal(err)
	}
	if len(manual.RestorePoints.Points) != 2 {
		t.Fatal("manual point missing")
	}
	write("node_modules/plugin/package.json", `{"name":"plugin","version":"2.0.0"}`)
	if _, err = m.SaveRestorePoint(ctx, "Unverified"); err == nil {
		t.Fatal("unverified plugin mutation was accepted")
	}
	stale := m.CaptureLaunchVersions(ctx, launch)
	write("node_modules/plugin/package.json", `{"name":"plugin","version":"3.0.0"}`)
	changed, err := m.CommitHealthy(ctx, stale)
	if err != nil || changed.Current == nil || changed.RestorePoints.SaveError == "" || changed.RestorePoints.LastRunning != id {
		t.Fatal("changed startup should preserve old point and ready Worker")
	}
	latest := commit()
	if latest.RestorePoints.LastRunning == id {
		t.Fatal("new version did not replace last-success pointer")
	}
	store.fail = true
	failed := commit()
	if failed.Current == nil || failed.RestorePoints.SaveError == "" {
		t.Fatal("save failure lost ready Worker")
	}
	store.fail = false
	reloaded, err := dshmanager.New(config)
	if err != nil {
		t.Fatal(err)
	}
	state, err := reloaded.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if state.Current != nil || state.KnownGood == nil || len(state.RestorePoints.Points) != 3 || state.RestorePoints.LastRunning != latest.RestorePoints.LastRunning {
		t.Fatalf("reload: %+v", state.RestorePoints)
	}
	// A process interruption can resume once; choose mode and a second process
	// interruption leave installation untouched until an explicit user retry.
	installer := &versionUnavailableInstaller{}
	config.RuntimeInstaller = installer
	config.CommandRunner = versionNoCommand{}
	persisted, err := store.Load(ctx, config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	persisted.VersionRecovery.Pending = &dshmanager.RecoveryOperation{PointID: latest.RestorePoints.LastRunning, Status: "running", Stage: "install-plugins"}
	if err = store.Save(ctx, config.StatePath, *persisted); err != nil {
		t.Fatal(err)
	}
	reloaded, err = dshmanager.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reloaded.ResumeVersionRecovery(ctx, false); err == nil || installer.calls != 0 {
		t.Fatal("choose mode resumed an interrupted recovery")
	}
	if _, err = reloaded.ResumeVersionRecovery(ctx, true); err == nil || installer.calls != 1 || reloaded.PendingVersionRecovery().ResumeCount != 1 {
		t.Fatalf("interrupted recovery was not attempted exactly once: calls=%d err=%v", installer.calls, err)
	}
	reloaded.FailVersionRecovery(ctx)
	if reloaded.PendingVersionRecovery().Error != "download unavailable" {
		t.Fatal("installation error was replaced by a generic startup failure")
	}
	persisted, _ = store.Load(ctx, config.StatePath)
	persisted.VersionRecovery.Pending.Status = "running"
	if err = store.Save(ctx, config.StatePath, *persisted); err != nil {
		t.Fatal(err)
	}
	reloaded, err = dshmanager.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reloaded.ResumeVersionRecovery(ctx, true); err == nil || installer.calls != 1 {
		t.Fatal("a second interruption caused an automatic retry loop")
	}
	if _, err = reloaded.RecoverVersionPoint(ctx, latest.RestorePoints.LastRunning); err == nil || installer.calls != 2 || reloaded.PendingVersionRecovery().ResumeCount != 0 {
		t.Fatal("explicit retry did not begin a new recovery attempt")
	}
}
