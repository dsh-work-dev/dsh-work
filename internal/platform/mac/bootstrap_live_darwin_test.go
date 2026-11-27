//go:build darwin && cgo

package mac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/workerchannel"
)

type bootstrapNodeCatalog struct{ release dshmanager.NodeReleaseInfo }

func (c bootstrapNodeCatalog) RefreshLatest(context.Context) (dshmanager.NodeReleaseInfo, error) {
	return c.release, nil
}

type bootstrapDSHCatalog []string

func (c bootstrapDSHCatalog) Refresh(context.Context) ([]dshmanager.DSHReleaseInfo, error) {
	releases := make([]dshmanager.DSHReleaseInfo, 0, len(c))
	for _, version := range c {
		releases = append(releases, dshmanager.DSHReleaseInfo{
			Version: version, Source: dshmanager.RuntimeArtifactSourceOfficial, ObservedAt: time.Now().UTC().Format(time.RFC3339),
		})
	}
	return releases, nil
}

func TestLiveMacBootstrapWithoutSystemNode(t *testing.T) {
	archive := os.Getenv("DSH_WORK_TEST_BOOTSTRAP_NODE_ARCHIVE")
	if archive == "" {
		t.Skip("requires a signed app fixture and the exact official Node archive")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", root)
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
	t.Setenv("DSH_WORK_CEF_MAC_PUBLICATION", "")
	t.Setenv("DSH_WORK_EXECUTABLE", "")
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	ctx = dshadapter.WithCommandOutput(ctx, func(line string) { t.Log(line) })

	const nodeVersion = "v26.13.1"
	const dshVersion = "0.2.2-rc.1"
	const failedVersion = "0.2.1-rc.5"
	release := dshmanager.NodeReleaseInfo{
		Version: nodeVersion, Platform: "darwin", Architecture: "arm64",
		Filename: "node-v26.13.1-darwin-arm64.tar.gz",
		SHA256:   "aec2514ea1ceb2ba52455e9ec65986f6749a06dd759e5db2a72e6889668c9735",
		Source:   dshmanager.RuntimeArtifactSourceOfficial, ObservedAt: time.Now().UTC().Format(time.RFC3339),
	}
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+release.Version+"/"+release.Filename {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, archive)
	}))
	defer mirror.Close()
	store, home := filepath.Join(root, "runtimes"), filepath.Join(root, "home")
	nodeInstaller := NewManagedNodeInstaller(store)
	nodeInstaller.sources = []acquisition.SourceCandidate{{Route: acquisition.RouteOfficial, Location: mirror.URL + "/"}}
	cfg := app.Config{
		DiscoveryRoot: root, BootstrapDirectory: filepath.Join(root, "bootstrap"), DSHDataDirectory: home,
		SettingsPath: filepath.Join(root, "settings.json"), CacheDirectory: filepath.Join(root, "cache"),
		ReadinessTimeout: time.Minute, ProbeTimeout: 2 * time.Second,
	}
	open := func() (*dshmanager.Manager, *app.Host) {
		t.Helper()
		runtime, err := newRuntime(nil)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.node != "" || runtime.cli != "" {
			t.Fatalf("fixture unexpectedly has a system Node or DSH seed: %q %q", runtime.node, runtime.cli)
		}
		adapter := dshadapter.New(runtime)
		manager, err := dshmanager.New(dshmanager.Config{
			StatePath: filepath.Join(root, "manager.json"), CommandRunner: runtime,
			PluginCommands: dshadapter.NewPluginCommands(), RuntimeInstaller: runtime.DSHInstaller(store),
			DSHCatalog: bootstrapDSHCatalog{dshVersion, failedVersion}, MinimumDSHVersion: dshadapter.MinimumVersion,
			NodeCatalog: bootstrapNodeCatalog{release}, NodeInstaller: nodeInstaller, NodeResolver: runtime.NodeResolver(store),
			ProfileCatalog: adapter, RuntimeVerifier: adapter, AppProfile: adapter,
			DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "dsh-work", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}},
		})
		if err != nil {
			t.Fatal(err)
		}
		host := app.NewHost(app.Dependencies{Manager: manager, DSH: adapter, Supervisor: runtime, Channel: workerchannel.New()}, cfg)
		t.Cleanup(func() { _ = host.ShutdownForApp() })
		return manager, host
	}

	manager, host := open()
	initial, err := manager.Snapshot(ctx)
	if err != nil || initial.SystemNode != nil || len(initial.Nodes) != 0 || len(initial.Runtimes) != 0 {
		t.Fatalf("unexpected clean manager catalog: %+v (%v)", initial, err)
	}
	if _, err = manager.RefreshDSHReleases(ctx); err != nil {
		t.Fatal(err)
	}
	installed, err := manager.InstallRuntimeWithProgress(ctx, dshVersion, func(p lifecycle.RuntimePreparation) {
		if p.Operation != lifecycle.RuntimePreparationOperationDownloadNode {
			t.Logf("preparation %s %s", p.Operation, p.State)
		}
	})
	if err != nil || len(installed.Nodes) != 1 || len(installed.Runtimes) != 1 {
		t.Fatalf("managed bootstrap failed: %+v (%v)", installed, err)
	}
	candidate := installed.Runtimes[0]
	if candidate.Toolchain != dshmanager.RuntimeToolchainManagedNodeNPM || installed.Nodes[0].Version != nodeVersion {
		t.Fatalf("wrong bootstrap toolchain: %+v %+v", candidate, installed.Nodes)
	}
	target := dshmanager.RunContext{
		RuntimeID: candidate.ID, Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionManaged, InstallationID: installed.Nodes[0].ID},
		Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: dshmanager.AppProfileName},
	}
	ready, err := host.SwitchRunContext(ctx, target)
	if err != nil || ready.Current == nil || *ready.Current != target || host.Status().State != lifecycle.StateReady {
		t.Fatalf("first Worker startup failed: %v; status=%+v; diagnostics=%+v", err, host.Status(), host.Diagnostics())
	}
	verifyMacCandidateRollback(t, ctx, manager, host, home, target, candidate, failedVersion)
	if err = host.ShutdownForApp(); err != nil {
		t.Fatal(err)
	}
	_, restarted := open()
	events := make(chan lifecycle.Status, 64)
	restarted.SetPublish(func(status lifecycle.Status) {
		select {
		case events <- status:
		default:
		}
	})
	restarted.Start()
	for {
		select {
		case status := <-events:
			switch status.State {
			case lifecycle.StateReady:
				if err := restarted.ShutdownForApp(); err != nil || restarted.Diagnostics().ActiveProcesses != 0 {
					t.Fatalf("restarted Worker did not retire: %v (%+v)", err, restarted.Diagnostics())
				}
				return
			case lifecycle.StateFailed:
				t.Fatalf("managed restart failed: %+v; diagnostics=%+v", status, restarted.Diagnostics())
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func verifyMacCandidateRollback(t *testing.T, ctx context.Context, manager *dshmanager.Manager, host *app.Host, home string, target dshmanager.RunContext, original dshmanager.RuntimeInfo, failedVersion string) {
	t.Helper()
	previous := host.Status().GenerationID
	if _, err := manager.InstallRuntime(ctx, failedVersion); err != nil {
		t.Fatalf("install second runtime for the rollback fixture: %v", err)
	}
	profile := filepath.Join(home, "profiles", "failed-candidate")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := []byte(`{"name":"failed-candidate","private":true,"dependencies":{},"dsh":{"profile":{"bundles":[]}}}`)
	if err := os.WriteFile(filepath.Join(profile, "package.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	patch := filepath.Join(profile, "cordis.patch.yml")
	invalidPatch := []byte("- id: [invalid\n")
	if err := os.WriteFile(patch, invalidPatch, 0600); err != nil {
		t.Fatal(err)
	}
	failed := target
	failed.RuntimeID = "dsh-" + failedVersion
	failed.Profile.Name = "failed-candidate"
	var eventsMu sync.Mutex
	var events []lifecycle.Status
	host.SetPublish(func(status lifecycle.Status) {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		events = append(events, status)
	})
	defer host.SetPublish(nil)
	restored, err := host.SwitchRunContext(ctx, failed)
	attempt := restored.LastSwitchAttempt
	if err == nil || attempt == nil || attempt.Target != failed || attempt.Stage != dshmanager.SwitchAttemptCandidate ||
		attempt.Rollback != dshmanager.RollbackRestored || restored.Current == nil || *restored.Current != target ||
		restored.KnownGood == nil || *restored.KnownGood != target || host.Status().State != lifecycle.StateReady ||
		host.Status().GenerationID == previous || host.Diagnostics().ActiveProcesses != 1 {
		t.Fatalf("failed startup did not restore the healthy Run context: %v; attempt=%+v; status=%+v; diagnostics=%+v", err, attempt, host.Status(), host.Diagnostics())
	}
	var failedGeneration string
	eventsMu.Lock()
	for _, status := range events {
		if status.LaunchSelection != nil && status.LaunchSelection.RuntimeVersion == failedVersion && status.GenerationID != "" {
			failedGeneration = status.GenerationID
		}
	}
	eventsMu.Unlock()
	if failedGeneration == "" || failedGeneration == previous || failedGeneration == host.Status().GenerationID {
		t.Fatalf("candidate did not have an independent Worker generation: %q", failedGeneration)
	}
	for _, item := range restored.Runtimes {
		if item.ID == original.ID && (item.Path == original.Path || item.Version != original.Version) {
			t.Fatalf("rollback did not reinstall the recorded DSH version in a new tree: %+v", item)
		}
	}
	data, err := os.ReadFile(patch)
	if err != nil || string(data) != string(invalidPatch) {
		t.Fatalf("rollback changed the failed profile: %q (%v)", data, err)
	}
	t.Logf("candidate generation %s failed and prior Run context reached Ready as %s", failedGeneration, host.Status().GenerationID)
}
