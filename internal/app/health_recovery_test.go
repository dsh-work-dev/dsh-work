package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
)

type blockedHealthManager struct {
	*dshmanager.Manager
	entered chan struct{}
	release chan struct{}
	done    chan struct{}
}

func (m *blockedHealthManager) CommitHealthy(ctx context.Context, _ dshmanager.ResolvedLaunch) (dshmanager.Snapshot, error) {
	close(m.entered)
	defer close(m.done)
	select {
	case <-ctx.Done():
	case <-m.release:
	}
	return dshmanager.Snapshot{}, ctx.Err()
}

func (m *blockedHealthManager) BeginRunContextSwitch(ctx context.Context) error {
	<-m.done // Like the real manager, wait for the commit operation to release its gate.
	return m.Manager.BeginRunContextSwitch(ctx)
}

func TestCancelInterruptsHealthCommitBeforeWaitingForManagerGuard(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	m := &blockedHealthManager{Manager: f.manager, entered: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
	f.host.deps.Manager = m
	defer f.close()
	defer close(m.release)
	f.host.Start()
	select {
	case <-m.entered:
	case <-time.After(time.Second):
		t.Fatal("startup did not reach health commit")
	}
	if state := f.host.Status(); state.State != lifecycle.StateStarting || state.Phase != lifecycle.PhaseCheckpoint {
		t.Fatalf("health commit published premature readiness: %+v", state)
	}
	result := make(chan lifecycle.Status, 1)
	go func() { result <- f.host.Cancel() }()
	select {
	case state := <-result:
		if state.State != lifecycle.StateStopping {
			t.Fatalf("cancel returned %s", state.State)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel waited for the commit before cancelling it")
	}
	f.waitForHostState(t, lifecycle.StateStopped)
}

func TestColdStartupRestoresPersistedVersionRecord(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	if err := f.host.ShutdownForApp(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	runtime := snapshot.Runtimes[0]
	statePath := filepath.Join(filepath.Dir(runtime.Path), "manager.json")
	installer := &versionRecoveryFixtureInstaller{runtime: runtime}
	manager, err := dshmanager.New(dshmanager.Config{StatePath: statePath, PluginCommands: switchVersionCommands{}, CommandRunner: versionRecoveryFixtureRunner{}, RuntimeInstaller: installer})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(runtime.Path); err != nil {
		t.Fatal(err)
	}
	f.host = NewHost(Dependencies{DSH: f.dsh, Manager: manager, Supervisor: f.supervisor, Channel: &testChannel{server: f.dsh.server}}, f.host.config)
	f.host.Start()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err = manager.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.LastSwitchAttempt != nil && snapshot.LastSwitchAttempt.Rollback == dshmanager.RollbackRestored {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if snapshot.LastSwitchAttempt == nil || snapshot.LastSwitchAttempt.Rollback != dshmanager.RollbackRestored {
		t.Fatalf("cold recovery did not complete: %#v, status %#v", snapshot.LastSwitchAttempt, f.host.Status())
	}
	assertRunContext(t, snapshot.Current, f.target("alpha"), "recovered current")
	if installer.calls != 1 || f.host.Status().State != lifecycle.StateReady {
		t.Fatal("version recovery did not reinstall and reach Ready")
	}
}

type filePluginManager struct {
	*dshmanager.Manager
	path string
	host *Host
}

func (m *filePluginManager) ApplyPlugin(ctx context.Context, launch dshmanager.ResolvedLaunch, spec, operation string) (dshmanager.PluginResult, error) {
	if run := m.host.activeRun(); run != nil && run.cleanupPending() {
		return dshmanager.PluginResult{}, errors.New("plugin ran before worker cleanup")
	}
	return dshmanager.PluginResult{Profile: launch.Target.Profile}, os.WriteFile(m.path, []byte("broken"), 0o600)
}

type fileHealthDSH struct {
	*switchTestDSH
	path string
}

func (d *fileHealthDSH) Probe(ctx context.Context, announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan, client *http.Client) error {
	data, err := os.ReadFile(d.path)
	if err != nil || string(data) != "healthy" {
		return errors.New("plugin failed to load")
	}
	return d.switchTestDSH.Probe(ctx, announcement, plan, client)
}

func TestPluginChangesReinstallVersionsThroughSwitchTransaction(t *testing.T) {
	for _, operation := range []string{"add", "update", "remove"} {
		t.Run(operation, func(t *testing.T) {
			f := newRunContextSwitchFixture(t, func(c *dshmanager.Config) {
				profile := filepath.Join(c.DataDirectories[0].Path, "profiles", "alpha")
				c.PluginCommands = dshadapter.NewPluginCommands()
				c.CommandRunner = versionRecoveryFixtureRunner{profile: profile}
				for name, contents := range map[string]string{"package.json": `{"dependencies":{"plugin":"1.0.0"}}`, "pnpm-lock.yaml": "lockfileVersion: '9.0'\n", "node_modules/plugin/package.json": `{"version":"1.0.0"}`} {
					path := filepath.Join(profile, name)
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
						t.Fatal(err)
					}
				}
			})
			defer f.close()
			snapshot, err := f.manager.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(snapshot.DataDirectories[0].Path, "profiles", "alpha", "plugin-state")
			if err := os.WriteFile(path, []byte("healthy"), 0o600); err != nil {
				t.Fatal(err)
			}
			f.host.deps.Manager = &filePluginManager{Manager: f.manager, path: path, host: f.host}
			f.host.deps.DSH = &fileHealthDSH{switchTestDSH: f.dsh, path: path}
			f.startReady(t)
			target := dshmanager.PluginTarget{Profile: f.target("alpha").Profile}
			switch operation {
			case "add":
				_, err = f.host.InstallPlugin(context.Background(), dshmanager.PluginInstallRequest{Target: target, Package: "plugin"})
			case "update":
				_, err = f.host.UpgradePlugin(context.Background(), dshmanager.PluginUpgradeRequest{Target: target, Package: "plugin"})
			case "remove":
				_, err = f.host.RemovePlugin(context.Background(), dshmanager.PluginRemoveRequest{Target: target, Package: "plugin"})
			}
			if err == nil {
				t.Fatal("broken plugin was accepted")
			}
			snapshot, err = f.manager.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.LastSwitchAttempt == nil || snapshot.LastSwitchAttempt.Rollback != dshmanager.RollbackRestored {
				t.Fatalf("plugin recovery = %#v", snapshot.LastSwitchAttempt)
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != "healthy" {
				t.Fatalf("plugin contents = %q, %v", data, err)
			}
			if f.host.Status().State != lifecycle.StateReady {
				t.Fatal("restored plugin worker is not Ready")
			}
		})
	}
}

func TestStartupMissingRuntimePreservesConfiguredEnvironment(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	before, err := f.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range before.Runtimes {
		if runtime.ID == before.Configured.RuntimeID {
			if err := os.Rename(runtime.Path, runtime.Path+".unavailable"); err != nil {
				t.Fatal(err)
			}
		}
	}
	f.host.Start()
	f.waitForHostState(t, lifecycle.StateFailed)
	after, err := f.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if after.Current != nil || after.Configured == nil || *after.Configured != *before.Configured {
		t.Fatalf("missing runtime changed environment: %#v", after)
	}
}
