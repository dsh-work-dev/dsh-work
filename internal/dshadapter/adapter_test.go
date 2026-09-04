package dshadapter

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workspacecontext"
)

type fakeExecutor struct {
	result CommandResult
	err    error
	path   string
	args   []string
}

func (f *fakeExecutor) Run(_ context.Context, path string, args []string, _ map[string]string, _ string) (CommandResult, error) {
	f.path = path
	f.args = append([]string(nil), args...)
	return f.result, f.err
}

func TestDiscoverRequiresExactPinnedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(path, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor := &fakeExecutor{result: CommandResult{Stdout: "dsh 0.1.2-alpha.3\n"}}
	adapter := New(executor, SupportedVersion)
	adapter.SetExecutableOverride(path)
	runtime, err := adapter.Discover(context.Background())
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if runtime.Path != path || runtime.Version != SupportedVersion || strings.Join(executor.args, " ") != "--version" {
		t.Fatalf("unexpected runtime or version command: %+v %#v", runtime, executor.args)
	}

	executor.result.Stdout = "dsh 0.1.2-alpha.2\n"
	_, err = adapter.Discover(context.Background())
	var failure lifecycle.Failure
	if err == nil || !asFailure(err, &failure) || failure.Code != lifecycle.ErrorDSHUnsupportedVersion {
		t.Fatalf("expected unsupported version failure, got %v", err)
	}
}

func TestParseAndValidateReadyAnnouncement(t *testing.T) {
	adapter := New(nil, SupportedVersion)
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
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     4321,
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

func TestBuildLaunchPlanUsesExplicitLoopbackPortAndDataDirectory(t *testing.T) {
	adapter := New(nil, SupportedVersion)
	bootstrapDirectory := t.TempDir()
	dataDirectory := filepath.Join(t.TempDir(), "dsh-data")
	plan, err := adapter.BuildLaunchPlan(LaunchContext{
		GenerationID:       "generation",
		Runtime:            Runtime{Path: "C:\\tools\\dsh.cmd", Version: SupportedVersion},
		BootstrapDirectory: bootstrapDirectory,
		DataDirectory:      dataDirectory,
		Profile:            "web",
		Workspace:          workspacecontext.Context{GenerationID: "generation", State: workspacecontext.StateSelectionRequired},
		Port:               4567,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(plan.Args, " ") != "--profile web --host 127.0.0.1 --port 4567 --no-open" {
		t.Fatalf("unexpected DSH args: %#v", plan.Args)
	}
	if plan.Env["DSH_HOME"] == "" || plan.Env["DSH_HOME"] != dataDirectory || plan.WorkingDirectory != bootstrapDirectory || plan.ExpectedOrigin != "http://127.0.0.1:4567" {
		t.Fatalf("unexpected launch plan: %+v", plan)
	}
}

func TestVerifyProfileValidatesTheRuntimeProfilePair(t *testing.T) {
	runtimePath := filepath.Join(t.TempDir(), "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := New(nil, SupportedVersion)
	if err := adapter.VerifyProfile(context.Background(), runtimePath, SupportedVersion, t.TempDir(), "web"); err != nil {
		t.Fatalf("VerifyProfile() error = %v", err)
	}
	if err := adapter.VerifyProfile(context.Background(), runtimePath, SupportedVersion, t.TempDir(), "../web"); err == nil {
		t.Fatal("VerifyProfile() accepted a path-shaped profile name")
	} else {
		var failure lifecycle.Failure
		if !asFailure(err, &failure) || failure.Code != lifecycle.ErrorRuntimeProfileIncompatible {
			t.Fatalf("VerifyProfile() error = %v, want runtime-profile-incompatible", err)
		}
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
	adapter := New(nil, SupportedVersion)
	plan := supervisor.LaunchPlan{
		GenerationID:     "generation",
		Executable:       "dsh.cmd",
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:" + strconv.Itoa(port),
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     port,
	}
	if err := adapter.Probe(context.Background(), ReadyAnnouncement{URL: plan.ExpectedOrigin + "/"}, plan); err != nil {
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
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(serverURL.Port())
	if err != nil {
		t.Fatal(err)
	}
	plan := supervisor.LaunchPlan{
		GenerationID:   "probe-token-exchange",
		ExpectedOrigin: server.URL,
		ExpectedHost:   serverURL.Hostname(),
		ExpectedPort:   port,
	}
	adapter := New(nil, SupportedVersion)
	if err := adapter.Probe(context.Background(), ReadyAnnouncement{URL: server.URL + "/?token=launch-token"}, plan); err != nil {
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
