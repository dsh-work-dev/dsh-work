//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/local/dsh-work/internal/supervisor"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
)

type installedBrowser struct{ deps string }

func (p installedBrowser) Prepare(context.Context, automation.Capability, func(automation.Progress)) error {
	_, err := os.Stat(filepath.Join(p.deps, "node_modules/@playwright/mcp/package.json"))
	return err
}

type recordedSupervisor struct {
	supervisor.Adapter
	t        *testing.T
	mu       sync.Mutex
	previous *recordedWorker
}
type recordedWorker struct {
	supervisor.Worker
	joined bool
	t      *testing.T
	mu     sync.Mutex
}

func (a *recordedSupervisor) Start(ctx context.Context, p supervisor.LaunchPlan, h supervisor.RawOutputHandler) (supervisor.Worker, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if strings.HasPrefix(strings.ToLower(filepath.Base(p.Executable)), "node") && a.previous != nil {
		a.previous.mu.Lock()
		joined := a.previous.joined
		a.previous.mu.Unlock()
		if !joined {
			a.t.Error("replacement executor started before old process-tree join")
		}
	}
	w, err := a.Adapter.Start(ctx, p, h)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(strings.ToLower(filepath.Base(p.Executable)), "node") {
		return w, nil
	}
	result := &recordedWorker{Worker: w, t: a.t}
	a.previous = result
	a.t.Log("started real observer/MCP executor", w.Diagnostics().PID)
	return result, nil
}
func (w *recordedWorker) WaitEmpty(ctx context.Context) error {
	err := w.Worker.WaitEmpty(ctx)
	w.mu.Lock()
	w.joined = err == nil
	w.mu.Unlock()
	w.t.Log("real executor process-tree join", w.Worker.Diagnostics().PID, err)
	return err
}
func TestLiveServiceOwnedAdmissionAndFinalDispatch(t *testing.T) {
	exe, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if exe == "" || deps == "" {
		t.Skip("requires actual CEF and locked MCP dependencies")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	marked := make(chan string, 8)
	var portBRequests atomic.Int32
	var crossOrigin string
	html := `<!doctype html><label>Message<input aria-label="Message"></label><button type=button onclick="fetch('/marked?value='+encodeURIComponent(document.querySelector('input').value));document.querySelector('output').textContent='Marked'">Mark</button><button type=button onclick="location.href='/?document=next'">Next</button><button type=button onclick="location.href='CROSS_ORIGIN'">Cross</button><output>Initial</output>`
	handler := func(b bool) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/marked" {
				marked <- r.URL.Query().Get("value")
				w.WriteHeader(204)
				return
			}
			if b && r.URL.Path == "/" {
				portBRequests.Add(1)
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(strings.ReplaceAll(html, "CROSS_ORIGIN", crossOrigin)))
		})
	}
	aServer, bServer := httptest.NewServer(handler(false)), httptest.NewServer(handler(true))
	cServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.ReplaceAll(html, "CROSS_ORIGIN", crossOrigin) + "<p>Unapproved secret</p>"))
	}))
	crossOrigin = cServer.URL
	defer cServer.Close()
	defer aServer.Close()
	defer bServer.Close()
	adapter := &recordedSupervisor{Adapter: win.NewJobObjectAdapter(), t: t}
	broker, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: adapter})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	manager, err := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	policy := settings.DefaultBrowserPolicy()
	policy.AskEachConversation = false
	policy.NewSites = "allow"
	policy.Sites = []settings.BrowserSiteRule{{Host: "127.0.0.1", Rule: "allow"}}
	if _, err = manager.SetBrowserPolicy(ctx, policy); err != nil {
		t.Fatal(err)
	}
	s := automation.New(manager, installedBrowser{deps: deps})
	defer s.Close()
	prompts := make(chan automation.BrowserConsent, 16)
	s.EnableBrowserGateway(automation.BrowserGatewayConfig{ExtensionSource: "external-source-must-not-be-faked", Notify: func(p automation.BrowserConsent) { prompts <- p }})
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{Service: s, Supervisor: adapter, Root: t.TempDir(), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}}), browserruntime.ToolSchemas())
	s.Worker("owned-worker", true, true)
	s.Gateway("owned-worker", true)
	if _, err = s.SetEnabled(ctx, automation.Browser, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetDefaultBrowser(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Retry(automation.Browser); err != nil {
		t.Fatal(err)
	}
	for s.Snapshot().Browser.Dependency != "prepared" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	r := automation.Request{Generation: "owned-worker", Epoch: s.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: "real-service", Lifetime: "life", Turn: "turn-1", Title: "Owned fixture"}
	if _, err = s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	}
	target, err := broker.OpenOwnedWebTarget(ctx, aServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: r.Generation, Session: r.Session, Lifetime: "forged-life"}, target); !errors.Is(err, automation.ErrRevoked) {
		t.Fatal("forged lifetime admitted", err)
	}
	if err = s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: r.Generation, Session: r.Session, Lifetime: r.Lifetime}, target); err != nil {
		t.Fatal(err)
	}
	second := r
	second.Session = "another-session"
	if _, e := s.BrowserTurn(second); e != nil {
		t.Fatal(e)
	}
	if e := s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: second.Generation, Session: second.Session, Lifetime: second.Lifetime}, target); e == nil || !strings.Contains(e.Error(), "ALREADY_CONTROLLED") {
		t.Fatal("same target admitted to another live session", e)
	}
	if s.Snapshot().Profiles.Selected != "" || len(s.Snapshot().Profiles.Bindings) != 0 {
		t.Fatal("owned provider impersonated an extension binding")
	}
	initial, err := target.OwnedIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity := initial
	oldIdentity.BrokerGeneration = "old-broker-generation"
	if _, e := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: oldIdentity, Token: "stale"}); e == nil {
		t.Fatal("native accepted old broker generation")
	}
	var granted []string
	authorize := func(request automation.Request, denySite string) (automation.BrowserDecision, error) {
		t.Helper()
		done := make(chan struct {
			d automation.BrowserDecision
			e error
		}, 1)
		go func() {
			d, e := s.AuthorizeBrowser(ctx, request)
			done <- struct {
				d automation.BrowserDecision
				e error
			}{d, e}
		}()
		for {
			select {
			case result := <-done:
				return result.d, result.e
			case p := <-prompts:
				if p.Session != request.Session {
					t.Fatal("permission escaped session")
				}
				if p.AllowAlways {
					t.Fatal("owned grant expanded legacy reusable host policy")
				}
				choice := "allow"
				if p.Kind == "site" && p.Site == denySite {
					choice = "deny"
				} else {
					granted = append(granted, p.Kind+":"+p.Site)
				}
				if _, e := s.AnswerBrowserConsent(ctx, p.ID, choice); e != nil {
					t.Fatal(e)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return automation.BrowserDecision{}, ctx.Err()
			}
		}
	}
	permit := func(tool string, args map[string]any) automation.Request {
		t.Helper()
		request := r
		request.Tool = tool
		request.Arguments = args
		d, e := authorize(request, "")
		if e != nil {
			t.Fatal(tool, e)
		}
		request.Epoch = d.Epoch
		request.Permit = d.Permit
		return request
	}
	execute := func(request automation.Request) any {
		t.Helper()
		t.Log("execute", request.Tool)
		value, e := s.Execute(ctx, request)
		if e != nil {
			t.Fatal(request.Tool, e)
		}
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), `"isError":true`) {
			t.Fatal(request.Tool, string(raw))
		}
		return value
	}
	input := permit("browser_type", map[string]any{"target": "getByRole('textbox', { name: 'Message' })", "text": "through-real-service"})
	execute(input)
	if len(granted) != 2 || granted[0] != "browser:" || granted[1] != "site:"+aServer.URL {
		t.Fatal("old host/global allow expanded to owned origin", granted)
	}
	markResult := execute(permit("browser_click", map[string]any{"target": "getByRole('button', { name: 'Mark', exact: true })"}))
	if raw, _ := json.Marshal(markResult); !strings.Contains(string(raw), "### Snapshot") || !strings.Contains(string(raw), "Marked") {
		t.Fatal("automatic action snapshot lost", string(raw))
	}
	select {
	case got := <-marked:
		if got != "through-real-service" {
			t.Fatal(got)
		}
	case <-ctx.Done():
		t.Fatal("real click did not reach fixture")
	}
	after, err := target.OwnedIdentity(ctx)
	if err != nil || after.Document != initial.Document {
		t.Fatal("Service did not operate the admitted document", after, err)
	}
	if _, err = s.Execute(ctx, input); err == nil {
		t.Fatal("single-use permit replayed")
	}
	request := r
	request.Tool = "browser_navigate"
	request.Arguments = map[string]any{"url": bServer.URL}
	if _, err = authorize(request, bServer.URL); err == nil || portBRequests.Load() != 0 {
		t.Fatal("origin A/legacy host grant reached unapproved port B", err, portBRequests.Load())
	}
	r.Turn = "turn-2"
	if _, err = s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	}
	execute(permit("browser_navigate", map[string]any{"url": bServer.URL}))
	t.Log("navigation B completed")
	if portBRequests.Load() == 0 {
		t.Fatal("approved port B never navigated")
	}
	stale := permit("browser_type", map[string]any{"target": "getByRole('textbox', { name: 'Message' })", "text": "must-not-run"})
	before, _ := target.OwnedIdentity(ctx)
	// A separately authorized action commits a new document before old input.
	next := permit("browser_click", map[string]any{"target": "getByRole('button', { name: 'Next', exact: true })"})
	t.Log("click commits next document; old action cannot observe it")
	nextResult, nextErr := s.Execute(ctx, next)
	if nextErr != nil {
		t.Fatal("click navigation failed", nextErr)
	}
	nextJSON, _ := json.Marshal(nextResult)
	if !strings.Contains(string(nextJSON), "### Snapshot") || !strings.Contains(string(nextJSON), "document=next") {
		t.Fatal("fresh document result missing", string(nextJSON))
	}
	t.Log("click navigation returned newly permitted snapshot")
	docWait, stopDocWait := context.WithTimeout(ctx, 12*time.Second)
	defer stopDocWait()
	for {
		current, e := target.OwnedIdentity(docWait)
		if e == nil && current.Document != before.Document {
			break
		}
		select {
		case <-docWait.Done():
			t.Fatal("document did not change", current, e, nextErr)
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, err = s.Execute(ctx, stale); err == nil {
		t.Fatal("old document permit reached new document")
	}
	// A link target is discovered by the browser, not self-declared in tool args.
	cross := permit("browser_click", map[string]any{"target": "getByRole('button', { name: 'Cross', exact: true })"})
	crossed := make(chan struct {
		value any
		err   error
	}, 1)
	go func() {
		v, e := s.Execute(ctx, cross)
		crossed <- struct {
			value any
			err   error
		}{v, e}
	}()
	select {
	case prompt := <-prompts:
		if prompt.Kind != "site" || prompt.Site != cServer.URL {
			t.Fatal("cross-origin result lacks independent grant", prompt)
		}
		if _, e := s.AnswerBrowserConsent(ctx, prompt.ID, "deny"); e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("cross-origin navigation did not ask", ctx.Err())
	}
	result := <-crossed
	raw, _ := json.Marshal(result.value)
	if result.err == nil || strings.Contains(string(raw), "Unapproved secret") {
		t.Fatal("unapproved new document leaked", string(raw), result.err)
	}
	current, _ := target.OwnedIdentity(ctx)
	if !strings.HasPrefix(current.URL, cServer.URL) {
		t.Fatal("cross-origin click did not actually navigate", current)
	}
	r.Turn = "turn-3"
	if _, e := s.BrowserTurn(r); e != nil {
		t.Fatal(e)
	}
	approved := execute(permit("browser_snapshot", map[string]any{}))
	approvedJSON, _ := json.Marshal(approved)
	if !strings.Contains(string(approvedJSON), "Unapproved secret") {
		t.Fatal("new-origin fresh permit did not observe current document", string(approvedJSON))
	}
	t.Log("cross-origin click completed without old-permit body read; denied result withheld, fresh grant returned actual snapshot")
	fresh := permit("browser_type", map[string]any{"target": "getByRole('textbox', { name: 'Message' })", "text": "after-revoke-must-not-run"})
	wrong := fresh
	wrong.Generation = "old-worker"
	if _, err = s.Execute(ctx, wrong); err == nil {
		t.Fatal("old worker generation executed")
	}
	fresh = permit("browser_type", fresh.Arguments)
	if err = s.StopBrowserUse(r.Session, r.Lifetime); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Execute(ctx, fresh); err == nil {
		t.Fatal("revoked permit executed")
	}
	t.Log("real Service -> pinned MCP -> same CEF document input/click; exact-port grants, one-use/doc/generation/revoke negatives passed")
	// An artifact principal comes from the Go lease object, not its virtual host.
	directory := t.TempDir()
	if err = os.WriteFile(filepath.Join(directory, "index.html"), []byte(html), 0600); err != nil {
		t.Fatal(err)
	}
	artifact, err := broker.OpenArtifact(ctx, directory, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Revoke(ctx)
	other, err := broker.OpenArtifact(ctx, directory, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Revoke(ctx)
	r = automation.Request{Generation: "owned-worker", Epoch: s.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: "artifact-service", Lifetime: "artifact-life", Turn: "one", Title: "Artifact fixture"}
	if _, err = s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	}
	if err = s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: r.Generation, Session: r.Session, Lifetime: r.Lifetime}, artifact.OwnedTarget()); err != nil {
		t.Fatal(err)
	}
	execute(permit("browser_type", map[string]any{"target": "getByRole('textbox', { name: 'Message' })", "text": "artifact-principal"}))
	request = r
	request.Tool = "browser_navigate"
	request.Arguments = map[string]any{"url": other.Origin() + "/index.html"}
	if _, err = authorize(request, ""); err == nil || !strings.Contains(err.Error(), "ARTIFACT_SCOPE_REJECTED") {
		t.Fatal("virtual host granted another root lease", err)
	}
	if err = artifact.Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	request = r
	request.Tool = "browser_snapshot"
	request.Arguments = map[string]any{}
	if _, err = authorize(request, ""); err == nil {
		t.Fatal("revoked artifact authorized")
	}
	t.Log("Service artifact principal bound to trusted lease; cross-root and revoked lease refused")
}
