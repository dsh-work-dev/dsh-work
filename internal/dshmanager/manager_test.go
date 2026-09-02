package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/work/internal/lifecycle"
)

func TestManagerPersistsAnExplicitLaunchSelection(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}

	config := Config{
		StatePath:     filepath.Join(root, "manager.json"),
		WorkspaceRoot: filepath.Join(root, "workspace"),
		Homes: []HomeInfo{{
			ID:        "work",
			Name:      "Work managed",
			Path:      homePath,
			Ownership: HomeOwnershipWork,
		}},
		Runtimes: []RuntimeInfo{{
			ID:      "dsh-0.1.2-alpha.3",
			Version: "0.1.2-alpha.3",
			Path:    runtimePath,
			Source:  RuntimeSourceDevelopmentFixture,
		}},
		DefaultSelection: LaunchSelection{
			RuntimeID: "dsh-0.1.2-alpha.3",
			Profile:   ProfileRef{HomeID: "work", Name: "web"},
		},
	}

	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}

	want := LaunchSelection{
		RuntimeID: "dsh-0.1.2-alpha.3",
		Profile:   ProfileRef{HomeID: "work", Name: "web"},
		Workspace: filepath.Join(root, "project-a"),
	}
	if _, err := manager.SetDesired(context.Background(), want); err != nil {
		t.Fatalf("SetDesired() error = %v", err)
	}

	reloaded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Desired == nil || *snapshot.Desired != want {
		t.Fatalf("reloaded desired selection = %#v, want %#v", snapshot.Desired, want)
	}
}

