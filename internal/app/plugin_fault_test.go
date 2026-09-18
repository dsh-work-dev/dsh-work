package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
)

const faultProfileManifest = `{
  "dependencies": {"@acme/widget": "1.0.0", "@acme/other": "1.0.0"},
  "dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-web-app", "@acme/widget", "@acme/other"]}}
}
`

// exitingSupervisor starts Workers that exit before readiness with the given
// captured stderr, the shape of a plugin tree that failed to load.
type exitingSupervisor struct {
	stderr string
	starts int
}

func (s *exitingSupervisor) Start(context.Context, supervisor.LaunchPlan, supervisor.RawOutputHandler) (supervisor.Worker, error) {
	s.starts++
	worker := &exitingWorker{testWorker: newTestWorker(), stderr: s.stderr}
	worker.once.Do(func() { close(worker.exited) })
	return worker, nil
}

type exitingWorker struct {
	*testWorker
	stderr string
}

func (w *exitingWorker) Diagnostics() supervisor.Diagnostics {
	return supervisor.Diagnostics{StderrTail: w.stderr}
}

func newFaultTestHost(t *testing.T, stderr string) (*Host, *exitingSupervisor, chan lifecycle.Status, string) {
	t.Helper()
	root := t.TempDir()
	dataDirectory := filepath.Join(root, "dsh-work")
	profilePath := filepath.Join(dataDirectory, "profiles", "web")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(faultProfileManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "dsh-work", Name: "dsh-work", Path: dataDirectory, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}},
		Runtimes:        []dshmanager.RuntimeInfo{{ID: "dsh-test", Version: dshadapter.SupportedVersion, Path: runtimePath}},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-test", Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	dsh := newTestDSH()
	t.Cleanup(dsh.server.Close)
	supervisorAdapter := &exitingSupervisor{stderr: stderr}
	host := NewHost(Dependencies{DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server}}, Config{
		BootstrapDirectory: t.TempDir(),
		DSHDataDirectory:   t.TempDir(),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
		EmptyTimeout:       time.Second,
		ShutdownTimeout:    time.Second,
	})
	statuses := make(chan lifecycle.Status, 32)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	return host, supervisorAdapter, statuses, profilePath
}

func faultBundles(t *testing.T, profilePath string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		DSH struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest.DSH.Profile.Bundles
}

func TestFailedStartNamesInstalledPluginAndDisablesIt(t *testing.T) {
	host, supervisorAdapter, statuses, profilePath := newFaultTestHost(t, "Error: dsh: plugin tree failed to load: failed to apply loader entry ui-widget (@acme/widget): boom\n    at init (file:///C:/x/node_modules/@deepseek-ai/dsh-app-boot/lib/index.js:1:1)")
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.PluginFault == nil || !slices.Equal(failed.PluginFault.Plugins, []string{"@acme/widget"}) {
		t.Fatalf("plugin fault = %+v, want only the installed third-party plugin", failed.PluginFault)
	}

	if _, err := host.DisableFaultPlugin(context.Background(), "@acme/other"); err == nil {
		t.Fatal("a plugin the failure did not name was changed")
	}
	status, err := host.DisableFaultPlugin(context.Background(), "@acme/widget")
	if err != nil {
		t.Fatal(err)
	}
	if status.PluginFault != nil {
		t.Fatalf("handled plugin is still offered: %+v", status.PluginFault)
	}
	if got := faultBundles(t, profilePath); !slices.Equal(got, []string{"@deepseek-ai/dsh-web-app", "@acme/other"}) {
		t.Fatalf("bundles after disable = %v", got)
	}

	// DSH's own plugin commands put every installed layer back; the next launch
	// must take the disabled plugin out again before the Worker starts.
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(faultProfileManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	host.Start()
	waitForStatus(t, statuses, lifecycle.StateFailed)
	if supervisorAdapter.starts != 2 {
		t.Fatalf("worker starts = %d, want 2", supervisorAdapter.starts)
	}
	if got := faultBundles(t, profilePath); !slices.Equal(got, []string{"@deepseek-ai/dsh-web-app", "@acme/other"}) {
		t.Fatalf("bundles at relaunch = %v, want the disabled plugin kept out", got)
	}
}

func TestStoredDataRejectionOffersNoPlugin(t *testing.T) {
	host, _, statuses, _ := newFaultTestHost(t, "Error: dsh: plugin tree failed to load: failed to apply loader entry ws (@acme/widget/workspace): corrupt Zstandard session log: first frame is not exactly one header line")
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.PluginFault != nil {
		t.Fatalf("stored-data rejection blamed a plugin: %+v", failed.PluginFault)
	}
}
