//go:build windows

package automation_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/computerruntime"
	"github.com/local/dsh-work/internal/automation/computeruia"
	"github.com/local/dsh-work/internal/hostplugins"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/workeripc"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type installedComputerFixture struct{}

func (installedComputerFixture) Ready(c automation.Capability) bool {
	return c == automation.Computer || c == automation.AUV
}
func (installedComputerFixture) Prepare(context.Context, automation.Capability, func(automation.Progress)) error {
	return fmt.Errorf("fixture never installs components")
}

// Opt-in real rc.5 tools registry → Host app consent/DSH approval → Cua SDK →
// owned native fixture. No model request, personal window, clipboard or foreground input.
func TestLiveOwnedCuaToolPath(t *testing.T) {
	liveOwnedComputerToolPath(t, "cua-driver")
}
func TestLiveOwnedAuvToolPath(t *testing.T) {
	t.Setenv("DSH_WORK_CUA_TEST_SCENARIO", "auv-model-contract")
	liveOwnedComputerToolPath(t, "auv")
}
func liveOwnedComputerToolPath(t *testing.T, engine string) {
	root := os.Getenv("DSH_WORK_CUA_TEST_ROOT")
	helper := os.Getenv("DSH_WORK_UIA_TEST_HELPER")
	deps := os.Getenv("DSH_WORK_CUA_TEST_DEPENDENCIES")
	driver := os.Getenv("DSH_WORK_CUA_TEST_DRIVER")
	if root == "" || helper == "" || deps == "" || driver == "" {
		t.Skip("explicit owned fixture and exact installed runtime required")
	}
	root, _ = filepath.Abs(root)
	statePath := filepath.Join(root, "state.json")
	state := readComputerWitness(t, statePath)
	pid, _ := state["pid"].(float64)
	hwnd, _ := state["window_id"].(float64)
	if state["title"] != "Owned Cua acceptance fixture" {
		t.Fatal("owned fixture required")
	}
	repo, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	if _, statErr := os.Stat(filepath.Join(repo, "go.mod")); statErr != nil {
		repo = filepath.Clean(filepath.Join(repo, "../.."))
	}
	home, e := os.MkdirTemp(root, "toolpath-")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	m, e := settings.New(settings.Config{Path: filepath.Join(home, "settings.json")})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetAutomationEnabled(ctx, "computer", true); e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetComputerEngine(ctx, engine); e != nil {
		t.Fatal(e)
	}
	s := automation.New(m, installedComputerFixture{})
	defer s.Close()
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	s.ConfigureComputerGateway(automation.ComputerGatewayConfig{Inspect: automation.InspectComputerTarget, Acquire: automation.AcquireComputerDesktopLease, Probe: computeruia.NewProbe(win.NewJobObjectAdapter(), helper), Factory: computerruntime.NewFactory(computerruntime.Config{Supervisor: win.NewJobObjectAdapter(), Root: filepath.Join(home, "sidecars"), Resolve: func(context.Context, string) (computerruntime.Runtime, error) {
		return computerruntime.Runtime{Node: node, Dependencies: deps, Driver: driver}, nil
	}})})
	s.Worker("owned-cua", true, true)
	bridge := automation.NewBridge()
	bridge.Attach(s)
	s.OnChange(func(automation.Snapshot) { bridge.Publish() })
	defer bridge.Stop()
	config, e := bridge.Begin("owned-cua")
	if e != nil {
		t.Fatal(e)
	}
	cli := filepath.Join(repo, "tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js")
	overlay := hostplugins.NewOverlay(filepath.Join(home, "plugins"), "development")
	patch, e := overlay.Prepare("owned-cua", map[string]map[string]string{"shell": {"generation": "owned-cua"}, "automation": config})
	if e != nil {
		t.Fatal(e)
	}
	data, _ := os.ReadFile(patch)
	var patches []any
	_ = json.Unmarshal(data, &patches)
	fixture := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(filepath.Join(repo, "internal/automation/testdata/worker-fixture.mjs"))}).String()
	patches = append(patches, map[string]any{"insert": []any{map[string]any{"id": "computer-fixture", "name": fixture, "config": map[string]string{"token": config["token"], "cwd": home, "cli": cli, "computer": "true", "modelContract": fmt.Sprint(strings.Contains(os.Getenv("DSH_WORK_CUA_TEST_SCENARIO"), "model-contract"))}}}})
	data, _ = json.Marshal(patches)
	if e = os.WriteFile(patch, data, 0600); e != nil {
		t.Fatal(e)
	}
	carrier, e := workeripc.New()
	if e != nil {
		t.Fatal(e)
	}
	defer carrier.Close()
	patch, e = carrier.Prepare(filepath.Join(home, "carrier"), cli, patch)
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.CommandContext(ctx, node, cli, "--profile", "web", "--patch", patch, "--host", "127.0.0.1", "--port", "1", "--no-open")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DSH_HOME="+filepath.Join(home, "dsh-home"))
	output, e := cmd.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	cmd.Stderr = cmd.Stdout
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() { cancel(); <-done }()
	urls := make(chan string, 1)
	logs := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		pattern := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+/\?token=[A-Za-z0-9_-]+`)
		var lines []string
		for scanner.Scan() {
			line := scanner.Text()
			if u := pattern.FindString(line); u != "" {
				select {
				case urls <- u:
				default:
				}
			}
			if len(lines) < 80 {
				lines = append(lines, pattern.ReplaceAllString(line, "[worker]"))
			}
		}
		logs <- strings.Join(lines, "\n")
	}()
	var auth string
	select {
	case auth = <-urls:
	case <-done:
		t.Fatal(<-logs)
	case <-ctx.Done():
		t.Fatal("worker timeout")
	}
	bridge.Start(ctx, auth, carrier.HTTP)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: carrier.HTTP, Jar: jar, Timeout: 40 * time.Second}
	resp, e := client.Get(auth)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	call := func(action, extra string) (string, error) {
		req, e := http.NewRequestWithContext(ctx, "GET", workeripc.Origin+"/__dshwork/automation-fixture?action="+action+"&id=owned"+extra, nil)
		if e != nil {
			return "", e
		}
		req.Header.Set("X-DSH-Work-Token", config["token"])
		resp, e := client.Do(req)
		if e != nil {
			return "", e
		}
		defer resp.Body.Close()
		b, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		return string(b), e
	}
	mustCall := func(action, extra string) string {
		v, e := call(action, extra)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	mustCall("create", "")
	until := time.Now().Add(8 * time.Second)
	for !strings.Contains(mustCall("schemas", ""), "my_computer_observe") {
		if time.Now().After(until) {
			t.Fatal("computer tools not assembled")
		}
		time.Sleep(30 * time.Millisecond)
	}
	version := automation.DriverVersion
	if engine == "auv" {
		version = automation.AUVVersion
	}
	evidence := map[string]any{"engine": engine, "driverVersion": version, "modelCalls": 0, "target": map[string]any{"pid": pid, "window_id": hwnd}, "home": home}
	defer func() {
		b, _ := json.MarshalIndent(evidence, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "toolpath-"+computerScenario()+"-evidence.json"), b, 0600)
	}()
	tool := func(name string, args map[string]any) (string, error) {
		b, _ := json.Marshal(args)
		return call("call", "&tool=my_"+name+"&args="+url.QueryEscape(string(b)))
	}
	observed := make(chan string, 1)
	toolErr := make(chan error, 1)
	go func() {
		v, e := tool("computer_observe", map[string]any{"pid": pid, "window_id": hwnd})
		observed <- v
		toolErr <- e
	}()
	deadline := time.Now().Add(8 * time.Second)
	for {
		snapshot := s.Snapshot()
		if snapshot.ComputerAccess != nil && len(snapshot.ComputerAccess.Requests) > 0 {
			p := snapshot.ComputerAccess.Requests[0]
			if p.Target.PID != uint32(pid) || p.Target.WindowID != uint64(hwnd) {
				t.Fatal("wrong app consent")
			}
			if _, e = s.AnswerComputerConsent(ctx, p.ID, "allow"); e != nil {
				t.Fatal(e)
			}
			break
		}
		if time.Now().After(deadline) {
			select {
			case v := <-observed:
				t.Fatal("observe before consent", v)
			default:
			}
			t.Fatal("app consent missing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	first := <-observed
	if e = <-toolErr; e != nil {
		t.Fatal(e)
	}
	evidence["observe"] = json.RawMessage(first)
	if engine == "auv" {
		liveAuvModelContract(t, ctx, s, node, cli, repo, home, first, statePath, tool, mustCall, evidence)
		return
	}
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "model-contract" {
		visible := exec.CommandContext(ctx, node, filepath.Join(repo, "internal/automation/testdata/computer-model-contract.mjs"), cli, filepath.Join(home, "dsh-home"), "Scratch editor:")
		visible.Stdin = strings.NewReader(first)
		b, err := visible.CombinedOutput()
		if err != nil {
			t.Fatalf("model-visible continuation: %v: %s", err, b)
		}
		var next struct {
			Arguments map[string]any `json:"arguments"`
		}
		if err = json.Unmarshal(b, &next); err != nil {
			t.Fatal(err, string(b))
		}
		evidence["finalModelVisibleContract"] = json.RawMessage(b)
		before := readComputerWitness(t, statePath)
		next.Arguments["text"] = "CUA_MODEL_VISIBLE_CONTRACT"
		result, err := tool("computer_type", next.Arguments)
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(150 * time.Millisecond)
		after := readComputerWitness(t, statePath)
		if after["text"] != before["text"].(string)+"CUA_MODEL_VISIBLE_CONTRACT" {
			t.Fatal("visible-contract append failed", result)
		}
		evidence["nextToolResult"] = json.RawMessage(result)
		evidence["exactAppendFromModelVisibleText"] = true
		released, err := tool("computer_release", map[string]any{})
		if err != nil || s.Snapshot().ComputerAccess.Use != nil {
			t.Fatal("release failed", err, released)
		}
		evidence["release"] = json.RawMessage(released)
		t.Log("real official projected observation -> offline final model payload text -> next background append -> release passed; model calls=0")
		return
	}
	content := findComputerContent(t, first)
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "release" {
		liveComputerRelease(t, ctx, s, tool, mustCall, pid, hwnd, evidence)
		return
	}
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "scroll-stop" {
		liveComputerScrollStop(t, ctx, s, tool, mustCall, content, statePath, evidence)
		return
	}
	snapshotID, _ := content["snapshot_id"].(string)
	element := findComputerElement(t, content, "Scratch editor:")
	before := readComputerWitness(t, statePath)
	marker := "CUA_GATEWAY_MARKER"
	typed, e := tool("computer_type", map[string]any{"pid": pid, "window_id": hwnd, "snapshot_id": snapshotID, "element_index": element["element_index"], "text": marker})
	if e != nil {
		t.Fatal(e)
	}
	evidence["type"] = json.RawMessage(typed)
	time.Sleep(150 * time.Millisecond)
	after := readComputerWitness(t, statePath)
	expected := before["text"].(string) + marker
	if after["text"] != expected {
		t.Fatal("type did not append exact marker", typed)
	}
	observeAgain := func() map[string]any {
		v, e := tool("computer_observe", map[string]any{"pid": pid, "window_id": hwnd})
		if e != nil {
			t.Fatal(e)
		}
		return findComputerContent(t, v)
	}
	content = observeAgain()
	button := findComputerElement(t, content, "Append marker")
	args := map[string]any{"pid": pid, "window_id": hwnd, "snapshot_id": content["snapshot_id"], "element_index": button["element_index"]}
	denied, e := tool("computer_click", args)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(denied, "rejected") || readComputerWitness(t, statePath)["clicks"] != float64(0) {
		t.Fatal("unapproved click dispatched", denied)
	}
	evidence["unapprovedClick"] = json.RawMessage(denied)
	mustCall("approval", "&outcome=allowed-once")
	clicked, e := tool("computer_click", args)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(150 * time.Millisecond)
	if readComputerWitness(t, statePath)["clicks"] != float64(1) {
		t.Fatal("approved click missing", clicked)
	}
	evidence["approvedClick"] = json.RawMessage(clicked)
	content = observeAgain()
	password := findComputerElement(t, content, "Dummy password:")
	secret, e := tool("computer_type", map[string]any{"pid": pid, "window_id": hwnd, "snapshot_id": content["snapshot_id"], "element_index": password["element_index"], "text": "must-not-write"})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(secret, "SECRET_INPUT_BLOCKED") {
		t.Fatal("password not blocked", secret)
	}
	evidence["secretBlocked"] = json.RawMessage(secret)
	use := s.Snapshot().ComputerAccess.Use
	if use == nil {
		t.Fatal("missing lease")
	}
	if e = s.StopComputerUse(use.Session, use.Lifetime); e != nil {
		t.Fatal(e)
	}
	stopped, e := tool("computer_observe", map[string]any{"pid": pid, "window_id": hwnd})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(stopped, "REVOKED") {
		t.Fatal("stopped tool dispatched", stopped)
	}
	evidence["stop"] = json.RawMessage(stopped)
	evidence["exactAppendWitness"] = true
	evidence["clickWitness"] = true
	evidence["finalState"] = readComputerWitness(t, statePath)
	t.Log("real rc.5 tool path, Host app consent, UIA password verification, exact append, DSH rejected/allowed-once click and scoped stop passed; model calls=0")
}
func readComputerWitness(t *testing.T, path string) map[string]any {
	t.Helper()
	for range 5 {
		b, e := os.ReadFile(path)
		if e == nil {
			var v map[string]any
			if json.Unmarshal(b, &v) == nil {
				return v
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("owned fixture witness unavailable")
	return nil
}
func findComputerContent(t *testing.T, value string) map[string]any {
	t.Helper()
	var v any
	if e := json.Unmarshal([]byte(value), &v); e != nil {
		t.Fatal(e)
	}
	var find func(any) map[string]any
	find = func(v any) map[string]any {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["snapshot_id"]; ok {
				return x
			}
			for _, child := range x {
				if found := find(child); found != nil {
					return found
				}
			}
		case []any:
			for _, child := range x {
				if found := find(child); found != nil {
					return found
				}
			}
		}
		return nil
	}
	if found := find(v); found != nil {
		return found
	}
	t.Fatal("no snapshot in real tool result", value)
	return nil
}
func findComputerElement(t *testing.T, content map[string]any, label string) map[string]any {
	t.Helper()
	list, _ := content["elements"].([]any)
	var found map[string]any
	for _, v := range list {
		el, _ := v.(map[string]any)
		if el["label"] == label {
			if found != nil {
				t.Fatal("ambiguous fixture control", label)
			}
			found = el
		}
	}
	if found == nil {
		t.Fatal("missing fixture control", label)
	}
	return found
}

func computerScenario() string {
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "auv-model-contract" {
		return "auv-model-contract"
	}
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "model-contract" {
		return "model-contract"
	}
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "release" {
		return "release"
	}
	if os.Getenv("DSH_WORK_CUA_TEST_SCENARIO") == "scroll-stop" {
		return "scroll-stop"
	}
	return "core"
}
func liveComputerScrollStop(t *testing.T, ctx context.Context, s *automation.Service, tool func(string, map[string]any) (string, error), call func(string, string) string, content map[string]any, statePath string, evidence map[string]any) {
	t.Helper()
	pid := content["pid"]
	hwnd := content["window_id"]
	before := readComputerWitness(t, statePath)
	if before["foregroundHwnd"] == hwnd {
		t.Fatal("fixture must remain background")
	}
	result, e := tool("computer_scroll", map[string]any{"pid": pid, "window_id": hwnd, "snapshot_id": content["snapshot_id"], "direction": "down", "amount": float64(3), "by": "page"})
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(150 * time.Millisecond)
	after := readComputerWitness(t, statePath)
	evidence["scrollResult"] = json.RawMessage(result)
	evidence["beforeScroll"] = before
	evidence["afterScroll"] = after
	if after["firstVisibleLine"].(float64) <= before["firstVisibleLine"].(float64) {
		t.Fatal("no native scrolling witness", result)
	}
	if after["foregroundHwnd"] == hwnd {
		t.Fatal("background scroll foregrounded fixture")
	}
	observed, e := tool("computer_observe", map[string]any{"pid": pid, "window_id": hwnd})
	if e != nil {
		t.Fatal(e)
	}
	content = findComputerContent(t, observed)
	slow := findComputerElement(t, content, "Slow marker")
	call("approval", "&outcome=allowed-once")
	done := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		v, e := tool("computer_click", map[string]any{"pid": pid, "window_id": hwnd, "snapshot_id": content["snapshot_id"], "element_index": slow["element_index"]})
		done <- v
		errs <- e
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		state := readComputerWitness(t, statePath)
		if started, _ := state["slowStarted"].(bool); started {
			evidence["submittedBeforeStop"] = state
			break
		}
		if time.Now().After(deadline) {
			select {
			case v := <-done:
				t.Fatal("slow action not submitted", v)
			default:
				t.Fatal("no native submission witness")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	use := s.Snapshot().ComputerAccess.Use
	if use == nil {
		t.Fatal("missing owned use")
	}
	at := time.Now()
	if e = s.StopComputerUse(use.Session, use.Lifetime); e != nil {
		t.Fatal(e)
	}
	evidence["stopLatencyMs"] = time.Since(at).Milliseconds()
	evidence["inflightToolResult"] = json.RawMessage(<-done)
	if e = <-errs; e != nil {
		t.Fatal(e)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		state := readComputerWitness(t, statePath)
		if finished, _ := state["slowFinished"].(bool); finished {
			evidence["afterSubmittedNativeAction"] = state
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned native action did not settle")
		}
		time.Sleep(20 * time.Millisecond)
	}
	rejected, e := tool("computer_observe", map[string]any{"pid": pid, "window_id": hwnd})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(rejected, "REVOKED") {
		t.Fatal("post-stop dispatch", rejected)
	}
	evidence["afterStopTool"] = json.RawMessage(rejected)
	evidence["cancelMeaning"] = "No subsequent dispatch; the already-submitted native callback completed after stop."
	t.Log("real background scrolling independently witnessed; scoped stop ended the owned engine and blocked later calls while an already-submitted native callback completed")
}

func liveComputerRelease(t *testing.T, ctx context.Context, s *automation.Service, tool func(string, map[string]any) (string, error), call func(string, string) string, pid, hwnd float64, evidence map[string]any) {
	t.Helper()
	released, e := tool("computer_release", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	if s.Snapshot().ComputerAccess.Use != nil {
		t.Fatal("release retained desktop")
	}
	evidence["explicitRelease"] = json.RawMessage(released)
	done := make(chan string, 1)
	errs := make(chan error, 1)
	go func() {
		v, e := tool("computer_observe", map[string]any{"pid": pid, "window_id": hwnd})
		done <- v
		errs <- e
	}()
	deadline := time.Now().Add(4 * time.Second)
	for {
		v := s.Snapshot()
		if len(v.ComputerAccess.Requests) > 0 {
			p := v.ComputerAccess.Requests[0]
			if p.Target.PID != uint32(pid) || p.Target.WindowID != uint64(hwnd) {
				t.Fatal("unexpected consent target")
			}
			if _, e = s.AnswerComputerConsent(ctx, p.ID, "allow"); e != nil {
				t.Fatal(e)
			}
			break
		}
		if time.Now().After(deadline) {
			select {
			case v := <-done:
				t.Fatal("release acted like forced stop or retained grant", v)
			default:
				t.Fatal("release did not reauthorize")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	observed := <-done
	if e = <-errs; e != nil {
		t.Fatal(e)
	}
	findComputerContent(t, observed)
	evidence["reauthorizeAfterRelease"] = true
	call("child-complete", "")
	time.Sleep(80 * time.Millisecond)
	if s.Snapshot().ComputerAccess.Use == nil {
		t.Fatal("child completion released top desktop")
	}
	evidence["childDidNotReleaseTop"] = true
	call("complete", "")
	deadline = time.Now().Add(3 * time.Second)
	for {
		if s.Snapshot().ComputerAccess.Use == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("top completion did not release")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var release func() error
	for {
		release, e = automation.AcquireComputerDesktopLease()
		if e == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("OS lease not released after Job teardown", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = release(); e != nil {
		t.Fatal(e)
	}
	evidence["topCompletionReleasedJobAndMutex"] = true
	t.Log("real rc.5 explicit release reauthorized, child completion preserved top lease, top completion released the Job and Windows desktop mutex")
}
func liveAuvModelContract(t *testing.T, ctx context.Context, s *automation.Service, node, cli, repo, home, first, statePath string, tool func(string, map[string]any) (string, error), call func(string, string) string, evidence map[string]any) {
	t.Helper()
	schemas := call("schemas", "")
	if strings.Contains(schemas, "computer_type") || strings.Contains(schemas, "computer_key") || strings.Contains(schemas, "element_index") {
		t.Fatal("AUV exposed unsupported indexed/text/key tools", schemas)
	}
	evidence["schemas"] = json.RawMessage(schemas)
	visible := func(observed, point string) map[string]any {
		command := exec.CommandContext(ctx, node, filepath.Join(repo, "internal/automation/testdata/computer-model-contract.mjs"), cli, filepath.Join(home, "dsh-home"), point)
		command.Stdin = strings.NewReader(observed)
		b, e := command.CombinedOutput()
		if e != nil {
			t.Fatal("AUV visible continuation", e, string(b))
		}
		var next struct {
			Arguments map[string]any `json:"arguments"`
		}
		if e = json.Unmarshal(b, &next); e != nil {
			t.Fatal(e, string(b))
		}
		evidence["finalModelVisibleContract"] = json.RawMessage(b)
		return next.Arguments
	}
	// Point selected from the actual inspected 938x729 owned screenshot, not UIA.
	args := visible(first, "@point:120,619")
	before := readComputerWitness(t, statePath)
	denied, e := tool("computer_click", args)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(denied, "rejected") || readComputerWitness(t, statePath)["clicks"] != before["clicks"] {
		t.Fatal("AUV unapproved click dispatched", denied)
	}
	evidence["unapprovedClick"] = json.RawMessage(denied)
	call("approval", "&outcome=allowed-once")
	clicked, e := tool("computer_click", args)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(200 * time.Millisecond)
	after := readComputerWitness(t, statePath)
	if after["clicks"].(float64) != before["clicks"].(float64)+1 {
		t.Fatal("AUV approved background click missing", clicked)
	}
	evidence["approvedClick"] = json.RawMessage(clicked)
	evidence["clickWitness"] = true
	observed, e := tool("computer_observe", map[string]any{"pid": args["pid"], "window_id": args["window_id"]})
	if e != nil {
		t.Fatal(e)
	}
	scroll := visible(observed, "@point:480,300")
	scroll["direction"] = "down"
	scroll["amount"] = float64(3)
	scroll["by"] = "line"
	scrolled, e := tool("computer_scroll", scroll)
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(200 * time.Millisecond)
	final := readComputerWitness(t, statePath)
	if final["firstVisibleLine"].(float64) <= after["firstVisibleLine"].(float64) {
		t.Fatal("AUV background scroll missing", scrolled)
	}
	if final["foregroundHwnd"] == args["window_id"] {
		t.Fatal("AUV foregrounded owned fixture")
	}
	evidence["scrollResult"] = json.RawMessage(scrolled)
	evidence["beforeWitness"] = before
	evidence["afterClickWitness"] = after
	evidence["afterScrollWitness"] = final
	released, e := tool("computer_release", map[string]any{})
	if e != nil || s.Snapshot().ComputerAccess.Use != nil {
		t.Fatal("AUV release", e, released)
	}
	evidence["release"] = json.RawMessage(released)
	// Reacquire metadata-only work, then verify the same scoped Stop kills AUV.
	windows, e := tool("computer_windows", map[string]any{})
	if e != nil {
		t.Fatal(e)
	}
	evidence["metadataReacquire"] = json.RawMessage(windows)
	use := s.Snapshot().ComputerAccess.Use
	if use == nil {
		t.Fatal("AUV lease missing")
	}
	if e = s.StopComputerUse(use.Session, use.Lifetime); e != nil {
		t.Fatal(e)
	}
	stopped, e := tool("computer_observe", map[string]any{"pid": args["pid"], "window_id": args["window_id"]})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(stopped, "REVOKED") || s.Snapshot().ComputerAccess.Use != nil {
		t.Fatal("AUV Stop did not revoke/release", stopped)
	}
	evidence["stop"] = json.RawMessage(stopped)
	releaseMutex, e := automation.AcquireComputerDesktopLease()
	if e != nil {
		t.Fatal("AUV desktop mutex not released", e)
	}
	if e = releaseMutex(); e != nil {
		t.Fatal(e)
	}
	evidence["desktopMutexReleased"] = true
	t.Log("AUV0.0.43 real rc5 registry/official image projection/offline final model text -> denied/approved background coordinate click -> observed scroll -> release/reacquire/Stop passed; model calls=0")
}
