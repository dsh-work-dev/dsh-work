//go:build windows

package embeddedbrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/automation"
	win "github.com/local/dsh-work/internal/platform/windows"
	"golang.org/x/sys/windows"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLiveClosingLatchRejectsQueuedAdmissionAndWrite(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("requires explicit actual CEF component")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Closing latch</title><p>Actual native target</p>`)
	}))
	defer server.Close()
	b, err := Start(ctx, Config{Executable: exe, Profile: filepath.Join(t.TempDir(), "profile"), Supervisor: win.NewJobObjectAdapter()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := b.Close(); err != nil {
			t.Error("actual CEF cleanup", err)
		}
	}()
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
	defer func() {
		if err := route.Close(); err != nil {
			t.Error("actual route cleanup", err)
		}
	}()
	end, err := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: "before-close"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := end(); err != nil {
			t.Error("actual guard cleanup", err)
		}
	}()
	process := b.worker.Diagnostics()
	// Read only this fixture's own process threads. Its initial thread runs the
	// CEF UI loop; keeping the reader running lets all negative commands queue
	// before that UI loop can execute native destruction. Always restore it.
	cmd := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf("(Get-Process -Id %d).Threads | Sort-Object StartTime | Select-Object -First 1 -ExpandProperty Id", process.PID))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := cmd.Output()
	if err != nil {
		t.Fatal("own CEF thread inventory", err)
	}
	tid, nerr := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 32)
	if nerr != nil {
		t.Fatal(nerr)
	}
	thread, err := windows.OpenThread(0x0002|0x0800, false, uint32(tid))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(thread)
	kernel := windows.NewLazySystemDLL("kernel32.dll")
	owner, _, _ := kernel.NewProc("GetProcessIdOfThread").Call(uintptr(thread))
	if owner != uintptr(process.PID) {
		t.Fatal("fixture thread process mismatch")
	}
	suspended, _, suspendErr := kernel.NewProc("SuspendThread").Call(uintptr(thread))
	if uint32(suspended) == ^uint32(0) {
		t.Fatal(suspendErr)
	}
	var once sync.Once
	resume := func() {
		once.Do(func() {
			v, _, e := kernel.NewProc("ResumeThread").Call(uintptr(thread))
			if uint32(v) == ^uint32(0) {
				t.Error("resume own CEF UI thread", e)
			}
		})
	}
	defer resume()
	closeResult, beginResult, stateResult := make(chan controlResult, 1), make(chan controlResult, 1), make(chan controlResult, 1)
	b.mu.Lock()
	b.controls["latch-close"] = closeResult
	b.controls["latch-begin"] = beginResult
	b.controls["latch-state"] = stateResult
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		delete(b.controls, "latch-close")
		delete(b.controls, "latch-begin")
		delete(b.controls, "latch-state")
		b.mu.Unlock()
	}()
	for _, m := range []map[string]any{
		{"kind": "target.close", "route": target.owner, "request": "latch-close"},
		{"kind": "target.begin", "route": target.owner, "request": "latch-begin", "arguments": automation.OwnedBrowserCall{Identity: identity, Token: "must-not-reopen"}},
		{"kind": "target.state", "route": target.owner, "request": "latch-state"},
	} {
		if err = b.write(m); err != nil {
			t.Fatal(err)
		}
	}
	tab, _ := strconv.Atoi(identity.Target)
	raw, _ := json.Marshal(map[string]any{"id": 919, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, "Runtime.evaluate", map[string]any{"expression": "document.documentElement.dataset.bad='after-close'"}}})
	if err = route.Send(ctx, raw); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	resume()
	read := func(ch <-chan controlResult) controlResult {
		select {
		case v := <-ch:
			return v
		case <-ctx.Done():
			t.Fatal(ctx.Err())
			return controlResult{}
		}
	}
	if v := read(beginResult); v.Error != "CEF_DOCUMENT_AUTHORIZATION_CHANGED" {
		t.Fatal("queued admission after close was not rejected by closing latch", v.Error)
	}
	if v := read(stateResult); v.Error != "CEF_TARGET_CLOSING" {
		t.Fatal("queued state did not observe irreversible closing before destruction", v.Error)
	}
	for {
		select {
		case raw := <-route.Messages():
			var m struct {
				ID    int
				Error string
			}
			json.Unmarshal(raw, &m)
			if m.ID == 919 {
				if m.Error != "CEF_TARGET_CLOSING" {
					t.Fatal("post-close write dispatch escaped", m.Error)
				}
				goto rejected
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
rejected:
	if v := read(closeResult); v.Error != "" {
		t.Fatal("native close barrier", v.Error)
	}
	select {
	case <-target.nativeClosed:
	default:
		t.Fatal("close ACK preceded final target/frame barrier")
	}
	t.Log("actual CEF queued close→begin/state/write before native destruction: admission, identity, and write rejected; final frame-close barrier completed")
}
