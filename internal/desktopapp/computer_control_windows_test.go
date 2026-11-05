//go:build windows

package desktopapp

import (
	"context"
	"encoding/json"
	"fmt"
	ole "github.com/go-ole/go-ole"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unsafe"

	dshapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/computerruntime"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/nativeui"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
)

type overlayPreparedFixture struct{}

type overlayCaptureResource struct{ automation.Resource }

func (r *overlayCaptureResource) Execute(ctx context.Context, tool string, args map[string]any) (any, error) {
	value, err := r.Resource.Execute(ctx, tool, args)
	if tool == "click" && err == nil {
		// UIA Invoke can return before a compositor frame. Hold only the test
		// provider result, not native execution, for deterministic UI capture.
		select {
		case <-ctx.Done():
			return value, ctx.Err()
		case <-time.After(8 * time.Second):
		}
	}
	return value, err
}

func (overlayPreparedFixture) Ready(c automation.Capability) bool {
	return c == automation.Computer || c == automation.AUV
}
func (overlayPreparedFixture) Prepare(context.Context, automation.Capability, func(automation.Progress)) error {
	return fmt.Errorf("native harness never installs components")
}

// Opt-in native renderer only: same production window owner, Stop binding and
// real Cua lease. No model, product execution endpoint or user-window input.
func TestOwnedComputerNativeUI(t *testing.T) {
	root := os.Getenv("DSH_WORK_NATIVE_UI_FIXTURE")
	deps := os.Getenv("DSH_WORK_CUA_TEST_DEPENDENCIES")
	driver := os.Getenv("DSH_WORK_CUA_TEST_DRIVER")
	if root == "" || deps == "" || driver == "" {
		t.Skip("explicit owned native fixture required")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if e := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); e != nil {
		t.Fatal(e)
	}
	defer ole.CoUninitialize()
	repo, _ := filepath.Abs("../..")
	root, _ = filepath.Abs(root)
	var target struct {
		PID      uint32 `json:"pid"`
		WindowID uint64 `json:"window_id"`
		Title    string `json:"title"`
	}
	b, e := os.ReadFile(filepath.Join(root, "state.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &target); e != nil {
		t.Fatal(e)
	}
	inspected, e := automation.InspectComputerTarget(target.PID, target.WindowID)
	if e != nil || target.Title != "Owned Cua acceptance fixture" || inspected.Title != target.Title {
		t.Fatal("owned live target required", e)
	}
	home, e := os.MkdirTemp(root, "native-ui-")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	manager, e := settings.New(settings.Config{Path: filepath.Join(home, "settings.json")})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = manager.SetAutomationEnabled(ctx, "computer", true); e != nil {
		t.Fatal(e)
	}
	engine := os.Getenv("DSH_WORK_NATIVE_UI_ENGINE")
	if engine == "auv" {
		if _, e = manager.SetComputerEngine(ctx, engine); e != nil {
			t.Fatal(e)
		}
	}
	service := automation.New(manager, overlayPreparedFixture{})
	defer service.Close()
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	factory := computerruntime.NewFactory(computerruntime.Config{Supervisor: win.NewJobObjectAdapter(), Root: filepath.Join(home, "jobs"), Resolve: func(context.Context, string) (computerruntime.Runtime, error) {
		return computerruntime.Runtime{Node: node, Dependencies: deps, Driver: driver}, nil
	}})
	service.ConfigureComputerGateway(automation.ComputerGatewayConfig{Inspect: automation.InspectComputerTarget, Acquire: automation.AcquireComputerDesktopLease, Factory: func(ctx context.Context, engine string) (automation.Resource, error) {
		r, e := factory(ctx, engine)
		if e != nil {
			return nil, e
		}
		if os.Getenv("DSH_WORK_NATIVE_UI_INFLIGHT") == "1" {
			return &overlayCaptureResource{r}, nil
		}
		return r, nil
	}})
	service.Worker("owned-native-ui", true, true)
	service.Gateway("owned-native-ui", true)
	assets := os.DirFS(repo)
	webAssets, e := fs.Sub(assets, "frontend/dist")
	if e != nil {
		t.Fatal(e)
	}
	app := application.New(application.Options{Name: "DSH owned computer UI acceptance", Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(home, "webview"), AdditionalBrowserArgs: []string{"--remote-debugging-port=9444"}, DisableQuitOnLastWindowClosed: true}, Services: []application.Service{application.NewService(&desktopclient.AutomationService{Local: dshapp.NewAutomationService(service)})}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(webAssets)}})
	ui := newComputerControlWindow(app, assets, func() bool { return false })
	service.OnChange(ui.update)
	evidence := map[string]any{"pid": target.PID, "window_id": target.WindowID, "modelCalls": 0, "home": home, "captureBarrier": os.Getenv("DSH_WORK_NATIVE_UI_INFLIGHT") == "1"}
	write := func(phase string) {
		evidence["phase"] = phase
		b, _ := json.MarshalIndent(evidence, "", "  ")
		_ = os.WriteFile(filepath.Join(root, "native-ui-harness.json"), b, 0600)
	}
	done := make(chan error, 1)
	started := make(chan struct{})
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { close(started) })
	go func() {
		<-started
		var failure error
		defer func() {
			application.InvokeSync(func() {
				if ui.pointer != nil && ui.pointer.NativeWindow() != nil {
					windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowDisplayAffinity").Call(uintptr(ui.pointer.NativeWindow()), 0x11)
				}
			})
			if failure != nil {
				evidence["error"] = failure.Error()
				write("failed")
			}
			done <- failure
			app.Quit()
		}()
		request := automation.Request{Generation: "owned-native-ui", Capability: automation.Computer, Session: "owned-native-ui", Lifetime: "owned-native-life", Turn: "owned-native-turn", UserTurn: true, Title: "Owned Cua acceptance fixture", Operation: "computer", Arguments: map[string]any{"pid": float64(target.PID), "window_id": float64(target.WindowID)}}
		if _, failure = service.ComputerTurn(request); failure != nil {
			return
		}
		run := func(tool string, args map[string]any) (any, error) {
			r := request
			r.Tool = tool
			r.Arguments = args
			r.Epoch = service.Snapshot().Computer.Epoch
			d, e := service.AuthorizeComputer(ctx, r)
			if e != nil {
				return nil, e
			}
			r.Permit = d.Permit
			return service.Execute(ctx, r)
		}
		go func() {
			for ctx.Err() == nil {
				state := service.Snapshot().ComputerAccess
				if state != nil && len(state.Requests) > 0 {
					r := state.Requests[0]
					if r.Target.PID == target.PID && r.Target.WindowID == target.WindowID {
						_, _ = service.AnswerComputerConsent(ctx, r.ID, "allow")
					}
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
		}()
		observed, e := run("computer_observe", request.Arguments)
		if e != nil {
			failure = e
			return
		}
		content := observed.(map[string]any)["structuredContent"].(map[string]any)
		var index any
		for _, v := range content["elements"].([]any) {
			el := v.(map[string]any)
			if el["label"] == "Slow marker" {
				index = el["element_index"]
			}
		}
		if index == nil && engine != "auv" {
			failure = fmt.Errorf("owned Slow marker absent")
			return
		}
		evidence["readyUse"] = service.Snapshot().ComputerAccess.Use
		time.Sleep(time.Second)
		write("ready")
		for {
			if _, e = os.Stat(filepath.Join(root, "native-ui-action.go")); e == nil {
				break
			}
			if ctx.Err() != nil {
				failure = ctx.Err()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		capture := func(phase string) {
			application.InvokeSync(func() {
				evidence["nativeAtAction"] = nativeOverlayEvidence(ui)
				evidence["windowCount"] = len(ui.app.Window.GetAll())
				foreground := windows.NewLazySystemDLL("user32.dll").NewProc("GetForegroundWindow")
				before, _, _ := foreground.Call()
				nativeui.ShowComputerOverlay(ui.control, 24, 24, false)
				nativeui.ShowComputerPointer(ui.pointer, ui.pointX, ui.pointY, target.WindowID, ui.hotspot.X, ui.hotspot.Y)
				after, _, _ := foreground.Call()
				evidence["showFocusWitness"] = map[string]any{"before": before, "after": after, "unchanged": before == after}
				windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowDisplayAffinity").Call(uintptr(ui.pointer.NativeWindow()), 0)
			})
			write(phase)
		}
		captured := make(chan struct{})
		inflight := os.Getenv("DSH_WORK_NATIVE_UI_INFLIGHT") == "1"
		if inflight {
			go func() {
				defer close(captured)
				for ctx.Err() == nil {
					u := service.Snapshot().ComputerAccess.Use
					if u != nil && u.Running && u.Action == "computer_click" {
						evidence["runningUse"] = u
						capture("running")
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
			}()
		}
		click := map[string]any{"pid": float64(target.PID), "window_id": float64(target.WindowID), "snapshot_id": content["snapshot_id"]}
		if engine == "auv" {
			click["x"], click["y"] = float64(362), float64(619)
		} else {
			click["element_index"] = index
		}
		result, e := run("computer_click", click)
		if inflight {
			<-captured
		} else {
			evidence["completedUse"] = service.Snapshot().ComputerAccess.Use
			capture("action-completed")
		}
		evidence["nativeCallResult"] = result
		if e != nil {
			evidence["nativeCallError"] = e.Error()
		}
		for service.Snapshot().ComputerAccess.Use != nil {
			if ctx.Err() != nil {
				failure = fmt.Errorf("Stop UI did not release lease: %w", ctx.Err())
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		time.Sleep(400 * time.Millisecond)
		application.InvokeSync(func() {
			windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowDisplayAffinity").Call(uintptr(ui.pointer.NativeWindow()), 0x11)
			evidence["nativeAfterStop"] = nativeOverlayEvidence(ui)
		})
		_, e = run("computer_observe", request.Arguments)
		if e == nil {
			failure = fmt.Errorf("Stop did not revoke next dispatch")
			return
		}
		evidence["nextDispatchError"] = e.Error()
		evidence["released"] = true
		write("stopped")
		time.Sleep(time.Second)
	}()
	if e = app.Run(); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
func nativeOverlayEvidence(ui *computerControlWindow) map[string]any {
	user32 := windows.NewLazySystemDLL("user32.dll")
	out := map[string]any{}
	// Read rectangles in physical pixels even if the test runner inherited an
	// unaware context. Restore the current thread before returning.
	dpi := user32.NewProc("SetThreadDpiAwarenessContext")
	old, _, _ := dpi.Call(^uintptr(3))
	defer dpi.Call(old)
	for name, w := range map[string]*application.WebviewWindow{"control": ui.control, "pointer": ui.pointer} {
		if w == nil {
			continue
		}
		h := uintptr(w.NativeWindow())
		visible, _, _ := user32.NewProc("IsWindowVisible").Call(h)
		d, _, _ := user32.NewProc("GetDpiForWindow").Call(h)
		var rect [4]int32
		user32.NewProc("GetWindowRect").Call(h, uintptr(unsafe.Pointer(&rect)))
		var affinity uint32
		user32.NewProc("GetWindowDisplayAffinity").Call(h, uintptr(unsafe.Pointer(&affinity)))
		out[name] = map[string]any{"hwnd": h, "visible": visible != 0, "dpi": d, "rectPhysical": rect, "captureAffinity": affinity}
	}
	return out
}
