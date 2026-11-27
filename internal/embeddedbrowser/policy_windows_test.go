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
	var manualDialog atomic.Bool
	var deniedDialogs atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manual" {
			if manualDialog.Swap(false) {
				fmt.Fprint(w, "fire")
			}
			return
		}
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
		fmt.Fprint(w, `<!doctype html><title>Policy fixture</title><button onclick="document.querySelector('output').textContent=String(confirm('fixed fixture confirm'))">Dialog</button><button onclick="let f=document.createElement('iframe');f.src='/child';document.body.append(f)">Frame</button><a href="/bytes" download="e67-fixed.txt">Download</a><input value="unchanged"><output>ready</output><script>setInterval(async()=>{if(await fetch('/manual').then(r=>r.text())==='fire')confirm('manual fixture')},100)</script>`)
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
			if d == "policy" && m == "CEF_DIALOG_UNSUPPORTED" {
				deniedDialogs.Add(1)
			}
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
				manualDialog.Store(true)
				for deniedDialogs.Load() == 0 {
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(10 * time.Millisecond):
					}
				}
				if _, e = call("browser_snapshot", map[string]any{}); e != nil {
					t.Fatal("manual dialog poisoned unrelated call", e)
				}
				t.Log("manual dialog diagnosed without guard; subsequent real snapshot succeeded")
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
			if scenario == "dialog" && (e == nil || !strings.Contains(e.Error(), "CEF_DIALOG_UNSUPPORTED")) {
				t.Fatal("dialog was not explicitly rejected", e)
			}
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
