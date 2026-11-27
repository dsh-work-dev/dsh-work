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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/windows"
)

type policyAdapter struct {
	supervisor.Adapter
	worker supervisor.Worker
}

func (a *policyAdapter) Start(c context.Context, p supervisor.LaunchPlan, h supervisor.RawOutputHandler) (supervisor.Worker, error) {
	w, e := a.Adapter.Start(c, p, h)
	if e == nil {
		a.worker = w
	}
	return w, e
}

// An offscreen, non-activating parent owned by this test. Keep its GUI pump
// alive until the registry has completed the real native close/frame barrier.
func policyParent(t *testing.T) (uintptr, embeddedbrowser.PresenterPeer, func()) {
	t.Helper()
	windows.NewLazySystemDLL("user32.dll").NewProc("SetProcessDpiAwarenessContext").Call(^uintptr(3))
	ready := make(chan uintptr, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		user := windows.NewLazySystemDLL("user32.dll")
		class, _ := windows.UTF16PtrFromString("STATIC")
		title, _ := windows.UTF16PtrFromString("E67 owned policy fixture")
		h, _, _ := user.NewProc("CreateWindowExW").Call(0x08000000, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), 0x90000000, uintptr(uint32(0xffff8300)), 0, 800, 600, 0, 0, 0, 0)
		ready <- h
		if h == 0 {
			return
		}
		type message struct {
			H       uintptr
			M       uint32
			W, L    uintptr
			Time    uint32
			X, Y    int32
			Private uint32
		}
		var m message
		for {
			select {
			case <-stop:
				user.NewProc("DestroyWindow").Call(h)
				return
			default:
			}
			for {
				n, _, _ := user.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 1)
				if n == 0 {
					break
				}
				user.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&m)))
				user.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&m)))
			}
			time.Sleep(time.Millisecond)
		}
	}()
	h := <-ready
	if h == 0 {
		t.Fatal("self-owned parent creation failed")
	}
	var a, b, c, d windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &a, &b, &c, &d); err != nil {
		t.Fatal(err)
	}
	peer := embeddedbrowser.PresenterPeer{PID: uint32(os.Getpid()), Started: uint64(a.HighDateTime)<<32 | uint64(a.LowDateTime)}
	return h, peer, func() { close(stop); <-done }
}

