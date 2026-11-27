//go:build windows

package embeddedbrowser_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
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

func TestLiveOwnedExecutorRecoveryAndScreenshotScopeRace(t *testing.T) {
	exe, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if exe == "" || deps == "" {
		t.Skip("requires actual CEF and locked MCP")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	var signalMu sync.Mutex
	signals := map[string]chan string{}
	signalFor := func(document string) chan string {
		signalMu.Lock()
		defer signalMu.Unlock()
		if signals[document] == nil {
			signals[document] = make(chan string, 4)
		}
		return signals[document]
	}
	visible, removed := make(chan struct{}, 4), make(chan struct{}, 4)
	remove := make(chan struct{}, 4)
	slowStarted, slowRelease := make(chan struct{}), make(chan struct{})
	var slowOnce sync.Once
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<body style="background:rgb(240,0,0)">Foreign frame pixels</body>`))
	}))
	defer foreign.Close()
	html := `<!doctype html><style>body{background:white}iframe{position:fixed;inset:0;width:800px;height:500px;border:0;z-index:100}</style><button onclick="location.href='/slow-next'">Slow next</button><output></output><script>document.querySelector('output').textContent=location.pathname+location.search;window.nonce=crypto.randomUUID();(async()=>{for(;;){let cmd=await fetch('/signal?document='+encodeURIComponent(location.pathname+location.search)).then(r=>r.text());if(cmd==='nav'){location.href='/?autonomous=1';return}if(cmd==='frame'){let f=document.createElement('iframe');f.src='FOREIGN';document.body.append(f);await new Promise(r=>f.onload=r);await fetch('/visible');await fetch('/remove');f.remove();await fetch('/removed')}}})()</script>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/signal":
			select {
			case cmd := <-signalFor(r.URL.Query().Get("document")):
				t.Log("fixture signal delivered", r.URL.Query().Get("document"), cmd)
				w.Write([]byte(cmd))
			case <-r.Context().Done():
			}
			return
		case "/visible":
			visible <- struct{}{}
			w.WriteHeader(204)
			return
		case "/removed":
			removed <- struct{}{}
			w.WriteHeader(204)
			return
		case "/remove":
			select {
			case <-remove:
				w.WriteHeader(204)
			case <-r.Context().Done():
			}
			return
		case "/slow-next":
			slowOnce.Do(func() { close(slowStarted) })
			select {
			case <-slowRelease:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(strings.ReplaceAll(html, "FOREIGN", foreign.URL)))
	}))
	defer server.Close()
	var armed, removeBeforeResult atomic.Bool
	raceErrors := make(chan error, 4)
	type screenshotEvidence struct {
		size          int
		foreignPixels bool
	}
	images := make(chan screenshotEvidence, 4)
	adapter := &recordedSupervisor{Adapter: win.NewJobObjectAdapter(), t: t}
	b, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: adapter, ObserveProtocol: func(direction, method string) {
		if direction == "dispatch" && method == "Page.captureScreenshot" && armed.CompareAndSwap(true, false) {
			signalFor("/?autonomous=1") <- "frame"
			select {
			case <-visible:
				t.Log("actual native screenshot dispatch preceded cross-origin iframe load")
			case <-time.After(5 * time.Second):
				raceErrors <- errors.New("iframe did not become visible after screenshot dispatch")
			}
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	target, err := b.OpenOwnedWebTarget(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	s := automation.New(manager, installedBrowser{deps: deps})
	defer s.Close()
	s.EnableBrowserGateway(automation.BrowserGatewayConfig{Notify: func(p automation.BrowserConsent) {
		if _, e := s.AnswerBrowserConsent(ctx, p.ID, "allow"); e != nil {
			raceErrors <- e
		}
	}})
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	s.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{Service: s, Supervisor: adapter, Root: t.TempDir(), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}, Observe: func(r automation.Request, o browserruntime.Observation) {
		if o.Tool != "browser_take_screenshot" {
			return
		}
		var result struct{ Content []struct{ Type, Data string } }
		json.Unmarshal(o.RawResult, &result)
		size := 0
		foreignPixels := false
		for _, part := range result.Content {
			if part.Type == "image" {
				data, e := base64.StdEncoding.DecodeString(part.Data)
				if e == nil {
					size += len(data)
					if img, e := png.Decode(bytes.NewReader(data)); e == nil {
						red, green, blue, _ := img.At(100, 100).RGBA()
						foreignPixels = red > 200*257 && green < 30*257 && blue < 30*257
					}
					if path := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"); path != "" {
						_ = os.WriteFile(filepath.Join(path, "service-scope-inflight.png"), data, 0600)
					}
				}
			}
		}
		images <- screenshotEvidence{size: size, foreignPixels: foreignPixels}
		if removeBeforeResult.Load() {
			remove <- struct{}{}
			select {
			case <-removed:
			case <-time.After(5 * time.Second):
				raceErrors <- errors.New("iframe not removed before result")
			}
			check, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			for {
				state, e := target.OwnedIdentity(check)
				if e != nil {
					raceErrors <- e
					break
				}
				if state.FrameScopeReady {
					break
				}
				select {
				case <-check.Done():
					raceErrors <- check.Err()
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
		}
	}}), browserruntime.ToolSchemas())
	s.Worker("scope-worker", true, true)
	s.Gateway("scope-worker", true)
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
	r := automation.Request{Generation: "scope-worker", Epoch: s.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: "scope-races", Lifetime: "one", Turn: "one"}
	if _, err = s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	}
	if err = s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: r.Generation, Session: r.Session, Lifetime: r.Lifetime}, target); err != nil {
		t.Fatal(err)
	}
	permit := func(tool string, args map[string]any) automation.Request {
		t.Helper()
		q := r
		q.Tool = tool
		q.Arguments = args
		d, e := s.AuthorizeBrowser(ctx, q)
		if e != nil {
			t.Fatal(tool, e)
		}
		q.Permit = d.Permit
		q.Epoch = d.Epoch
		return q
	}
	snapshot := func(want string) {
		t.Helper()
		value, e := s.Execute(ctx, permit("browser_snapshot", map[string]any{}))
		raw, _ := json.Marshal(value)
		if e != nil || strings.Contains(string(raw), `"isError":true`) || !strings.Contains(string(raw), want) {
			t.Fatal("fresh snapshot", string(raw), e)
		}
	}
	snapshot("Slow next")
	// Actual navigation while a real MCP action is in flight, followed by caller
	// cancellation. The old executor must still be joined before the next permit.
	action, cancelAction := context.WithCancel(ctx)
	q := permit("browser_click", map[string]any{"target": "getByRole('button', { name: 'Slow next' })"})
	completed := make(chan error, 1)
	go func() { _, e := s.Execute(action, q); completed <- e }()
	select {
	case <-slowStarted:
		cancelAction()
		close(slowRelease)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if e := <-completed; !errors.Is(e, context.Canceled) {
		t.Fatal("expected canceled action after actual navigation", e)
	}
	snapshot("/slow-next")
	t.Log("canceled action's actual navigation recovered with a joined old executor and fresh document observation")
	before, err := target.OwnedIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	signalFor("/slow-next") <- "nav"
	navCtx, stopNav := context.WithTimeout(ctx, 12*time.Second)
	defer stopNav()
	for {
		state, e := target.OwnedIdentity(navCtx)
		if e != nil {
			t.Fatal(e)
		}
		if state.Document != before.Document && strings.Contains(state.URL, "autonomous=1") {
			break
		}
		select {
		case <-navCtx.Done():
			t.Fatal("autonomous navigation not observed", state, navCtx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	snapshot("autonomous=1")
	t.Log("autonomous navigation between calls retired the executor before installing the new document guard")
	for _, transient := range []bool{false, true} {
		q := permit("browser_take_screenshot", map[string]any{"type": "png", "scale": "css"})
		before, e := target.OwnedIdentity(ctx)
		if e != nil {
			t.Fatal(e)
		}
		removeBeforeResult.Store(transient)
		armed.Store(true)
		value, e := s.Execute(ctx, q)
		if e == nil || !strings.Contains(e.Error(), "BROWSER_FRAME_SCOPE_CHANGED") || value != nil {
			raw, _ := json.Marshal(value)
			t.Fatal("screenshot escaped final scope validation", string(raw), e)
		}
		var evidence screenshotEvidence
		select {
		case evidence = <-images:
		case <-ctx.Done():
			t.Fatal("no actual MCP screenshot result", ctx.Err())
		}
		if evidence.size == 0 || !evidence.foreignPixels {
			t.Fatal("race did not produce an actual PNG before the result barrier")
		}
		if !transient {
			remove <- struct{}{}
			select {
			case <-removed:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		var after automation.OwnedBrowserIdentity
		for {
			after, e = target.OwnedIdentity(ctx)
			if e != nil {
				t.Fatal(e)
			}
			if after.FrameScopeReady {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
		if after.Document != before.Document || after.ScopeEpoch == before.ScopeEpoch {
			t.Fatal("fixture did not preserve document and advance monotonic scope", before, after)
		}
		t.Log(fmt.Sprintf("actual foreign-pixel PNG=%d bytes suppressed; frame removed before result=%v; same document, scope epoch %s -> %s", evidence.size, transient, before.ScopeEpoch, after.ScopeEpoch))
	}
	snapshot("autonomous=1")
	select {
	case e := <-raceErrors:
		t.Fatal(e)
	default:
	}
}
