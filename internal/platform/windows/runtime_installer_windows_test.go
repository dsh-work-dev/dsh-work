//go:build windows

package windows

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

type managedNodeCommandFake struct {
	version string
	calls   []installerCommandCall
}

func (f *managedNodeCommandFake) Run(_ context.Context, executable string, args []string, _ map[string]string, directory string) (dshadapter.CommandResult, error) {
	f.calls = append(f.calls, installerCommandCall{executable: executable, args: append([]string(nil), args...), directory: directory})
	lower := strings.ToLower(filepath.Base(executable))
	switch lower {
	case "node.exe":
		return dshadapter.CommandResult{Stdout: "v24.20.0"}, nil
	case "npm.cmd":
		if len(args) == 1 && args[0] == "--version" {
			return dshadapter.CommandResult{Stdout: "11.19.0"}, nil
		}
		stage := argumentValue(args, "--prefix")
		if stage == "" {
			stage = argumentValue(args, "--dir")
		}
		launcher := filepath.Join(stage, "node_modules", ".bin", "dsh.cmd")
		if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
			return dshadapter.CommandResult{}, err
		}
		if err := os.WriteFile(launcher, []byte("test launcher"), 0o600); err != nil {
			return dshadapter.CommandResult{}, err
		}
		return dshadapter.CommandResult{}, nil
	case "dsh.cmd":
		return dshadapter.CommandResult{Stdout: "dsh " + f.version}, nil
	default:
		return dshadapter.CommandResult{}, errors.New("unexpected executable " + executable)
	}
}

type nodeArchiveDownloaderFake struct {
	urls   []string
	data   []byte
	errors []error
}

func (d *nodeArchiveDownloaderFake) Download(_ context.Context, source, destination, _ string, report func(int64, int64, bool)) error {
	d.urls = append(d.urls, source)
	index := len(d.urls) - 1
	if index < len(d.errors) && d.errors[index] != nil {
		return d.errors[index]
	}
	if err := os.WriteFile(destination, d.data, 0o600); err != nil {
		return err
	}
	if report != nil {
		report(int64(len(d.data)), int64(len(d.data)), true)
	}
	return nil
}