func TestManagerRequiresAProfileReferenceForSelection(t *testing.T) {
	manager := newTestManager(t)

	_, err := manager.SetDesired(context.Background(), LaunchSelection{
		RuntimeID: "dsh-test",
	})
	if err == nil {
		t.Fatal("SetDesired() error = nil, want profile-required failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorProfileRequired)
}

func TestManagerRejectsUnknownProfileReference(t *testing.T) {
	manager := newTestManager(t)

	_, err := manager.Resolve(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{HomeID: "work", Name: "missing"},
	})
	if err == nil {
		t.Fatal("Resolve() error = nil, want profile-not-found failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorProfileNotFound)
}

func TestManagerRejectsAnUnverifiedRuntimeBeforeLaunch(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:     filepath.Join(root, "manager.json"),
		WorkspaceRoot: root,
		Homes:         []HomeInfo{{ID: "work", Name: "Work", Path: homePath, Ownership: HomeOwnershipWork}},
		Runtimes:      []RuntimeInfo{{ID: "missing", Version: "0.1.2-alpha.3", Path: filepath.Join(root, "missing-dsh.cmd")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].Installed {
		t.Fatalf("runtime verification projection = %#v", snapshot.Runtimes)
	}
	_, err = manager.Resolve(context.Background(), LaunchRequest{
		RuntimeID: "missing", Profile: ProfileRef{HomeID: "work", Name: "web"},
	})
	assertFailureCode(t, err, lifecycle.ErrorDSHRuntimeNotFound)
}

func TestManagerDelegatesProfileCatalogAndRuntimeVerification(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	verifier := &recordingRuntimeVerifier{}
	manager, err := New(Config{
		StatePath:     filepath.Join(root, "manager.json"),
		WorkspaceRoot: root,
		ProfileCatalog: testProfileCatalog{definitions: []ProfileDefinition{{
			Name: "web", Kind: ProfileKindBuiltIn, AutoInitialize: true,
		}}},
		RuntimeVerifier: verifier,
		Homes:           []HomeInfo{{ID: "work", Name: "Work", Path: homePath, Ownership: HomeOwnershipWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := manager.ResolveLaunch(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{HomeID: "work", Name: "web"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Selection.Profile.Name != "web" {
		t.Fatalf("resolved launch profile = %#v, want web", resolved.Selection.Profile)
	}
	if verifier.path != runtimePath || verifier.version != "0.1.2-alpha.3" || verifier.calls != 1 {
		t.Fatalf("runtime verifier call = path %q version %q calls %d", verifier.path, verifier.version, verifier.calls)
	}
}

func TestManagerPreservesAdapterRuntimeCompatibilityFailure(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(homePath, 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		WorkspaceRoot:   root,
		ProfileCatalog:  testProfileCatalog{definitions: []ProfileDefinition{{Name: "web", Kind: ProfileKindBuiltIn}}},
		RuntimeVerifier: failingRuntimeVerifier{},
		Homes:           []HomeInfo{{ID: "work", Name: "Work", Path: homePath, Ownership: HomeOwnershipWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.ResolveLaunch(context.Background(), LaunchRequest{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{HomeID: "work", Name: "web"},
	})
	assertFailureCode(t, err, lifecycle.ErrorDSHUnsupportedVersion)
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.CorrelationID == "" {
		t.Fatalf("compatibility failure = %#v, want correlation id", err)
	}
}

func TestManagerKeepsActiveAndDesiredSelectionsSeparate(t *testing.T) {
	manager := newTestManager(t)
	desired := LaunchSelection{
		RuntimeID: "dsh-test",
		Profile:   ProfileRef{HomeID: "work", Name: "web"},
	}
	if _, err := manager.SetDesired(context.Background(), desired); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.MarkActive(context.Background(), &desired); err != nil {
		t.Fatal(err)
	}

	changed := desired
	changed.Profile.Name = "web-clean"
	if _, err := manager.SetDesired(context.Background(), changed); err != nil {
		t.Fatal(err)
	}

	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Desired == nil || snapshot.Desired.Profile.Name != "web-clean" {
		t.Fatalf("desired = %#v, want web-clean", snapshot.Desired)
	}
	if snapshot.Active == nil || snapshot.Active.Profile.Name != "web" {
		t.Fatalf("active = %#v, want web", snapshot.Active)
	}
}

func TestManagerDelegatesPluginOperationsToDSHForExplicitProfile(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	for _, profile := range []string{"alpha", "beta"} {
		if err := os.MkdirAll(filepath.Join(homePath, "profiles", profile), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "profiles", "alpha", "package.json"), []byte(`{"dependencies":{"@example/alpha":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "profiles", "beta", "package.json"), []byte(`{"dependencies":{"@example/beta":"2.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	manager, err := New(Config{
		StatePath:      filepath.Join(root, "manager.json"),
		WorkspaceRoot:  filepath.Join(root, "workspace"),
		CommandRunner:  runner,
		PluginCommands: testPluginCommands{},
		Homes:          []HomeInfo{{ID: "work", Name: "Work", Path: homePath, Ownership: HomeOwnershipWork}},
		Runtimes:       []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
		DefaultSelection: LaunchSelection{
			RuntimeID: "dsh-test", Profile: ProfileRef{HomeID: "work", Name: "alpha"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := LaunchSelection{RuntimeID: "dsh-test", Profile: ProfileRef{HomeID: "work", Name: "alpha"}}
	if _, err := manager.MarkActive(context.Background(), &want); err != nil {
		t.Fatal(err)
	}
	result, err := manager.InstallPlugin(context.Background(), PluginInstallRequest{
		Target:  PluginTarget{Profile: ProfileRef{HomeID: "work", Name: "alpha"}},
		Package: "@example/new-plugin@3.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.RestartRequired || result.Profile.Name != "alpha" {
		t.Fatalf("plugin result = %#v, want alpha and restart required", result)
	}
	if runner.path != filepath.Join(root, "dsh.cmd") || runner.cwd != homePath || runner.env["DSH_HOME"] != homePath {
		t.Fatalf("DSH command target = path %q cwd %q env %#v", runner.path, runner.cwd, runner.env)
	}
	if got := strings.Join(runner.args, " "); got != "plugin --profile alpha add @example/new-plugin@3.0.0" {
		t.Fatalf("DSH plugin args = %q", got)
	}
	alpha, err := manager.ListPlugins(context.Background(), PluginListRequest{Target: PluginTarget{Profile: ProfileRef{HomeID: "work", Name: "alpha"}}})
	if err != nil {
		t.Fatal(err)
	}
	beta, err := manager.ListPlugins(context.Background(), PluginListRequest{Target: PluginTarget{Profile: ProfileRef{HomeID: "work", Name: "beta"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(alpha) != 1 || alpha[0].Name != "@example/alpha" || len(beta) != 1 || beta[0].Name != "@example/beta" {
		t.Fatalf("profile plugin lists crossed ownership boundary: alpha=%#v beta=%#v", alpha, beta)
	}
}

func TestManagerDoesNotExposeProfileDependencyDirectory(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "node_modules"), 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:     filepath.Join(root, "manager.json"),
		WorkspaceRoot: root,
		Homes:         []HomeInfo{{ID: "work", Name: "Work", Path: homePath}},
		Runtimes:      []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: filepath.Join(root, "dsh.cmd")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref.Name == "node_modules" {
			t.Fatal("dependency directory was exposed as a DSH profile")
		}
	}
}

func TestManagerPersistsCatalogEntriesProducedByExplicitRuntimeInstall(t *testing.T) {
	root := t.TempDir()
	installer := &recordingInstaller{runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")}}
	config := Config{
		StatePath:        filepath.Join(root, "manager.json"),
		WorkspaceRoot:    root,
		RuntimeInstaller: installer,
	}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.InstallRuntime(context.Background(), "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if installer.version != "1.2.3" || len(snapshot.Runtimes) != 1 || snapshot.Runtimes[0].ID != "dsh-1.2.3" {
		t.Fatalf("install result = %#v, installer version = %q", snapshot.Runtimes, installer.version)
	}
	reloaded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	reloadedSnapshot, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reloadedSnapshot.Runtimes) != 1 || reloadedSnapshot.Runtimes[0].Version != "1.2.3" {
		t.Fatalf("reloaded runtime catalog = %#v", reloadedSnapshot.Runtimes)
	}
}

func TestManagerRegistersOnlyExistingUserDSHHomes(t *testing.T) {
	manager := newTestManager(t)
	existing := filepath.Join(t.TempDir(), "personal-dsh")
	if err := os.MkdirAll(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := manager.RegisterHome(context.Background(), HomeInfo{
		ID:        "personal",
		Name:      "Personal DSH",
		Path:      existing,
		Ownership: HomeOwnershipUser,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Homes) != 2 || snapshot.Homes[1].ID != "personal" {
		t.Fatalf("registered homes = %#v", snapshot.Homes)
	}

	_, err = manager.RegisterHome(context.Background(), HomeInfo{
		ID:        "missing",
		Name:      "Missing DSH",
		Path:      filepath.Join(t.TempDir(), "does-not-exist"),
		Ownership: HomeOwnershipUser,
	})
	assertFailureCode(t, err, lifecycle.ErrorProfileNotFound)
}

type recordingRunner struct {
	path string
	args []string
	env  map[string]string
	cwd  string
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingRunner) Run(ctx context.Context, _ string, _ []string, _ map[string]string, _ string) (CommandResult, error) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.release:
		return CommandResult{}, nil
	case <-ctx.Done():
		return CommandResult{}, ctx.Err()
	}
}

type testPluginCommands struct{}

func (testPluginCommands) Install(profile, packageSpec string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "add", packageSpec}, nil
}

func (testPluginCommands) Remove(profile, packageName string) ([]string, error) {
	return []string{"plugin", "--profile", profile, "remove", packageName}, nil
}

type recordingInstaller struct {
	runtime RuntimeInfo
	version string
}

func (i *recordingInstaller) Install(_ context.Context, version string) (RuntimeInfo, error) {
	i.version = version
	return i.runtime, nil
}

func (r *recordingRunner) Run(_ context.Context, path string, args []string, env map[string]string, cwd string) (CommandResult, error) {
	r.path = path
	r.args = append([]string(nil), args...)
	r.env = env
	r.cwd = cwd
	return CommandResult{}, nil
}

func TestManagerSerializesExternalPluginOperations(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "alpha"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	manager, err := New(Config{
		StatePath:      filepath.Join(root, "manager.json"),
		WorkspaceRoot:  root,
		CommandRunner:  runner,
		PluginCommands: testPluginCommands{},
		Homes:          []HomeInfo{{ID: "work", Name: "Work", Path: homePath, Ownership: HomeOwnershipWork}},
		Runtimes:       []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
	})
	if err != nil {
		t.Fatal(err)
	}
	target := PluginTarget{RuntimeID: "dsh-test", Profile: ProfileRef{HomeID: "work", Name: "alpha"}}
	firstDone := make(chan error, 1)
	go func() {
		_, firstErr := manager.InstallPlugin(context.Background(), PluginInstallRequest{Target: target, Package: "@example/first"})
		firstDone <- firstErr
	}()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("first plugin operation did not reach the command runner")
	}
	secondCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, secondErr := manager.InstallPlugin(secondCtx, PluginInstallRequest{Target: target, Package: "@example/second"})
	assertFailureCode(t, secondErr, lifecycle.ErrorCancelled)
	close(runner.release)
	select {
	case firstErr := <-firstDone:
		if firstErr != nil {
			t.Fatalf("first plugin operation error = %v", firstErr)
		}
	case <-time.After(time.Second):
		t.Fatal("first plugin operation did not release")
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	for _, profile := range []string{"web", "web-clean"} {
		if err := os.MkdirAll(filepath.Join(homePath, "profiles", profile), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:     filepath.Join(root, "manager.json"),
		WorkspaceRoot: filepath.Join(root, "workspace"),
		Homes:         []HomeInfo{{ID: "work", Name: "Work managed", Path: homePath, Ownership: HomeOwnershipWork}},
		Runtimes:      []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
		DefaultSelection: LaunchSelection{
			RuntimeID: "dsh-test",
			Profile:   ProfileRef{HomeID: "work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func assertFailureCode(t *testing.T, err error, want lifecycle.ErrorCode) {
	t.Helper()
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a lifecycle failure", err)
	}
	if failure.Code != want {
		t.Fatalf("failure code = %s, want %s", failure.Code, want)
	}
}

type testProfileCatalog struct {
	definitions []ProfileDefinition
}

func (c testProfileCatalog) BuiltInProfiles() []ProfileDefinition {
	return c.definitions
}

type recordingRuntimeVerifier struct {
	path    string
	version string
	calls   int
}

func (v *recordingRuntimeVerifier) Verify(_ context.Context, path, version string) error {
	v.path = path
	v.version = version
	v.calls++
	return nil
}

type failingRuntimeVerifier struct{}

func (failingRuntimeVerifier) Verify(context.Context, string, string) error {
	return lifecycle.Failure{
		Code:    lifecycle.ErrorDSHUnsupportedVersion,
		Summary: "the test adapter rejected this runtime",
	}
}