func TestLiveManagedMCPFrameDialogDownloadPolicy(t *testing.T) {
	exe, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if exe == "" || deps == "" {
		t.Skip("requires independent actual CEF and official prepared publication")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	root := t.TempDir()
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	var downloadRequests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bytes" {
			downloadRequests.Add(1)
			w.Header().Set("Content-Disposition", `attachment; filename="e67-fixed.txt"`)
			w.Header().Set("Content-Type", "application/octet-stream")
			fmt.Fprint(w, "e67 fixed harmless bytes\n")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/child" {
			fmt.Fprint(w, "<p>Own child</p>")
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Policy fixture</title><button onclick="document.querySelector('output').textContent=String(confirm('fixed fixture confirm'))">Dialog</button><button onclick="let f=document.createElement('iframe');f.src='/child';document.body.append(f)">Frame</button><a href="/bytes" download="e67-fixed.txt">Download</a><button onclick="alert('fixed alert');document.querySelector('output').textContent='alert done'">Alert</button><button onclick="document.querySelector('output').textContent=String(prompt('fixed prompt','default text'))">Prompt</button><button onclick="alert('x'.repeat(9*1024*1024));document.querySelector('output').textContent='long done'">Long message</button><button onclick="let v=prompt('oversized default','x'.repeat(32769));document.querySelector('output').textContent='default:'+(v===null?'cancel':v.length)">Oversized default</button><button onclick="window.onbeforeunload=e=>{e.preventDefault();e.returnValue=''};document.querySelector('output').textContent='armed'">Arm unload</button><a href='/next'>Leave</a><input value="unchanged"><output>ready</output>`)
	}))
	defer fixture.Close()
	acquire := func(dir string) (io.Closer, error) {
		return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
	}
	store, err := embeddedbrowser.OpenInstanceStore(filepath.Join(root, "instances"), acquire)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var broker *embeddedbrowser.Broker
	nativeAdapter := &policyAdapter{Adapter: win.NewJobObjectAdapter()}
	reg, err := embeddedbrowser.NewInstancePresenterRegistry(embeddedbrowser.InstanceRegistryConfig{Store: store, AcquireProfile: acquire, StartBroker: func(c context.Context, i embeddedbrowser.InstanceConfig) (*embeddedbrowser.Broker, error) {
		var e error
		broker, e = embeddedbrowser.Start(c, embeddedbrowser.Config{Executable: exe, Profile: i.Profile, Supervisor: nativeAdapter, ObserveProtocol: func(d, m string) {
			if d == "result" || d == "policy" {
				t.Log("native", d, m)
			}
		}})
		return broker, e
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		e := reg.Close()
		if e != nil {
			t.Error(e)
		}
		if broker != nil {
			t.Logf("native final exit=%+v pending=%v", nativeAdapter.worker.ExitResult(), broker.CleanupPending())
		}
	}()
	window, err := reg.RegisterInstanceWindow(ctx, peer, "policy-only", "window", parent)
	if err != nil {
		t.Fatal(err)
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"dialog", "download", "frame"} {
		t.Run(scenario, func(t *testing.T) {
			slot := "slot-" + scenario
			view, e := reg.Update(ctx, peer, "window", parent, window.Lease, slot, fixture.URL, embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
			if e != nil {
				t.Fatal(e)
			}
			for view.State != "ready" {
				if ctx.Err() != nil {
					t.Fatal(ctx.Err())
				}
				time.Sleep(20 * time.Millisecond)
				view, e = reg.State(ctx, peer, "window", slot)
				if e != nil {
					t.Fatal(e)
				}
			}
			target, e := reg.OwnedWindowViewTarget(peer, "window", parent, window.Lease, slot, view.Lease)
			if e != nil {
				t.Fatal(e)
			}
			m, e := settings.New(settings.Config{Path: filepath.Join(root, scenario, "settings.json")})
			if e != nil {
				t.Fatal(e)
			}
			s := automation.New(m, installedBrowser{deps: deps})
			defer s.Close()
			prompts := make(chan automation.BrowserConsent, 8)
			s.EnableBrowserGateway(automation.BrowserGatewayConfig{Notify: func(p automation.BrowserConsent) { prompts <- p }})
			s.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{Service: s, Supervisor: win.NewJobObjectAdapter(), Root: filepath.Join(root, scenario, "executors"), Resolve: func(context.Context) (browserruntime.Runtime, error) {
				return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
			}}), browserruntime.ToolSchemas())
			s.Worker("policy-fixture", true, true)
			s.Gateway("policy-fixture", true)
			if _, e = s.SetEnabled(ctx, automation.Browser, true); e != nil {
				t.Fatal(e)
			}
			if _, e = s.SetDefaultBrowser(ctx, true); e != nil {
				t.Fatal(e)
			}
			if _, e = s.Retry(automation.Browser); e != nil {
				t.Fatal(e)
			}
			for s.Snapshot().Browser.Dependency != "prepared" {
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			request := automation.Request{Generation: "policy-fixture", Epoch: s.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: scenario, Lifetime: "one", Turn: "one", Title: "Own policy fixture"}
			if _, e = s.BrowserTurn(request); e != nil {
				t.Fatal(e)
			}
			if e = s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: request.Generation, Session: request.Session, Lifetime: request.Lifetime}, target); e != nil {
				t.Fatal(e)
			}
			call := func(tool string, args map[string]any) (any, error) {
				q := request
				q.Tool = tool
				q.Arguments = args
				type result struct {
					d automation.BrowserDecision
					e error
				}
				ready := make(chan result, 1)
				go func() { d, e := s.AuthorizeBrowser(ctx, q); ready <- result{d, e} }()
				for {
					select {
					case p := <-prompts:
						if p.Session != scenario || (p.Kind == "site" && p.Site != fixture.URL) {
							return nil, fmt.Errorf("unexpected consent scope %+v", p)
						}
						if _, e := s.AnswerBrowserConsent(ctx, p.ID, "allow"); e != nil {
							return nil, e
						}
					case r := <-ready:
						if r.e != nil {
							return nil, r.e
						}
						q.Epoch = r.d.Epoch
						q.Permit = r.d.Permit
						return s.Execute(ctx, q)
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			v, e := call("browser_snapshot", map[string]any{})
			if e != nil {
				t.Fatal(e)
			}
			t.Log("managed actual listTools contract and snapshot ready")
			if scenario == "download" {
				pid := nativeAdapter.worker.Diagnostics().PID
				out, err := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf("@(Get-NetTCPConnection -State Listen -OwningProcess %d -ErrorAction SilentlyContinue).Count", pid)).CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) != "0" {
					t.Fatal("native listener sampling", string(out), err)
				}
				t.Logf("actual native PID=%d TCP LISTEN count=%s", pid, strings.TrimSpace(string(out)))
			}

			if scenario == "dialog" {
				before, _ := target.OwnedIdentity(ctx)
				presentDialog := func() automation.OwnedBrowserIdentity {
					t.Helper()
					state, e := reg.State(ctx, peer, "window", slot)
					if e != nil || state.Dialog == nil || state.Dialog.Origin != fixture.URL {
						t.Fatal("missing authenticated panel dialog", state.Dialog, e)
					}
					if e = reg.PresentDialog(ctx, peer, "window", slot, *state.Dialog, false); e != nil {
						t.Fatal("dialog suspend", e)
					}
					suspended, e := target.OwnedIdentity(ctx)
					if e != nil || suspended.ViewVisible || suspended.DialogVisible {
						t.Fatal("suspend granted hidden-page control", suspended, e)
					}
					if done, e := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: suspended, Token: "not-a-dialog-permit"}); e == nil {
						done()
						t.Fatal("ordinary guard accepted suspended page")
					}
					if e = reg.PresentDialog(ctx, peer, "window", slot, *state.Dialog, true); e != nil {
						t.Fatal("dialog ready", e)
					}
					presented, e := target.OwnedIdentity(ctx)
					if e != nil || presented.ViewVisible || !presented.DialogVisible {
						t.Fatal("missing dedicated panel-dialog visibility", presented, e)
					}
					if _, e = call("browser_snapshot", map[string]any{}); e == nil {
						t.Fatal("normal browser snapshot admitted dialog-hidden page")
					}
					return presented
				}
				for _, step := range []struct {
					label, want, text string
					accept            bool
				}{
					{"Alert", "alert done", "", true},
					{"Dialog", "false", "", false},
					{"Prompt", "entered exact", "entered exact", true},
					{"Prompt", "default text", "", true},
					{"Prompt", "null", "", false},
				} {
					v, e = call("browser_click", map[string]any{"target": fmt.Sprintf("getByRole('button', { name: '%s', exact: true })", step.label)})
					raw, _ := json.Marshal(v)
					t.Logf("dialog click %s result=%s error=%v", step.label, raw, e)
					if e != nil {
						t.Fatal(e)
					}
					pending, e := target.OwnedIdentity(ctx)
					if e != nil || pending.Dialog == "" {
						t.Fatal("missing real pending dialog", pending, e)
					}

					presentDialog()
					args := map[string]any{"accept": step.accept}
					if step.text != "" {
						args["promptText"] = step.text
					}
					v, e = call("browser_handle_dialog", args)
					raw, _ = json.Marshal(v)
					t.Logf("dialog handle %s result=%s error=%v", step.label, raw, e)
					if e != nil {
						t.Fatal(e)
					}
					after, e := target.OwnedIdentity(ctx)
					if e != nil || after.Dialog != "" || !after.ViewVisible || after.DialogVisible || after.Document != before.Document {
						t.Fatal("dialog close lost document or pending state", after, e)
					}
					v, e = call("browser_snapshot", map[string]any{})
					raw, _ = json.Marshal(v)
					if e != nil || !strings.Contains(string(raw), step.want) {
						t.Fatal("dialog result missing", string(raw), e)
					}
				}
				if _, e = call("browser_handle_dialog", map[string]any{"accept": true}); e == nil {
					t.Fatal("stale answer accepted with no pending dialog")
				}
				t.Log("no-pending answer rejected; document retained")
				longResult, e := call("browser_click", map[string]any{"target": "getByRole('button', { name: 'Long message', exact: true })"})
				if e != nil {
					t.Fatal("long-message opening", e)
				}
				longRaw, _ := json.Marshal(longResult)
				t.Logf("long message actual SDK result bytes=%d head=%s", len(longRaw), string(longRaw[:min(len(longRaw), 500)]))
				longState, e := reg.State(ctx, peer, "window", slot)
				if longState.Dialog != nil {
					t.Logf("long actual projection: kind=%s truncated=%v bytes=%d", longState.Dialog.Kind, longState.Dialog.Truncated, len(longState.Dialog.Message))
				} else {
					t.Logf("long actual state=%s visible=%v failure=%s", longState.State, longState.Visible, longState.Failure)
				}
				if e != nil || longState.Dialog == nil || len(longState.Dialog.Message) > 16*1024 {
					t.Fatal("long message lacked bounded marked projection", e)
				}
				presentDialog()
				if _, e = call("browser_handle_dialog", map[string]any{"accept": true}); e != nil {
					t.Fatal("bounded message could not be answered", e)
				}
				_, defaultError := call("browser_click", map[string]any{"target": "getByRole('button', { name: 'Oversized default', exact: true })"})
				if defaultError != nil {
					if !strings.Contains(defaultError.Error(), "CEF_DIALOG_PAYLOAD_TOO_LARGE") {
						t.Fatal(defaultError)
					}
					t.Log("actual CEF default exceeded host input budget; typed cancellation observed")
				} else {
					defaultState, e := reg.State(ctx, peer, "window", slot)
					if e != nil || defaultState.Dialog == nil || len(defaultState.Dialog.DefaultPrompt) > 32*1024 {
						t.Fatal("actual prompt default not bounded", e)
					}
					actualDefaultLength := len(defaultState.Dialog.DefaultPrompt)
					t.Logf("actual pinned CEF default bytes=%d from requested32769; host preserves this real input unchanged", actualDefaultLength)
					presentDialog()
					if _, e = call("browser_handle_dialog", map[string]any{"accept": true}); e != nil {
						t.Fatal("accept default prompt", e)
					}
					snapshot, e := call("browser_snapshot", map[string]any{})
					raw, _ := json.Marshal(snapshot)
					if e != nil || !strings.Contains(string(raw), fmt.Sprintf("default:%d", actualDefaultLength)) {
						t.Fatal("omitted promptText did not preserve actual CEF default", e)
					}
				}
				afterBudget, e := target.OwnedIdentity(ctx)
				if e != nil || afterBudget.Document != before.Document || afterBudget.Dialog != "" {
					t.Fatal("budget refusal lost page or left callback", afterBudget, e)
				}
				if _, e = call("browser_snapshot", map[string]any{}); e != nil {
					t.Fatal("budget refusal poisoned next permitted observation", e)
				}
				t.Log("9MiB website source message remained bounded and answerable; real default contract checked; original document and next snapshot retained")
				if _, e = call("browser_click", map[string]any{"target": "getByRole('button', { name: 'Alert', exact: true })"}); e != nil {
					t.Fatal(e)
				}
				presentDialog()
				if e = reg.Hide(ctx, peer, "window"); e != nil {
					t.Fatal(e)
				}
				hidden, e := target.OwnedIdentity(ctx)
				if e != nil || hidden.Dialog != "" || hidden.DialogVisible || hidden.ViewVisible {
					t.Fatal("hide did not cancel panel dialog", hidden, e)
				}
				view, e = reg.Update(ctx, peer, "window", parent, window.Lease, slot, "", embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
				if e != nil {
					t.Fatal(e)
				}
				v, e = call("browser_snapshot", map[string]any{})
				rawHidden, _ := json.Marshal(v)
				if e != nil || !strings.Contains(string(rawHidden), "alert done") {
					t.Fatal("show after cancel did not retain page", string(rawHidden), e)
				}
				t.Log("panel hide canceled pending callback; re-present retained original document and usable snapshot")
				if _, e = call("browser_click", map[string]any{"target": "getByRole('button', { name: 'Arm unload', exact: true })"}); e != nil {
					t.Fatal(e)
				}
				v, e = call("browser_click", map[string]any{"target": "getByRole('link', { name: 'Leave', exact: true })"})
				raw, _ := json.Marshal(v)
				t.Logf("beforeunload click result=%s error=%v", raw, e)
				if e != nil {
					t.Fatal(e)
				}
				pending, e := target.OwnedIdentity(ctx)
				if e != nil || pending.Dialog == "" {
					t.Fatal("beforeunload missing", pending, e)
				}
				pending = presentDialog()
				v, e = call("browser_handle_dialog", map[string]any{"accept": false})
				raw, _ = json.Marshal(v)
				t.Logf("beforeunload cancel result=%s error=%v", raw, e)
				if e != nil {
					t.Fatal(e)
				}
				after, e := target.OwnedIdentity(ctx)
				if e != nil || after.Dialog != "" || after.Document != before.Document || after.URL != before.URL {
					t.Fatal("beforeunload cancellation lost original document", after, e)
				}
				v, e = call("browser_snapshot", map[string]any{})
				raw, _ = json.Marshal(v)
				if e != nil || !strings.Contains(string(raw), "armed") {
					t.Fatal("beforeunload cancellation lost page state", string(raw), e)
				}
				t.Log("actual beforeunload cancel retained original document, URL and dirty page state")
				if _, e = call("browser_click", map[string]any{"target": "getByRole('link', { name: 'Leave', exact: true })"}); e != nil {
					t.Fatal(e)
				}
				presentDialog()
				if cleanup, e := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: pending, Token: "expired-dialog-fixture", DialogOnly: true}); e == nil {
					cleanup()
					t.Fatal("old pending-dialog identity installed a new dialog guard")
				}
				state, e := reg.State(ctx, peer, "window", slot)
				if e != nil || state.Dialog == nil || state.Dialog.Origin != fixture.URL {
					t.Fatal("host dialog missing authenticated origin metadata", state.Dialog, e)
				}
				old := *state.Dialog
				wrong := old
				wrong.Epoch += "-expired"
				if e = reg.AnswerDialog(ctx, peer, "window", slot, wrong, true); e == nil {
					t.Fatal("expired host answer accepted")
				}
				if e = reg.AnswerDialog(ctx, peer, "window", slot, old, false); e != nil {
					t.Fatal("host cancellation failed", e)
				}
				if e = reg.AnswerDialog(ctx, peer, "window", slot, old, true); e == nil {
					t.Fatal("duplicate host answer accepted")
				}
				after, e = target.OwnedIdentity(ctx)
				if e != nil || after.Dialog != "" || after.Document != before.Document {
					t.Fatal("host cancellation lost original document", after, e)
				}
				t.Log("old pending guard, expired/duplicate host answers rejected; host cancel retained original document and true origin")
				if e = target.CloseNative(ctx); e != nil {
					t.Fatal(e)
				}
				return
			}
			label := map[string]string{"dialog": "Dialog", "download": "Download", "frame": "Frame"}[scenario]
			role := "button"
			if scenario == "download" {
				role = "link"
			}
			before, _ := target.OwnedIdentity(ctx)
			v, e = call("browser_click", map[string]any{"target": fmt.Sprintf("getByRole('%s', { name: '%s', exact: true })", role, label)})
			raw, _ := json.Marshal(v)
			t.Logf("real MCP %s result=%s error=%v", scenario, raw, e)
			if scenario == "frame" {
				after, e := target.OwnedIdentity(ctx)
				if e != nil || after.FrameScopeReady || after.ScopeEpoch == before.ScopeEpoch {
					t.Fatal("actual frame scope not observed", after, e)
				}
				if e == nil {
					_, e = call("browser_snapshot", map[string]any{})
				}
				if e == nil {
					t.Fatal("iframe snapshot admitted")
				}
				t.Log("actual frame snapshot rejected", e)
			}
			if scenario == "download" {
				if e == nil || !strings.Contains(e.Error(), "CEF_DOWNLOAD_UNSUPPORTED") {
					t.Fatal("download was not explicitly rejected", e)
				}
				if downloadRequests.Load() != 0 {
					t.Fatal("download denial issued unexpected HTTP request", downloadRequests.Load())
				}
				var found []string
				filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
					if e == nil && !d.IsDir() && (strings.HasSuffix(p, ".crdownload") || d.Name() == "e67-fixed.txt") {
						found = append(found, p)
					}
					return nil
				})
				if len(found) > 0 {
					t.Fatal("unexpected download output", found)
				}
				t.Log("actual CanDownload denied fixed link before HTTP request; explicit CEF_DOWNLOAD_UNSUPPORTED; no file in isolated roots")
			}
			if scenario != "frame" {
				after, e := target.OwnedIdentity(ctx)
				if e != nil || after.Document != before.Document {
					t.Fatal("policy refusal lost document", after, e)
				}
			}
			// A policy refusal must not create a native modal or pause the broker.
			user := windows.NewLazySystemDLL("user32.dll")
			var visibleNative int
			callback := windows.NewCallback(func(h, l uintptr) uintptr {
				var pid uint32
				user.NewProc("GetWindowThreadProcessId").Call(h, uintptr(unsafe.Pointer(&pid)))
				visible, _, _ := user.NewProc("IsWindowVisible").Call(h)
				if pid == uint32(nativeAdapter.worker.Diagnostics().PID) && visible != 0 {
					visibleNative++
				}
				return 1
			})
			user.NewProc("EnumWindows").Call(callback, 0)
			if visibleNative != 0 || broker.ControlPaused() {
				t.Fatal("policy denial created modal or faulted broker", visibleNative)
			}
			t.Log("actual native top-level visible windows=0; broker unpaused; document retained")
			if e := target.CloseNative(ctx); e != nil {
				t.Fatal("native close/frame barrier", e)
			}
		})
	}
}
