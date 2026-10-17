package dshadapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workspacecontext"
)

const testDSHVersion = "0.2.1-rc.2"

type fakeExecutor struct {
	result CommandResult
	err    error
	path   string
	args   []string
	env    map[string]string
}

func (f *fakeExecutor) Run(_ context.Context, path string, args []string, env map[string]string, _ string) (CommandResult, error) {
	f.path = path
	f.args = append([]string(nil), args...)
	f.env = nil
	if env != nil {
		f.env = make(map[string]string, len(env))
		for key, value := range env {
			f.env[key] = value
		}
	}
	return f.result, f.err
}

func TestDiscoverAcceptsAnyVersionAtOrAboveMinimum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.2.0-rc.2", "0.2.1-rc.2", "0.3.0"} {
		executor := &fakeExecutor{result: CommandResult{Stdout: "dsh " + version + "\n"}}
		adapter := New(executor)
		adapter.SetExecutableOverride(path)
		runtime, err := adapter.Discover(context.Background())
		if err != nil {
			t.Fatalf("Discover() with %s error = %v", version, err)
		}
		if runtime.Path != path || runtime.Version != version || strings.Join(executor.args, " ") != "--version" {
			t.Fatalf("unexpected runtime or version command for %s: %+v %#v", version, runtime, executor.args)
		}
	}
}

func TestDiscoverRejectsVersionsBelowMinimum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"0.1.7-rc.2", "0.2.0-rc.1"} {
		adapter := New(&fakeExecutor{result: CommandResult{Stdout: "dsh " + version + "\n"}})
		_, err := adapter.DiscoverPath(context.Background(), path)
		var failure lifecycle.Failure
		if err == nil || !asFailure(err, &failure) || failure.Code != lifecycle.ErrorDSHUnsupportedVersion {
			t.Fatalf("DiscoverPath() with %s: expected unsupported version failure, got %v", version, err)
		}
		if !strings.Contains(failure.Detail, "0.2.0-rc.2") {
			t.Fatalf("DiscoverPath() with %s: detail = %q, want the minimum version", version, failure.Detail)
		}
	}
}

func TestDiscoverRejectsUnparseableVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := New(&fakeExecutor{result: CommandResult{Stdout: "dsh dev"}})
	_, err := adapter.DiscoverPath(context.Background(), path)
	var failure lifecycle.Failure
	if err == nil || !asFailure(err, &failure) || failure.Code != lifecycle.ErrorDSHVersionCheckFailed {
		t.Fatalf("expected version check failure, got %v", err)
	}
}

func TestVerifyWithEnvironmentPassesChildToolchainOverlay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{result: CommandResult{Stdout: "dsh " + testDSHVersion}}
	adapter := New(executor)
	version, err := adapter.VerifyWithEnvironment(context.Background(), path, map[string]string{"PATH": "managed-node"})
	if err != nil {
		t.Fatalf("VerifyWithEnvironment() error = %v", err)
	}
	if version != testDSHVersion || executor.env["PATH"] != "managed-node" {
		t.Fatalf("verification version = %q environment = %#v", version, executor.env)
	}
}

func TestDiscoverSelectedPathReportsObservedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{result: CommandResult{Stdout: "dsh 2.4.6"}}
	adapter := New(executor)
	runtime, err := adapter.DiscoverSelectedPath(context.Background(), path, map[string]string{"PATH": "managed-node"})
	if err != nil {
		t.Fatalf("DiscoverSelectedPath() error = %v", err)
	}
	if runtime.Version != "2.4.6" || executor.env["PATH"] != "managed-node" {
		t.Fatalf("selected runtime = %#v env=%#v", runtime, executor.env)
	}
}

func TestDiscoverUsesDSHWorkOverrideNameOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_WORK_EXECUTABLE", path)
	t.Setenv("WORK_DSH_EXECUTABLE", filepath.Join(t.TempDir(), "old-dsh.cmd"))
	adapter := New(&fakeExecutor{result: CommandResult{Stdout: "dsh " + testDSHVersion}})

	runtime, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() with DSH_WORK_EXECUTABLE error = %v", err)
	}
	if runtime.Path != path {
		t.Fatalf("runtime path = %q, want %q", runtime.Path, path)
	}

	t.Setenv("DSH_WORK_EXECUTABLE", "")
	adapter.SetDiscoveryRoot(t.TempDir())
	if _, err := adapter.Discover(context.Background()); err == nil {
		t.Fatal("Discover() accepted the removed WORK_DSH_EXECUTABLE compatibility name")
	}
}

