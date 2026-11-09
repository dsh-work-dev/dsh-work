//go:build linux && cgo && e67presenter && e67nativefixture && !server

package desktopapp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/localipc"
	linuxplatform "github.com/local/dsh-work/internal/platform/linux"
	"github.com/local/dsh-work/internal/redact"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if linuxplatform.RunBootstrap() {
		return
	}
	os.Exit(m.Run())
}

type x11FixtureWindow struct {
	ID, Parent      uint32
	PID             uint32
	Started         uint64
	Native, Visible bool
	Width, Height   int
}
type linuxUISlot struct {
	Slot  string
	State embeddedbrowser.ViewState
}

// Runs the original built application, not a second host/probe UI. The Node
// driver uses only the real DSH DOM and the normal WebKit inspector/bindings.
func TestLiveLinuxWailsDSHSlot(t *testing.T) {
	app, native, observer := os.Getenv("DSH_WORK_LINUX_WAILS_EXECUTABLE"), os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_X11_WINDOW_OBSERVER")
	if app == "" || native == "" || observer == "" {
		t.Skip("requires explicit real Wails/native/window-observer components")
	}
	if os.Getenv("DSH_WORK_ISOLATED_XVFB") != "1" {
		t.Fatal("this test requires its own isolated Xvfb, never a user's display")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	out := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if out == "" {
		out = filepath.Join(root, "evidence")
	}
	out, err = filepath.Abs(out)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	save := func(name string, value any) {
		b, e := json.MarshalIndent(value, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(out, name+".json"), append(b, '\n'), 0600)
		}
		if e != nil {
			t.Fatal(e)
		}
	}
	var reportMu sync.Mutex
	var report struct {
		Nonce, Input string
		Tick         int
	}
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/report" {
			reportMu.Lock()
			_ = json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&report)
			reportMu.Unlock()
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Linux DSH slot fixture</title><style>body{background:#ddf4e4;color:#143324;font:22px system-ui;padding:24px}input{font:inherit}</style><h1>Real DSH Linux CEF slot</h1><input aria-label="Unsubmitted fixture input" value="linux-dsh-original"><script>const nonce=crypto.randomUUID();let tick=0;setInterval(()=>fetch('/report',{method:'POST',body:JSON.stringify({nonce,input:document.querySelector('input').value,tick:++tick})}),250)</script>`)
	}))
	defer fixture.Close()
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if k, v, ok := strings.Cut(entry, "="); ok {
			env[k] = v
		}
	}
	delete(env, "WEBKIT_INSPECTOR_SERVER")
	delete(env, "WEBKIT_INSPECTOR_HTTP_SERVER")
	env["GDK_BACKEND"] = "x11"
	env["DSH_WORK_EMBEDDED_CHECKPOINT"] = "1"
	env["DSH_WORK_EMBEDDED_ROOT"] = root
	env["DSH_WORK_EMBEDDED_UI_INSTANCE"] = "1"
	env["DSH_WORK_CEF_EXECUTABLE"] = native
	owner := linuxplatform.NewSystemdAdapter()
	var workers []supervisor.Worker
	nativeFD := -1
	groupPath := ""
	defer func() {
		for i := len(workers) - 1; i >= 0; i-- {
			w := workers[i]
			d := w.Diagnostics()
			d.StdoutTail = redact.Secrets(d.StdoutTail)
			d.StderrTail = redact.Secrets(d.StderrTail)
			if data, e := json.MarshalIndent(d, "", "  "); e != nil {
				t.Errorf("save owned diagnostics: %v", e)
			} else if e = os.WriteFile(filepath.Join(out, "owned-process-"+strconv.Itoa(i)+".json"), append(data, '\n'), 0600); e != nil {
				t.Errorf("save owned diagnostics: %v", e)
			}
			c, stop := context.WithTimeout(context.Background(), 8*time.Second)
			if w.Diagnostics().ActiveProcesses != 0 {
				t.Log("failure-only cleanup of retained self-owned unit", w.Diagnostics().PID)
				if e := w.RequestStop(c); e != nil {
					t.Errorf("self-owned unit stop failed: %v", e)
				}
			}
			e := w.WaitEmpty(c)
			stop()
			e2 := w.Close()
			if e != nil || e2 != nil {
				t.Errorf("fixture owner cleanup unfinished: %v %v", e, e2)
			}
		}
		if nativeFD >= 0 {
			defer unix.Close(nativeFD)
			deadline := time.Now().Add(5 * time.Second)
			for {
				poll := []unix.PollFd{{Fd: int32(nativeFD), Events: unix.POLLIN}}
				n, pollErr := unix.Poll(poll, 0)
				b, groupErr := os.ReadFile(filepath.Join(groupPath, "cgroup.events"))
				if pollErr == nil && n == 1 && (os.IsNotExist(groupErr) || groupErr == nil && strings.Contains(string(b), "populated 0")) {
					break
				}
				if time.Now().After(deadline) {
					t.Error("fixture native pidfd/cgroup cleanup unfinished")
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
		}
		for _, name := range []string{"daemon.log", "ui.log"} {
			if b, e := os.ReadFile(filepath.Join(root, "logs", name)); e == nil {
				_ = os.WriteFile(filepath.Join(out, name), b, 0600)
			}
		}
	}()
	start := func(label string, args []string, environment map[string]string) supervisor.Worker {
		w, e := owner.Start(ctx, supervisor.LaunchPlan{GenerationID: "e67-real-ui-" + label, Executable: app, Args: args, Env: environment, WorkingDirectory: repo, ExpectedOrigin: workeripc.Origin}, nil)
		if e != nil {
			t.Fatal(label, e)
		}
		workers = append(workers, w)
		return w
	}
	background := start("daemon", []string{"--daemon"}, env)
	identity := background.Diagnostics()
	client := daemon.NewProcessClient(root, daemon.ProcessPeer{PID: identity.PID, Started: identity.ProcessStarted})
	defer client.Close()
	wait := func(label string, check func() bool) {
		t.Helper()
		for !check() {
			select {
			case <-ctx.Done():
				t.Fatal(label, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var snapshot daemon.Snapshot
	lastReadiness := ""
	probeDaemon := func() bool {
		select {
		case <-background.Exited():
			t.Fatal("real daemon exited before readiness", background.ExitResult())
		default:
		}
		c, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if e := client.JSON(c, "/snapshot", nil, &snapshot); e != nil {
			return false
		}
		code, summary := "", ""
		if snapshot.Status.Error != nil {
			code = string(snapshot.Status.Error.Code)
			summary = redact.Secrets(snapshot.Status.Error.Summary)
		}
		key := fmt.Sprint(snapshot.Status.State, "/", snapshot.Status.Phase, "/", code)
		if key != lastReadiness {
			lastReadiness = key
			save("readiness-last", map[string]any{"state": snapshot.Status.State, "phase": snapshot.Status.Phase, "generation": snapshot.Status.GenerationID, "code": code, "summary": summary})
		}
		if snapshot.Status.State == lifecycle.StateFailed {
			t.Fatal("real daemon boot failed", code, summary)
		}
		return snapshot.Protocol == daemon.Protocol && snapshot.PID == int(identity.PID)
	}
	// Ready requires the actual UI's plugin/mount confirmation. Start that
	// owned UI as soon as authenticated IPC exists, not after full Ready.
	wait("authenticated daemon IPC", probeDaemon)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	uiEnv := make(map[string]string, len(env)+1)
	for k, v := range env {
		uiEnv[k] = v
	}
	uiEnv["WEBKIT_INSPECTOR_HTTP_SERVER"] = "127.0.0.1:" + strconv.Itoa(port)
	ui := start("ui", nil, uiEnv)
	uiIdentity := ui.Diagnostics()
	wait("real daemon Ready after UI mount", func() bool {
		select {
		case <-ui.Exited():
			t.Fatal("owned UI exited before mount confirmation", ui.ExitResult())
		default:
		}
		return probeDaemon() && snapshot.Status.State == lifecycle.StateReady
	})
	save("daemon-ready", map[string]any{"pid": identity.PID, "created": identity.ProcessStarted, "generation": snapshot.Status.GenerationID, "state": snapshot.Status.State})
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	driver := exec.CommandContext(ctx, node, filepath.Join(repo, "scripts", "probe-linux-dsh-slot.mjs"), "http://127.0.0.1:"+strconv.Itoa(port), fixture.URL+"/", out)
	driver.Dir = repo
	var driverOutput bytes.Buffer
	driver.Stdout = &driverOutput
	driver.Stderr = &driverOutput
	if e = driver.Start(); e != nil {
		t.Fatal(e)
	}
	driverJoined := make(chan struct{})
	var driverErr error
	go func() { driverErr = driver.Wait(); close(driverJoined) }()
	defer func() {
		select {
		case <-driverJoined:
		default:
			killErr := driver.Process.Kill()
			<-driverJoined
			t.Logf("failure-only inspector driver cleanup kill=%v wait=%v", killErr, driverErr)
		}
		_ = os.WriteFile(filepath.Join(out, "driver.log"), driverOutput.Bytes(), 0600)
	}()
	readPhase := func(name string, dst any) {
		wait(name, func() bool {
			if b, e := os.ReadFile(filepath.Join(out, "failure.json")); e == nil {
				t.Fatalf("first inspector failure: %s", b)
			}
			b, e := os.ReadFile(filepath.Join(out, name+".json"))
			if e == nil && json.Unmarshal(b, dst) == nil {
				return true
			}
			select {
			case <-driverJoined:
				t.Fatalf("inspector driver exited before %s: %v %s", name, driverErr, driverOutput.String())
			default:
			}
			return false
		})
	}
	var visible linuxUISlot
	readPhase("visible", &visible)
	observe := func() []x11FixtureWindow {
		output, e := exec.CommandContext(ctx, observer, strconv.FormatUint(uint64(uiIdentity.PID), 10), strconv.FormatUint(uiIdentity.ProcessStarted, 10), native).Output()
		if e != nil {
			t.Fatal("owned XRes observation", e)
		}
		var rows []x11FixtureWindow
		if e = json.Unmarshal(output, &rows); e != nil {
			t.Fatal(e)
		}
		return rows
	}
	before := observe()
	save("windows-visible", before)
	var guest x11FixtureWindow
	for _, w := range before {
		if w.Native && w.Visible && w.Width > 100 && w.Height > 100 {
			for _, p := range before {
				if p.ID == w.Parent && p.PID == uiIdentity.PID {
					guest = w
				}
			}
		}
	}
	if guest.ID == 0 {
		t.Fatal("no actual visible CEF child under the admitted real UI", before)
	}
	nativeFD, e = unix.PidfdOpen(int(guest.PID), 0)
	if e != nil {
		t.Fatal(e)
	}
	if birth, e := localipc.ProcessStarted(guest.PID); e != nil || birth != guest.Started {
		t.Fatal("native process instance changed", birth, e)
	}
	group, e := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", guest.PID))
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(group), "\n") {
		if strings.HasPrefix(line, "0::/") {
			groupPath = filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::"))
		}
	}
	if groupPath == "" {
		t.Fatal("native owned cgroup unavailable")
	}
	imagePath := filepath.Join(out, "real-linux-dsh-slot.png")
	output, e := exec.CommandContext(ctx, "import", "-window", strconv.FormatUint(uint64(guest.Parent), 10), imagePath).CombinedOutput()
	if e != nil {
		t.Fatal("capture only verified own UI XID", e, string(output))
	}
	pixels, e := os.ReadFile(imagePath)
	if e != nil {
		t.Fatal(e)
	}
	image, e := png.Decode(bytes.NewReader(pixels))
	if e != nil {
		t.Fatal(e)
	}
	green := 0
	for y := image.Bounds().Min.Y; y < image.Bounds().Max.Y; y++ {
		for x := image.Bounds().Min.X; x < image.Bounds().Max.X; x++ {
			r, g, b, _ := image.At(x, y).RGBA()
			if r/257 == 0xdd && g/257 == 0xf4 && b/257 == 0xe4 {
				green++
			}
		}
	}
	if green < 1000 {
		t.Fatal("CEF fixture pixels absent from actual parent capture", green)
	}
	reportMu.Lock()
	baseline := report
	reportMu.Unlock()
	wait("real fixture JS heartbeat", func() bool {
		reportMu.Lock()
		defer reportMu.Unlock()
		baseline = report
		return report.Nonce != "" && report.Input == "linux-dsh-original" && report.Tick > 0
	})
	save("document-visible", baseline)
	if e = os.WriteFile(filepath.Join(out, "hide.request"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	var hidden linuxUISlot
	readPhase("hidden", &hidden)
	after := observe()
	save("windows-hidden", after)
	found := false
	for _, w := range after {
		if w.ID == guest.ID && w.PID == guest.PID && w.Started == guest.Started {
			found = true
			if w.Visible {
				t.Fatal("actual native guest remained visible after Hide ACK")
			}
		}
	}
	if !found {
		t.Fatal("Hide destroyed or lost the page instead of retaining it")
	}
	wait("same document alive after Hide", func() bool {
		reportMu.Lock()
		defer reportMu.Unlock()
		return report.Nonce == baseline.Nonce && report.Input == baseline.Input && report.Tick > baseline.Tick
	})
	if visible.State.Document != hidden.State.Document || hidden.State.Visible {
		t.Fatal("hidden slot document changed", visible, hidden)
	}
	if e = os.WriteFile(filepath.Join(out, "quit.request"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	readPhase("quit-requested", &map[string]any{})
	for _, w := range []supervisor.Worker{ui, background} {
		select {
		case <-w.Exited():
		case <-ctx.Done():
			t.Fatal("normal app exit deadline", ctx.Err())
		}
		if result := w.ExitResult(); result.Code != 0 || result.Err != "" {
			t.Fatal("normal real app exit", result)
		}
		if e = w.WaitEmpty(ctx); e != nil {
			t.Fatal("real app tree join", e)
		}
		if e = w.Close(); e != nil {
			t.Fatal(e)
		}
	}
	poll := []unix.PollFd{{Fd: int32(nativeFD), Events: unix.POLLIN}}
	if n, e := unix.Poll(poll, 0); e != nil || n != 1 {
		t.Fatal("actual native process not exited", n, e)
	}
	wait("native whole cgroup empty", func() bool {
		b, e := os.ReadFile(filepath.Join(groupPath, "cgroup.events"))
		return os.IsNotExist(e) || e == nil && strings.Contains(string(b), "populated 0")
	})
	select {
	case <-driverJoined:
	case <-ctx.Done():
		t.Fatal("inspector driver join", ctx.Err())
	}
	if driverErr != nil {
		t.Fatal("inspector driver", driverErr, driverOutput.String())
	}
	daemonLog, e := os.ReadFile(filepath.Join(root, "logs", "daemon.log"))
	if e != nil {
		t.Fatal("normal daemon shutdown log unavailable", e)
	}
	for _, marker := range []string{"CEF_SHUTDOWN_", "embedded cleanup completed-abnormally", "embedded instance shutdown incomplete", "embedded instance final cleanup incomplete"} {
		if bytes.Contains(daemonLog, []byte(marker)) {
			t.Fatal("native protocol cleanup did not complete normally", marker)
		}
	}
	save("normal-cleanup", map[string]any{"ui": ui.ExitResult(), "daemon": background.ExitResult(), "nativePID": guest.PID, "nativeStart": guest.Started, "nativePidfdExited": true, "nativeCgroupEmpty": true, "uiTreeEmpty": true, "daemonTreeEmpty": true})
	t.Log("original Wails/DSH slot rendered actual CEF pixels; Hide retained live document; native/root trees joined normally")
}
