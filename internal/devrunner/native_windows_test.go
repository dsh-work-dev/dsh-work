//go:build windows

package devrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/supervisor"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// This helper is a test-owned process/pipe fixture, never a DSH/CEF replacement.
func TestMain(m *testing.M) {
	if os.Getenv("E67_RUNNER_FIXTURE") == "1" {
		if err := fixtureProcess(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fixtureProcess() error {
	root := os.Getenv("E67_RUNNER_FIXTURE_ROOT")
	base := filepath.Join(root, "bin", "e67-ui-state")
	role := os.Getenv("DSH_WORK_EMBEDDED_UI_INSTANCE")
	isDaemon := len(os.Args) > 1 && os.Args[1] == "--daemon"
	endpoint := base
	if !isDaemon {
		endpoint += "-ui"
		if role == "2" {
			endpoint += "-embedded-2"
		}
	}
	listener, err := daemon.Listen(endpoint)
	if err != nil {
		return err
	}
	defer listener.Close()
	server := &http.Server{}
	var detached atomic.Bool
	stop := func() { go func() { time.Sleep(20 * time.Millisecond); listener.Close() }() }
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/snapshot":
			json.NewEncoder(w).Encode(daemon.Snapshot{PID: os.Getpid(), Root: filepath.Join(base, "app"), Status: lifecycle.Status{State: lifecycle.StateReady, GenerationID: "fixture-generation"}})
		case "/status":
			json.NewEncoder(w).Encode(map[string]any{"PID": os.Getpid(), "Marker": os.Getenv("E67_ORDINARY_MARKER")})
		case "/stop":
			json.NewEncoder(w).Encode(true)
			if role != "1" || os.Getenv("E67_RUNNER_FIXTURE_STALL") != "1" {
				stop()
			}
		case "/detach":
			detached.Store(true)
			json.NewEncoder(w).Encode(true)
			stop()
		case "/release":
			json.NewEncoder(w).Encode(true)
			stop()
		case "/control":
			var call daemon.Call
			json.NewDecoder(r.Body).Decode(&call)
			if call.Service != "EmbeddedBrowserService" || call.Method != "QuitOwned" {
				http.Error(w, "wrong fixed lifecycle", 400)
				return
			}
			for _, suffix := range []string{"-ui", "-ui-embedded-2"} {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				client := daemon.NewClient(base + suffix)
				_ = client.JSON(ctx, "/stop", nil, nil)
				client.Close()
				cancel()
			}
			json.NewEncoder(w).Encode(daemon.Result{Value: json.RawMessage(`{"state":"Stopped"}`)})
			stop()
		default:
			http.NotFound(w, r)
		}
	})
	_ = server.Serve(listener)
	server.Close()
	if !isDaemon && role == "1" && os.Getenv("E67_RUNNER_FIXTURE_HOLD_EXIT") == "1" {
		if err := os.WriteFile(filepath.Join(root, "fixture-ui-exit-held"), nil, 0600); err != nil {
			return err
		}
		for {
			if _, err := os.Stat(filepath.Join(root, "release-fixture-ui-exit")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if detached.Load() {
		for {
			if _, e := os.Stat(filepath.Join(root, "release-detached")); e == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}
func fixtureNative(t *testing.T) *Native {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	dest, err := os.Create(filepath.Join(bin, "dsh-work.exe"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(dest, source)
	dest.Close()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	env["E67_RUNNER_FIXTURE"] = "1"
	env["E67_RUNNER_FIXTURE_ROOT"] = root
	env["E67_ORDINARY_MARKER"] = "ordinary-terminal-value"
	env["E67_RUNNER_FIXTURE_STALL"] = "1"
	n := &Native{config: Config{Workspace: root, Runtime: filepath.Join(bin, "dsh-work.exe")}, env: env, tools: map[string]string{}, children: map[int]*owned{}, build: "complete", adapter: win.NewJobObjectAdapter()}
	n.readyProbe = func(ctx context.Context, instance int) bool {
		var state struct {
			PID    int
			Marker string
		}
		return probe(ctx, n.uiIdentity(instance), "/status", &state) == nil && state.Marker == "ordinary-terminal-value"
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := cleanupFixture(ctx, n); err != nil {
			t.Error("fixture cleanup", err)
		}
	})
	return n
}

func cleanupFixture(ctx context.Context, n *Native) error {
	for _, i := range []int{1, 2} {
		p := n.get(i)
		if p == nil {
			continue
		}
		if live(p.worker) {
			bounded, cancel := context.WithTimeout(ctx, time.Second)
			releaseErr := ownedProbe(bounded, n.uiIdentity(i), "/release", p, nil)
			cancel()
			// The fixture closes its listener before main returns. A missing pipe
			// is not process exit: join the held owner before asking stopAll to
			// authenticate the remaining endpoints. No force-stop is needed.
			select {
			case <-p.worker.Exited():
			case <-ctx.Done():
				return fmt.Errorf("fixture UI %d release=%v; exit not joined: %w", i, releaseErr, ctx.Err())
			}
		}
		if err := n.reap(ctx, i); err != nil {
			return err
		}
	}
	return n.stopAll(ctx)
}

func TestFixtureCleanupJoinsReleasedUIBeforeGlobalStop(t *testing.T) {
	n := fixtureNative(t)
	n.env["E67_RUNNER_FIXTURE_HOLD_EXIT"] = "1"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	release := filepath.Join(n.config.Workspace, "release-fixture-ui-exit")
	defer os.WriteFile(release, nil, 0600)
	if err := n.startUI(ctx, 1); err != nil {
		t.Fatal(err)
	}
	p := n.get(1)
	if err := ownedProbe(ctx, n.uiIdentity(1), "/release", p, nil); err != nil {
		t.Fatal(err)
	}
	if err := n.wait(ctx, func() bool {
		_, err := os.Stat(filepath.Join(n.config.Workspace, "fixture-ui-exit-held"))
		return err == nil
	}); err != nil {
		t.Fatal(err)
	}
	if !live(p.worker) || !live(n.get(0).worker) {
		t.Fatal("fixture did not isolate closed UI endpoint from process exit")
	}
	// Hold the real process after closing its pipe, then let it exit naturally.
	// No shutdown request can use that endpoint during this interval.
	joined := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		joined <- os.WriteFile(release, nil, 0600)
	}()
	err := cleanupFixture(ctx, n)
	if releaseErr := <-joined; releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil {
		t.Fatal("cleanup raced a naturally exiting owned UI", err)
	}
	if !n.CleanupComplete() {
		t.Fatal("cleanup returned before joining the actual owned trees")
	}
}
func TestOwnedLifecycleEnvironmentForeignRefusalAndFailedStop(t *testing.T) {
	n := fixtureNative(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// A real fixture outside this manager's map must not be adopted or stopped.
	foreign, err := n.launch(ctx, 2, filepath.Join(n.config.Workspace, "bin", "dsh-work.exe"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = n.wait(ctx, func() bool { var v struct{ PID int }; return probe(ctx, n.uiIdentity(2), "/status", &v) == nil }); err != nil {
		t.Fatal(err)
	}
	if err = n.startUI(ctx, 2); err == nil || n.get(2) != nil || !live(foreign) {
		t.Fatal("foreign process was adopted/stopped", err)
	}
	if err = probe(ctx, n.uiIdentity(2), "/release", nil); err != nil {
		t.Fatal(err)
	}
	<-foreign.Exited()
	if err = foreign.WaitEmpty(ctx); err != nil {
		t.Fatal(err)
	}
	if err = foreign.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("E67_ORDINARY_MARKER", "controller-must-not-change-runner-env")
	if err = n.startUI(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = n.startUI(ctx, 2); err != nil {
		t.Fatal(err)
	}
	first, other, background := n.get(1), n.get(2), n.get(0)
	status := n.Status(ctx)
	if !status.UIs[1].Alive || !status.UIs[1].IPCReady || !status.UIs[1].Ready || !status.Daemon.Ready {
		t.Fatal("readiness was only a spawn assertion", status)
	}
	if err = n.buildCandidate(ctx); err == nil {
		t.Fatal("build accepted a live UI")
	}
	short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	err = n.Execute(short, "stop", 1)
	stop()
	if err == nil || n.get(1) != first || !live(first.worker) {
		t.Fatal("failed normal stop discarded or killed its process owner", err)
	}
	if !live(other.worker) || !live(background.worker) {
		t.Fatal("failed A stop affected B/daemon")
	}
	if err = probe(ctx, n.uiIdentity(1), "/release", nil); err != nil {
		t.Fatal(err)
	}
	<-first.worker.Exited()
	if err = n.stopUI(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err = n.Execute(ctx, "restart", 2); err != nil {
		t.Fatal(err)
	}
	if n.get(2).worker.Diagnostics().ProcessStarted == other.worker.Diagnostics().ProcessStarted || n.get(0) != background {
		t.Fatal("restart did not replace only the owned UI")
	}
	if err = n.stopAll(ctx); err != nil {
		t.Fatal(err)
	}
	if !n.CleanupComplete() || n.get(0) != nil {
		t.Fatal("normal shutdown retained active owners")
	}
	for _, state := range n.Status(ctx).UIs {
		if state.Alive || state.TreeOwned || state.ExitCode == nil {
			t.Fatal("exit history misrepresented as live ownership")
		}
	}
	t.Log("actual self-owned process/pipe lifecycle: inherited env unchanged; unowned peer refused; live-build refused; failed normal stop retained owner without force; per-UI restart preserved daemon; normal global stop joined all trees")
}

func TestGlobalStopRefusesForeignUI(t *testing.T) {
	n := fixtureNative(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := n.startUI(ctx, 1); err != nil {
		t.Fatal(err)
	}
	foreign, err := n.launch(ctx, 2, filepath.Join(n.config.Workspace, "bin", "dsh-work.exe"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = probe(ctx, n.uiIdentity(2), "/release", nil)
		<-foreign.Exited()
		_ = foreign.WaitEmpty(ctx)
		_ = foreign.Close()
	}()
	if err = n.wait(ctx, func() bool { var v struct{ PID int }; return probe(ctx, n.uiIdentity(2), "/status", &v) == nil }); err != nil {
		t.Fatal(err)
	}
	if err = n.stopAll(ctx); err == nil || !live(foreign) || !live(n.get(0).worker) || !live(n.get(1).worker) {
		t.Fatal("foreign UI global shutdown was not refused", err)
	}
	t.Log("actual foreign UI2 endpoint blocks global QuitOwned; owned UI1/daemon and foreign process remain live")
}

func TestReplacementPipeCannotReceiveOwnedStop(t *testing.T) {
	n := fixtureNative(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := n.startUI(ctx, 2); err != nil {
		t.Fatal(err)
	}
	original := n.get(2)
	if err := ownedProbe(ctx, n.uiIdentity(2), "/detach", original, nil); err != nil {
		t.Fatal(err)
	}
	if err := n.wait(ctx, func() bool { var v any; return absent(probe(ctx, n.uiIdentity(2), "/status", &v)) }); err != nil {
		t.Fatal(err)
	}
	replacement, err := n.launch(ctx, 2, filepath.Join(n.config.Workspace, "bin", "dsh-work.exe"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = probe(ctx, n.uiIdentity(2), "/release", nil)
		<-replacement.Exited()
		_ = replacement.WaitEmpty(ctx)
		_ = replacement.Close()
		_ = os.WriteFile(filepath.Join(n.config.Workspace, "release-detached"), nil, 0600)
		<-original.worker.Exited()
	}()
	if err = n.wait(ctx, func() bool { var v struct{ PID int }; return probe(ctx, n.uiIdentity(2), "/status", &v) == nil }); err != nil {
		t.Fatal(err)
	}
	if err = n.stopUI(ctx, 2); err == nil || !live(replacement) || !live(original.worker) {
		t.Fatal("replacement received owned stop", err)
	}
	// Correct PID with wrong creation identity must also be rejected before HTTP.
	d := replacement.Diagnostics()
	client := daemon.NewProcessClient(n.uiIdentity(2), daemon.ProcessPeer{PID: uint32(d.PID), Started: d.ProcessStarted + 1})
	defer client.Close()
	if err = client.JSON(ctx, "/stop", nil, nil); err == nil || !live(replacement) {
		t.Fatal("wrong creation accepted", err)
	}
	t.Log("same pipe endpoint rebound by distinct live process: owned stop rejected; matching PID/wrong creation rejected before HTTP; both live")
}

func TestBuildInterpreterUsesExplicitLauncherImage(t *testing.T) {
	// Actual installed pwsh executes a local -File under its existing policy.
	// No execution-policy argument/environment mutation is supplied.
	actual := os.Getenv("E67_TEST_LAUNCHER_POWERSHELL")
	if actual == "" {
		t.Skip("explicit actual launcher interpreter required")
	}
	selected, err := launcherPowerShell(actual)
	if err != nil || !strings.EqualFold(selected, actual) {
		t.Fatal("launcher interpreter replaced", selected, err)
	}
	for _, bad := range []string{"", "powershell.exe", filepath.Join(t.TempDir(), "cmd.exe")} {
		if _, err = launcherPowerShell(bad); err == nil {
			t.Fatal("invalid launcher image accepted", bad)
		}
	}
	script := filepath.Join(t.TempDir(), "interpreter.ps1")
	if err = os.WriteFile(script, []byte(`@{Path=(Get-Process -Id $PID).Path;Policy=[string](Get-ExecutionPolicy)}|ConvertTo-Json -Compress`), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(selected, "-NoProfile", "-File", script)
	hideCommand(command)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("real launcher interpreter -File refused", err, string(output))
	}
	var observed struct{ Path, Policy string }
	if err = json.Unmarshal(output, &observed); err != nil || !strings.EqualFold(observed.Path, actual) || observed.Policy == "" {
		t.Fatal("wrong actual child image", string(output), err)
	}
	t.Logf("real -File child=%s policy=%s; no fallback or policy override", observed.Path, observed.Policy)
}

func TestRunnerBuildDescendantHelper(t *testing.T) {
	if os.Getenv("E67_RUNNER_BUILD_TREE") != "1" {
		t.Skip("process fixture")
	}
	if os.Args[len(os.Args)-1] == "child" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestRunnerBuildDescendantHelper$", "child")
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if os.Getenv("E67_RUNNER_BUILD_FAIL") == "1" {
		os.Exit(7)
	}
	os.Exit(0)
}

func TestReapBuildJoinsOwnedDescendantsAfterRootExit(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	worker, err := win.NewJobObjectAdapter().Start(context.Background(), supervisor.LaunchPlan{
		GenerationID: "runner-build-descendants", Executable: executable,
		Args: []string{"-test.run=^TestRunnerBuildDescendantHelper$"}, Env: map[string]string{"E67_RUNNER_BUILD_TREE": "1"},
		WorkingDirectory: t.TempDir(), ExpectedOrigin: "http://127.0.0.1:1",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	select {
	case <-worker.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("root did not exit")
	}
	if result := worker.ExitResult(); result.Code != 0 {
		t.Fatalf("root failed: %+v", result)
	}
	if worker.Diagnostics().ActiveProcesses == 0 {
		t.Fatal("fixture did not retain owned descendant")
	}
	n := &Native{buildOwner: worker}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := n.reapBuild(ctx); err != nil {
		t.Fatalf("reap build: %v", err)
	}
	if n.buildOwner != nil || worker.Diagnostics().ActiveProcesses != 0 {
		t.Fatal("build owner/tree was not joined")
	}
}

type buildTreeFixtureAdapter struct {
	executable string
	calls      int
}

func (a *buildTreeFixtureAdapter) Start(ctx context.Context, plan supervisor.LaunchPlan, handler supervisor.RawOutputHandler) (supervisor.Worker, error) {
	a.calls++
	plan.Executable = a.executable
	plan.Args = []string{"-test.run=^TestRunnerBuildDescendantHelper$"}
	return win.NewJobObjectAdapter().Start(ctx, plan, handler)
}
func TestBuildCandidateJoinsDescendantsBeforeNextStage(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter := &buildTreeFixtureAdapter{executable: executable}
	n := &Native{config: Config{Workspace: t.TempDir()}, children: map[int]*owned{}, env: map[string]string{"E67_RUNNER_BUILD_TREE": "1"}, tools: map[string]string{}, adapter: adapter}
	defer func() {
		if n.buildOwner != nil {
			n.buildOwner.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := n.buildCandidate(ctx); err != nil {
		t.Fatal(err)
	}
	if n.build != "complete" || n.buildOwner != nil || adapter.calls != 2 {
		t.Fatalf("build=%s owner=%v stages=%d", n.build, n.buildOwner, adapter.calls)
	}
}

func TestBuildCandidatePreservesFailedRootAfterDescendantCleanup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	adapter := &buildTreeFixtureAdapter{executable: executable}
	n := &Native{config: Config{Workspace: t.TempDir()}, children: map[int]*owned{}, env: map[string]string{"E67_RUNNER_BUILD_TREE": "1", "E67_RUNNER_BUILD_FAIL": "1"}, tools: map[string]string{}, adapter: adapter}
	defer func() {
		if n.buildOwner != nil {
			n.buildOwner.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err = n.buildCandidate(ctx)
	if err == nil || !strings.Contains(err.Error(), "exit=7") {
		t.Fatalf("root failure lost: %v", err)
	}
	if n.build != "failed" || n.buildOwner != nil || adapter.calls != 1 {
		t.Fatalf("build=%s owner=%v stages=%d", n.build, n.buildOwner, adapter.calls)
	}
}