func TestParseAndValidateReadyAnnouncement(t *testing.T) {
	adapter := New(nil)
	announcement, ok := adapter.ParseReadyAnnouncement("\x1b[32mdsh web:\x1b[0m http://127.0.0.1:4321/.\r\n")
	if !ok || announcement.URL != "http://127.0.0.1:4321/" {
		t.Fatalf("unexpected announcement: %+v, %v", announcement, ok)
	}
	if _, ok := adapter.ParseReadyAnnouncement("server is starting"); ok {
		t.Fatal("unexpected readiness announcement")
	}
	plan := supervisor.LaunchPlan{
		GenerationID:     "generation",
		Executable:       "dsh.cmd",
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:4321",
	}
	if err := adapter.ValidateReady(announcement, plan); err != nil {
		t.Fatalf("ValidateReady() error = %v", err)
	}
	if err := adapter.ValidateReady(ReadyAnnouncement{URL: "http://127.0.0.1:4321/?token=launch-token"}, plan); err != nil {
		t.Fatalf("ValidateReady() rejected the DSH session token: %v", err)
	}
	for _, value := range []string{
		"http://localhost:4321/",
		"http://127.0.0.1:4322/",
		"http://127.0.0.1:4321/?untrusted=1",
		"http://127.0.0.1:4321/?token=one&token=two",
		"http://attacker@127.0.0.1:4321/",
		"https://127.0.0.1:4321/",
	} {
		if err := adapter.ValidateReady(ReadyAnnouncement{URL: value}, plan); err == nil {
			t.Errorf("ValidateReady(%q) unexpectedly succeeded", value)
		}
	}
}

