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
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
	"golang.org/x/sys/windows"
)

func TestLiveWindowsFilePickerBoundary(t *testing.T) {
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
		fmt.Fprint(w, `<!doctype html><title>File fixture</title><input type="file" id="file" onchange="this.files[0].text().then(text=>document.querySelector('output').textContent=this.files[0].name+'='+text)" oncancel="document.querySelector('output').textContent='canceled'"><button onclick="document.querySelector('#file').value='';setTimeout(()=>document.querySelector('#file').click(),1500)">Choose later</button><output>ready</output>`)
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
			if d == "result" || d == "policy" || d == "control" {
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
	for _, scenario := range []string{"file"} {
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
			var v any
			// The manager becoming ready can precede the page's final initial
			// document commit. Honor the normal fresh-observation contract;
			// retry only this explicit fence, with a new permit each time.
			for attempt := 0; attempt < 3; attempt++ {
				v, e = call("browser_snapshot", map[string]any{})
				if e == nil {
					break
				}
				if !strings.Contains(e.Error(), "BROWSER_PAGE_CHANGED") || attempt == 2 {
					t.Fatal(e)
				}
				t.Log("initial document advanced; requesting a fresh authorized snapshot")
			}
			t.Log("managed actual listTools contract and snapshot ready")
			script := os.Getenv("DSH_WORK_PICKER_UIA_SCRIPT")
			pid := uint32(nativeAdapter.worker.Diagnostics().PID)
			process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
			if err != nil {
				t.Fatal(err)
			}
			var created, exited, kernel, user windows.Filetime
			if err = windows.GetProcessTimes(process, &created, &exited, &kernel, &user); err != nil {
				t.Fatal(err)
			}
			windows.CloseHandle(process)
			started := uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
			own := filepath.Join(t.TempDir(), "e71-own-selected.txt")
			if err = os.WriteFile(own, []byte("own-file-bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			picker := func(t *testing.T, action string) error {
				t.Helper()
				command := exec.CommandContext(ctx, `C:/Program Files/PowerShell/7/pwsh.exe`, "-NoProfile", "-File", script, "-FixturePid", fmt.Sprint(pid), "-Started", fmt.Sprint(started), "-Owner", fmt.Sprint(parent), "-Action", action, "-OwnTempFile", own, "-ExpectedTitle", "为 "+fixture.URL+" 选择文件")
				command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
				out, err := command.CombinedOutput()
				t.Logf("scoped picker %s: %s", action, out)
				return err
			}
			// Directly observe only this fixture's exact PID/birth/owner/title.
			// This performs no input or window operation.
			waitPicker := func(t *testing.T, wanted bool) {
				t.Helper()
				dll := windows.NewLazySystemDLL("user32.dll")
				var found bool
				callback := windows.NewCallback(func(hwnd, unused uintptr) uintptr {
					var actual uint32
					dll.NewProc("GetWindowThreadProcessId").Call(hwnd, uintptr(unsafe.Pointer(&actual)))
					if actual != pid {
						return 1
					}
					owner, _, _ := dll.NewProc("GetWindow").Call(hwnd, 4)
					if owner != parent {
						return 1
					}
					var text [1024]uint16
					dll.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)))
					if windows.UTF16ToString(text[:]) == "为 "+fixture.URL+" 选择文件" {
						found = true
						return 0
					}
					return 1
				})
				deadline := time.Now().Add(8 * time.Second)
				for {
					h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
					if err != nil {
						t.Fatal(err)
					}
					var a, b, c, d windows.Filetime
					err = windows.GetProcessTimes(h, &a, &b, &c, &d)
					alive, _ := windows.WaitForSingleObject(h, 0)
					windows.CloseHandle(h)
					if err != nil || uint64(a.HighDateTime)<<32|uint64(a.LowDateTime) != started || alive != uint32(windows.WAIT_TIMEOUT) {
						t.Fatal("fixture process identity lost", err)
					}
					found = false
					dll.NewProc("EnumWindows").Call(callback, 0)
					if found == wanted {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("exact fixture picker visible=%v wanted=%v", found, wanted)
					}
					time.Sleep(30 * time.Millisecond)
				}
			}

			// Optional successful selection gate remains explicit when the
			// machine disallows the scoped UIA fixture execution path.
			t.Run("user-selection", func(t *testing.T) {
				if script == "" {
					t.Skip("successful native selection awaits an authorized UIA execution path")
				}
				t.Cleanup(func() {
					cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
					defer stop()
					if cleanupErr := target.RevokeOwned(cleanup); cleanupErr != nil {
						probe, probeStop := context.WithTimeout(context.Background(), time.Second)
						defer probeStop()
						phase, phaseErr := embeddedbrowser.NativePickerStateForTest(probe, target)
						t.Errorf("selection cleanup did not revoke picker: %v; native picker=%s probe=%v", cleanupErr, phase, phaseErr)
						return
					}
					waitPicker(t, false)
				})
				if _, e = call("browser_click", map[string]any{"target": "getByRole('button', {name:'Choose later',exact:true})"}); e != nil {
					t.Fatal(e)
				}
				action := "select"
				if os.Getenv("DSH_WORK_PICKER_UIA_DIAGNOSE") == "1" {
					action = "diagnose"
				}
				if pickerErr := picker(t, action); pickerErr != nil {
					t.Fatal("scoped picker selection failed", pickerErr)
				}
				if action == "diagnose" {
					return
				}
				time.Sleep(250 * time.Millisecond)
				v, e = call("browser_snapshot", map[string]any{})
				raw, _ := json.Marshal(v)
				if e != nil || !strings.Contains(string(raw), "e71-own-selected.txt=own-file-bytes") {
					t.Fatal("selected file did not reach only local fixture", string(raw), e)
				}
				t.Log("actual Windows picker selected own file after MCP connected; page received exact filename and bytes")
			})
			if os.Getenv("DSH_WORK_PICKER_UIA_DIAGNOSE") == "1" || os.Getenv("DSH_WORK_PICKER_SELECTION_ONLY") == "1" {
				return
			}
			_, e = call("browser_click", map[string]any{"target": "#file"})
			raw, _ := json.Marshal(v)
			if e == nil || !strings.Contains(e.Error(), "CEF_FILE_RESOURCE_REQUIRED") {
				t.Fatal("Agent file chooser was not typed fail closed", e, string(raw))
			}
			t.Log("Agent file click typed fail closed; no native picker")
			for _, tool := range []string{"browser_file_upload", "browser_drop"} {
				if _, e = call(tool, map[string]any{"paths": []any{own}}); e == nil || !strings.Contains(e.Error(), "BROWSER_FILE_RESOURCE_REQUIRED") {
					t.Fatal(tool, e)
				}
			}
			for _, action := range []string{"cancel", "hide", "navigate", "close"} {
				if _, e = call("browser_click", map[string]any{"target": "getByRole('button', {name:'Choose later',exact:true})"}); e != nil {
					t.Fatal(action, e)
				}
				if action == "cancel" {
					waitPicker(t, true)
					for _, rejected := range []string{"file:///e71-never-read", "about:blank", "not-a-url"} {
						if rejectErr := embeddedbrowser.NativePickerNavigateForTest(ctx, target, rejected); rejectErr == nil || rejectErr.Error() != "CEF_NAVIGATION_REJECTED" {
							t.Fatal("native rejected-navigation gate", rejectErr)
						}
						waitPicker(t, true)
						phase, phaseErr := embeddedbrowser.NativePickerStateForTest(ctx, target)
						var state struct{ Canceled bool }
						if phaseErr != nil || json.Unmarshal(phase, &state) != nil || state.Canceled {
							t.Fatal("rejected native navigation canceled pending picker", string(phase), phaseErr)
						}
					}
					t.Log("authenticated native rejected file/about/invalid navigation preserved same pending picker")
					if _, checkErr := target.OwnedIdentity(ctx); checkErr != nil {
						t.Fatal("CEF UI unresponsive while picker open", checkErr)
					}
					if e = target.RevokeOwned(ctx); e != nil {
						t.Fatal(e)
					}
				} else {
					waitPicker(t, true)
					switch action {
					case "hide":
						if e = reg.Hide(ctx, peer, "window"); e != nil {
							t.Fatal(e)
						}
						_, e = reg.Update(ctx, peer, "window", parent, window.Lease, slot, fixture.URL, embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
					case "navigate":
						e = target.Navigate(ctx, fixture.URL+"/next")
					case "close":
						e = target.CloseNative(ctx)
					}
					if e != nil {
						t.Fatal(action, e)
					}
				}
				waitPicker(t, false)
				if action != "close" {
					time.Sleep(200 * time.Millisecond)
					if _, e = call("browser_snapshot", map[string]any{}); e != nil {
						t.Fatal("post-"+action+" page unhealthy", e)
					}
				}
				t.Log("actual picker", action, "completed without stale selection")
			}
		})
	}
}
