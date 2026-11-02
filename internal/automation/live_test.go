//go:build windows

package automation

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/hostplugins"
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
	"sync/atomic"
	"testing"
	"time"
)

// This fixture drives real rc.5 Agents and tools over the production authenticated
// pipe. It has no browser, desktop input, native registration or model calls.
func TestLiveDSHAutomationHostRoute(t *testing.T)  { liveDSHAutomationHost(t, false) }
func TestLiveDSHBrowserAuthorization(t *testing.T) { liveDSHAutomationHost(t, true) }
func liveDSHAutomationHost(t *testing.T, product bool) {
	if os.Getenv("DSH_WORK_LIVE_TEST") != "1" {
		t.Skip("set DSH_WORK_LIVE_TEST=1 for installed rc.5 integration")
	}
	root, _ := filepath.Abs("../..")
	cli := filepath.Join(root, "tools/dsh/node_modules/@deepseek-ai/dsh/lib/bin.js")
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s := New(managerForTest(t), prepareFunc(func(context.Context, Capability, func(Progress)) error { return nil }))
	defer s.Close()
	resource := &gatewayResource{element: BrowserElement{Tag: "input", Type: "text", Name: "Search"}}
	if product {
		s.EnableBrowserGateway(BrowserGatewayConfig{})
		var schemas []json.RawMessage
		for _, name := range []string{"browser_navigate", "browser_click", "browser_type"} {
			schema, _ := json.Marshal(map[string]any{"name": name, "description": "Pinned browser gate fixture", "inputSchema": map[string]any{"type": "object", "additionalProperties": true}})
			schemas = append(schemas, schema)
		}
		s.ConfigureBrowserBackend(func(context.Context, Request) (Resource, error) { return resource, nil }, schemas)
	}
	s.Worker("live-automation", true, true)
	bridge := NewBridge()
	bridge.Attach(s)
	s.OnChange(func(Snapshot) { bridge.Publish() })
	defer bridge.Stop()
	config, err := bridge.Begin("live-automation")
	if err != nil {
		t.Fatal(err)
	}
	config["validation"] = "true"
	if product {
		config["validation"] = "false"
	}
	overlay := hostplugins.NewOverlay(filepath.Join(home, "plugins"), "development")
	patch, err := overlay.Prepare("live-automation", map[string]map[string]string{"shell": {"generation": "live-automation"}, "automation": config})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(patch)
	var patches []any
	json.Unmarshal(data, &patches)
	fixture := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(filepath.Join(root, "internal/automation/testdata/worker-fixture.mjs"))}).String()
	patches = append(patches, map[string]any{"insert": []any{map[string]any{"id": "automation-fixture", "name": fixture, "config": map[string]string{"token": config["token"], "cwd": home, "cli": cli, "product": fmt.Sprint(product)}}}})
	data, _ = json.Marshal(patches)
	os.WriteFile(patch, data, 0600)
	carrier, err := workeripc.New()
	if err != nil {
		t.Fatal(err)
	}
	defer carrier.Close()
	patch, err = carrier.Prepare(filepath.Join(home, "carrier"), cli, patch)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, "node", cli, "--profile", "web", "--patch", patch, "--host", "127.0.0.1", "--port", "1", "--no-open")
	cmd.Dir = home
	cmd.Env = append(os.Environ(), "DSH_HOME="+filepath.Join(home, "dsh-home"))
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = cmd.Stdout
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
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
			if len(lines) < 60 {
				lines = append(lines, pattern.ReplaceAllString(line, "[Worker URL]"))
			}
		}
		logs <- strings.Join(lines, "\n")
	}()
	var auth string
	select {
	case auth = <-urls:
	case <-done:
		t.Fatalf("Worker exited: %s", <-logs)
	case <-ctx.Done():
		t.Fatal("Worker startup timed out")
	}
	bridge.Start(ctx, auth, carrier.HTTP)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: carrier.HTTP, Jar: jar, Timeout: 10 * time.Second}
	resp, err := client.Get(auth)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	call := func(action, id, extra string) string {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, "GET", workeripc.Origin+"/__dshwork/automation-fixture?action="+action+"&id="+id+extra, nil)
		r.Header.Set("X-DSH-Work-Token", config["token"])
		resp, e := client.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if resp.StatusCode != 200 {
			t.Fatalf("fixture HTTP %d: %s", resp.StatusCode, data)
		}
		return string(data)
	}

	if product {
		liveProductBrowserScenario(t, ctx, s, call, resource)
		return
	}
	call("create", "a", "")
	call("create", "b", "")
	call("create", "child", "&parent=a")
	if got := call("schemas", "a", ""); got != "[]" {
		t.Fatal("disabled capability registered tools", got)
	}
	var creates atomic.Int32
	s.SetValidationFactory(func(_ context.Context, r Request) (Resource, error) {
		creates.Add(1)
		return &routedResource{session: r.Session}, nil
	})
	s.SetEnabled(ctx, Browser, true)
	awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	until := time.Now().Add(8 * time.Second)
	for !strings.Contains(call("schemas", "a", ""), "my_automation_probe") {
		if time.Now().After(until) {
			t.Fatal("dynamic schema not registered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := call("call", "a", ""); !strings.Contains(got, "host-routed:a") {
		t.Fatal("Go route failed", got)
	}
	if got := call("call", "child", ""); !strings.Contains(got, "host-routed:a") {
		t.Fatal("child route did not inherit", got)
	}
	if got := call("call", "b", ""); !strings.Contains(got, "host-routed:b") {
		t.Fatal("b route failed", got)
	}
	if creates.Load() != 2 {
		t.Fatal("top-level ownership did not merge child", creates.Load())
	}
	if got := call("bad", "a", ""); !strings.Contains(got, "AUTOMATION_INVALID_ARGUMENT") {
		t.Fatal("routing argument accepted", got)
	}
	call("dispose", "child", "")
	if got := call("call", "a", ""); !strings.Contains(got, "host-routed:a") {
		t.Fatal("child disposal released top", got)
	}
	call("stop", "a", "")
	if got := call("call", "a", ""); !strings.Contains(got, "AUTOMATION_REVOKED") {
		t.Fatal("stopped top session executed", got)
	}
	if got := call("call", "b", ""); !strings.Contains(got, "host-routed:b") {
		t.Fatal("stopping a affected b", got)
	}
	s.SetEnabled(ctx, Browser, false)
	until = time.Now().Add(5 * time.Second)
	for call("schemas", "a", "") != "[]" {
		if time.Now().After(until) {
			t.Fatal("tools not unregistered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := call("old", "a", ""); strings.Contains(got, "host-routed") {
		t.Fatal("old schema executed", got)
	}
	s.SetEnabled(ctx, Browser, true)
	until = time.Now().Add(5 * time.Second)
	for !strings.Contains(call("schemas", "a", ""), "my_automation_probe") {
		if time.Now().After(until) {
			t.Fatal("reenable schema missing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A prepared dependency without an execution backend still refuses at Go.
	s.SetValidationFactory(nil)
	if got := call("call", "b", ""); !strings.Contains(got, "AUTOMATION_BACKEND_UNAVAILABLE") {
		t.Fatal("missing backend executed", got)
	}
	// Broken host channel rejects even a still-known tool. It cannot recreate state.
	bridge.Stop()
	if got := call("call", "a", ""); strings.Contains(got, "host-routed") {
		t.Fatal("host disconnect executed", got)
	}
	// Reconnect the same validation Worker, then kill only its PID. The pipe EOF
	// must revoke Go resources without a UI event or a graceful plugin teardown.
	s.Worker("live-automation", true, true)
	s.SetValidationFactory(func(_ context.Context, r Request) (Resource, error) { return &routedResource{session: r.Session}, nil })
	bridge.Start(ctx, auth, carrier.HTTP)
	until = time.Now().Add(5 * time.Second)
	for !strings.Contains(call("schemas", "a", ""), "my_automation_probe") {
		if time.Now().After(until) {
			t.Fatal("reconnected schema missing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := call("call", "a", ""); !strings.Contains(got, "host-routed:a") {
		t.Fatal("new connection did not start a valid lifecycle", got)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	awaitState(t, s, func(v Snapshot) bool { return !v.GatewayConnected })
	t.Log("real rc.5: dynamic assembled schemas, top/child Go routing, argument rejection, disable/reenable, missing backend, host disconnect and Worker hard exit passed; model calls=0")
}

type routedResource struct{ session string }

func (r *routedResource) Execute(context.Context, string, map[string]any) (any, error) {
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": "host-routed:" + r.session}}}, nil
}
func (r *routedResource) Close() error { return nil }

func liveProductBrowserScenario(t *testing.T, ctx context.Context, s *Service, call func(string, string, string) string, resource *gatewayResource) {
	t.Helper()
	call("create", "a", "")
	connection := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	binding := mustBind(t, s, connection, "")
	if err := s.SelectBrowserBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled(ctx, Browser, true); err != nil {
		t.Fatal(err)
	}
	awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	until := time.Now().Add(5 * time.Second)
	for !strings.Contains(call("schemas", "a", ""), "my_browser_navigate") {
		if time.Now().After(until) {
			t.Fatal("browser schema missing")
		}
		time.Sleep(20 * time.Millisecond)
	}
	args := func(tool string, value map[string]any) string {
		data, _ := json.Marshal(value)
		return "&tool=" + tool + "&args=" + url.QueryEscape(string(data))
	}
	done := make(chan string, 1)
	go func() {
		done <- call("call", "a", args("my_browser_navigate", map[string]any{"url": "http://localhost/fixture"}))
	}()
	var snapshot Snapshot
	deadline := time.After(6 * time.Second)
waiting:
	for {
		snapshot = s.Snapshot()
		if len(snapshot.BrowserAccess.Requests) > 0 {
			break waiting
		}
		select {
		case result := <-done:
			t.Fatal("browser call returned before consent", result)
		case <-deadline:
			t.Fatal("no browser access request")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if resource.calls.Load() != 0 {
		t.Fatal("browser body ran before user consent")
	}
	if _, err := s.AnswerBrowserConsent(ctx, snapshot.BrowserAccess.Requests[0].ID, "allow"); err != nil {
		t.Fatal(err)
	}
	if got := <-done; !strings.Contains(got, "ok") || resource.calls.Load() != 1 {
		t.Fatal("authorized real DSH path failed", got)
	}
	resource.element = BrowserElement{Tag: "button", Name: "Buy now"}
	if got := call("call", "a", args("my_browser_click", map[string]any{"target": "#payment"})); !strings.Contains(got, "rejected") || resource.calls.Load() != 1 {
		t.Fatal("DSH confirmation did not return the expected rejection, or dispatched an action", got)
	}
	call("approval", "a", "&outcome=allowed-once")
	if got := call("call", "a", args("my_browser_click", map[string]any{"target": "#payment"})); !strings.Contains(got, "ok") || resource.calls.Load() != 2 {
		t.Fatal("DSH approval did not consume matching permit", got)
	}
	resource.element = BrowserElement{Tag: "input", Type: "password", Name: "Password"}
	if got := call("call", "a", args("my_browser_type", map[string]any{"target": "#password", "text": "fixture-only"})); !strings.Contains(got, "SECRET_INPUT_BLOCKED") || resource.calls.Load() != 2 {
		t.Fatal("secret input was not blocked", got)
	}
	snapshot = s.Snapshot()
	if len(snapshot.BrowserAccess.Sessions) != 1 {
		t.Fatal("active browser context missing")
	}
	use := snapshot.BrowserAccess.Sessions[0]
	if err := s.StopBrowserUse(use.Session, use.Lifetime); err != nil {
		t.Fatal(err)
	}
	if got := call("call", "a", args("my_browser_navigate", map[string]any{"url": "http://localhost/fixture"})); !strings.Contains(got, "BROWSER_STOPPED") || resource.calls.Load() != 2 {
		t.Fatal("stopped context revived", got)
	}
	t.Log("real rc.5 ToolExecution -> authenticated Go access consent -> DSH ask reject/allow -> one-use Go permit -> owned backend; password and stop refusal passed; model calls=0; browser backend=fake")
}
