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
	// The fixed driver exercises DSH's real in-page directory picker. Do not
	// accidentally open a native chooser that this inspector cannot observe.
	for _, chooser := range []string{"zenity", "kdialog"} {
		if path, err := exec.LookPath(chooser); err == nil {
			t.Fatalf("DOM-only workspace fixture requires the auto browse backend; native chooser present: %s", path)
		}
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
	agentTest := os.Getenv("DSH_WORK_LINUX_AGENT_TEST") == "1"
	dualTest := os.Getenv("DSH_WORK_LINUX_DUAL_TEST") == "1"
	if os.Getenv("DSH_WORK_LINUX_DUAL_FAULT") == "1" && !dualTest {
		t.Fatal("fault lifecycle requires explicit dual fixture mode")
	}
	if agentTest && dualTest {
		t.Fatal("Agent and dual lifecycle fixtures must run independently")
	}
	if agentTest {
		for _, name := range []string{"agent-permission-browser.captured", "agent-permission-site.captured", "agent-completed.json", "agent-failure-observation-request.json", "agent-failure-observation.json"} {
			if _, err = os.Stat(filepath.Join(out, name)); !os.IsNotExist(err) {
				t.Fatal("Agent evidence directory contains a stale handshake", name, err)
			}
		}
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
	var report linuxFixtureReport
	var reportAt time.Time
	reports := map[string]linuxFixtureReport{}
	pageLoads := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/report" {
			reportMu.Lock()
			var next linuxFixtureReport
			if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&next) == nil && next.Nonce != "" {
				reports[next.Nonce] = next
				if report.Nonce == "" || next.Nonce == report.Nonce {
					report = next
					reportAt = time.Now()
				}
			}
			reportMu.Unlock()
			w.WriteHeader(204)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if dualTest {
			reportMu.Lock()
			pageLoads++
			sequence := pageLoads
			reportMu.Unlock()
			fmt.Fprintf(w, `<!doctype html><title>Linux DSH slot fixture</title><style>body{background:#ddf4e4;color:#143324;font:22px system-ui;padding:24px}input{font:inherit}</style><h1>Real DSH Linux CEF slot</h1><input aria-label="Unsubmitted fixture input" value=""><script>const nonce=crypto.randomUUID(),storageBefore=localStorage.getItem("e67-marker")||"",cookieBefore=document.cookie,marker=storageBefore||crypto.randomUUID();localStorage.setItem("e67-marker",marker);document.cookie="e67="+marker+"; Path=/; SameSite=Strict; Max-Age=3600";const input=document.querySelector("input");input.value=%d===1?"linux-dsh-original":"linux-dsh-dirty-"+nonce;let tick=0;setInterval(()=>fetch("/report",{method:"POST",body:JSON.stringify({nonce,input:input.value,dirty:input.value!==input.defaultValue,tick:++tick,marker,storageBefore,cookieBefore})}),250)</script>`, sequence)
			return
		}
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
	if agentTest {
		env["DSH_WORK_LINUX_AGENT_DIAGNOSTICS"] = "1"
	}
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
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	var diagnosticStarted bool
	var diagnosticJoined chan struct{}
	var diagnosticErr error
	defer func() {
		if diagnosticJoined != nil {
			<-diagnosticJoined
			if diagnosticErr != nil {
				t.Log("bounded read-only boot diagnostic", diagnosticErr)
			}
		}
	}()
	startBootDiagnostic := func() {
		diagnosticStarted = true
		diagnosticJoined = make(chan struct{})
		go func() {
			defer close(diagnosticJoined)
			diagnosticCtx, stop := context.WithTimeout(ctx, 20*time.Second)
			defer stop()
			// Capture only an XRes/instance-verified window of this own UI. This
			// still works if WebKit's inspector cannot connect its child process.
			output, e := exec.CommandContext(diagnosticCtx, observer, strconv.FormatUint(uint64(uiIdentity.PID), 10), strconv.FormatUint(uiIdentity.ProcessStarted, 10), native).Output()
			var rows []x11FixtureWindow
			if e == nil {
				e = json.Unmarshal(output, &rows)
				_ = os.WriteFile(filepath.Join(out, "boot-windows.json"), output, 0600)
			}
			var window x11FixtureWindow
			for _, w := range rows {
				if w.PID == uiIdentity.PID && w.Started == uiIdentity.ProcessStarted && w.Visible && w.Width*w.Height > window.Width*window.Height {
					window = w
				}
			}
			if e == nil && window.ID != 0 {
				capture, captureErr := exec.CommandContext(diagnosticCtx, "import", "-window", strconv.FormatUint(uint64(window.ID), 10), filepath.Join(out, "boot-ui.png")).CombinedOutput()
				if captureErr != nil {
					_ = os.WriteFile(filepath.Join(out, "boot-capture.log"), []byte(redact.Secrets(fmt.Sprint(captureErr, " ", string(capture)))), 0600)
				}
			}
			command := exec.CommandContext(diagnosticCtx, node, filepath.Join(repo, "scripts", "probe-linux-dsh-slot.mjs"), "http://127.0.0.1:"+strconv.Itoa(port), fixture.URL+"/", out, "--boot-diagnostic")
			command.Dir = repo
			output, diagnosticErr = command.CombinedOutput()
			_ = os.WriteFile(filepath.Join(out, "boot-inspector.log"), []byte(redact.Secrets(string(output))), 0600)
			save("boot-diagnostic-result", map[string]any{"error": fmt.Sprint(diagnosticErr), "contextError": fmt.Sprint(diagnosticCtx.Err())})
		}()
	}
	wait("real daemon Ready after UI mount", func() bool {
		if evidence, e := os.ReadFile(filepath.Join(out, "boot-origin-unsafe.json")); e == nil {
			t.Fatalf("opaque WebView origin is unsupported: %s", evidence)
		}
		select {
		case <-ui.Exited():
			t.Fatal("owned UI exited before mount confirmation", ui.ExitResult())
		default:
		}
		available := probeDaemon()
		if available && snapshot.Status.Phase == lifecycle.PhaseCheckpoint && !diagnosticStarted {
			startBootDiagnostic()
		}
		return available && snapshot.Status.State == lifecycle.StateReady
	})
	save("daemon-ready", map[string]any{"pid": identity.PID, "created": identity.ProcessStarted, "generation": snapshot.Status.GenerationID, "state": snapshot.Status.State})
	// A WebPage has one inspector frontend owner. The read-only checkpoint
	// diagnostic keeps its original bounded lifetime and must fully disconnect
	// before the functional driver can attach, even if Ready arrived first.
	if diagnosticJoined != nil {
		<-diagnosticJoined
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
	defer func() {
		if !t.Failed() {
			return
		}
		if nativeFD >= 0 {
			poll := []unix.PollFd{{Fd: int32(nativeFD), Events: unix.POLLIN}}
			ready, pollErr := unix.Poll(poll, 0)
			save("native-held-at-failure", map[string]any{"pidfdReady": ready, "revents": poll[0].Revents, "error": fmt.Sprint(pollErr)})
		}
		// One failure capture of the still-held UI only, before owned cleanup.
		// XRes PID + creation verification is the same as the normal capture.
		captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		output, err := exec.CommandContext(captureCtx, observer, strconv.FormatUint(uint64(uiIdentity.PID), 10), strconv.FormatUint(uiIdentity.ProcessStarted, 10), native).Output()
		var rows []x11FixtureWindow
		if err == nil {
			err = json.Unmarshal(output, &rows)
		}
		var window x11FixtureWindow
		for _, w := range rows {
			if w.PID == uiIdentity.PID && w.Started == uiIdentity.ProcessStarted && w.Visible && w.Width*w.Height > window.Width*window.Height {
				window = w
			}
		}
		if err == nil && window.ID != 0 {
			output, err = exec.CommandContext(captureCtx, "import", "-window", strconv.FormatUint(uint64(window.ID), 10), filepath.Join(out, "failure-ui.png")).CombinedOutput()
		}
		if err != nil {
			_ = os.WriteFile(filepath.Join(out, "failure-capture.log"), []byte(redact.Secrets(fmt.Sprint(err, " ", string(output)))), 0600)
		}
	}()
	var observeAgentFailure func()
	readPhase := func(name string, dst any) {
		wait(name, func() bool {
			if observeAgentFailure != nil {
				observeAgentFailure()
			}
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
	hiddenBaseline := baseline
	wait("same document alive after Hide", func() bool {
		reportMu.Lock()
		defer reportMu.Unlock()
		hiddenBaseline = report
		return report.Nonce == baseline.Nonce && report.Input == baseline.Input && report.Tick > baseline.Tick
	})
	save("document-hidden", hiddenBaseline)
	if visible.State.Document != hidden.State.Document || hidden.State.Visible {
		t.Fatal("hidden slot document changed", visible, hidden)
	}
	if e = os.WriteFile(filepath.Join(out, "resume.request"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	var restored linuxUISlot
	readPhase("restored", &restored)
	if restored.Slot != visible.Slot || restored.State.Document != visible.State.Document || restored.State.Target != visible.State.Target || restored.State.Lease != visible.State.Lease || restored.State.BrokerGeneration != visible.State.BrokerGeneration || !restored.State.Visible {
		t.Fatal("sidebar reopening replaced the retained page", visible, restored)
	}
	resumedWindows := observe()
	save("windows-restored", resumedWindows)
	found = false
	for _, w := range resumedWindows {
		if w.ID == guest.ID && w.PID == guest.PID && w.Started == guest.Started && w.Parent == guest.Parent && w.Visible {
			found = true
		}
	}
	if !found {
		t.Fatal("original native guest did not become visible again", resumedWindows)
	}
	resumedBaseline := baseline
	wait("same document alive after reopening sidebar", func() bool {
		reportMu.Lock()
		defer reportMu.Unlock()
		resumedBaseline = report
		return report.Nonce == baseline.Nonce && report.Input == baseline.Input && report.Tick > hiddenBaseline.Tick
	})
	save("document-restored", resumedBaseline)
	output, e = exec.CommandContext(ctx, "import", "-window", strconv.FormatUint(uint64(guest.Parent), 10), filepath.Join(out, "real-linux-dsh-slot-restored.png")).CombinedOutput()
	if e != nil {
		t.Fatal("capture verified restored UI", e, string(output))
	}
	if agentTest {
		failureObserved := false
		observeAgentFailure = func() {
			if failureObserved {
				return
			}
			if _, err := os.Stat(filepath.Join(out, "agent-failure-observation-request.json")); err != nil {
				return
			}
			failureObserved = true
			type heartbeat struct {
				ReceivedAt     time.Time `json:"receivedAt"`
				SameNonce      bool      `json:"sameNonce"`
				InputUnchanged bool      `json:"inputUnchanged"`
				Tick           int       `json:"tick"`
			}
			measure := func() heartbeat {
				reportMu.Lock()
				defer reportMu.Unlock()
				return heartbeat{reportAt, report.Nonce == resumedBaseline.Nonce, report.Input == resumedBaseline.Input, report.Tick}
			}
			before := measure()
			after := before
			// One bounded read-only observation of the fixture's own HTTP reports;
			// it neither evaluates the guest nor retries the failed MCP operation.
			for end := time.Now().Add(750 * time.Millisecond); time.Now().Before(end); {
				after = measure()
				if after.ReceivedAt.After(before.ReceivedAt) || ctx.Err() != nil {
					break
				}
				time.Sleep(25 * time.Millisecond)
			}
			save("agent-failure-observation", map[string]any{"before": before, "after": after, "heartbeatAdvanced": after.Tick > before.Tick && after.ReceivedAt.After(before.ReceivedAt), "sameDocumentNonce": before.SameNonce && after.SameNonce})
		}
		for _, kind := range []string{"browser", "site"} {
			var permission struct {
				Request struct {
					ID, Kind, Session, Browser, Site, Phase string
					AllowAlways                             bool
				}
			}
			readPhase("agent-permission-"+kind, &permission)
			request := permission.Request
			if request.ID == "" || request.Kind != kind || request.Phase != "waiting" || request.Browser != "embedded" || request.AllowAlways || request.Session != strings.TrimSuffix(visible.Slot, "/"+filepath.Base(visible.Slot)) || kind == "site" && request.Site != fixture.URL {
				t.Fatal("unexpected actual Agent permission", request)
			}
			wait("current native permission content rendered", func() bool {
				if failure, err := os.ReadFile(filepath.Join(out, "failure.json")); err == nil {
					t.Fatalf("first Agent driver failure before native render: %s", failure)
				}
				data, err := os.ReadFile(filepath.Join(root, "logs", "daemon.log"))
				if err != nil {
					return false
				}
				for _, line := range bytes.Split(data, []byte{'\n'}) {
					var event struct{ Msg, Request, Window string }
					if json.Unmarshal(line, &event) == nil && event.Msg == "native permission content ready" && event.Request == request.ID && event.Window == "browser-permission" {
						save("agent-permission-"+kind+"-rendered", event)
						return true
					}
				}
				return false
			})
			var popup x11FixtureWindow
			wait("actual native "+kind+" permission window", func() bool {
				output, err := exec.CommandContext(ctx, observer, strconv.FormatUint(uint64(identity.PID), 10), strconv.FormatUint(identity.ProcessStarted, 10), native).Output()
				if err != nil {
					t.Fatal("held daemon XRes observation", err)
				}
				var rows []x11FixtureWindow
				if err = json.Unmarshal(output, &rows); err != nil {
					t.Fatal(err)
				}
				found := 0
				for _, w := range rows {
					if w.PID == identity.PID && w.Started == identity.ProcessStarted && w.Visible && w.Width == 540 && w.Height == 410 {
						popup = w
						found++
					}
				}
				if found > 1 {
					t.Fatal("ambiguous own native permission windows", rows)
				}
				return found == 1
			})
			save("agent-permission-"+kind+"-window", popup)
			output, e = exec.CommandContext(ctx, "import", "-window", strconv.FormatUint(uint64(popup.ID), 10), filepath.Join(out, "agent-permission-"+kind+".png")).CombinedOutput()
			if e != nil {
				t.Fatal("capture own native permission window", e, string(output))
			}
			if e = os.WriteFile(filepath.Join(out, "agent-permission-"+kind+".captured"), nil, 0600); e != nil {
				t.Fatal(e)
			}
		}
		readPhase("agent-completed", &map[string]any{})
		reportMu.Lock()
		agentReport := report
		reportMu.Unlock()
		save("document-after-agent", agentReport)
		if agentReport.Nonce != baseline.Nonce || agentReport.Input != baseline.Input || agentReport.Tick <= resumedBaseline.Tick {
			t.Fatal("read-only Agent tool changed or lost the document", agentReport)
		}
	}
	var extraUI supervisor.Worker
	var extraNativeJoin func()
	if dualTest {
		readReport := func(nonce string) (linuxFixtureReport, map[string]linuxFixtureReport) {
			reportMu.Lock()
			defer reportMu.Unlock()
			copy := map[string]linuxFixtureReport{}
			for k, v := range reports {
				copy[k] = v
			}
			return reports[nonce], copy
		}
		extraUI, extraNativeJoin = exerciseLinuxDualLifecycle(t, ctx, repo, root, out, node, native, observer, fixture.URL+"/", env, client, snapshot.Status.GenerationID, ui, restored, guest, resumedBaseline, readReport, start)
	}
	if e = os.WriteFile(filepath.Join(out, "quit.request"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	readPhase("quit-requested", &map[string]any{})
	normalWorkers := []supervisor.Worker{ui, background}
	if extraUI != nil {
		normalWorkers = append(normalWorkers, extraUI)
	}
	for _, w := range normalWorkers {
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
	if extraNativeJoin != nil {
		extraNativeJoin()
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
