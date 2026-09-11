package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

type versionRecoveryFixtureInstaller struct {
	runtime dshmanager.RuntimeInfo
	calls   int
}

func (i *versionRecoveryFixtureInstaller) Install(context.Context, string) (dshmanager.RuntimeInfo, error) {
	return i.runtime, nil
}
func (i *versionRecoveryFixtureInstaller) ForceInstall(context.Context, string, dshmanager.ResolvedNode) (dshmanager.RuntimeInfo, error) {
	i.calls++
	if err := os.WriteFile(i.runtime.Path, []byte("test runtime"), 0600); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	return i.runtime, nil
}

type versionRecoveryFixtureRunner struct{ profile string }

func (r versionRecoveryFixtureRunner) Run(_ context.Context, _ string, args []string, _ map[string]string, _ string) (dshmanager.CommandResult, error) {
	if len(args) > 3 && args[3] == "install" {
		if err := os.WriteFile(filepath.Join(r.profile, "plugin-state"), []byte("healthy"), 0600); err != nil {
			return dshmanager.CommandResult{}, err
		}
	}
	return dshmanager.CommandResult{}, nil
}

func TestVersionRecoveryHostPolicyAndReadyLifetime(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(map[bool]string{true: "automatic", false: "choose"}[automatic], func(t *testing.T) {
			installer := &versionRecoveryFixtureInstaller{}
			var profile string
			f := newRunContextSwitchFixture(t, func(c *dshmanager.Config) {
				c.PluginCommands = dshadapter.NewPluginCommands()
				installer.runtime = c.Runtimes[0]
				c.RuntimeInstaller = installer
				profile = filepath.Join(c.DataDirectories[0].Path, "profiles", "alpha")
				c.CommandRunner = versionRecoveryFixtureRunner{profile: profile}
				if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(`{"dependencies":{"plugin":"1.0.0"}}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(profile, "node_modules", "plugin"), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(profile, "node_modules", "plugin", "package.json"), []byte(`{"version":"1.0.0"}`), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(profile, "pnpm-lock.yaml"), []byte("lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      plugin:\n        specifier: 1.0.0\n        version: 1.0.0\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(profile, "plugin-state"), []byte("healthy"), 0600); err != nil {
					t.Fatal(err)
				}
			})
			defer f.close()
			f.host.deps.AutomaticRuntimeRollback = func() bool { return automatic }
			f.host.deps.DSH = &fileHealthDSH{switchTestDSH: f.dsh, path: filepath.Join(profile, "plugin-state")}
			f.startReady(t)
			s, err := f.manager.Snapshot(context.Background())
			if err != nil || s.RestorePoints.LastRunning == "" {
				t.Fatalf("no startup point: %+v %v", s.RestorePoints, err)
			}
			// A candidate that fails readiness uses the saved point despite an identical
			// runtime/profile selection. Tuple equality is not a recovery shortcut.
			target := f.target("alpha")
			var mutate = func(context.Context, dshmanager.ResolvedLaunch) error {
				return os.WriteFile(filepath.Join(profile, "plugin-state"), []byte("broken"), 0600)
			}
			if !automatic {
				target = f.target("beta")
				f.dsh.setFailure("beta", true)
				mutate = nil
			}
			_, err = f.host.applyRunContext(context.Background(), target, mutate, nil, false)
			if err == nil {
				t.Fatal("candidate failure was lost")
			}
			s, _ = f.manager.Snapshot(context.Background())
			if automatic {
				if installer.calls != 1 || s.Current == nil || f.host.Status().State != lifecycle.StateReady {
					t.Fatalf("automatic restore did not complete: calls=%d snapshot=%+v", installer.calls, s)
				}
			} else {
				if installer.calls != 0 || s.Current != nil {
					t.Fatal("choose mode performed automatic installation")
				}
				if _, err = f.manager.PrepareSafeMode(context.Background()); err != nil {
					t.Fatal(err)
				}
				returnTo, err := f.manager.SafeModeReturnTarget(context.Background())
				if err != nil || returnTo != f.target("alpha") {
					t.Fatalf("safe mode must return to the last successful environment: %+v %v", returnTo, err)
				}
				if err = f.manager.AbortPreparedSafeMode(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, err = f.host.RestoreKnownGood(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			run := f.host.activeRun()
			select {
			case <-run.done:
				t.Fatal("restored Worker died after request completed")
			case <-time.After(20 * time.Millisecond):
			}
			s, _ = f.manager.Snapshot(context.Background())
			if s.RestorePoints.Operation == nil || s.RestorePoints.Operation.Status != "completed" {
				t.Fatal("recovery was not committed")
			}
		})
	}
}
