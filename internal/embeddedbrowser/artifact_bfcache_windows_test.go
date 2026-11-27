//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/supervisor"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type bfcacheOwner struct {
	supervisor.Adapter
	worker supervisor.Worker
}

func (a *bfcacheOwner) Start(ctx context.Context, p supervisor.LaunchPlan, f supervisor.RawOutputHandler) (supervisor.Worker, error) {
	w, e := a.Adapter.Start(ctx, p, f)
	a.worker = w
	return w, e
}

// A fresh artifact separates cache eligibility from the larger isolation matrix.
// Navigation is observed from Page events, never by evaluating the cached page.
func TestLiveArtifactBFCacheRetainedRevocation(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("explicit official candidate native executable required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	root := t.TempDir()
	html := `<!doctype html><title>Clean artifact history</title><script>window.documentNonce=crypto.randomUUID();window.persisted=false;addEventListener('pageshow',e=>window.persisted=e.persisted);addEventListener('pagehide',e=>localStorage.setItem('left:'+documentNonce,String(e.persisted)));</script><p>Clean document</p>`
	for _, name := range []string{"index.html", "other.html"} {
		if e := os.WriteFile(filepath.Join(root, name), []byte(html), 0600); e != nil {
			t.Fatal(e)
		}
	}
	owner := &bfcacheOwner{Adapter: win.NewJobObjectAdapter()}
	b, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: owner})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("self-owned native pid=%d creation=%d", owner.worker.Diagnostics().PID, owner.worker.Diagnostics().ProcessStarted)
	defer func() {
		e := b.Close()
		t.Logf("native cleanup err=%v pending=%t exit=%+v", e, b.CleanupPending(), owner.worker.ExitResult())
		if e != nil || b.CleanupPending() {
			t.Error("native lifecycle cleanup unfinished/abnormal", e)
		}
	}()
	artifact, err := b.OpenArtifact(ctx, root, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	choice, err := artifact.Select(ctx, automation.Request{Session: "isolated-bfcache-evidence", Lifetime: "one-owned-run"})
	if err != nil {
		t.Fatal(err)
	}
	route, err := choice.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	tab := 0
	sequence := 0
	var navigations []string
	var reasons []json.RawMessage
	consume := func(raw json.RawMessage) (int, json.RawMessage, string) {
		var m struct {
			ID     int
			Method string
			Result json.RawMessage
			Error  string
			Params []json.RawMessage
		}
		_ = json.Unmarshal(raw, &m)
		if m.Method == "tab.created" {
			var v struct{ ID int }
			_ = json.Unmarshal(m.Params[0], &v)
			tab = v.ID
		}
		if m.Method == "devtools.event" && len(m.Params) >= 3 {
			var name string
			_ = json.Unmarshal(m.Params[1], &name)
			if name == "Page.frameNavigated" {
				var p struct {
					Frame struct{ URL, ParentID string }
				}
				_ = json.Unmarshal(m.Params[2], &p)
				if p.Frame.ParentID == "" {
					navigations = append(navigations, p.Frame.URL)
				}
			}
			if name == "Page.backForwardCacheNotUsed" {
				reasons = append(reasons, append(json.RawMessage(nil), m.Params[2]...))
				t.Log("BFCache not restored:", string(m.Params[2]))
			}
		}
		return m.ID, m.Result, m.Error
	}
	next := func() json.RawMessage {
		t.Helper()
		select {
		case m := <-route.Messages():
			return m
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-route.Done():
			t.Fatal(route.Err())
		}
		return nil
	}
	for tab == 0 {
		consume(next())
	}
	rpc := func(method string, args map[string]any) json.RawMessage {
		t.Helper()
		sequence++
		id := sequence
		raw, _ := json.Marshal(map[string]any{"id": id, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, method, args}})
		if e := route.Send(ctx, raw); e != nil {
			t.Fatal(e)
		}
		for {
			mid, result, e := consume(next())
			if mid == id {
				if e != "" {
					t.Fatal(method, e)
				}
				return result
			}
		}
	}
	evaluate := func(expression string) json.RawMessage {
		t.Helper()
		raw := rpc("Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "awaitPromise": true})
		var r struct {
			Result           struct{ Value json.RawMessage }
			ExceptionDetails json.RawMessage
		}
		_ = json.Unmarshal(raw, &r)
		if len(r.ExceptionDetails) > 0 {
			t.Fatal(string(raw))
		}
		return r.Result.Value
	}
	rpc("Page.enable", map[string]any{})
	// Finish the first load before establishing the navigation baseline.
	var original string
	for n := 0; n < 100; n++ {
		var v struct{ Nonce, Ready string }
		_ = json.Unmarshal(evaluate("({nonce:window.documentNonce,ready:document.readyState})"), &v)
		if v.Nonce != "" && v.Ready == "complete" {
			original = v.Nonce
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if original == "" {
		t.Fatal("initial document did not load")
	}
	navigate := func(expression, url string) {
		t.Helper()
		start := len(navigations)
		evaluate(expression)
		for {
			found := false
			for _, seen := range navigations[start:] {
				if seen == url {
					found = true
				}
			}
			if found {
				break
			}
			consume(next())
		}
		time.Sleep(80 * time.Millisecond)
	}
	navigate("setTimeout(()=>location.assign('/other.html'),0);true", artifact.Origin()+"/other.html")
	var second struct{ Nonce string }
	_ = json.Unmarshal(evaluate("({nonce:documentNonce})"), &second)
	if second.Nonce == "" || second.Nonce == original {
		t.Fatal("second document missing")
	}
	navigate("setTimeout(()=>history.back(),0);true", artifact.Origin()+"/index.html")
	var back struct {
		Nonce     string
		Persisted bool
		Reasons   json.RawMessage
	}
	_ = json.Unmarshal(evaluate("({nonce:documentNonce,persisted, reasons:performance.getEntriesByType('navigation')[0]?.notRestoredReasons||null})"), &back)
	t.Logf("minimal artifact back original=%s second=%s back=%s persisted=%t notRestored=%s events=%d", original, second.Nonce, back.Nonce, back.Persisted, back.Reasons, len(reasons))
	if back.Persisted && back.Nonce != original {
		t.Fatal("persisted nonce mismatch")
	}
	if !back.Persisted {
		t.Fatal("required retained-document restore not entered; diagnostic reasons above")
	}
	key, _ := json.Marshal("left:" + second.Nonce)
	var retained string
	_ = json.Unmarshal(evaluate("localStorage.getItem("+string(key)+")"), &retained)
	if retained != "true" {
		t.Fatal("B pagehide did not confirm retention", retained)
	}
	t.Logf("current A restored; B nonce=%s pagehide.persisted=%s without evaluating cached B", second.Nonce, retained)
	if e := artifact.Revoke(ctx); e != nil {
		t.Fatal(e)
	}
	t.Logf("artifact final frame barrier=%+v", artifact.Stats())
	if artifact.Stats().ClosedFrames < 2 || artifact.Stats().ClosedBrowsers != 1 || !artifact.Stats().PartitionRetired {
		t.Fatal(fmt.Sprint(artifact.Stats()))
	}
	oldReads := artifact.Stats().ReadRequests
	if _, err := artifact.Select(ctx, automation.Request{Session: "isolated-bfcache-evidence", Lifetime: "one-owned-run"}); !errors.Is(err, embeddedbrowser.ErrArtifactRevoked) {
		t.Fatal("revoked lease admitted", err)
	}
	oldMessage, _ := json.Marshal(map[string]any{"id": 999, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, "Runtime.evaluate", map[string]any{"expression": "fetch('/index.html')"}}})
	if err := route.Send(ctx, oldMessage); !errors.Is(err, embeddedbrowser.ErrArtifactRevoked) {
		t.Fatal("revoked route accepted dispatch", err)
	}
	fresh, err := b.OpenArtifact(ctx, root, "index.html")
	if err != nil {
		t.Fatal(err)
	}
	freshChoice, err := fresh.Select(ctx, automation.Request{Session: "fresh-artifact", Lifetime: "one-owned-run"})
	if err != nil {
		t.Fatal(err)
	}
	route, err = freshChoice.Open(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	tab = 0
	for tab == 0 {
		consume(next())
	}
	freshReady := false
	for n := 0; n < 100; n++ {
		var state struct{ Origin, Ready string }
		_ = json.Unmarshal(evaluate("({origin:location.origin,ready:document.readyState})"), &state)
		if state.Origin == fresh.Origin() && state.Ready == "complete" {
			freshReady = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !freshReady {
		t.Fatal("fresh principal did not finish loading")
	}
	oldURL, _ := json.Marshal(artifact.Origin() + "/index.html")
	var denied bool
	_ = json.Unmarshal(evaluate("fetch("+string(oldURL)+").then(()=>false,()=>true)"), &denied)
	if !denied || artifact.Stats().ReadRequests != oldReads {
		t.Fatal("fresh partition reached revoked resource authority")
	}
	if err := fresh.Revoke(ctx); err != nil {
		t.Fatal(err)
	}
	if b.ArtifactCount() != 0 {
		t.Fatal("lease/root registry not retired")
	}
	t.Log("retained B + restored A revoked; old selection/route rejected; fresh partition old-origin fetch denied; both roots/partitions retired")
}
