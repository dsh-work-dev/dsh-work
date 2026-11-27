//go:build darwin && cgo

package mac

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

func TestLiveMacProfilePackageMaintenance(t *testing.T) {
	if os.Getenv("DSH_WORK_TEST_LIVE_MAC_PROFILE_PACKAGES") != "1" {
		t.Skip("requires the signed Mac CEF fixture, pinned DSH, and npm registry access")
	}
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", work)
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	ctx = dshadapter.WithCommandOutput(ctx, func(line string) { t.Log(line) })
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(work, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(work, "runtimes")
	installer := runtime.DSHInstaller(store)
	commands := dshadapter.NewPluginCommands()
	run := func(args []string, buildErr error) dshadapter.CommandResult {
		t.Helper()
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		result, err := runtime.Run(ctx, runtime.cli, args, map[string]string{"DSH_HOME": home}, home)
		if err != nil {
			t.Fatalf("DSH %v: %v\nstdout: %s\nstderr: %s", args, err, result.Stdout, result.Stderr)
		}
		return result
	}
	run(commands.Install("web", "is-number@7.0.0"))
	var listed []struct{ Dependencies map[string]json.RawMessage }
	result := run(commands.List("web"))
	if err := json.Unmarshal([]byte(result.Stdout), &listed); err != nil || len(listed) != 1 || listed[0].Dependencies["is-number"] == nil {
		t.Fatalf("invalid profile list: %q (%v)", result.Stdout, err)
	}
	profile := filepath.Join(home, "profiles", "web")
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		logs := filepath.Join(profile, ".plugin-manager", "logs")
		entries, _ := os.ReadDir(logs)
		for _, entry := range entries {
			data, err := os.ReadFile(filepath.Join(logs, entry.Name(), "pnpm.log"))
			if err == nil {
				t.Logf("package operation %s\n%s", entry.Name(), data)
			}
		}
	})
	lock := filepath.Join(profile, "pnpm-lock.yaml")
	read := func(path string) []byte {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	wantLock := read(lock)
	const version = "0.2.2-rc.1"
	target := dshmanager.RunContext{RuntimeID: "fixture", Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem},
		Profile: dshmanager.ProfileRef{DataDirectoryID: "home", Name: "web"}}
	resolver := runtime.NodeResolver(store)
	if os.Getenv("DSH_WORK_TEST_MAC_RESTORE_FROM_FIXTURE") == "1" {
		resolver = useFixtureDSHAcquisition(t, resolver, runtime.cli, version)
	}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath: filepath.Join(work, "manager.json"), CommandRunner: runtime, PluginCommands: commands,
		RuntimeInstaller: installer, NodeResolver: resolver, ProfileCatalog: dshadapter.New(runtime),
		Runtimes:        []dshmanager.RuntimeInfo{{ID: "fixture", Version: version, Path: runtime.cli}},
		DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "home", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}},
	})
	if err != nil {
		t.Fatal(err)
	}
	launch, err := manager.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := manager.CommitHealthy(ctx, manager.CaptureLaunchVersions(ctx, launch))
	if err != nil || saved.RestorePoints.LastRunning == "" {
		t.Fatalf("version snapshot unavailable: %v (%+v)", err, saved.RestorePoints)
	}
	pluginTarget := dshmanager.PluginTarget{Profile: target.Profile}
	installResult, err := manager.InstallPlugin(ctx, dshmanager.PluginInstallRequest{Target: pluginTarget, Package: "is-number@6.0.0"})
	if err != nil || len(installResult.Plugins) != 1 || installResult.Plugins[0].Version != "6.0.0" {
		t.Fatalf("manager registry install did not select the requested version: %+v (%v)", installResult, err)
	}
	plugins, err := manager.ListPlugins(ctx, dshmanager.PluginListRequest{Target: pluginTarget})
	if err != nil || len(plugins) != 1 || plugins[0].UpdateCheck != dshmanager.PluginUpdateAvailable || plugins[0].AvailableVersion != "7.0.0" {
		t.Fatalf("manager did not report the available upgrade: %+v (%v)", plugins, err)
	}
	upgraded, err := manager.UpgradePlugin(ctx, dshmanager.PluginUpgradeRequest{Target: pluginTarget, Package: "is-number"})
	if err != nil || len(upgraded.Plugins) != 1 || upgraded.Plugins[0].Version != "7.0.0" {
		t.Fatalf("manager did not apply the available upgrade: %+v (%v)", upgraded, err)
	}
	removed, err := manager.RemovePlugin(ctx, dshmanager.PluginRemoveRequest{Target: pluginTarget, Package: "is-number"})
	if err != nil || len(removed.Plugins) != 0 {
		t.Fatalf("manager did not remove the selected package: %+v (%v)", removed, err)
	}
	if _, err = manager.ClearCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	run(commands.Install("web", "is-number@6.0.0"))
	run(commands.Install("web", "is-odd@3.0.1"))
	manifestPath := filepath.Join(profile, "package.json")
	var manifest map[string]json.RawMessage
	if err = json.Unmarshal(read(manifestPath), &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["scripts"] = json.RawMessage(`{"postinstall":"touch unexpected-build"}`)
	body, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifestPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(profile, ".pnpmfile.cjs"), []byte("throw Error('unexpected profile hook');"), 0600); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(profile, "node_modules", ".host-keeps-directory")
	settings := filepath.Join(home, "settings.yaml")
	for _, path := range []string{kept, settings} {
		if err = os.WriteFile(path, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	restored, err := manager.RecoverVersionPoint(ctx, saved.RestorePoints.LastRunning)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Runtime.Path == runtime.cli || restored.Runtime.Source != dshmanager.RuntimeSourceManaged {
		t.Fatalf("restore did not publish a managed runtime revision: %+v", restored.Runtime)
	}
	if !bytes.Equal(wantLock, read(lock)) || string(read(settings)) != "keep" || string(read(kept)) != "keep" {
		t.Fatal("restore changed the frozen lock, package directory sentinel, or user settings")
	}
	input, err := commands.CaptureVersions(ctx, profile)
	if err != nil || len(input.Plugins) != 1 || input.Plugins[0].Version != "7.0.0" ||
		input.PackageManager != "pnpm@"+defaultProfilePNPM {
		t.Fatalf("restored versions differ: %+v (%v)", input, err)
	}
	result, err = runtime.Run(ctx, restored.Runtime.Path, []string{"--version"}, restored.Node.ChildEnvironment, home)
	if err != nil || dshadapter.ParseVersion(result.Stdout) != version {
		t.Fatalf("restored CLI unavailable: %q (%v)", result.Stdout, err)
	}
	if _, err = os.Stat(filepath.Join(profile, "unexpected-build")); !os.IsNotExist(err) {
		t.Fatalf("restore ran a lifecycle script: %v", err)
	}
	if _, err = manager.CommitHealthy(ctx, manager.CaptureLaunchVersions(ctx, restored)); err != nil {
		t.Fatal(err)
	}
	if _, err = manager.ClearCurrent(ctx); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(profile, "node_modules", "is-number", "index.js")
	original := read(module)
	replacement := module + ".broken"
	if err = os.WriteFile(replacement, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	// Replace the path without altering any package-manager store link.
	if err = os.Rename(replacement, module); err != nil {
		t.Fatal(err)
	}
	repaired, err := manager.RecoverVersionPoint(ctx, saved.RestorePoints.LastRunning)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Runtime.Path == restored.Runtime.Path {
		t.Fatal("forced repair reused the prior runtime revision")
	}
	if _, err = os.Stat(restored.Runtime.Path); err != nil {
		t.Fatalf("forced repair removed the prior runtime revision: %v", err)
	}
	if !bytes.Equal(original, read(module)) || !bytes.Equal(wantLock, read(lock)) || string(read(kept)) != "keep" {
		t.Fatal("same-version restore failed to repair the damaged package and preserve the frozen lock and directory")
	}

	requested := make(chan struct{}, 1)
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(25 * time.Second):
			w.WriteHeader(http.StatusGatewayTimeout)
		}
	}))
	defer registry.Close()
	stalled, err := commands.Install("web", "dsh-work-package-cancellation-fixture@1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	cancelCtx, stop := context.WithTimeout(ctx, 25*time.Second)
	defer stop()
	done := make(chan error, 1)
	go func() {
		_, err := runtime.Run(cancelCtx, restored.Runtime.Path, stalled, map[string]string{
			"DSH_HOME": home, "npm_config_registry": registry.URL,
		}, home)
		done <- err
	}()
	select {
	case <-requested:
	case err := <-done:
		t.Fatalf("maintenance exited before making the registry request: %v", err)
	case <-cancelCtx.Done():
		t.Fatal("maintenance did not make a registry request")
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("maintenance did not report cancellation: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("maintenance did not retire after cancellation")
	}
	run(commands.List("web"))
	if !bytes.Equal(wantLock, read(lock)) {
		t.Fatal("canceling before a registry response changed the frozen lock")
	}
}

// Only the npm acquisition is replaced. The production installer validates
// and publishes both revisions, and the actual pnpm process restores the profile.
func useFixtureDSHAcquisition(t *testing.T, resolver dshmanager.NodeResolver, cli, version string) dshmanager.NodeResolver {
	t.Helper()
	root := filepath.Clean(filepath.Join(filepath.Dir(cli), "..", "..", "..", ".."))
	entry, err := macDSHPackage(root, version)
	if err != nil || entry != cli {
		t.Fatalf("expected a complete pinned DSH fixture: %s (%v)", cli, err)
	}
	nodeModules, err := json.Marshal(filepath.Join(root, "node_modules"))
	if err != nil {
		t.Fatal(err)
	}
	packageSpec, err := json.Marshal("@deepseek-ai/dsh@" + version)
	if err != nil {
		t.Fatal(err)
	}
	npm := filepath.Join(t.TempDir(), "fixture-npm")
	script := "#!/usr/bin/env node\n" +
		"const fs = require('node:fs');\n" +
		"if (process.argv[2] !== 'install' || !process.argv.includes(" + string(packageSpec) + ")) process.exit(2);\n" +
		"fs.cpSync(" + string(nodeModules) + ", 'node_modules', { recursive: true, verbatimSymlinks: true, mode: fs.constants.COPYFILE_FICLONE });\n"
	if err := os.WriteFile(npm, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return fixtureDSHNodeResolver{NodeResolver: resolver, npm: npm}
}

type fixtureDSHNodeResolver struct {
	dshmanager.NodeResolver
	npm string
}

func (r fixtureDSHNodeResolver) Resolve(ctx context.Context, selection dshmanager.NodeSelection, nodes []dshmanager.NodeInstallationInfo) (dshmanager.ResolvedNode, error) {
	node, err := r.NodeResolver.Resolve(ctx, selection, nodes)
	if err == nil {
		node.NPMPath = r.npm
	}
	return node, err
}

func TestLiveMacProfilePrebuiltSources(t *testing.T) {
	if os.Getenv("DSH_WORK_TEST_LIVE_MAC_PROFILE_PACKAGES") != "1" {
		t.Skip("requires the signed Mac CEF fixture, pinned DSH, and npm registry access")
	}
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", work)
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(work, "home")
	if err = os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(work, "runtimes")
	installer := runtime.DSHInstaller(store)
	commands := dshadapter.NewPluginCommands()
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath: filepath.Join(work, "manager.json"), CommandRunner: runtime, PluginCommands: commands,
		RuntimeInstaller: installer, NodeResolver: runtime.NodeResolver(store), ProfileCatalog: dshadapter.New(runtime),
		Runtimes:        []dshmanager.RuntimeInfo{{ID: "fixture", Version: "0.2.2-rc.1", Path: runtime.cli}},
		DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "home", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := dshmanager.PluginTarget{Profile: dshmanager.ProfileRef{DataDirectoryID: "home", Name: "web"}}
	profile := filepath.Join(home, "profiles", "web")
	ctx = dshadapter.WithCommandOutput(ctx, func(line string) { t.Log(line) })
	launch, err := manager.ResolveLaunch(ctx, dshmanager.LaunchRequest{
		RuntimeID: "fixture", Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: target.Profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = manager.CommitHealthy(ctx, launch); err != nil {
		t.Fatal(err)
	}

	const archiveName = "dsh-work-prebuilt-fixture"
	payload := &bytes.Buffer{}
	compressor := gzip.NewWriter(payload)
	archive := tar.NewWriter(compressor)
	for _, file := range []struct{ name, body string }{
		{"package.json", `{"name":"dsh-work-prebuilt-fixture","version":"1.0.0","main":"index.js","scripts":{"postinstall":"node build.cjs"},"dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`},
		{"index.js", `module.exports = "prebuilt";`},
		{"cordis.patch.yml", "[]\n"},
		{"build.cjs", `require("node:fs").writeFileSync("unexpected-build", "ran");`},
	} {
		if err = archive.WriteHeader(&tar.Header{
			Name: "package/" + file.name, Mode: 0644, Size: int64(len(file.body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err = archive.Write([]byte(file.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err = compressor.Close(); err != nil {
		t.Fatal(err)
	}
	tarball := filepath.Join(home, archiveName+".tgz")
	if err = os.WriteFile(tarball, payload.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/"+archiveName+".tgz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(payload.Bytes())
	}))
	defer server.Close()
	for _, source := range []struct {
		spec string
		kind dshmanager.PluginSourceKind
	}{
		{"file:" + tarball, dshmanager.PluginSourceLocal},
		{server.URL + "/" + archiveName + ".tgz", dshmanager.PluginSourceURL},
	} {
		installed, err := manager.InstallPlugin(ctx, dshmanager.PluginInstallRequest{Target: target, Package: source.spec})
		if err != nil || len(installed.Plugins) != 1 || installed.Plugins[0].Name != archiveName ||
			installed.Plugins[0].Version != "1.0.0" || installed.Plugins[0].SourceKind != source.kind {
			t.Fatalf("install %s: %+v (%v)", source.spec, installed, err)
		}
		input, err := commands.CaptureVersions(ctx, profile)
		if err != nil || !slices.Contains(input.Bundles, archiveName) {
			t.Fatalf("DSH did not register the installed bundle: %v (%v)", input.Bundles, err)
		}
		dir := filepath.Join(profile, "node_modules", archiveName)
		entry, err := os.ReadFile(filepath.Join(dir, "index.js"))
		if err != nil || string(entry) != `module.exports = "prebuilt";` {
			t.Fatalf("installed package entry differs for %s: %q (%v)", source.spec, entry, err)
		}
		if _, err = os.Stat(filepath.Join(dir, "unexpected-build")); !os.IsNotExist(err) {
			t.Fatalf("install ran the dependency lifecycle script for %s: %v", source.spec, err)
		}
		removed, err := manager.RemovePlugin(ctx, dshmanager.PluginRemoveRequest{Target: target, Package: archiveName})
		if err != nil || len(removed.Plugins) != 0 {
			t.Fatalf("remove %s: %+v (%v)", source.spec, removed, err)
		}
		if _, err = os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatalf("package remained linked after removal: %v", err)
		}
		input, err = commands.CaptureVersions(ctx, profile)
		if err != nil || slices.Contains(input.Bundles, archiveName) {
			t.Fatalf("DSH bundle registration remained after removal: %v (%v)", input.Bundles, err)
		}
	}
}
