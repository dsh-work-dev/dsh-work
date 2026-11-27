//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
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
)

// Actual CEF official observer -> authenticated process pipe -> unchanged
// locked MCP tools. This gate is transport evidence, not M0 product acceptance:
// it intentionally does not claim Service permissions or a DSH presentation.
func TestLivePrivateCEFAndLockedMCP(t *testing.T) {
	executable, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if executable == "" || deps == "" {
		t.Skip("requires explicit CEF component and locked MCP dependency paths")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	adapter := win.NewJobObjectAdapter()
	var protocolMu sync.Mutex
	methods := map[string]int{}
	b, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: executable, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: adapter, ObserveProtocol: func(direction, method string) {
		protocolMu.Lock()
		methods[direction+" "+method]++
		protocolMu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	clicked := make(chan string, 1)
	var cachedRequests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cached" {
			cachedRequests.Add(1)
			w.Header().Set("Cache-Control", "public,max-age=3600")
			_, _ = w.Write([]byte("cached-B"))
			return
		}
		if r.URL.Path == "/clicked" {
			select {
			case clicked <- r.URL.Query().Get("value"):
			default:
			}
			w.WriteHeader(204)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<label>Message<input aria-label="Message"></label><button onclick="fetch('/clicked?value='+encodeURIComponent(document.querySelector('input').value));document.querySelector('output').textContent='Clicked'">Send</button><output>Ready</output>`))
	}))
	defer fixture.Close()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	factory := browserruntime.NewFactory(browserruntime.Config{Routes: b, Supervisor: adapter, Root: t.TempDir(), OutputRoot: os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}})
	r, err := factory(ctx, automation.Request{Session: "private-cef-transport", Lifetime: "single-launch"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	call := func(tool string, args map[string]any) string {
		t.Helper()
		v, err := r.Execute(ctx, tool, args)
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		data, _ := json.Marshal(v)
		if strings.Contains(string(data), `"isError":true`) {
			t.Fatalf("%s: %s", tool, data)
		}
		if tool == "browser_take_screenshot" {
			t.Log("browser_take_screenshot: PNG saved by real MCP")
		} else {
			t.Logf("%s: %s", tool, data)
		}
		return string(data)
	}
	call("browser_navigate", map[string]any{"url": fixture.URL})
	call("browser_type", map[string]any{"target": "getByRole('textbox', { name: 'Message' })", "text": "private-route-input"})
	call("browser_click", map[string]any{"target": "getByRole('button', { name: 'Send' })"})
	select {
	case got := <-clicked:
		if got != "private-route-input" {
			t.Fatalf("fixture input: %q", got)
		}
	case <-ctx.Done():
		t.Fatal("fixture did not receive real click")
	}
	if s := call("browser_snapshot", map[string]any{}); !strings.Contains(s, "Clicked") {
		t.Fatal("same document click result missing")
	}
	call("browser_tabs", map[string]any{"action": "new", "url": fixture.URL})
	call("browser_tabs", map[string]any{"action": "select", "index": 0})
	call("browser_tabs", map[string]any{"action": "close", "index": 1})
	call("browser_take_screenshot", map[string]any{"type": "png", "scale": "css"})
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	openRoute := func(session string) (automation.BrowserRoute, int) {
		t.Helper()
		selection, err := b.Select(ctx, automation.Request{Session: session, Lifetime: "negative-check"})
		if err != nil {
			t.Fatal(err)
		}
		route, err := selection.Open(ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case raw := <-route.Messages():
				var event struct {
					Method string
					Params []json.RawMessage
				}
				_ = json.Unmarshal(raw, &event)
				if event.Method == "tab.created" {
					var tab struct{ ID int }
					_ = json.Unmarshal(event.Params[0], &tab)
					return route, tab.ID
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil, 0
			}
		}
	}
	a, tabA := openRoute("route-A")
	defer a.Close()
	z, tabZ := openRoute("route-B")
	defer z.Close()
	rejected := func(args []any, want string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"id": 900, "method": "devtools.dispatch", "params": args})
		if err := z.Send(ctx, raw); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case raw := <-z.Messages():
				var result struct {
					ID    int
					Error string
				}
				_ = json.Unmarshal(raw, &result)
				if result.ID == 900 {
					if result.Error != want {
						t.Fatalf("wanted %s got %s", want, raw)
					}
					t.Log(want)
					return
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return
			}
		}
	}
	rejected([]any{map[string]any{"tabId": tabA}, "Page.enable", map[string]any{}}, "CEF_CROSS_TARGET_REJECTED")
	rejected([]any{map[string]any{"tabId": tabZ}, "Browser.close", map[string]any{}}, "CEF_UNSCOPED_COMMAND_REJECTED")
	rejected([]any{map[string]any{"tabId": tabZ, "sessionId": "foreign-session"}, "Page.enable", map[string]any{}}, "CEF_CROSS_SESSION_REJECTED")
	rpcID := 1000
	rpc := func(route automation.BrowserRoute, tab int, method string, params map[string]any) (json.RawMessage, string) {
		t.Helper()
		rpcID++
		id := rpcID
		raw, _ := json.Marshal(map[string]any{"id": id, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, method, params}})
		if err := route.Send(ctx, raw); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case raw := <-route.Messages():
				var m struct {
					ID     int
					Result json.RawMessage
					Error  string
				}
				_ = json.Unmarshal(raw, &m)
				if m.ID == id {
					return m.Result, m.Error
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil, "timeout"
			}
		}
	}
	if _, err := rpc(z, tabZ, "Page.navigate", map[string]any{"url": fixture.URL}); err != "" {
		t.Fatal(err)
	}
	for {
		raw, err := rpc(z, tabZ, "Runtime.evaluate", map[string]any{"expression": "location.href", "returnByValue": true})
		if err == "" && strings.Contains(string(raw), fixture.URL) {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	eval := func(expression string) string {
		t.Helper()
		raw, err := rpc(z, tabZ, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true})
		if err != "" {
			t.Fatal(err)
		}
		var value struct {
			Result           struct{ Value json.RawMessage }
			ExceptionDetails json.RawMessage
		}
		_ = json.Unmarshal(raw, &value)
		if len(value.ExceptionDetails) > 0 {
			t.Fatalf("fixture evaluation failed: %s", raw)
		}
		return string(value.Result.Value)
	}
	state := eval(`(async()=>{document.cookie='routeB=kept; path=/';window.routeBNonce='B-live-document';return {cookie:document.cookie,nonce:window.routeBNonce,cache:await fetch('/cached',{cache:'force-cache'}).then(r=>r.text())}})()`)
	if !strings.Contains(state, "routeB=kept") || cachedRequests.Load() != 1 {
		t.Fatalf("B fixture not established: %s requests=%d", state, cachedRequests.Load())
	}
	for _, method := range []string{"Network.clearBrowserCookies", "Network.getAllCookies", "Network.getCookies", "Network.clearBrowserCache", "Network.setCacheDisabled", "Storage.clearDataForOrigin", "Unknown.method"} {
		if _, err := rpc(a, tabA, method, map[string]any{}); err != "CEF_UNSCOPED_COMMAND_REJECTED" {
			t.Fatalf("%s: %s", method, err)
		}
		t.Log("A rejected before dispatch:", method)
	}
	if _, err := rpc(a, tabA, "Target.getTargetInfo", map[string]any{"targetId": "foreign-target"}); err != "CEF_UNSCOPED_COMMAND_REJECTED" {
		t.Fatal("target selector accepted", err)
	}
	if _, err := rpc(a, tabA, "Page.createIsolatedWorld", map[string]any{"frameId": "foreign-frame", "worldName": "__playwright_utility_world_test"}); err != "CEF_CROSS_FRAME_REJECTED" {
		t.Fatal("foreign frame accepted", err)
	}
	if got := eval(`(async()=>({cookie:document.cookie,nonce:window.routeBNonce,cache:await fetch('/cached',{cache:'force-cache'}).then(r=>r.text())}))()`); got != state || cachedRequests.Load() != 1 {
		t.Fatalf("A affected B: before=%s after=%s cache requests=%d", state, got, cachedRequests.Load())
	}
	t.Log("B cookie, document nonce and HTTP cached resource preserved")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := z.Send(ctx, json.RawMessage(`{"id":901,"method":"tab.create","params":[{"url":"about:blank"}]}`)); err != automation.ErrRevoked {
		t.Fatalf("closed route dispatch: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	protocolMu.Lock()
	data, _ := json.MarshalIndent(methods, "", "  ")
	protocolMu.Unlock()
	t.Logf("protocol command/event inventory: %s", data)
	t.Log("broker completed OnBeforeClose / CefShutdown and exited with code 0")
	owned, cancelOwned := context.WithCancel(ctx)
	b2, err := embeddedbrowser.Start(owned, embeddedbrowser.Config{Executable: executable, Profile: filepath.Join(t.TempDir(), "cancel-profile"), Supervisor: adapter})
	if err != nil {
		cancelOwned()
		t.Fatal(err)
	}
	cancelOwned()
	select {
	case <-b2.Closed():
	case <-ctx.Done():
		t.Fatal("cancelled owner did not close broker")
	}
	if err := b2.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("parent cancellation closed real broker and joined supervisor process-tree barrier")
}