func TestBuildLaunchPlanUsesHostChannelAndDataDirectory(t *testing.T) {
	adapter := New(nil)
	bootstrapDirectory := t.TempDir()
	dataDirectory := filepath.Join(t.TempDir(), "dsh-data")
	plan, err := adapter.BuildLaunchPlan(LaunchContext{
		GenerationID:       "generation",
		Runtime:            Runtime{Path: "C:\\tools\\dsh.cmd", Version: testDSHVersion},
		BootstrapDirectory: bootstrapDirectory,
		DataDirectory:      dataDirectory,
		Profile:            "web",
		Workspace:          workspacecontext.Context{GenerationID: "generation", State: workspacecontext.StateSelectionRequired},
		HostPatch:          "host.patch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Args, " ") != "--patch host.patch.json --profile web --host 127.0.0.1 --port 1 --no-open" {
		t.Fatalf("unexpected DSH args: %#v", plan.Args)
	}
	if plan.Env["DSH_HOME"] == "" || plan.Env["DSH_HOME"] != dataDirectory || plan.WorkingDirectory != bootstrapDirectory || plan.ExpectedOrigin != "http://127.0.0.1:1" {
		t.Fatalf("unexpected launch plan: %+v", plan)
	}
}

func TestBuildLaunchPlanAcceptsSelectedExactRuntimeVersion(t *testing.T) {
	adapter := New(nil)
	plan, err := adapter.BuildLaunchPlan(LaunchContext{
		GenerationID: "generation", Runtime: Runtime{Path: `C:\tools\dsh-9.cmd`, Version: "9.8.7"},
		BootstrapDirectory: t.TempDir(), DataDirectory: t.TempDir(), Profile: "web",
		Workspace: workspacecontext.Context{GenerationID: "generation", State: workspacecontext.StateSelectionRequired}, HostPatch: "host.patch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Executable != `C:\tools\dsh-9.cmd` {
		t.Fatalf("plan = %#v", plan)
	}
}

func TestVerifyReturnsObservedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{result: CommandResult{Stdout: "dsh 9.8.7"}}
	adapter := New(executor)
	version, err := adapter.Verify(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if version != "9.8.7" {
		t.Fatalf("Verify() version = %q, want 9.8.7", version)
	}
}

func TestBuildLaunchPlanCarriesOnlyTheRuntimeToolchainOverlay(t *testing.T) {
	adapter := New(nil)
	plan, err := adapter.BuildLaunchPlan(LaunchContext{
		GenerationID: "generation",
		Runtime: Runtime{
			Path:    `C:\tools\dsh.cmd`,
			Version: testDSHVersion,
			Env:     map[string]string{"PATH": `C:\dsh-work\node`},
		},
		BootstrapDirectory: t.TempDir(),
		DataDirectory:      filepath.Join(t.TempDir(), "dsh-data"),
		Profile:            "web",
		Workspace:          workspacecontext.Context{GenerationID: "generation", State: workspacecontext.StateSelectionRequired},
		HostPatch:          "host.patch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["PATH"] != `C:\dsh-work\node` || plan.Env["DSH_HOME"] == "" {
		t.Fatalf("launch environment = %#v", plan.Env)
	}
}

func TestVerifyProfileValidatesTheRuntimeProfilePair(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := New(nil)
	if err := adapter.VerifyProfile(context.Background(), runtimePath, testDSHVersion, t.TempDir(), "web"); err != nil {
		t.Fatalf("VerifyProfile() error = %v", err)
	}
	if err := adapter.VerifyProfile(context.Background(), runtimePath, testDSHVersion, t.TempDir(), "../web"); err == nil {
		t.Fatal("VerifyProfile() accepted a path-shaped profile name")
	} else {
		var failure lifecycle.Failure
		if !asFailure(err, &failure) || failure.Code != lifecycle.ErrorRuntimeProfileIncompatible {
			t.Fatalf("VerifyProfile() error = %v, want runtime-profile-incompatible", err)
		}
	}
}

func TestDesktopRejectsNonWebBuiltInProfiles(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("placeholder"), 0600); err != nil {
		t.Fatal(err)
	}
	adapter := New(nil)
	for _, name := range []string{"headless", "sdk", "sdk-minimal", "acp"} {
		err := adapter.VerifyProfile(context.Background(), runtimePath, testDSHVersion, t.TempDir(), name)
		var failure lifecycle.Failure
		if !asFailure(err, &failure) || failure.Code != lifecycle.ErrorRuntimeProfileIncompatible || failure.Retryable {
			t.Errorf("%s: want non-retryable incompatibility before launch, got %v", name, err)
		}
	}
	if err := adapter.VerifyProfile(context.Background(), runtimePath, testDSHVersion, t.TempDir(), "my-web"); err != nil {
		t.Fatal(err)
	}
}

func TestProbeRequiresHTMLAtTheExpectedOrigin(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html><body>ready</body></html>"))
	}))
	server.Listener = listener
	server.Start()
	defer server.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	adapter := New(nil)
	plan := supervisor.LaunchPlan{
		GenerationID:     "generation",
		Executable:       "dsh.cmd",
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:" + strconv.Itoa(port),
	}
	if err := adapter.Probe(context.Background(), ReadyAnnouncement{URL: plan.ExpectedOrigin + "/"}, plan, server.Client()); err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
}

func TestProbeCompletesDSHLaunchTokenExchange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("token") != "" {
			http.SetCookie(w, &http.Cookie{Name: "dsh-session", Value: "test-session", Path: "/"})
			w.Header().Set("Location", "/")
			w.WriteHeader(http.StatusSeeOther)
			return
		}
		if _, err := r.Cookie("dsh-session"); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!doctype html><html></html>"))
	}))
	defer server.Close()
	server.Client().CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	plan := supervisor.LaunchPlan{
		GenerationID:   "probe-token-exchange",
		ExpectedOrigin: server.URL,
	}
	adapter := New(nil)
	if err := adapter.Probe(context.Background(), ReadyAnnouncement{URL: server.URL + "/?token=launch-token"}, plan, server.Client()); err != nil {
		t.Fatalf("Probe() did not complete token exchange: %v", err)
	}
}

func asFailure(err error, target *lifecycle.Failure) bool {
	value, ok := err.(lifecycle.Failure)
	if ok {
		*target = value
	}
	return ok
}
