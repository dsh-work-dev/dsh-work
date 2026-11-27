//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
)

func TestLiveCanceledAdmissionAndNavigationGuard(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("requires actual CEF")
	}
	ctx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!doctype html><input aria-label="Input"><p>real target</p>`))
	}))
	defer server.Close()
	var cancelBegin context.CancelFunc
	b, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: win.NewJobObjectAdapter(), ObserveProtocol: func(direction, method string) {
		if direction == "control" && method == "target.begin-dispatched" && cancelBegin != nil {
			cancel := cancelBegin
			cancelBegin = nil
			cancel()
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
	identity, err := target.OwnedIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	route, err := target.OpenOwnedRoute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer route.Close()
	tab, _ := strconv.Atoi(identity.Target)
	sequence := 0
	rpc := func(method string, params map[string]any) (json.RawMessage, string) {
		t.Helper()
		sequence++
		id := sequence
		raw, _ := json.Marshal(map[string]any{"id": id, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, method, params}})
		if err := route.Send(ctx, raw); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case raw := <-route.Messages():
				var result struct {
					ID     int
					Result json.RawMessage
					Error  string
				}
				json.Unmarshal(raw, &result)
				if result.ID == id {
					return result.Result, result.Error
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil, "timeout"
			}
		}
	}
	beginCtx, cancel := context.WithCancel(ctx)
	cancelBegin = cancel
	if _, err = target.BeginOwnedCall(beginCtx, automation.OwnedBrowserCall{Identity: identity, Token: "canceled-after-write"}); !errors.Is(err, context.Canceled) {
		t.Fatal("begin was not canceled after actual write", err)
	}
	if _, failure := rpc("Runtime.evaluate", map[string]any{"expression": "document.documentElement.dataset.bad='orphan'"}); failure != "CEF_DOCUMENT_AUTHORIZATION_CHANGED" {
		t.Fatal("canceled begin retained an effective guard", failure)
	}
	t.Log("actual target.begin was dispatched then caller canceled; independent revoke ACK left no effective native guard")
	end, err := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "old-document-navigation", NavigationOrigin: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, failure := rpc("Page.navigate", map[string]any{"url": server.URL + "/?next=1"}); failure != "" {
		t.Fatal(failure)
	}
	var current automation.OwnedBrowserIdentity
	for {
		current, err = target.OwnedIdentity(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if current.Document != identity.Document {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	for method, params := range map[string]map[string]any{
		"Input.insertText":       {"text": "old-permit-write"},
		"Runtime.evaluate":       {"expression": "document.documentElement.dataset.bad='old-write'", "returnByValue": true},
		"Runtime.callFunctionOn": {"functionDeclaration": "function(){document.documentElement.dataset.bad='old-function'}", "executionContextId": 0},
	} {
		if _, failure := rpc(method, params); failure != "CEF_DOCUMENT_AUTHORIZATION_CHANGED" {
			t.Fatal("old navigation guard inherited write authority", method, failure)
		}
		t.Log("post-navigation old guard refused", method)
	}
	if _, failure := rpc("Page.enable", map[string]any{}); failure != "" {
		t.Fatal("necessary navigation lifecycle was not admitted", failure)
	}
	if err = end(); err != nil {
		t.Fatal(err)
	}
	end, err = target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: current, Token: "fresh-document-observation"})
	if err != nil {
		t.Fatal(err)
	}
	raw, failure := rpc("Runtime.evaluate", map[string]any{"expression": "[document.querySelector('input').value,document.documentElement.dataset.bad||'']", "returnByValue": true})
	if failure != "" {
		t.Fatal(failure)
	}
	var value struct{ Result struct{ Value []string } }
	json.Unmarshal(raw, &value)
	if len(value.Result.Value) != 2 || value.Result.Value[0] != "" || value.Result.Value[1] != "" {
		t.Fatal("new document was modified by old guard", string(raw))
	}
	if err = end(); err != nil {
		t.Fatal(err)
	}
	t.Log("fresh document token observed untouched input and dataset; only navigation lifecycle continued under old permit")
}