func nodeArchive(t *testing.T, traversal bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entries := []string{"node-v24.20.0-win-x64/", "node-v24.20.0-win-x64/node.exe", "node-v24.20.0-win-x64/npm.cmd", "node-v24.20.0-win-x64/node_modules/npm/bin/npm-cli.js"}
	if traversal {
		entries = append(entries, "node-v24.20.0-win-x64/../../escape.txt")
	}
	for _, name := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasSuffix(name, "/") {
			continue
		}
		if _, err := entry.Write([]byte("fixture")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

type installerCommandCall struct {
	executable string
	args       []string
	directory  string
}

type installerCommandFake struct {
	calls          []installerCommandCall
	version        string
	versions       []string
	dshVersionCall int
	failPackage    bool
	networkFailure bool
}

func (f *installerCommandFake) Run(_ context.Context, executable string, args []string, _ map[string]string, directory string) (dshadapter.CommandResult, error) {
	f.calls = append(f.calls, installerCommandCall{
		executable: executable,
		args:       append([]string(nil), args...),
		directory:  directory,
	})
	if strings.HasSuffix(strings.ToLower(executable), "dsh.cmd") {
		version := f.version
		if f.dshVersionCall < len(f.versions) {
			version = f.versions[f.dshVersionCall]
		}
		f.dshVersionCall++
		return dshadapter.CommandResult{Stdout: "dsh " + version}, nil
	}
	if f.failPackage {
		f.failPackage = false
		if f.networkFailure {
			return dshadapter.CommandResult{Stderr: "fetch failed: ECONNRESET"}, errors.New("package manager failed")
		}
		return dshadapter.CommandResult{Stderr: "No matching version found"}, errors.New("package manager failed")
	}
	stage := argumentValue(args, "--prefix")
	if stage == "" {
		stage = argumentValue(args, "--dir")
	}
	if stage == "" {
		return dshadapter.CommandResult{}, errors.New("missing package install directory")
	}
	launcher := filepath.Join(stage, "node_modules", ".bin", "dsh.cmd")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		return dshadapter.CommandResult{}, err
	}
	if err := os.WriteFile(launcher, []byte("test launcher"), 0o600); err != nil {
		return dshadapter.CommandResult{}, err
	}
	return dshadapter.CommandResult{}, nil
}

type staticRuntimeToolchainResolver struct {
	toolchain runtimeToolchain
	err       error
}

func (r staticRuntimeToolchainResolver) Resolve(context.Context, dshmanager.RuntimeInstallObserver) (runtimeToolchain, error) {
	return r.toolchain, r.err
}

func argumentValue(args []string, name string) string {
	for index, arg := range args {
		if arg == name && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func newTestRuntimeInstaller(fake *installerCommandFake, store string, kind dshmanager.RuntimeToolchain, options ...RuntimeInstallerOption) *RuntimeInstaller {
	options = append([]RuntimeInstallerOption{
		WithRuntimeToolchainResolver(staticRuntimeToolchainResolver{toolchain: runtimeToolchain{
			kind:               kind,
			packageManagerPath: string(kind) + ".cmd",
		}}),
	}, options...)
	return NewRuntimeInstaller(fake, store, options...)
}

func TestRuntimeInstallerUsesResolvedSystemPNPMAndStagesBeforeCommit(t *testing.T) {
	fake := &installerCommandFake{version: "0.1.2-alpha.3"}
	store := t.TempDir()
	installer := newTestRuntimeInstaller(fake, store, dshmanager.RuntimeToolchainSystemPNPM)
	runtime, err := installer.Install(context.Background(), "0.1.2-alpha.3")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if runtime.Version != "0.1.2-alpha.3" || runtime.Source != dshmanager.RuntimeSourceManaged || runtime.Toolchain != dshmanager.RuntimeToolchainSystemPNPM || runtime.InstallSource != dshmanager.RuntimeArtifactSourceOfficial || !runtime.Installed || !runtime.Removable {
		t.Fatalf("unexpected installed runtime: %+v", runtime)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("command calls = %#v", fake.calls)
	}
	installCall := fake.calls[0]
	if installCall.executable != "system-pnpm.cmd" || installCall.directory != argumentValue(installCall.args, "--dir") {
		t.Fatalf("package-manager call = %#v", installCall)
	}
	if installCall.args[0] != "add" || argumentValue(installCall.args, "--registry") != defaultOfficialRegistry {
		t.Fatalf("pnpm args = %#v", installCall.args)
	}
	if strings.Contains(installCall.directory, filepath.Join(store, "dsh-0.1.2-alpha.3")) {
		t.Fatalf("package manager wrote directly to final directory: %q", installCall.directory)
	}
	if _, err := os.Stat(filepath.Join(store, "dsh-0.1.2-alpha.3")); err != nil {
		t.Fatalf("committed runtime missing: %v", err)
	}
}

func TestRuntimeInstallerUsesNpmWhenResolverSelectsNpm(t *testing.T) {
	fake := &installerCommandFake{version: "0.1.2-alpha.3"}
	installer := newTestRuntimeInstaller(fake, t.TempDir(), dshmanager.RuntimeToolchainSystemNPM)
	if _, err := installer.Install(context.Background(), "0.1.2-alpha.3"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if fake.calls[0].executable != "system-npm.cmd" || fake.calls[0].args[0] != "install" {
		t.Fatalf("npm package-manager call = %#v", fake.calls[0])
	}
}

func TestRuntimeInstallerFallsBackToMirrorOnlyForReachabilityFailure(t *testing.T) {
	fake := &installerCommandFake{version: "0.1.2-alpha.3", failPackage: true, networkFailure: true}
	installer := newTestRuntimeInstaller(fake, t.TempDir(), dshmanager.RuntimeToolchainSystemNPM)
	runtime, err := installer.Install(context.Background(), "0.1.2-alpha.3")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if runtime.InstallSource != dshmanager.RuntimeArtifactSourceMirror {
		t.Fatalf("install source = %q, want mirror", runtime.InstallSource)
	}
	if len(fake.calls) != 3 {
		t.Fatalf("command calls = %#v", fake.calls)
	}
	if got := argumentValue(fake.calls[0].args, "--registry"); got != defaultOfficialRegistry {
		t.Fatalf("official registry = %q", got)
	}
	if got := argumentValue(fake.calls[1].args, "--registry"); got != defaultMirrorRegistry {
		t.Fatalf("mirror registry = %q", got)
	}
}

func TestRuntimeInstallerDoesNotFallbackForSemanticPackageFailure(t *testing.T) {
	fake := &installerCommandFake{version: "0.1.2-alpha.3", failPackage: true}
	installer := newTestRuntimeInstaller(fake, t.TempDir(), dshmanager.RuntimeToolchainSystemNPM)
	_, err := installer.Install(context.Background(), "0.1.2-alpha.3")
	if err == nil {
		t.Fatal("Install() error = nil, want semantic install failure")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("mirror was used for a semantic failure: %#v", fake.calls)
	}
}

func TestRuntimeInstallerRejectsVersionMismatchBeforeCommit(t *testing.T) {
	fake := &installerCommandFake{version: "9.9.9"}
	store := t.TempDir()
	installer := newTestRuntimeInstaller(fake, store, dshmanager.RuntimeToolchainSystemNPM)
	_, err := installer.Install(context.Background(), "0.1.2-alpha.3")
	if err == nil {
		t.Fatal("Install() error = nil, want version mismatch")
	}
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorDSHUnsupportedVersion {
		t.Fatalf("Install() error = %v, want unsupported-version failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(store, "dsh-0.1.2-alpha.3")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("mismatched runtime was committed, stat error = %v", statErr)
	}
}

func TestPackageInstallArgsKeepNativeCachesAndAllowLifecycleScripts(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "stage")
	for _, kind := range []dshmanager.RuntimeToolchain{dshmanager.RuntimeToolchainSystemPNPM, dshmanager.RuntimeToolchainSystemNPM} {
		args := packageInstallArgs(kind, stage, "2.4.6", defaultOfficialRegistry)
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "--ignore-scripts") || strings.Contains(joined, "--cache") || strings.Contains(joined, "--store-dir") {
			t.Fatalf("%s changed lifecycle or native cache/store policy: %#v", kind, args)
		}
	}
	args := packageInstallArgs(dshmanager.RuntimeToolchainSystemPNPM, stage, "2.4.6", defaultOfficialRegistry)
	for _, packageName := range pnpmAllowedBuildPackages {
		want := "--allow-build=" + packageName
		if !slices.Contains(args, want) {
			t.Fatalf("pnpm install args missing explicit build permission %q: %#v", want, args)
		}
	}
}

func TestRuntimeInstallerRestoresPreviousSameVersionAfterCandidateDiscard(t *testing.T) {
	store := t.TempDir()
	finalLauncher := filepath.Join(store, "dsh-0.1.2-alpha.3", "node_modules", ".bin", "dsh.cmd")
	if err := os.MkdirAll(filepath.Dir(finalLauncher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(finalLauncher, []byte("previous runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &installerCommandFake{
		version:  "0.1.2-alpha.3",
		versions: []string{"9.9.9", "0.1.2-alpha.3"},
	}
	installer := newTestRuntimeInstaller(fake, store, dshmanager.RuntimeToolchainSystemNPM)
	runtime, err := installer.Install(context.Background(), "0.1.2-alpha.3")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if err := installer.Discard(context.Background(), runtime); err != nil {
		t.Fatalf("Discard() error = %v", err)
	}
	content, err := os.ReadFile(finalLauncher)
	if err != nil {
		t.Fatalf("restored launcher read error = %v", err)
	}
	if string(content) != "previous runtime" {
		t.Fatalf("restored launcher content = %q", content)
	}
}

func TestRuntimeInstallerUsesLatestInstalledManagedNodeWhenSystemToolchainIsUnavailable(t *testing.T) {
	fake := &managedNodeCommandFake{version: "0.1.2-alpha.3"}
	store := t.TempDir()
	installer := NewRuntimeInstaller(fake, store,
		WithRuntimeToolchainResolver(staticRuntimeToolchainResolver{err: errSystemToolchainUnavailable}),
	)
	nodes := []dshmanager.NodeInstallationInfo{managedNodeFixture(t, store, "v20.1.0"), managedNodeFixture(t, store, "v24.20.0")}
	runtime, err := installer.InstallWithNodes(context.Background(), "0.1.2-alpha.3", nodes, nil)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if runtime.Toolchain != dshmanager.RuntimeToolchainManagedNodeNPM || runtime.InstallSource != dshmanager.RuntimeArtifactSourceOfficial {
		t.Fatalf("managed Node result = %#v", runtime)
	}
	if len(fake.calls) < 3 || fake.calls[0].executable != nodes[1].NodePath || fake.calls[1].executable != nodes[1].NPMPath {
		t.Fatalf("managed toolchain calls = %#v", fake.calls)
	}
	if _, err := os.Stat(runtime.Path); err != nil {
		t.Fatalf("managed runtime path missing: %v", err)
	}
}

func TestRunNodeResolverRejectsCatalogPathsOutsideManagedStore(t *testing.T) {
	store := t.TempDir()
	installation := managedNodeFixture(t, store, "v24.20.0")
	installation.NodePath = filepath.Join(t.TempDir(), "node.exe")
	executor := &managedNodeCommandFake{}
	resolver := NewRunNodeResolver(executor, store)
	if _, err := resolver.Resolve(context.Background(), dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionManaged, InstallationID: installation.ID}, []dshmanager.NodeInstallationInfo{installation}); err == nil {
		t.Fatal("Resolve() accepted a catalog path outside the managed store")
	}
	if len(executor.calls) != 0 {
		t.Fatalf("untrusted executable was invoked: %#v", executor.calls)
	}
}

func TestRuntimeInstallerDoesNotAcquireNodeWhenNoToolchainExists(t *testing.T) {
	fake := &managedNodeCommandFake{version: "0.1.2-alpha.3"}
	installer := NewRuntimeInstaller(fake, t.TempDir(),
		WithRuntimeToolchainResolver(staticRuntimeToolchainResolver{err: errSystemToolchainUnavailable}),
	)
	required, err := installer.RequiresNodeAcquisition(context.Background(), nil)
	if err != nil || !required {
		t.Fatalf("RequiresNodeAcquisition() = %v, %v", required, err)
	}
	if _, err := installer.InstallWithNodes(context.Background(), "0.1.2-alpha.3", nil, nil); err == nil {
		t.Fatal("InstallWithNodes() error = nil")
	}
	if len(fake.calls) != 0 {
		t.Fatalf("unexpected command or Node acquisition = %#v", fake.calls)
	}
}

func managedNodeFixture(t *testing.T, store, version string) dshmanager.NodeInstallationInfo {
	t.Helper()
	root := filepath.Join(store, "toolchains", "node", version, "windows-x64")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"node.exe", "npm.cmd"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := nodeInstallationManifest{
		Version: version, Platform: "windows", Architecture: "x64",
		SHA256: strings.Repeat("a", 64), Route: acquisition.RouteOfficial,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, nodeManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return nodeInstallationFromManifest(root, manifest)
}

func TestNodeArchiveRejectsTraversal(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "node.zip")
	if err := os.WriteFile(archivePath, nodeArchive(t, true), 0o600); err != nil {
		t.Fatal(err)
	}
	err := extractNodeArchive(archivePath, filepath.Join(t.TempDir(), "node"))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "traversal") {
		t.Fatalf("extractNodeArchive() error = %v, want traversal rejection", err)
	}
}

func TestRuntimeInstallerRemoveDeletesManagedRuntimeDirectory(t *testing.T) {
	store := t.TempDir()
	root := filepath.Join(store, "dsh-0.1.2-alpha.3")
	launcher := filepath.Join(root, "node_modules", ".bin", "dsh.cmd")
	if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(launcher, []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := NewRuntimeInstaller(nil, store)
	runtime := dshmanager.RuntimeInfo{ID: "dsh-0.1.2-alpha.3", Version: "0.1.2-alpha.3", Path: launcher, Source: dshmanager.RuntimeSourceManaged, InstallSource: dshmanager.RuntimeArtifactSourceLocal}
	if err := installer.Remove(context.Background(), runtime); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("runtime directory still exists: %v", err)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("store retained entries after removal: %v", entries)
	}
	if err := installer.Remove(context.Background(), runtime); err != nil {
		t.Fatalf("repeated Remove() error = %v", err)
	}
}

func TestRuntimeInstallerRemoveRejectsRuntimeOutsideStore(t *testing.T) {
	store := t.TempDir()
	outside := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(outside, []byte("runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	installer := NewRuntimeInstaller(nil, store)
	runtime := dshmanager.RuntimeInfo{ID: "dsh-0.1.2-alpha.3", Version: "0.1.2-alpha.3", Path: outside, Source: dshmanager.RuntimeSourceManaged}
	if err := installer.Remove(context.Background(), runtime); err == nil {
		t.Fatal("Remove() error = nil, want outside-store rejection")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside runtime was touched: %v", err)
	}
}
