//go:build windows

package embeddedbrowser

import (
	"context"
	"encoding/json"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	win "github.com/local/dsh-work/internal/platform/windows"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveBrokerRejectsPriorGenerationEnvelope(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("explicit actual CEF executable required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	launch := func() *Broker {
		t.Helper()
		b, e := Start(ctx, Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: win.NewJobObjectAdapter()})
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	previous := launch()
	oldGeneration := previous.generation
	if e := previous.Close(); e != nil {
		t.Fatal("previous real broker join", e)
	}
	var forbidden atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/forbidden" {
			forbidden.Add(1)
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<!doctype html><input value='preserved'>"))
	}))
	defer fixture.Close()
	b := launch()
	defer func() {
		if e := b.Close(); e != nil {
			t.Error("current broker join", e)
		}
	}()
	if b.generation == oldGeneration {
		t.Fatal("launch reused generation")
	}
	target, e := b.OpenOwnedWebTarget(ctx, fixture.URL)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := target.OwnedIdentity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	end, e := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "current-call"})
	if e != nil {
		t.Fatal(e)
	}
	defer end()
	tab, _ := strconv.Atoi(identity.Target)
	// Use the actual authenticated broker connection, bypassing the Go writer's
	// generation stamp solely to replay an old launch envelope into native CEF.
	stale := []map[string]any{
		{"kind": "target.navigate", "route": target.owner, "request": "stale-navigation", "arguments": map[string]any{"url": fixture.URL + "/forbidden"}},
		{"kind": "wire", "route": target.owner, "payload": map[string]any{"id": 901, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, "Runtime.evaluate", map[string]any{"expression": "document.documentElement.dataset.replay='executed'", "returnByValue": true}}}},
		{"kind": "target.close", "route": target.owner, "request": "stale-close"},
		{"kind": "shutdown"},
	}
	b.writeMu.Lock()
	for _, m := range stale {
		m["generation"] = oldGeneration
		if e := browsernative.WriteWireMessage(b.conn, m); e != nil {
			b.writeMu.Unlock()
			t.Fatal(e)
		}
	}
	b.writeMu.Unlock()
	// This valid subsequent request is an ordering barrier on native's UI task
	// queue; rejection is not inferred merely from a timeout or missing ACK.
	after, e := target.OwnedIdentity(ctx)
	if e != nil {
		t.Fatal("valid state after replay", e)
	}
	if after.Document != identity.Document || after.URL != identity.URL || forbidden.Load() != 0 {
		t.Fatalf("old generation changed target: before=%+v after=%+v forbidden=%d", identity, after, forbidden.Load())
	}
	raw, _ := json.Marshal(map[string]any{"id": 902, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, "Runtime.evaluate", map[string]any{"expression": "[document.querySelector('input').value,document.documentElement.dataset.replay||'untouched']", "returnByValue": true}}})
	if e := target.route.Send(ctx, raw); e != nil {
		t.Fatal(e)
	}
	for {
		select {
		case message := <-target.route.Messages():
			var result struct {
				ID     int
				Error  string
				Result json.RawMessage
			}
			if json.Unmarshal(message, &result) != nil {
				t.Fatal("bad result")
			}
			if result.ID == 901 {
				t.Fatal("stale wire dispatched")
			}
			if result.ID != 902 {
				continue
			}
			if result.Error != "" || !strings.Contains(string(result.Result), `"preserved","untouched"`) {
				t.Fatalf("current readback=%s error=%s", result.Result, result.Error)
			}
			t.Log("real prior broker closed/joined; old generation navigation, writable Runtime, close and shutdown envelopes rejected on current authenticated pipe; current document/input survived and valid readback succeeded")
			return
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}
