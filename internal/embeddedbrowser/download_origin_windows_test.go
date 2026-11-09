//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	win "github.com/local/dsh-work/internal/platform/windows"
)

// Independent native observer and SDK session lifecycle acceptance.
func TestLiveDownloadOriginObserverRouteIsolation(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("requires independent CEF candidate")
	}
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	var workerReady, childGone atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/worker.js" {
			w.Header().Set("Content-Type", "text/javascript")
			fmt.Fprint(w, `fetch('/worker-ready');onmessage=()=>{}`)
			return
		}
		if r.URL.Path == "/worker-ready" {
			workerReady.Add(1)
			return
		}
		if r.URL.Path == "/childgone" {
			childGone.Add(1)
			return
		}
		if r.URL.Path == "/child" {
			fmt.Fprint(w, `<!doctype html><script>addEventListener('load',()=>parent.postMessage('child-loaded','*'))</script><p>Cross-site child</p>`)
			return
		}
		if r.URL.Path == "/bytes" {
			w.Header().Set("Content-Disposition", `attachment; filename="origin.txt"`)
			fmt.Fprint(w, "actual observer download")
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Own origin probe</title><script>Object.defineProperty(window,'origin',{value:'https://spoof.invalid'});new Worker('/worker.js')</script><a id="download" href="/bytes">Local fixture</a>`)
	}))
	defer server.Close()
	root := t.TempDir()
	acquire := func(dir string) (io.Closer, error) {
		return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
	}
	store, e := embeddedbrowser.OpenInstanceStore(filepath.Join(root, "instances"), acquire)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	approved := make(chan embeddedbrowser.DownloadConsentRequest, 2)
	var observationsMu sync.Mutex
	var observations []string
	reg, e := embeddedbrowser.NewInstancePresenterRegistry(embeddedbrowser.InstanceRegistryConfig{Store: store, AcquireProfile: acquire, StartBroker: func(c context.Context, i embeddedbrowser.InstanceConfig) (*embeddedbrowser.Broker, error) {
		return embeddedbrowser.Start(c, embeddedbrowser.Config{Executable: exe, Profile: i.Profile, Supervisor: win.NewJobObjectAdapter(), DownloadConsent: func(c context.Context, p embeddedbrowser.DownloadConsentRequest) (embeddedbrowser.DownloadConsentDecision, error) {
			approved <- p
			d, e := downloads.PrepareDestination(filepath.Join(root, "origin.txt"), false)
			return embeddedbrowser.DownloadConsentDecision{Prepared: d, RiskConfirmed: true}, e
		}, ObserveProtocol: func(direction, method string) {
			if direction == "origin" {
				observationsMu.Lock()
				observations = append(observations, method)
				observationsMu.Unlock()
			}
		}})
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := reg.Close(); e != nil {
			t.Error(e)
		}
	}()
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	admission, e := reg.RegisterInstanceWindow(ctx, peer, "origin-probe", "probe", parent)
	if e != nil {
		t.Fatal(e)
	}
	view, e := reg.Update(ctx, peer, "probe", parent, admission.Lease, "source", server.URL, embeddedbrowser.SlotBounds{Width: 300, Height: 200}, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	for view.State != "ready" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
		view, e = reg.State(ctx, peer, "probe", "source")
		if e != nil {
			t.Fatal(e)
		}
	}
	target, e := reg.OwnedWindowViewTarget(peer, "probe", parent, admission.Lease, "source", view.Lease)
	if e != nil {
		t.Fatal(e)
	}
	route, e := target.OpenOwnedRoute(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	defer route.Close()
	identity, e := target.OwnedIdentity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	tab, _ := strconv.Atoi(identity.Target)
	end, e := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "origin-probe"})
	if e != nil {
		t.Fatal(e)
	}
	cleanup := end
	defer func() {
		if cleanup != nil {
			cleanup()
		}
	}()
	serial := 0
	var contexts []json.RawMessage
	rpc := func(method string, params map[string]any, session string) json.RawMessage {
		t.Helper()
		serial++
		selector := map[string]any{"tabId": tab}
		if session != "" {
			selector["sessionId"] = session
		}
		raw, _ := json.Marshal(map[string]any{"id": serial, "method": "devtools.dispatch", "params": []any{selector, method, params}})
		if e := route.Send(ctx, raw); e != nil {
			t.Fatal(e)
		}
		for {
			select {
			case raw := <-route.Messages():
				var reply struct {
					ID     int
					Result json.RawMessage
					Error  string
					Method string
					Params []json.RawMessage
				}
				json.Unmarshal(raw, &reply)
				if reply.Method == "devtools.event" && len(reply.Params) > 1 && string(reply.Params[1]) == `"Runtime.executionContextCreated"` {
					contexts = append(contexts, append(json.RawMessage(nil), raw...))
				}
				if reply.ID == serial {
					if reply.Error != "" {
						t.Fatal(method, reply.Error)
					}
					return reply.Result
				}
			case <-ctx.Done():
				t.Fatal(method, ctx.Err())
				return nil
			}
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		observationsMu.Lock()
		seen := strings.Join(observations, "; ")
		observationsMu.Unlock()
		if strings.Contains(seen, "failed") {
			t.Fatal("internal observer protocol failure", seen)
		}
		if strings.Contains(seen, "ready Runtime.enable") {
			t.Log("actual internal observer self-attach initialized", seen)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("internal observer did not initialize", seen)
		}
		time.Sleep(10 * time.Millisecond)
	}
	rpc("Runtime.enable", map[string]any{}, "")
	before := len(contexts)
	if before == 0 {
		t.Fatal("SDK Runtime.enable did not receive root contexts")
	}
	if e := end(); e != nil {
		t.Fatal(e)
	}
	cleanup = nil
	if e := route.Close(); e != nil {
		t.Fatal(e)
	}
	// An acknowledged native identity query orders after route release on the
	// same private transport. Reattach performs the existing root Runtime reset.
	identity, e = target.OwnedIdentity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	route, e = target.OpenOwnedRoute(ctx, "")
	if e != nil {
		t.Fatal(e)
	}
	defer route.Close()
	end, e = target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "origin-after-reattach"})
	if e != nil {
		t.Fatal(e)
	}
	cleanup = end
	before = len(contexts)
	rpc("Runtime.enable", map[string]any{}, "")
	if len(contexts) <= before {
		t.Fatal("AttachRoute root Runtime reset lost SDK contexts")
	}
	deadline = time.Now().Add(4 * time.Second)
	for workerReady.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("ordinary worker did not execute")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// A genuine cross-site iframe is attached then removed while its private
	// observer initialization can still be in flight. It must not kill root.
	childURL := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/child"
	expression := fmt.Sprintf(`(()=>{const f=document.createElement('iframe');addEventListener('message',e=>{if(e.source===f.contentWindow&&e.data==='child-loaded'){f.remove();fetch('/childgone')}});f.src=%q;document.body.append(f)})()`, childURL)
	rpc("Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true}, "")
	deadline = time.Now().Add(4 * time.Second)
	for childGone.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("real child detach missing")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e := end(); e != nil {
		t.Fatal(e)
	}
	cleanup = nil
	identity, e = target.OwnedIdentity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	end, e = target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "after-real-child-detach"})
	if e != nil {
		t.Fatal(e)
	}
	cleanup = end
	rpc("Runtime.evaluate", map[string]any{"expression": "document.getElementById('download').click()", "returnByValue": true}, "")
	select {
	case p := <-approved:
		if p.SourceOrigin != server.URL {
			t.Fatal("post-AttachRoute source origin", p.SourceOrigin)
		}
	case <-ctx.Done():
		t.Fatal("normal download unavailable after native Runtime.disable/AttachRoute", ctx.Err())
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		got, e := os.ReadFile(filepath.Join(root, "origin.txt"))
		if e == nil && string(got) == "actual observer download" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("post-reattach actual GET did not publish", e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, raw := range contexts {
		var event struct{ Params []json.RawMessage }
		json.Unmarshal(raw, &event)
		var source struct{ SessionID string }
		json.Unmarshal(event.Params[0], &source)
		if source.SessionID != "" {
			t.Fatal("private observer session leaked to SDK", string(raw))
		}
	}
	observationsMu.Lock()
	t.Log("observer remained isolated and normal download published after actual AttachRoute Runtime.disable reset", observations)
	observationsMu.Unlock()
}

func TestLiveDownloadFrameSecurityOrigin(t *testing.T) {
	for _, mode := range []string{"normal", "opaque", "same-url-late", "same-document-late"} {
		t.Run(mode, func(t *testing.T) { runDownloadFrameOrigin(t, mode) })
	}
}
func runDownloadFrameOrigin(t *testing.T, mode string) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("requires independent current CEF candidate")
	}
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	root := t.TempDir()
	var loaded, command, reload, within, navigated, gets, opaqueReports atomic.Int32
	payload := "trusted frame origin bytes"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		switch r.URL.Path {
		case "/loaded":
			loaded.Add(1)
			if r.Header.Get("Origin") == "null" {
				opaqueReports.Add(1)
			}
			return
		case "/within":
			within.Add(1)
			return
		case "/control":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"command":%d,"reload":%d,"navigate":%d}`, command.Load(), reload.Load(), navigated.Load())
			return
		case "/bytes":
			gets.Add(1)
			w.Header().Set("Content-Disposition", `attachment; filename="origin.txt"`)
			fmt.Fprint(w, payload)
			return
		case "/child":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<!doctype html><a id="download" href="/bytes">Download</a><script>Object.defineProperty(window,'origin',{value:'https://spoof.invalid'});let cmd=%d,rev=%d,nav=%d;addEventListener('load',()=>requestAnimationFrame(()=>requestAnimationFrame(()=>fetch('/loaded'))));setInterval(async()=>{const s=await(await fetch('/control')).json();if(s.reload!==rev){location.reload();return}if(s.navigate!==nav){nav=s.navigate;history.pushState({},'','#'+nav);fetch('/within')}if(s.command!==cmd){cmd=s.command;document.getElementById('download').click()}},40)</script>`, command.Load(), reload.Load(), navigated.Load())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		sandbox := "allow-scripts allow-downloads allow-same-origin"
		if mode == "opaque" {
			sandbox = "allow-scripts allow-downloads"
		}
		fmt.Fprintf(w, `<!doctype html><title>Download frame source</title><iframe sandbox="%s" src="/child"></iframe>`, sandbox)
	}))
	defer server.Close()
	type approval struct {
		request   embeddedbrowser.DownloadConsentRequest
		decision  chan bool
		cancelled <-chan struct{}
	}
	approvals := make(chan approval, 4)
	var logMu sync.Mutex
	var originLog []string
	adapter := &policyAdapter{Adapter: win.NewJobObjectAdapter()}
	broker, e := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: exe, Profile: filepath.Join(root, "profile"), Supervisor: adapter,
		ObserveProtocol: func(direction, method string) {
			if direction == "origin" {
				logMu.Lock()
				originLog = append(originLog, method)
				logMu.Unlock()
			}
		},
		DownloadConsent: func(c context.Context, p embeddedbrowser.DownloadConsentRequest) (embeddedbrowser.DownloadConsentDecision, error) {
			decide := make(chan bool, 1)
			select {
			case approvals <- approval{p, decide, c.Done()}:
			case <-ctx.Done():
				return embeddedbrowser.DownloadConsentDecision{}, ctx.Err()
			}
			// Deliberately permit a stale test-Host reply after source context reload;
			// native pending validation, not cooperative cancellation, must reject it.
			select {
			case allowed := <-decide:
				if !allowed {
					return embeddedbrowser.DownloadConsentDecision{}, nil
				}
				d, e := downloads.PrepareDestination(filepath.Join(root, p.ID+".txt"), false)
				t.Logf("test Host decision context=%v prepare=%v", c.Err(), e)
				return embeddedbrowser.DownloadConsentDecision{Prepared: d, RiskConfirmed: true}, e
			case <-ctx.Done():
				return embeddedbrowser.DownloadConsentDecision{}, ctx.Err()
			}
		},
	})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := broker.Close(); e != nil {
			t.Error(e)
		}
		if broker.CleanupPending() {
			t.Error("fixture native cleanup pending")
		}
	}()
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	registry := embeddedbrowser.NewPresenterRegistry(broker)
	defer registry.Close()
	admission, e := registry.RegisterWindow(ctx, peer, "origin-frame", parent)
	if e != nil {
		t.Fatal(e)
	}
	_, e = registry.Update(ctx, peer, "origin-frame", parent, admission.Lease, "source", server.URL, embeddedbrowser.SlotBounds{Width: 300, Height: 200}, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	wait := func(label string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !predicate() {
			if time.Now().After(deadline) || ctx.Err() != nil {
				logMu.Lock()
				t.Log("private engine origin observations", originLog)
				logMu.Unlock()
				t.Logf("downloads=%+v GETs=%d loaded=%d", broker.DownloadState(), gets.Load(), loaded.Load())
				files, _ := os.ReadDir(root)
				for _, f := range files {
					t.Log("fixture output", f.Name())
				}
				t.Fatal("timeout", label, ctx.Err())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait("source frame loaded", func() bool { return loaded.Load() > 0 })
	wait("engine default origin observation", func() bool {
		logMu.Lock()
		defer logMu.Unlock()
		joined := strings.Join(originLog, ";")
		if mode == "opaque" {
			return strings.Contains(joined, "rejected-context")
		}
		return strings.Count(joined, "context Runtime.executionContextCreated") >= 2
	})
	command.Add(1)
	wait("actual attachment response", func() bool { return gets.Load() > 0 })
	if mode == "opaque" {
		time.Sleep(200 * time.Millisecond)
		select {
		case p := <-approvals:
			p.decision <- false
			t.Fatalf("opaque frame got Host approval: %+v", p.request)
		default:
		}
		if opaqueReports.Load() == 0 {
			t.Fatal("opaque fixture lacked real Origin:null evidence")
		}
		if gets.Load() != 1 {
			t.Fatal("unapproved follow GET", gets.Load())
		}
		t.Log("real same-site opaque sandbox (allow-downloads, no allow-same-origin), spoofed window.origin: no Host request, no approved GET; engine rejected opaque context")
		return
	}
	var first approval
	select {
	case first = <-approvals:
	case <-ctx.Done():
		t.Fatal("normal iframe lost approval", ctx.Err())
	}
	if first.request.SourceOrigin != server.URL {
		first.decision <- false
		t.Fatalf("wrong real frame origin %+v", first.request)
	}
	if mode == "same-url-late" || mode == "same-document-late" {
		oldLoaded := loaded.Load()
		logMu.Lock()
		oldContexts := strings.Count(strings.Join(originLog, ";"), "context Runtime.executionContextCreated")
		logMu.Unlock()
		if mode == "same-url-late" {
			reload.Add(1)
			wait("same-URL source context replacement", func() bool { return loaded.Load() > oldLoaded })
		} else {
			navigated.Add(1)
			wait("within-document source history change", func() bool { return within.Load() > 0 })
		}
		wait("old pending cancelled", func() bool {
			select {
			case <-first.cancelled:
				return true
			default:
				return false
			}
		})
		first.decision <- true
		time.Sleep(150 * time.Millisecond)
		if gets.Load() != 1 {
			t.Fatal("late old context reply started new GET", gets.Load())
		}
		if _, e := os.Stat(filepath.Join(root, first.request.ID+".txt")); !os.IsNotExist(e) {
			t.Fatal("late source reply published", e)
		}
		command.Add(1)
		wait("new document attachment", func() bool { return gets.Load() > 1 })
		var second approval
		select {
		case second = <-approvals:
		case <-ctx.Done():
			t.Fatal("new same-URL context lost normal approval", ctx.Err())
		}
		logMu.Lock()
		newContexts := strings.Count(strings.Join(originLog, ";"), "context Runtime.executionContextCreated")
		logMu.Unlock()
		if (mode == "same-url-late" && newContexts <= oldContexts) || (mode == "same-document-late" && newContexts != oldContexts) || second.request.ID == first.request.ID {
			second.decision <- false
			t.Fatal("fresh same-URL source context/pending not observed")
		}
		if second.request.SourceOrigin != server.URL {
			second.decision <- false
			t.Fatal("new context origin", second.request.SourceOrigin)
		}
		t.Logf("same-URL context-created count %d -> %d; old pending=%s new=%s; stale Host reply did not transmit", oldContexts, newContexts, first.request.ID, second.request.ID)
		first = second
	}
	first.decision <- true
	destination := filepath.Join(root, first.request.ID+".txt")
	wait("actual approved frame GET publication", func() bool { got, e := os.ReadFile(destination); return e == nil && string(got) == payload })
	expected := int32(2)
	if mode == "same-url-late" || mode == "same-document-late" {
		expected = 3
	}
	if gets.Load() != expected {
		t.Fatal("GET count", gets.Load(), expected)
	}
	logMu.Lock()
	t.Logf("normal iframe engine origin=%s with spoofed JS origin; exact bytes published; observations=%v", first.request.SourceOrigin, originLog)
	logMu.Unlock()
}
