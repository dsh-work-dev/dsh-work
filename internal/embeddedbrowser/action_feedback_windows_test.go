//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
	sup "github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/windows"
)

// Qualification-only mux: observation uses the existing scoped wire while all
// actions still originate in the locked MCP. No production script seam is added.
type feedbackFixtureRoute struct {
	automation.BrowserRoute
	out     chan json.RawMessage
	mu      sync.Mutex
	pending map[int]chan json.RawMessage
	next    int
	tab     int
	onPress func()
	input   []string
}

func newFeedbackFixtureRoute(route automation.BrowserRoute) *feedbackFixtureRoute {
	r := &feedbackFixtureRoute{BrowserRoute: route, out: make(chan json.RawMessage, 256), pending: map[int]chan json.RawMessage{}, next: 1000000}
	go func() {
		defer close(r.out)
		for raw := range route.Messages() {
			var m struct {
				ID     int
				Method string
				Params []json.RawMessage
			}
			json.Unmarshal(raw, &m)
			r.mu.Lock()
			ch := r.pending[m.ID]
			if ch != nil {
				delete(r.pending, m.ID)
			}
			if m.Method == "tab.created" && len(m.Params) > 0 {
				var tab struct{ ID int }
				json.Unmarshal(m.Params[0], &tab)
				r.tab = tab.ID
			}
			r.mu.Unlock()
			if ch != nil {
				ch <- raw
			} else {
				r.out <- raw
			}
		}
	}()
	return r
}
func (r *feedbackFixtureRoute) Messages() <-chan json.RawMessage { return r.out }
func (r *feedbackFixtureRoute) Send(ctx context.Context, raw json.RawMessage) error {
	var m struct {
		Method string
		Params []json.RawMessage
	}
	json.Unmarshal(raw, &m)
	if m.Method == "devtools.dispatch" && len(m.Params) > 2 {
		var method string
		json.Unmarshal(m.Params[1], &method)
		var p struct{ Type string }
		json.Unmarshal(m.Params[2], &p)
		r.mu.Lock()
		hook := r.onPress
		r.mu.Unlock()
		if method == "Input.dispatchMouseEvent" {
			r.mu.Lock()
			r.input = append(r.input, string(m.Params[2]))
			r.mu.Unlock()
		}
		if method == "Input.dispatchMouseEvent" && p.Type == "mousePressed" && hook != nil {
			hook()
		}
	}
	return r.BrowserRoute.Send(ctx, raw)
}
func (r *feedbackFixtureRoute) rpc(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	r.mu.Lock()
	r.next++
	id := r.next
	tab := r.tab
	ch := make(chan json.RawMessage, 1)
	r.pending[id] = ch
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(r.pending, id); r.mu.Unlock() }()
	message := map[string]any{"id": id, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, method, params}}
	if method == "dsh.indicator" {
		message = map[string]any{"id": id, "method": method, "params": params}
	}
	raw, _ := json.Marshal(message)
	if err := r.BrowserRoute.Send(ctx, raw); err != nil {
		return nil, err
	}
	select {
	case raw := <-ch:
		var m struct {
			Result json.RawMessage
			Error  string
		}
		json.Unmarshal(raw, &m)
		if m.Error != "" {
			return nil, fmt.Errorf("%s", m.Error)
		}
		return m.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type feedbackFixtureProvider struct {
	target *embeddedbrowser.OwnedTarget
	route  *feedbackFixtureRoute
}

func (p *feedbackFixtureProvider) Select(context.Context, automation.Request) (browserruntime.RouteSelection, error) {
	return browserruntime.RouteSelection{Protocol: "owned-cef-v1", Open: func(ctx context.Context, title string) (automation.BrowserRoute, error) {
		route, err := p.target.OpenOwnedRoute(ctx, title)
		if err != nil {
			return nil, err
		}
		p.route = newFeedbackFixtureRoute(route)
		return p.route, nil
	}}, nil
}

type feedbackFixtureSupervisor struct {
	sup.Adapter
	reduced    bool
	executable string
}

func (a feedbackFixtureSupervisor) Start(ctx context.Context, p sup.LaunchPlan, h sup.RawOutputHandler) (sup.Worker, error) {
	if a.reduced && p.Executable == a.executable {
		p.Args = append(p.Args, "--force-prefers-reduced-motion")
	}
	return a.Adapter.Start(ctx, p, h)
}
func TestLiveCEFActionFeedback(t *testing.T)               { runCEFActionFeedback(t, "ordered") }
func TestLiveCEFActionFeedbackAdjacentClicks(t *testing.T) { runCEFActionFeedback(t, "adjacent") }
func TestLiveCEFActionFeedbackLifecycle(t *testing.T)      { runCEFActionFeedback(t, "lifecycle") }
func TestLiveCEFActionFeedbackReducedMotion(t *testing.T)  { runCEFActionFeedback(t, "reduced") }
func runCEFActionFeedback(t *testing.T, scenario string) {
	exe, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if exe == "" || deps == "" {
		t.Skip("requires explicit actual CEF and locked MCP dependencies")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	evidence := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if evidence == "" {
		evidence = t.TempDir()
	}
	if err := os.MkdirAll(evidence, 0700); err != nil {
		t.Fatal(err)
	}
	var presses atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/icon-") {
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, `<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><rect width="32" height="32" fill="green"/></svg>`)
			return
		}
		if r.URL.Path == "/pressed" {
			presses.Add(1)
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><link id="siteIcon" rel="icon" href="/icon-old.svg"><title>Feedback fixture</title><style>body{margin:32px;background:#f7f7f5;font:20px system-ui;color:#222}button{position:fixed;left:70vw;top:70vh;width:22vw;height:50px;font:20px system-ui}input{position:fixed;left:30px;top:40vh;width:180px;height:36px}h1{font-size:28px}p{max-width:550px}</style><h1>CEF action feedback</h1><p>Real MCP actions. This page and profile belong only to the qualification fixture.</p><input aria-label="Message"><button type="button" onmousedown="window.pressedAt=performance.now();fetch('/pressed')" onclick="document.querySelector('output').textContent='Clicked';document.querySelector('#siteIcon').href='/icon-new.svg'">Far target</button><output>Ready</output><script>window.originalIcon=document.querySelector('#siteIcon');window.pressedAt=0;window.mountedAt=0;window.inputEvents=[];for(const type of ['mousedown','mouseup','click'])addEventListener(type,e=>inputEvents.push({type,x:e.clientX,y:e.clientY,target:e.target.tagName,t:performance.now()}),true);new MutationObserver(rs=>{if(rs.some(r=>[...r.addedNodes].some(n=>n.nodeType===1&&n.getAttribute('aria-hidden')==='true')))window.mountedAt=performance.now()}).observe(document.documentElement,{childList:true})</script>`)
	}))
	defer server.Close()
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	// Resize only this fixture's offscreen, non-activating HWND. A genuinely
	// distant CSS target gives the unchanged renderer its normal 350ms budget.
	if ok, _, err := windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowPos").Call(parent, 0, 0, 0, 2400, 1400, 0x16); ok == 0 {
		t.Fatal(err)
	}
	supervisor := feedbackFixtureSupervisor{Adapter: win.NewJobObjectAdapter(), reduced: scenario == "reduced", executable: exe}
	root := t.TempDir()
	acquire := func(dir string) (io.Closer, error) {
		return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
	}
	store, err := embeddedbrowser.OpenInstanceStore(filepath.Join(root, "instances"), acquire)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var broker *embeddedbrowser.Broker
	reg, err := embeddedbrowser.NewInstancePresenterRegistry(embeddedbrowser.InstanceRegistryConfig{Store: store, AcquireProfile: acquire, StartBroker: func(c context.Context, i embeddedbrowser.InstanceConfig) (*embeddedbrowser.Broker, error) {
		var err error
		broker, err = embeddedbrowser.Start(c, embeddedbrowser.Config{Executable: exe, Profile: i.Profile, Supervisor: supervisor, ObserveProtocol: func(direction, method string) {
			if direction == "control" || direction == "result" {
				t.Log("native", direction, method)
			}
		}})
		return broker, err
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reg.Close(); err != nil {
			t.Error(err)
		}
		if broker != nil {
			t.Log("native cleanupPending", broker.CleanupPending())
		}
	}()
	window, err := reg.RegisterInstanceWindow(ctx, peer, "feedback-fixture", "feedback-window", parent)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	type doc struct {
		name     string
		target   *embeddedbrowser.OwnedTarget
		provider *feedbackFixtureProvider
		resource automation.Resource
	}
	sequence := 0
	guard := func(target *embeddedbrowser.OwnedTarget) func() {
		t.Helper()
		identity, err := target.OwnedIdentity(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sequence++
		end, err := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: fmt.Sprintf("feedback-%d", sequence)})
		if err != nil {
			t.Fatal(err)
		}
		return func() {
			if err := end(); err != nil {
				t.Fatal(err)
			}
		}
	}
	makeDoc := func(name string) *doc {
		t.Helper()
		t.Log("makeDoc", name)
		view, err := reg.Update(ctx, peer, "feedback-window", parent, window.Lease, name, server.URL+"/?document="+name, embeddedbrowser.SlotBounds{Width: 2400, Height: 1400}, 1, true)
		if err != nil {
			t.Fatal(err)
		}
		for view.State != "ready" {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
			view, err = reg.State(ctx, peer, "feedback-window", name)
			if err != nil {
				t.Fatal(err)
			}
		}
		target, err := reg.OwnedWindowViewTarget(peer, "feedback-window", parent, window.Lease, name, view.Lease)
		if err != nil {
			t.Fatal(err)
		}
		p := &feedbackFixtureProvider{target: target}
		end := guard(target)
		factory := browserruntime.NewFactory(browserruntime.Config{Routes: p, Supervisor: supervisor, Root: t.TempDir(), OutputRoot: evidence, Resolve: func(context.Context) (browserruntime.Runtime, error) {
			return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
		}})
		resource, err := factory(ctx, automation.Request{Session: "feedback-" + name, Lifetime: "qualification"})
		end()
		if err != nil {
			t.Fatal(err)
		}
		return &doc{name, target, p, resource}
	}
	present := func(d *doc, visible bool) {
		t.Helper()
		t.Log("present", d.name, visible)
		_, err := reg.Update(ctx, peer, "feedback-window", parent, window.Lease, d.name, "", embeddedbrowser.SlotBounds{Width: 2400, Height: 1400}, 1, visible)
		if err != nil {
			t.Fatal(err)
		}
		t.Log("present complete", d.name, visible)
	}
	a := makeDoc("A")
	defer a.resource.Close()
	call := func(d *doc, tool string, args map[string]any) any {
		t.Helper()
		end := guard(d.target)
		defer end()
		value, err := d.resource.Execute(ctx, tool, args)
		if err != nil {
			t.Fatal(tool, err)
		}
		raw, _ := json.Marshal(value)
		if strings.Contains(string(raw), `"isError":true`) {
			t.Fatal(tool, string(raw))
		}
		return value
	}
	read := func(d *doc, expression string) string {
		t.Helper()
		end := guard(d.target)
		defer end()
		raw, err := d.provider.route.rpc(ctx, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	pointer := func(d *doc) string {
		raw, err := d.provider.route.rpc(ctx, "Runtime.evaluate", map[string]any{"expression": "document.documentElement"})
		if err != nil {
			return err.Error()
		}
		var root struct {
			Result struct {
				ObjectID string `json:"objectId"`
			}
		}
		json.Unmarshal(raw, &root)
		raw, err = d.provider.route.rpc(ctx, "DOM.describeNode", map[string]any{"objectId": root.Result.ObjectID, "depth": -1, "pierce": true})
		if err != nil {
			return err.Error()
		}
		var tree any
		json.Unmarshal(raw, &tree)
		var backend float64
		var walk func(any)
		walk = func(value any) {
			switch n := value.(type) {
			case map[string]any:
				if attrs, ok := n["attributes"].([]any); ok {
					for i := 0; i+1 < len(attrs); i += 2 {
						if attrs[i] == "id" && attrs[i+1] == "cursor" {
							backend, _ = n["backendNodeId"].(float64)
						}
					}
				}
				for _, child := range n {
					walk(child)
				}
			case []any:
				for _, child := range n {
					walk(child)
				}
			}
		}
		walk(tree)
		if backend == 0 {
			return "pointer absent"
		}
		raw, err = d.provider.route.rpc(ctx, "DOM.resolveNode", map[string]any{"backendNodeId": int(backend)})
		if err != nil {
			return err.Error()
		}
		var node struct {
			Object struct {
				ObjectID string `json:"objectId"`
			}
		}
		json.Unmarshal(raw, &node)
		raw, err = d.provider.route.rpc(ctx, "Runtime.callFunctionOn", map[string]any{"objectId": node.Object.ObjectID, "functionDeclaration": "function(){return {transform:getComputedStyle(this).transform,visibility:getComputedStyle(this).visibility,bounds:this.getBoundingClientRect().toJSON(),animations:this.getAnimations().map(a=>({currentTime:a.currentTime,playState:a.playState,frames:a.effect.getKeyframes()}))}}", "returnByValue": true})
		if err != nil {
			return err.Error()
		}
		return string(raw)
	}
	assertAt := func(d *doc, selector string) {
		t.Helper()
		raw := read(d, "(()=>{const r=document.querySelector("+fmt.Sprintf("%q", selector)+").getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()")
		var expected struct {
			Result struct{ Value struct{ X, Y float64 } }
		}
		json.Unmarshal([]byte(raw), &expected)
		var actual struct {
			Result struct {
				Value struct{ Bounds struct{ X, Y float64 } }
			}
		}
		end := guard(d.target)
		geometry := pointer(d)
		end()
		json.Unmarshal([]byte(geometry), &actual)
		dx, dy := actual.Result.Value.Bounds.X-expected.Result.Value.X, actual.Result.Value.Bounds.Y-expected.Result.Value.Y
		if dx < -1 || dx > 1 || dy < -1 || dy > 1 {
			t.Fatal("pointer did not arrive at actual MCP target", raw, geometry)
		}
	}
	shot := func(d *doc, name string) {
		t.Helper()
		raw, err := d.provider.route.rpc(ctx, "Page.captureScreenshot", map[string]any{"format": "png"})
		if err != nil {
			t.Log("intermediate screenshot", name, err)
			return
		}
		var v struct{ Data string }
		json.Unmarshal(raw, &v)
		data, err := base64.StdEncoding.DecodeString(v.Data)
		if err != nil {
			t.Error(err)
			return
		}
		if err = os.WriteFile(filepath.Join(evidence, name+".png"), data, 0600); err != nil {
			t.Error(err)
		}
	}
	for _, d := range []*doc{a} {
		call(d, "browser_snapshot", map[string]any{})
		if got := read(d, "window.mountedAt"); !strings.Contains(got, `"value":0`) {
			t.Fatal("untouched read initialized pointer", got)
		}
	}
	if scenario == "adjacent" {
		end := guard(a.target)
		defer end()
		rpc := func(method string, params map[string]any) json.RawMessage {
			t.Helper()
			raw, err := a.provider.route.rpc(ctx, method, params)
			if err != nil {
				t.Fatal(method, err)
			}
			return raw
		}
		position := func(selector string) (float64, float64) {
			raw := rpc("Runtime.evaluate", map[string]any{"expression": "(()=>{const r=document.querySelector(" + fmt.Sprintf("%q", selector) + ").getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()", "returnByValue": true})
			var v struct {
				Result struct{ Value struct{ X, Y float64 } }
			}
			json.Unmarshal(raw, &v)
			return v.Result.Value.X, v.Result.Value.Y
		}
		farX, farY := position("button")
		nearX, nearY := position("input")
		rpc("dsh.indicator", map[string]any{"running": true, "action": "browser_click"})
		click := func(x, y float64) {
			for _, kind := range []string{"mousePressed", "mouseReleased"} {
				buttons := 0
				if kind == "mousePressed" {
					buttons = 1
				}
				t.Log("adjacent input", kind, x, y)
				raw, err := a.provider.route.rpc(ctx, "Input.dispatchMouseEvent", map[string]any{"type": kind, "x": x, "y": y, "button": "left", "buttons": buttons, "clickCount": 1})
				if err != nil {
					t.Log("failed preparation geometry", pointer(a))
					t.Fatal(kind, err, string(raw))
				}
			}
		}
		first := time.Now()
		click(farX, farY)
		secondStart := time.Since(first)
		click(nearX, nearY)
		secondEnd := time.Since(first)
		rpc("dsh.indicator", map[string]any{"running": false})
		raw := rpc("Runtime.evaluate", map[string]any{"expression": "inputEvents.filter(e=>e.type==='click').map(e=>e.target)", "returnByValue": true})
		var value struct{ Result struct{ Value []string } }
		json.Unmarshal(raw, &value)
		if len(value.Result.Value) != 2 || value.Result.Value[0] != "BUTTON" || value.Result.Value[1] != "INPUT" {
			t.Fatal("adjacent prepared clicks lost a real click", string(raw))
		}
		if secondStart >= 450*time.Millisecond || secondEnd <= 450*time.Millisecond {
			t.Fatal("old first-attempt deadline did not overlap second preparation", secondStart, secondEnd)
		}
		t.Log("adjacent owned-route prepared clicks: first 450ms deadline inside second preparation", secondStart, secondEnd, "real click targets", value.Result.Value)
		return
	}
	if scenario != "ordered" {
		var sample sync.WaitGroup
		var early string
		if scenario == "reduced" {
			if got := read(a, "matchMedia('(prefers-reduced-motion: reduce)').matches"); !strings.Contains(got, `"value":true`) {
				t.Fatal("native reduced-motion preference missing", got)
			}
			a.provider.route.mu.Lock()
			a.provider.route.onPress = func() {
				sample.Add(1)
				go func() { defer sample.Done(); time.Sleep(15 * time.Millisecond); early = pointer(a) }()
			}
			a.provider.route.mu.Unlock()
		}
		call(a, "browser_click", map[string]any{"target": "getByRole('button', { name: 'Far target' })"})
		sample.Wait()
		assertAt(a, "button")
		if got := read(a, "document.querySelector('output').textContent"); !strings.Contains(got, `"value":"Clicked"`) {
			t.Fatal("supplemental actual click did not complete", got)
		}
		end := guard(a.target)
		shot(a, scenario+"-arrived")
		end()
		if scenario == "reduced" {
			t.Log("native reduced-motion early renderer", early)
			if !strings.Contains(early, `"animations":[]`) {
				t.Fatal("reduced motion animated pointer", early)
			}
			return
		}
		original, err := a.target.OwnedIdentity(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = a.target.Navigate(ctx, server.URL+"/?document=navigated"); err != nil {
			t.Fatal(err)
		}
		for {
			now, err := a.target.OwnedIdentity(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if now.Document != original.Document && strings.Contains(now.URL, "document=navigated") {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
		if got := read(a, "window.mountedAt"); !strings.Contains(got, `"value":0`) {
			t.Fatal("navigation initialized new pointer", got)
		}
		end = guard(a.target)
		if got := pointer(a); got != "pointer absent" {
			t.Fatal("old pointer survived navigation", got)
		}
		shot(a, "navigation-reset")
		end()
		call(a, "browser_click", map[string]any{"target": "getByRole('button', { name: 'Far target' })"})
		assertAt(a, "button")
		if got := read(a, "document.querySelector('#siteIcon').getAttribute('href')"); !strings.Contains(got, "/icon-new.svg") {
			t.Fatal("fixture did not update site favicon", got)
		}
		iconDeadline := time.Now().Add(2 * time.Second)
		for {
			got := read(a, "({siteNodePreserved:originalIcon===document.querySelector('#siteIcon'),siteSource:originalIcon.getAttribute('href'),feedbackIcon:[...document.querySelectorAll('link[rel~=icon]')].some(n=>n!==originalIcon&&n.href.startsWith('data:image/png')),icons:document.querySelectorAll('link[rel~=icon]').length})")
			if strings.Contains(got, `"siteNodePreserved":true`) && strings.Contains(got, `"feedbackIcon":true`) && strings.Contains(got, `"icons":2`) {
				t.Log("active favicon positive control", got)
				break
			}
			if time.Now().After(iconDeadline) {
				t.Fatal("active feedback favicon did not draw", got)
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err = a.resource.Close(); err != nil {
			t.Fatal(err)
		}
		route, err := a.target.OpenOwnedRoute(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		defer route.Close()
		a.provider.route = newFeedbackFixtureRoute(route)
		for {
			a.provider.route.mu.Lock()
			tab := a.provider.route.tab
			a.provider.route.mu.Unlock()
			if tab != 0 {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
		}
		if got := read(a, "({icons:document.querySelectorAll('link[rel~=icon]').length,source:document.querySelector('#siteIcon').getAttribute('href'),siteNodePreserved:originalIcon===document.querySelector('#siteIcon')})"); !strings.Contains(got, `"icons":1`) || !strings.Contains(got, "/icon-new.svg") || !strings.Contains(got, `"siteNodePreserved":true`) {
			t.Fatal("latest site icon not restored after release", got)
		}
		end = guard(a.target)
		if got := pointer(a); got != "pointer absent" {
			t.Fatal("release retained pointer", got)
		}
		shot(a, "release-removed")
		end()
		t.Log("navigation reset and release removal with site's latest favicon verified")
		return
	}
	var frames sync.WaitGroup
	a.provider.route.mu.Lock()
	a.provider.route.onPress = func() {
		frames.Add(1)
		go func() {
			defer frames.Done()
			for i, delay := range []time.Duration{5, 70, 100} {
				time.Sleep(delay * time.Millisecond)
				t.Log("pointer intermediate", i, pointer(a))
				shot(a, fmt.Sprintf("A-click-frame-%d", i))
			}
		}()
	}
	a.provider.route.mu.Unlock()
	started := time.Now()
	call(a, "browser_click", map[string]any{"target": "getByRole('button', { name: 'Far target' })"})
	elapsed := time.Since(started)
	a.provider.route.mu.Lock()
	t.Log("actual MCP input params", a.provider.route.input)
	a.provider.route.mu.Unlock()
	frames.Wait()
	if presses.Load() != 1 {
		t.Fatal("real MCP press count", presses.Load())
	}
	t.Log("real MCP distant click duration", elapsed, "page timing", read(a, "({pressedAt,mountedAt,delta:pressedAt-mountedAt,output:document.querySelector('output').textContent,inputEvents})"))
	if got := read(a, "document.querySelector('output').textContent"); !strings.Contains(got, `"value":"Clicked"`) {
		t.Fatal("real MCP click did not complete", got)
	}
	assertAt(a, "button")
	end := guard(a.target)
	t.Log("pointer arrived", pointer(a))
	shot(a, "A-arrived")
	end()
	present(a, false)
	b := makeDoc("B")
	defer b.resource.Close()
	call(b, "browser_snapshot", map[string]any{})
	if got := read(b, "window.mountedAt"); !strings.Contains(got, `"value":0`) {
		t.Fatal("untouched second document initialized pointer", got)
	}
	call(b, "browser_hover", map[string]any{"target": "getByRole('textbox', { name: 'Message' })"})
	time.Sleep(400 * time.Millisecond)
	assertAt(b, "input")
	end = guard(b.target)
	t.Log("pointer B position", pointer(b))
	shot(b, "B-position")
	end()
	time.Sleep(3300 * time.Millisecond)
	present(b, false)
	present(a, true)
	assertAt(a, "button")
	end = guard(a.target)
	a.provider.route.rpc(ctx, "Page.bringToFront", map[string]any{})
	t.Log("pointer A retained", pointer(a))
	shot(a, "A-return-retained")
	end()
	// A new native guard does not retain an old in-flight click after revoke.
	a.provider.route.mu.Lock()
	a.provider.route.onPress = nil
	a.provider.route.mu.Unlock()
	call(a, "browser_hover", map[string]any{"target": "getByRole('textbox', { name: 'Message' })"})
	time.Sleep(400 * time.Millisecond)
	revoked := make(chan error, 1)
	a.provider.route.mu.Lock()
	a.provider.route.onPress = func() { go func() { time.Sleep(45 * time.Millisecond); revoked <- a.target.RevokeOwned(ctx) }() }
	a.provider.route.mu.Unlock()
	end = guard(a.target)
	value, clickErr := a.resource.Execute(ctx, "browser_click", map[string]any{"target": "getByRole('button', { name: 'Far target' })"})
	end()
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	if clickErr == nil && !strings.Contains(string(raw), `"isError":true`) {
		t.Fatal("revoked movement returned successful click", string(raw))
	}
	time.Sleep(100 * time.Millisecond)
	if presses.Load() != 1 {
		t.Fatal("revoked movement reached page", presses.Load())
	}
	t.Log("revoked during pointer travel: no additional mousedown", clickErr)
	// The fixture remains model-free. Actual Wails/DSH visuals are a separate gate.
}
