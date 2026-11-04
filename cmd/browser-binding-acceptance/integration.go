package main

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/hostplugins"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

//go:embed worker-fixture.mjs
var browserWorkerFixture []byte

type integrationConfig struct {
	Node, CLI, Dependencies, BrowserExecutable string
	Reliability                                bool
}
type readyDependencies struct{}

func listenFixture() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
func (readyDependencies) Prepare(context.Context, automation.Capability, func(automation.Progress)) error {
	return nil
}

type browserWorker struct {
	client      *http.Client
	token       string
	bridge      *automation.Bridge
	carrier     *workeripc.Transport
	worker      supervisor.Worker
	mu          sync.Mutex
	resources   map[string]*observedResource
	reliability *reliabilityRun
}

func startBrowserWorker(ctx context.Context, root string, s *automation.Service, record func(string, map[string]any)) (*browserWorker, error) {
	var config integrationConfig
	data, err := os.ReadFile(filepath.Join(root, "integration-config.json"))
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return nil, err
	}
	for _, p := range []string{config.Node, config.CLI, config.Dependencies} {
		if !filepath.IsAbs(p) {
			return nil, errors.New("absolute fixture paths required")
		}
	}
	worker := &browserWorker{bridge: automation.NewBridge(), resources: map[string]*observedResource{}}
	worker.bridge.Attach(s)
	factory := browserruntime.NewFactory(browserruntime.Config{Service: s, Supervisor: platform.New().Supervisor, Root: filepath.Join(root, "resources"), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: config.Node, Dependencies: config.Dependencies}, nil
	}})
	s.ConfigureBrowserBackend(func(ctx context.Context, r automation.Request) (automation.Resource, error) {
		record("browser-resource-create", map[string]any{"session": r.Session, "lifetime": r.Lifetime, "generation": r.Generation, "epoch": r.Epoch, "workingDirectory": r.WorkingDirectory})
		resource, err := factory(ctx, r)
		if err != nil {
			return nil, err
		}
		observed := &observedResource{Resource: resource, request: r}
		worker.mu.Lock()
		worker.resources[r.Session] = observed
		worker.mu.Unlock()
		return observed, nil
	}, browserruntime.ToolSchemas())
	s.OnChange(func(v automation.Snapshot) { record("state", map[string]any{"snapshot": v}); worker.bridge.Publish() })
	good := false
	defer func() {
		if !good {
			worker.Close()
		}
	}()
	generation := "browser-integration-" + rand.Text()
	launch, err := worker.bridge.Begin(generation)
	if err != nil {
		return nil, err
	}
	worker.token = launch["token"]
	overlay := hostplugins.NewOverlay(filepath.Join(root, "plugins"), "development")
	patch, err := overlay.Prepare(generation, map[string]map[string]string{"shell": {"generation": generation}, "automation": launch})
	if err != nil {
		return nil, err
	}
	fixture := filepath.Join(root, "worker-fixture.mjs")
	if err = os.WriteFile(fixture, browserWorkerFixture, 0600); err != nil {
		return nil, err
	}
	data, err = os.ReadFile(patch)
	if err != nil {
		return nil, err
	}
	var patches []any
	if err = json.Unmarshal(data, &patches); err != nil {
		return nil, err
	}
	u := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(fixture)}).String()
	patches = append(patches, map[string]any{"insert": []any{map[string]any{"id": "browser-acceptance", "name": u, "config": map[string]string{"token": worker.token, "cli": config.CLI, "cwd": root}}}})
	data, _ = json.Marshal(patches)
	if err = os.WriteFile(patch, data, 0600); err != nil {
		return nil, err
	}
	worker.carrier, err = workeripc.New()
	if err != nil {
		return nil, err
	}
	patch, err = worker.carrier.Prepare(filepath.Join(root, "carrier"), config.CLI, patch)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		key := strings.ToUpper(k)
		if ok && !strings.Contains(key, "TOKEN") && !strings.Contains(key, "API_KEY") && !strings.Contains(key, "SECRET") && !strings.Contains(key, "PASSWORD") && key != "NODE_OPTIONS" {
			env[k] = v
		}
	}
	env["DSH_HOME"] = filepath.Join(root, "worker-runs", generation, "dsh-home")
	urls := make(chan string, 1)
	pattern := regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+/\?token=[A-Za-z0-9_-]+`)
	worker.worker, err = platform.New().Supervisor.Start(ctx, supervisor.LaunchPlan{GenerationID: generation, Executable: config.Node, Args: []string{config.CLI, "--profile", "web", "--patch", patch, "--host", "127.0.0.1", "--port", "1", "--no-open"}, Env: env, WorkingDirectory: root, ExpectedOrigin: workeripc.Origin}, func(_ supervisor.OutputStream, line string) {
		if match := pattern.FindString(line); match != "" {
			select {
			case urls <- match:
			default:
			}
		}
	})
	if err != nil {
		return nil, err
	}
	go func() {
		for range worker.worker.Events() {
		}
	}()
	var auth string
	select {
	case auth = <-urls:
	case <-worker.worker.Exited():
		return nil, errors.New("DSH fixture exited before readiness")
	case <-time.After(30 * time.Second):
		return nil, errors.New("DSH fixture readiness timeout")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.Worker(generation, true, true)
	worker.bridge.Start(ctx, auth, worker.carrier.HTTP)
	jar, _ := cookiejar.New(nil)
	worker.client = &http.Client{Transport: worker.carrier.HTTP, Jar: jar, Timeout: 125 * time.Second}
	response, err := worker.client.Get(auth)
	if err != nil {
		return nil, err
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		return nil, errors.New("DSH fixture authentication failed")
	}
	if _, err = s.SetEnabled(ctx, automation.Browser, true); err != nil {
		return nil, err
	}
	record("real-worker-started", map[string]any{"pid": worker.worker.Diagnostics().PID, "modelCalls": 0, "productExecution": false})
	good = true
	return worker, nil
}
func (w *browserWorker) Close() {
	if w.reliability != nil && w.reliability.server != nil {
		_ = w.reliability.server.Close()
	}
	if w.bridge != nil {
		w.bridge.Stop()
	}
	if w.worker != nil {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		w.worker.ForceStop(ctx)
		w.worker.WaitEmpty(ctx)
		w.worker.Close()
	}
	if w.carrier != nil {
		w.carrier.Close()
	}
}
func (w *browserWorker) call(ctx context.Context, value map[string]any) (json.RawMessage, error) {
	data, _ := json.Marshal(value)
	req, err := http.NewRequestWithContext(ctx, "POST", workeripc.Origin+"/__dshwork/browser-acceptance", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-DSH-Work-Token", w.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("fixture HTTP %d", resp.StatusCode)
	}
	var parsed any
	if json.Unmarshal(b, &parsed) != nil {
		return b, errors.New("invalid fixture JSON")
	}
	if v, ok := parsed.(map[string]any); ok && (v["isError"] == true || v["error"] != nil) {
		return b, fmt.Errorf("DSH tool failure: %s", b)
	}
	return b, nil
}
func (w *browserWorker) exercise(ctx context.Context, evidence string) error {
	// Local-only pages. No model is run and no real account/browser data is read.
	server := &http.Server{Handler: http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(rw, `<!doctype html><title>Fixture %s</title><h1>Fixture %s</h1><label>Name<input id="name" aria-label="Name"></label><button onclick="document.querySelector('#result').textContent='Applied: '+document.querySelector('#name').value">Apply</button><p id="result" role="status">Ready</p><label>Attachment<input type="file" aria-label="Attachment" onchange="document.querySelector('#file').textContent=this.files[0]?.name"></label><p id="file" role="status">No file</p>`, r.URL.Path, r.URL.Path)
	})}
	l, err := listenFixture()
	if err != nil {
		return err
	}
	go server.Serve(l)
	defer server.Close()
	base := "http://" + l.Addr().String()
	sequence := 0
	var seqMu sync.Mutex
	call := func(session, tool string, args map[string]any) (string, error) {
		seqMu.Lock()
		sequence++
		n := sequence
		seqMu.Unlock()
		raw, err := w.call(ctx, map[string]any{"action": "call", "id": fmt.Sprintf("call-%d", n), "session": session, "tool": "my_browser_" + tool, "arguments": args})
		os.WriteFile(filepath.Join(evidence, fmt.Sprintf("action-%02d-%s-%s.json", n, session, tool)), raw, 0600)
		return string(raw), err
	}
	for _, id := range []string{"conversation-A", "conversation-B", "child-A"} {
		v := map[string]any{"action": "create", "session": id}
		if id == "child-A" {
			v["parent"] = "conversation-A"
		}
		if _, err = w.call(ctx, v); err != nil {
			return err
		}
	}
	// Both first calls in B use real exec.agent and the same shared Go creation.
	if _, err = call("conversation-A", "navigate", map[string]any{"url": base + "/A"}); err != nil {
		return err
	}
	errorsCh := make(chan error, 2)
	for _, tool := range []string{"navigate", "snapshot"} {
		go func(tool string) {
			args := map[string]any{}
			if tool == "navigate" {
				args["url"] = base + "/B"
			}
			_, e := call("conversation-B", tool, args)
			errorsCh <- e
		}(tool)
	}
	for range 2 {
		if err = <-errorsCh; err != nil {
			return err
		}
	}
	child, err := call("child-A", "snapshot", map[string]any{})
	if err != nil {
		return err
	}
	if !strings.Contains(child, base+"/A") {
		return errors.New("child did not inherit A page")
	}
	foreign, err := call("conversation-B", "snapshot", map[string]any{})
	if err != nil {
		return err
	}
	if !strings.Contains(foreign, base+"/B") {
		return errors.New("B did not retain its own page")
	}
	ref := regexp.MustCompile(`\[ref=([^\]]+)\]`).FindStringSubmatch(foreign)
	if len(ref) != 2 {
		return errors.New("missing scoped real snapshot reference")
	}
	if text, e := call("conversation-A", "click", map[string]any{"target": ref[1]}); e == nil || !strings.Contains(text, "BROWSER_FOREIGN_OR_STALE_REFERENCE") {
		return errors.New("foreign snapshot reference not refused before MCP")
	}
	// Use MCP's unique selectors, unchanged schema, and a real local upload.
	if _, err = call("conversation-A", "type", map[string]any{"target": "getByRole('textbox', { name: 'Name' })", "text": "typed-A"}); err != nil {
		return err
	}
	if _, err = call("conversation-A", "click", map[string]any{"target": "getByRole('button', { name: 'Apply', exact: true })"}); err != nil {
		return err
	}
	if _, err = call("conversation-A", "click", map[string]any{"target": "getByLabel('Attachment', { exact: true })"}); err != nil {
		return err
	}
	file := filepath.Join(filepath.Dir(evidence), "upload-fixture.txt")
	if err = os.WriteFile(file, []byte("isolated upload"), 0600); err != nil {
		return err
	}
	if _, err = call("conversation-A", "file_upload", map[string]any{"paths": []string{file}}); err != nil {
		return err
	}
	if _, err = call("conversation-A", "tabs", map[string]any{"action": "new", "url": base + "/A-second"}); err != nil {
		return err
	}
	if _, err = call("conversation-A", "type", map[string]any{"target": "getByRole('textbox', { name: 'Name' })", "text": "second-tab"}); err != nil {
		return err
	}
	second, err := call("conversation-A", "snapshot", map[string]any{})
	if err != nil {
		return err
	}
	if !strings.Contains(second, base+"/A-second") || !strings.Contains(second, "second-tab") {
		return errors.New("second-tab current page/input state missing")
	}
	if _, err = call("conversation-A", "tabs", map[string]any{"action": "select", "index": 0}); err != nil {
		return err
	}
	text, err := call("conversation-A", "snapshot", map[string]any{})
	if err != nil {
		return err
	}
	if !strings.Contains(text, "Applied: typed-A") || !strings.Contains(text, "upload-fixture.txt") || strings.Contains(text, "second-tab") {
		return errors.New("first-tab state or real upload missing")
	}
	if _, err = w.call(ctx, map[string]any{"action": "stop", "session": "conversation-A"}); err != nil {
		return err
	}
	if _, err = call("conversation-A", "snapshot", map[string]any{}); err == nil {
		return errors.New("stopped A executed again")
	}
	if _, err = call("conversation-B", "snapshot", map[string]any{}); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(evidence, "integration-summary.json"), []byte(`{"status":"passed","scope":"core real DSH/formal native/MCP chain only","modelCalls":0,"productExecution":false}`), 0600)
}
