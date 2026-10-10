package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
)

// The profile has two selected bundles, one installed but unselected bundle
// and one plain library dependency.
const faultProfileManifest = `{
  "dependencies": {"@acme/widget": "1.0.0", "@acme/other": "1.0.0", "@acme/idle": "1.0.0", "@acme/lib": "1.0.0"},
  "dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-web-app", "@acme/widget", "@acme/other"]}}
}
`

var faultBundles = []string{"@acme/widget", "@acme/other", "@acme/idle"}

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
	for _, name := range append(slices.Clone(faultBundles), "@acme/lib") {
		packagePath := filepath.Join(append([]string{profilePath, "node_modules"}, strings.Split(name, "/")...)...)
		if err := os.MkdirAll(packagePath, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := `{"name":"` + name + `","version":"1.0.0"}`
		if slices.Contains(faultBundles, name) {
			manifest = `{"name":"` + name + `","version":"1.0.0","dsh":{"bundle":{"patch":"patch.yml"}}}`
		}
		if err := os.WriteFile(filepath.Join(packagePath, "package.json"), []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
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

func TestStoredDataRejectionOffersNoPlugin(t *testing.T) {
	host, _, statuses, _ := newFaultTestHost(t, "Error: dsh: plugin tree failed to load: failed to apply loader entry ws (@acme/widget/workspace): corrupt Zstandard session log: first frame is not exactly one header line")
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.PluginFault != nil {
		t.Fatalf("stored-data rejection blamed a plugin: %+v", failed.PluginFault)
	}
}

func TestPluginFaultListsProfilePluginsWithSuspectsFirst(t *testing.T) {
	host, _, statuses, _ := newFaultTestHost(t, "Error: dsh: plugin tree failed to load: failed to apply loader entry ws (@acme/widget/workspace): boom")
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	want := []lifecycle.FaultPlugin{
		{Package: "@acme/widget", Enabled: true, Suspected: true},
		{Package: "@acme/idle"},
		{Package: "@acme/other", Enabled: true},
	}
	if failed.PluginFault == nil || !reflect.DeepEqual(failed.PluginFault.Plugins, want) {
		t.Fatalf("plugins = %+v", failed.PluginFault)
	}
}

func TestPluginFaultWithoutNamedPluginStillOffersPlugins(t *testing.T) {
	host, _, statuses, _ := newFaultTestHost(t, "Error: worker exited")
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.PluginFault == nil || len(failed.PluginFault.Plugins) != 3 {
		t.Fatalf("plugins = %+v", failed.PluginFault)
	}
	for _, plugin := range failed.PluginFault.Plugins {
		if plugin.Suspected {
			t.Fatalf("unnamed plugin marked suspected: %+v", plugin)
		}
	}
}

func TestDisableFaultPluginsDeselectsEveryThirdPartyBundle(t *testing.T) {
	host, _, statuses, profilePath := newFaultTestHost(t, "Error: worker exited")
	host.Start()
	waitForStatus(t, statuses, lifecycle.StateFailed)
	status, err := host.DisableFaultPlugins(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, plugin := range status.PluginFault.Plugins {
		if plugin.Enabled {
			t.Fatalf("plugin still enabled: %+v", plugin)
		}
	}
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Dependencies map[string]string `json:"dependencies"`
		DSH          struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.DSH.Profile.Bundles, []string{"@deepseek-ai/dsh-web-app"}) {
		t.Fatalf("bundles = %v", manifest.DSH.Profile.Bundles)
	}
	if len(manifest.Dependencies) != 4 {
		t.Fatalf("disabling removed installed packages: %v", manifest.Dependencies)
	}
	if _, err := host.DisableFaultPlugin(context.Background(), "@acme/widget"); err == nil {
		t.Fatal("disabled an already disabled plugin")
	}
}
