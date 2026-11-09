//go:build darwin && cgo && e67nativefixture

package macowner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/platform/automationpackages"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
)

type macPublishedMCP struct{ dependencies string }

func (p macPublishedMCP) Prepare(_ context.Context, cap automation.Capability, _ func(automation.Progress)) error {
	if cap != automation.Browser {
		return automation.ErrUnavailable
	}
	return automationpackages.Validate(p.dependencies, cap)
}

type observedMCP struct {
	adapter *MCPAdapter
	mu      sync.Mutex
	workers []*Worker
}

func (o *observedMCP) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	w, err := o.adapter.Start(ctx, plan, raw)
	if owned, ok := w.(*mcpWorker); ok {
		o.mu.Lock()
		o.workers = append(o.workers, owned.Worker)
		o.mu.Unlock()
	}
	return w, err
}

// Formal public Service/permit/MCP engine path. No Wails parent or managed view
// is invented: InstancePresenterRegistry and real UI presentation remain separate.
func TestLiveMacEngineServiceMCP(t *testing.T) {
	manifest, deps, evidence := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES"), os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if manifest == "" || deps == "" || evidence == "" {
		t.Fatal("explicit formal publication, prepared locked MCP dependencies and evidence required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pub, err := OpenPublication(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	root := os.Getenv("DSH_WORK_MAC_DEATH_ROOT")
	deathChild := root != "" && os.Getenv("DSH_WORK_MAC_DEATH_CONTROL") != ""
	if !deathChild {
		root, err = os.MkdirTemp("/tmp", "dsh-mac-service-")
		if err != nil {
			t.Fatal(err)
		}
	} else if err = privateDir(root); err != nil {
		t.Fatal(err)
	}
	evidenceFile := func(name string) string {
		if deathChild {
			name = "death-active-" + name
		}
		return filepath.Join(evidence, name)
	}
	var deathControl net.Conn
	if deathChild {
		deathControl = connectServiceDeathController(t, ctx)
		defer deathControl.Close()
	}
	complete := false
	t.Cleanup(func() {
		if complete {
			if err := os.RemoveAll(root); err != nil {
				t.Error(err)
			}
		} else {
			t.Log("private failed Service recovery evidence retained at", root)
		}
	})
	ready := make(chan string, 1)
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			select {
			case ready <- r.URL.Query().Get("nonce"):
			default:
			}
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><title>Formal Mac engine Service</title><style>body{background:#dff8ed;font:24px system-ui;padding:36px}</style><h1>Formal Mac engine Service</h1><label>Unsubmitted input<input aria-label="Unsubmitted input" value="mac-original"></label><output id="nonce"></output><script>window.fixtureNonce=crypto.randomUUID();window.fixtureDirty='mac-private-dirty';document.querySelector('input').value=window.fixtureDirty;document.querySelector('output').textContent=window.fixtureNonce;fetch('/ready?nonce='+window.fixtureNonce)</script>`))
	}))
	defer page.Close()
	native, err := New(pub, filepath.Join(root, "profile"))
	if err != nil {
		t.Fatal(err)
	}
	broker, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: pub.Executable, Profile: native.profile, Supervisor: native, ObserveCommand: func(o embeddedbrowser.ProtocolObservation) {
		t.Log("browser-cdp", o.Stage, o.Route, o.ID, o.Method, o.At.UTC())
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	t.Cleanup(func() {
		if native.worker != nil {
			_ = writeTerminalEvidence(t, evidenceFile("service-cef-terminal.json"), native.worker)
		}
	})
	target, err := broker.OpenOwnedWebTarget(ctx, page.URL)
	if err != nil {
		t.Fatal(err)
	}
	var nonce string
	select {
	case nonce = <-ready:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if nonce == "" {
		t.Fatal("actual document nonce missing")
	}
	before, err := target.OwnedIdentity(ctx)
	if err != nil || !before.FrameScopeReady || before.ViewLease != "" {
		t.Fatal("non-presenter engine identity", before, err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(root, "runtime")
	mcp, err := NewMCPAdapter(pub, node, deps, runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	observed := &observedMCP{adapter: mcp}
	defer func() {
		observed.mu.Lock()
		workers := append([]*Worker{}, observed.workers...)
		observed.mu.Unlock()
		for i, w := range workers {
			if err := w.Close(); err != nil {
				t.Error("MCP owner retirement", err)
			}
			_ = writeTerminalEvidence(t, evidenceFile("service-mcp-"+string(rune('1'+i))+"-terminal.json"), w)
		}
	}()
	manager, err := settings.New(settings.Config{Path: filepath.Join(root, "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	service := automation.New(manager, macPublishedMCP{deps})
	defer service.Close()
	prompts := make(chan automation.BrowserConsent, 8)
	service.EnableBrowserGateway(automation.BrowserGatewayConfig{Notify: func(p automation.BrowserConsent) { prompts <- p }})
	images := make(chan []byte, 2)
	service.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{Service: service, Supervisor: observed, Root: runtimeRoot, OutputRoot: filepath.Join(root, "output"), ObserveStage: func(stage string) { t.Log("browser-runtime", stage) }, Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps, Environment: map[string]string{"DSH_WORK_BROWSER_DIAGNOSTICS": "1"}}, nil
	}, Observe: func(_ automation.Request, o browserruntime.Observation) {
		if o.Tool != "browser_take_screenshot" {
			return
		}
		var result struct{ Content []struct{ Type, Data string } }
		if json.Unmarshal(o.RawResult, &result) == nil {
			for _, part := range result.Content {
				if part.Type == "image" {
					if data, e := base64.StdEncoding.DecodeString(part.Data); e == nil {
						select {
						case images <- data:
						default:
						}
					}
				}
			}
		}
	}}), browserruntime.ToolSchemas())
	service.Worker("mac-engine-service", true, true)
	service.Gateway("mac-engine-service", true)
	if _, err = service.SetEnabled(ctx, automation.Browser, true); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetDefaultBrowser(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Retry(automation.Browser); err != nil {
		t.Fatal(err)
	}
	for service.Snapshot().Browser.Dependency != "prepared" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	request := automation.Request{Generation: "mac-engine-service", Epoch: service.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: "mac-service-fixture", Lifetime: "one", Turn: "one", Tool: "browser_snapshot", Arguments: map[string]any{}}
	if _, err = service.BrowserTurn(request); err != nil {
		t.Fatal(err)
	}
	if err = service.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: request.Generation, Session: request.Session, Lifetime: request.Lifetime}, target); err != nil {
		t.Fatal(err)
	}
	type authorized struct {
		decision automation.BrowserDecision
		err      error
	}
	decisions := make(chan authorized, 1)
	go func() { d, e := service.AuthorizeBrowser(ctx, request); decisions <- authorized{d, e} }()
	consentKinds := []string{}
consentLoop:
	for {
		select {
		case prompt := <-prompts:
			if prompt.Session != request.Session || (prompt.Kind == "site" && prompt.Site != page.URL) {
				t.Fatal("isolated consent escaped controlled fixture")
			}
			consentKinds = append(consentKinds, prompt.Kind)
			if _, err = service.AnswerBrowserConsent(ctx, prompt.ID, "allow"); err != nil {
				t.Fatal(err)
			}
		case result := <-decisions:
			if result.err != nil {
				t.Fatal(result.err)
			}
			request.Permit = result.decision.Permit
			request.Epoch = result.decision.Epoch
			break consentLoop
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	value, err := service.Execute(ctx, request)
	data, _ := json.Marshal(value)
	if err != nil || strings.Contains(string(data), `"isError":true`) || !strings.Contains(string(data), nonce) || !strings.Contains(string(data), "mac-private-dirty") {
		t.Fatal("formal MCP same-document snapshot failed", err, string(data))
	}
	request.Tool = "browser_take_screenshot"
	request.Arguments = map[string]any{"type": "png", "scale": "css"}
	request.Permit = ""
	decision, err := service.AuthorizeBrowser(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Permit, request.Epoch = decision.Permit, decision.Epoch
	if _, err = service.Execute(ctx, request); err != nil {
		t.Fatal(err)
	}
	select {
	case data := <-images:
		image, e := png.Decode(bytes.NewReader(data))
		if e != nil || image.Bounds().Dx() < 100 || image.Bounds().Dy() < 100 {
			t.Fatal("MCP actual screenshot", e)
		}
		if err = os.WriteFile(evidenceFile("formal-mac-engine-service.png"), data, 0600); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	after, err := target.OwnedIdentity(ctx)
	if err != nil || after.BrokerGeneration != before.BrokerGeneration || after.Target != before.Target || after.Frame != before.Frame || after.Document != before.Document {
		t.Fatal("Service moved document scope", err)
	}
	proof, _ := json.MarshalIndent(map[string]any{"qualification": "formal-engine-service-mcp-without-managed-ui", "documentNonce": nonce, "dirty": "mac-private-dirty", "before": before, "after": after, "consentKinds": consentKinds, "mcpVersion": "0.0.83", "sdkVersion": "1.32.2", "wsVersion": "8.22.1"}, "", "  ")
	if err = os.WriteFile(evidenceFile("formal-mac-engine-service.json"), proof, 0600); err != nil {
		t.Fatal(err)
	}
	if deathChild {
		holdServiceHostForDeath(t, ctx, deathControl, native, observed, after, nonce)
		t.Fatal("death-cut child unexpectedly resumed")
	}
	if err = service.StopBrowserUse(request.Session, request.Lifetime); err != nil {
		t.Fatal("formal Service stop", err)
	}
	service.Close()
	observed.mu.Lock()
	workers := append([]*Worker{}, observed.workers...)
	observed.mu.Unlock()
	if len(workers) != 1 {
		t.Fatal("unexpected MCP resources", len(workers))
	}
	for _, worker := range workers {
		if err = worker.WaitEmpty(ctx); err != nil {
			t.Fatal("actual MCP tree empty", err)
		}
		if err = worker.Close(); err != nil {
			t.Fatal(err)
		}
		worker.mu.Lock()
		state := worker.state
		worker.mu.Unlock()
		roles := map[string]int{}
		for _, owned := range state.Owned {
			roles[owned.Role]++
		}
		if state.Phase != "empty" || roles["mcp-adapter"] != 1 || roles["mcp-stdio-child"] != 1 {
			t.Fatal("actual two-Node inventory incomplete", roles)
		}
	}
	if err = target.CloseNative(ctx); err != nil && !errors.Is(err, embeddedbrowser.ErrClosed) {
		t.Fatal(err)
	}
	if err = broker.Close(); err != nil || broker.CleanupPending() {
		t.Fatal("formal CEF normal exit", err)
	}
	if native.worker.ExitResult().Code != 0 {
		t.Fatal("CEF normal exit code", native.worker.ExitResult())
	}
	if err = native.worker.WaitEmpty(ctx); err != nil {
		t.Fatal(err)
	}
	complete = true
}
