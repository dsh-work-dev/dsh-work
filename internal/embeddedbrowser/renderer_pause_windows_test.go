//go:build windows

package embeddedbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	win "github.com/local/dsh-work/internal/platform/windows"
	"golang.org/x/sys/windows"
)

// This qualifies a broker-wide unknown-scope pause. Distinct observed processes
// are not evidence for shared-renderer/OOPIF fault coverage.
func TestLiveRendererPauseBlocksQueuedControlAndPreservesHealthyPage(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("explicit actual CEF component required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	var mu sync.Mutex
	type report struct {
		Nonce, Input string
		Tick         int
	}
	reports := map[string]report{}
	release, reading := make(chan struct{}), make(chan struct{}, 1)
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tick":
			var value report
			json.NewDecoder(r.Body).Decode(&value)
			mu.Lock()
			reports[r.URL.Query().Get("role")] = value
			mu.Unlock()
			return
		case "/release":
			select {
			case reading <- struct{}{}:
			default:
			}
			select {
			case <-release:
				fmt.Fprint(w, "private-body-after-fault")
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><input value="initial"><script>const nonce=crypto.randomUUID(),input=document.querySelector('input');input.value='dirty-'+nonce;let tick=0;setInterval(()=>fetch('/tick?role='+new URL(location.href).searchParams.get('role'),{method:'POST',body:JSON.stringify({Nonce:nonce,Input:input.value,Tick:++tick})}),100)</script>`)
	}))
	defer server.Close()
	b, err := Start(ctx, Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: win.NewJobObjectAdapter()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := b.Close()
		t.Logf("native exit=%+v cleanup=%v pending=%t", b.worker.ExitResult(), err, b.CleanupPending())
		if err != nil || b.CleanupPending() {
			t.Error("native cleanup incomplete", err)
		}
	}()
	a, err := b.OpenOwnedWebTarget(ctx, server.URL+"/?role=A")
	if err != nil {
		t.Fatal(err)
	}
	addressB := strings.Replace(server.URL, "127.0.0.1", "localhost", 1) + "/?role=B"
	healthy, err := b.OpenOwnedWebTarget(ctx, addressB)
	if err != nil {
		t.Fatal(err)
	}
	identityA, err := a.OwnedIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	identityB, err := healthy.OwnedIdentity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	childA, err := strconv.ParseUint(strings.Split(identityA.Frame, "-")[0], 16, 32)
	if err != nil {
		t.Fatal(err)
	}
	childB, err := strconv.ParseUint(strings.Split(identityB.Frame, "-")[0], 16, 32)
	if err != nil || childA == childB {
		t.Fatal("no distinct renderer association; refuse injection", identityA, identityB, err)
	}
	var before report
	for {
		mu.Lock()
		av, bv := reports["A"], reports["B"]
		mu.Unlock()
		if av.Tick > 1 && bv.Tick > 1 {
			before = bv
			t.Logf("actual baseline A=%+v B=%+v identities A=%+v B=%+v", av, bv, identityA, identityB)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fixture never ready")
		case <-time.After(20 * time.Millisecond):
		}
	}
	// Locked CEF MakeFrameIdentifier encodes the logical child_id as hex.
	// Locked Chromium passes that same ID in --renderer-client-id. Enumerate only
	// this owned broker's direct children, then hold/revalidate a kernel instance.
	brokerPID := b.worker.Diagnostics().PID
	query := fmt.Sprintf(`Get-CimInstance Win32_Process -Filter 'ParentProcessId=%d' | Where-Object {$_.CommandLine -match '--type=renderer' -and $_.CommandLine -match '--renderer-client-id=([0-9]+)(?: |$)'} | ForEach-Object { $id=[regex]::Match($_.CommandLine,'--renderer-client-id=([0-9]+)').Groups[1].Value; $p=Get-Process -Id $_.ProcessId; [pscustomobject]@{PID=$_.ProcessId;ClientId=[int]$id;Started=$p.StartTime.ToFileTimeUtc().ToString();Executable=$_.ExecutablePath} } | ConvertTo-Json -Compress`, brokerPID)
	data, err := exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", query).Output()
	if err != nil {
		t.Fatal("owned renderer inventory", err)
	}
	var peers []struct {
		PID                 uint32
		ClientId            uint64
		Started, Executable string
	}
	if err = json.Unmarshal(data, &peers); err != nil {
		t.Fatal("owned process inventory shape", string(data), err)
	}
	var pid uint32
	var started uint64
	for _, p := range peers {
		if p.ClientId == childA {
			if pid != 0 {
				t.Fatal("ambiguous renderer instance")
			}
			pid = p.PID
			started, _ = strconv.ParseUint(p.Started, 10, 64)
			if !strings.EqualFold(filepath.Clean(p.Executable), filepath.Clean(exe)) {
				t.Fatal("foreign executable")
			}
		}
	}
	if pid == 0 || started == 0 {
		t.Fatal("actual renderer association unavailable; no injection")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != started {
		t.Fatal("renderer instance changed", err)
	}
	if status, _ := windows.WaitForSingleObject(handle, 0); status != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("renderer no longer live")
	}
	t.Logf("held actual renderer OS pid=%d creation=%d logical child A=%d B=%d broker pid=%d", pid, started, childA, childB, brokerPID)
	agentRoute, err := healthy.OpenOwnedRoute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	end, err := healthy.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identityB, Token: "actual-inflight-before-fault"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"id": 900, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": mustTab(t, identityB.Target)}, "Runtime.evaluate", map[string]any{"expression": "fetch('/release').then(r=>r.text())", "awaitPromise": true, "returnByValue": true}}})
	if err = agentRoute.Send(ctx, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reading:
	case <-ctx.Done():
		t.Fatal("no real inflight renderer read")
	}
	if err = windows.TerminateProcess(handle, 91); err != nil {
		t.Fatal(err)
	}
	if status, err := windows.WaitForSingleObject(handle, 3000); err != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("renderer exit not observed", status, err)
	}
	select {
	case <-b.done:
	case <-ctx.Done():
		t.Fatal("no termination fault")
	}
	evidence, ok := b.RendererTermination()
	if !ok || evidence.ReportedTab != mustTab(t, identityA.Target) || evidence.ReportedBrowser == 0 {
		t.Fatal("missing actual callback association", evidence)
	}
	t.Logf("actual callback=%+v; scope remains unknown/all broker", evidence)
	select {
	case <-b.Closed():
		t.Fatal("pause destroyed broker")
	default:
	}
	// Only this fixture reopens a result observer after Go invalidates the route.
	// Commands below deliberately bypass Go's stopped write gate to prove native
	// enforcement on envelopes already queued/received when a fault occurs.
	observer := &route{broker: b, id: healthy.route.id, messages: healthy.route.messages, done: make(chan struct{})}
	b.mu.Lock()
	b.routes[observer.id] = observer
	b.mu.Unlock()
	read := func(id int) string {
		t.Helper()
		for {
			select {
			case raw := <-observer.messages:
				var result struct {
					ID    int
					Error string
				}
				json.Unmarshal(raw, &result)
				if result.ID == id {
					if strings.Contains(string(raw), "private-body-after-fault") {
						t.Fatal("inflight body leaked")
					}
					return result.Error
				}
			case <-ctx.Done():
				t.Fatal("missing native result")
				return ""
			}
		}
	}
	if got := read(900); got != ErrControlPaused.Error() {
		t.Fatal("inflight result escaped pause", got)
	}
	sendRaw := func(m map[string]any) {
		t.Helper()
		m["generation"] = b.generation
		b.writeMu.Lock()
		e := browsernative.WriteWireMessage(b.conn, m)
		b.writeMu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
	}
	for i, method := range []string{"Runtime.evaluate", "Page.enable"} {
		payload := map[string]any{"id": 901 + i, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": mustTab(t, identityB.Target)}, method, map[string]any{}}}
		sendRaw(map[string]any{"kind": "wire", "route": observer.id, "payload": payload})
		if got := read(901 + i); got != ErrControlPaused.Error() {
			t.Fatal("old guard/navigation wait escaped pause", method, got)
		}
	}
	for _, kind := range []string{"target.begin", "target.attach", "target.navigate", "target.action", "target.present"} {
		id := "paused-" + kind
		ch := make(chan controlResult, 1)
		b.mu.Lock()
		b.controls[id] = ch
		b.mu.Unlock()
		sendRaw(map[string]any{"kind": kind, "route": healthy.owner, "request": id, "arguments": map[string]any{"identity": identityB, "token": "old-identity", "route": "replacement", "url": addressB, "action": "reload"}})
		select {
		case result := <-ch:
			if result.Error != ErrControlPaused.Error() {
				t.Fatal(kind, result)
			}
			t.Log(kind, result.Error)
		case <-ctx.Done():
			t.Fatal(kind, "no rejection")
		}
		b.mu.Lock()
		delete(b.controls, id)
		b.mu.Unlock()
	}
	current, err := healthy.OwnedIdentity(ctx)
	if err != nil || current.Document != identityB.Document || current.Frame != identityB.Frame {
		t.Fatal("healthy identity destroyed", current, err)
	}
	for {
		mu.Lock()
		after := reports["B"]
		mu.Unlock()
		if after.Tick > before.Tick+2 {
			if after.Nonce != before.Nonce || after.Input != before.Input {
				t.Fatal("healthy document/input changed", after, before)
			}
			t.Logf("healthy actual heartbeat after fault=%+v same document=%s", after, current.Document)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("healthy page no longer executes")
		case <-time.After(20 * time.Millisecond):
		}
	}
	if err = end(); err != nil {
		t.Fatal("end blocked", err)
	}
	if err = healthy.RevokeOwned(ctx); err != nil {
		t.Fatal("revoke/drain blocked", err)
	}
	t.Log("inflight result suppressed; end/revoke drained while renderer fetch promise remains blocked")
	releaseOnce.Do(func() { close(release) })
	if _, err = b.controlCall(ctx, "target.hide", healthy.owner, map[string]any{"epoch": "cleanup"}); err != nil {
		t.Fatal("hide blocked", err)
	}
	if err = a.CloseNative(ctx); err != nil {
		t.Fatal("A close blocked", err)
	}
	if err = healthy.CloseNative(ctx); err != nil {
		t.Fatal("B close blocked", err)
	}
}
func mustTab(t *testing.T, id string) int {
	t.Helper()
	v, e := strconv.Atoi(id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
