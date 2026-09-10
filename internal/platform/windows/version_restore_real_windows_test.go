//go:build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/workergateway"
)

// This explicitly enabled integration uses the installed package managers and
// the pinned real DSH CLI, with a disposable data home and managed runtime store.
func TestVersionRestoreRealDSH(t *testing.T) {
	if os.Getenv("DSH_WORK_VERSION_RESTORE_REAL") != "1" {
		t.Skip("set DSH_WORK_VERSION_RESTORE_REAL=1 for real package-manager recovery")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(root, "tools", "dsh", "run-dsh.cmd")
	work := t.TempDir()
	home := filepath.Join(work, "home")
	if err = os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ctx = dshadapter.WithCommandOutput(ctx, func(line string) { t.Log(line) })
	run := CommandExecutor{}
	env := map[string]string{"DSH_HOME": home}
	command := func(args ...string) {
		t.Helper()
		if _, e := run.Run(ctx, fixture, args, env, home); e != nil {
			t.Fatal(e)
		}
	}
	command("plugin", "--profile", "web", "add", "is-number@7.0.0", "--save-exact")
	store := filepath.Join(work, "runtimes")
	config := dshmanager.Config{EnableVersionRestorePoints: true, DisableHealthSnapshots: true, StatePath: filepath.Join(work, "manager.json"), CommandRunner: preparationCommandRunner{}, PluginCommands: dshadapter.NewPluginCommands(), RuntimeInstaller: NewRuntimeInstaller(run, store), NodeResolver: NewRunNodeResolver(run, store), ProfileCatalog: dshadapter.New(run, dshadapter.SupportedVersion), Runtimes: []dshmanager.RuntimeInfo{{ID: "fixture", Version: dshadapter.SupportedVersion, Path: fixture}}, DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "home", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}}}
	m, err := dshmanager.New(config)
	if err != nil {
		t.Fatal(err)
	}
	target := dshmanager.RunContext{RuntimeID: "fixture", Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "home", Name: "web"}}
	launch, err := m.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := m.CommitHealthy(ctx, m.CaptureLaunchVersions(ctx, launch))
	if err != nil {
		t.Fatal(err)
	}
	if saved.RestorePoints.SaveError != "" || len(saved.RestorePoints.Points) != 1 {
		t.Fatalf("snapshot not captured: %+v", saved.RestorePoints)
	}
	id := saved.RestorePoints.LastRunning
	_, err = m.ClearCurrent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	command("plugin", "--profile", "web", "add", "is-number@6.0.0", "is-odd@3.0.1", "--save-exact", "--config.optimistic-repeat-install=false")
	profile := filepath.Join(home, "profiles", "web")
	sentinel := filepath.Join(profile, "node_modules", ".host-keeps-directory")
	if err = os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "settings.yaml"), []byte("user-setting: keep"), 0600); err != nil {
		t.Fatal(err)
	}
	restored, err := m.RecoverVersionPoint(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatal("Host removed node_modules", err)
	}
	input, err := dshadapter.NewPluginCommands().CaptureVersions(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Plugins) != 1 || input.Plugins[0].Version != "7.0.0" {
		t.Fatalf("wrong installed plugin set: %+v", input.Plugins)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "settings.yaml")); string(data) != "user-setting: keep" {
		t.Fatal("version restore changed user settings")
	}
	// Readiness is separately exercised by the Host integration. This invocation
	// proves the restored CLI can load its profile and render the composition.
	if _, err = run.Run(ctx, restored.Runtime.Path, []string{"--version"}, restored.Node.ChildEnvironment, home); err != nil {
		t.Fatal(err)
	}
	_, err = m.CommitHealthy(ctx, m.CaptureLaunchVersions(ctx, restored))
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.ClearCurrent(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Replace, rather than overwrite a hard link, to avoid corrupting pnpm's store.
	corrupt := filepath.Join(profile, "node_modules", "is-number", "index.js")
	temp := corrupt + ".broken"
	if err = os.WriteFile(temp, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(temp, corrupt); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RecoverVersionPoint(ctx, id); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(corrupt); string(data) == "broken" {
		t.Fatal("forced same-version install did not repair damaged files")
	}
	if _, err = os.Stat(sentinel); err != nil {
		t.Fatal("Host removed node_modules", err)
	}
}

func TestVersionPointRealStartupAndSafeMode(t *testing.T) {
	if os.Getenv("DSH_WORK_VERSION_RESTORE_REAL") != "1" {
		t.Skip("real DSH startup is opt-in")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	home := filepath.Join(work, "home")
	fixture := filepath.Join(root, "tools", "dsh", "run-dsh.cmd")
	run := CommandExecutor{}
	adapter := dshadapter.New(run, dshadapter.SupportedVersion)
	adapter.SetDiscoveryRoot(root)
	m, err := dshmanager.New(dshmanager.Config{EnableVersionRestorePoints: true, DisableHealthSnapshots: true, StatePath: filepath.Join(work, "manager.json"), CommandRunner: preparationCommandRunner{}, PluginCommands: dshadapter.NewPluginCommands(), RuntimeInstaller: NewRuntimeInstaller(run, filepath.Join(work, "runtimes")), NodeResolver: NewRunNodeResolver(run, filepath.Join(work, "runtimes")), ProfileCatalog: adapter, Runtimes: []dshmanager.RuntimeInfo{{ID: "fixture", Version: dshadapter.SupportedVersion, Path: fixture}}, DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "home", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}}, DefaultRunContext: dshmanager.RunContext{RuntimeID: "fixture", Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "home", Name: "web"}}})
	if err != nil {
		t.Fatal(err)
	}
	cfg := app.DefaultConfig(root)
	cfg.BootstrapDirectory = filepath.Join(work, "bootstrap")
	cfg.DSHDataDirectory = home
	cfg.ReadinessTimeout = 60 * time.Second
	cfg.ProbeTimeout = 2 * time.Second
	h := app.NewHost(app.Dependencies{Manager: m, DSH: adapter, Supervisor: NewJobObjectAdapter(), Gateway: workergateway.New()}, cfg)
	defer h.ShutdownForApp()
	statuses := make(chan lifecycle.Status, 256)
	h.SetPublish(func(s lifecycle.Status) {
		select {
		case statuses <- s:
		default:
		}
	})
	h.Start()
	deadline := time.NewTimer(75 * time.Second)
	defer deadline.Stop()
	for ready := false; !ready; {
		select {
		case s := <-statuses:
			if s.State == lifecycle.StateFailed {
				t.Fatalf("real startup: %+v; diagnostics=%+v", s, h.Diagnostics())
			}
			ready = s.State == lifecycle.StateReady
		case <-deadline.C:
			t.Fatal("real startup timed out")
		}
	}
	s, err := m.Snapshot(context.Background())
	if err != nil || s.RestorePoints.LastRunning == "" {
		t.Fatalf("first boot did not record snapshot: %+v %v", s.RestorePoints, err)
	}
	id := s.RestorePoints.LastRunning
	if _, err = m.SaveRestorePoint(context.Background(), "Real first boot"); err != nil {
		t.Fatal(err)
	}
	if _, err = h.EnterSafeMode(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err = m.Snapshot(context.Background())
	if err != nil || s.RestorePoints.LastRunning != id || s.RestorePoints.CanSave {
		t.Fatalf("safe mode changed normal snapshot: %+v %v", s.RestorePoints, err)
	}
	if err = h.ShutdownForApp(); err != nil {
		t.Fatal(err)
	}
	if h.Diagnostics().ActiveProcesses != 0 {
		t.Fatal("real Worker processes survived shutdown")
	}
}

func TestRestoreRuntimeRejectsLinkedLauncher(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "node_modules", ".bin", "dsh.cmd")
	if err := os.MkdirAll(filepath.Dir(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(outside, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, launcher); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if err := validateRestoreRuntimePaths(root); err == nil {
		t.Fatal("outside launcher accepted")
	}
}
