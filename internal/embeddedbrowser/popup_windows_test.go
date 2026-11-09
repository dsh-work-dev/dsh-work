//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
)

// Independent native fixture only. This exercises CEF/Host authority, not DSH
// tab creation; the latter still requires the real app's source-tab callback.
func TestLiveManagedPopupPendingDispatch(t *testing.T) {
	exe := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE")
	if exe == "" {
		t.Skip("requires independently built actual CEF")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var sourceLoaded, adLoaded, clicked, sourceReload, adReload, opaqueReports, mouseDown, mouseUp atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "no-store")
		switch r.URL.Path {
		case "/loaded":
			if r.URL.Query().Get("kind") == "source" {
				sourceLoaded.Add(1)
				if r.Header.Get("Origin") == "null" {
					opaqueReports.Add(1)
				}
			} else {
				adLoaded.Add(1)
			}
			return
		case "/mouse":
			if r.URL.Query().Get("event") == "mousedown" {
				mouseDown.Add(1)
			} else {
				mouseUp.Add(1)
			}
			return
		case "/clicked":
			if r.URL.Query().Get("null") != "true" {
				t.Error("iframe window.open returned non-null")
			}
			clicked.Add(1)
			return
		case "/control":
			if r.URL.Query().Get("kind") == "source" {
				fmt.Fprint(w, sourceReload.Load())
			} else {
				fmt.Fprint(w, adReload.Load())
			}
			return
		case "/frames":
			sandbox := "allow-scripts allow-popups allow-same-origin"
			if r.URL.Query().Get("opaque") == "1" {
				sandbox = "allow-scripts allow-popups"
			}
			fmt.Fprintf(w, `<!doctype html><title>Iframe source</title><style>body{margin:0}iframe{border:0;width:160px;height:100px}</style><iframe sandbox="%s" src="/child" id="source"></iframe><iframe src="/ad"></iframe>`, sandbox)
			return
		case "/child", "/ad":
			kind := "source"
			epoch := sourceReload.Load()
			if r.URL.Path == "/ad" {
				kind = "ad"
				epoch = adReload.Load()
			}
			fmt.Fprintf(w, `<!doctype html><style>body{margin:0}button{width:160px;height:80px}</style><script>Object.defineProperty(window,'origin',{value:'https://spoof.invalid'});let epoch=%d;document.addEventListener('DOMContentLoaded',()=>requestAnimationFrame(()=>requestAnimationFrame(()=>fetch('/loaded?kind=%s'))));for(const event of ['mousedown','mouseup'])addEventListener(event,()=>fetch('/mouse?event='+event));setInterval(async()=>{let n=Number(await(await fetch('/control?kind=%s')).text());if(n!==epoch)location.reload()},40)</script><button onclick="let result=window.open('/target','_blank');fetch('/clicked?null='+String(result===null))">Open child</button>`, epoch, kind, kind)
			return
		}
		fmt.Fprint(w, `<!doctype html><title>Popup source</title><script>Object.defineProperty(window,'origin',{value:'https://spoof.invalid'})</script><input value="source unchanged"><p>Own fixture</p>`)
	}))
	defer server.Close()
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	root := t.TempDir()
	acquire := func(dir string) (io.Closer, error) {
		return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
	}
	store, err := embeddedbrowser.OpenInstanceStore(filepath.Join(root, "instances"), acquire)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	nativeAdapter := &policyAdapter{Adapter: win.NewJobObjectAdapter()}
	reg, err := embeddedbrowser.NewInstancePresenterRegistry(embeddedbrowser.InstanceRegistryConfig{Store: store, AcquireProfile: acquire, StartBroker: func(c context.Context, i embeddedbrowser.InstanceConfig) (*embeddedbrowser.Broker, error) {
		return embeddedbrowser.Start(c, embeddedbrowser.Config{Executable: exe, Profile: i.Profile, Supervisor: nativeAdapter})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := reg.Close(); e != nil {
			t.Error(e)
		}
	}()
	admission, err := reg.RegisterInstanceWindow(ctx, peer, "popup-fixture", "window", parent)
	if err != nil {
		t.Fatal(err)
	}
	const slot = "source-session/source-tab"
	state, err := reg.Update(ctx, peer, "window", parent, admission.Lease, slot, server.URL, embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	read := func() embeddedbrowser.ViewState {
		t.Helper()
		s, e := reg.State(ctx, peer, "window", slot)
		if e != nil {
			t.Fatal(e)
		}
		return s
	}
	for state.State != "ready" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
		state = read()
	}
	target, err := reg.OwnedWindowViewTarget(peer, "window", parent, admission.Lease, slot, state.Lease)
	if err != nil {
		t.Fatal(err)
	}
	route, err := target.OpenOwnedRoute(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer route.Close()
	sequence := 0
	evaluate := func(expression string, gesture bool) json.RawMessage {
		t.Helper()
		identity, e := target.OwnedIdentity(ctx)
		if e != nil {
			t.Fatal(e)
		}
		sequence++
		end, e := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: fmt.Sprint("popup-", sequence)})
		if e != nil {
			t.Fatal(e)
		}
		defer func() {
			if e := end(); e != nil {
				t.Error(e)
			}
		}()
		tab, _ := strconv.Atoi(identity.Target)
		request, _ := json.Marshal(map[string]any{"id": sequence, "method": "devtools.dispatch", "params": []any{map[string]any{"tabId": tab}, "Runtime.evaluate", map[string]any{"expression": expression, "returnByValue": true, "userGesture": gesture}}})
		if e := route.Send(ctx, request); e != nil {
			t.Fatal(e)
		}
		for {
			select {
			case raw := <-route.Messages():
				var reply struct {
					ID     int
					Result json.RawMessage
					Error  string
				}
				json.Unmarshal(raw, &reply)
				if reply.ID == sequence {
					if reply.Error != "" {
						t.Fatal(reply.Error)
					}
					return reply.Result
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil
			}
		}
	}
	open := func(url string, gesture bool) {
		t.Helper()
		raw := evaluate(fmt.Sprintf("window.open(%q,'_blank')===null", url), gesture)
		var result struct{ Result struct{ Value bool } }
		if json.Unmarshal(raw, &result) != nil || !result.Result.Value {
			t.Fatalf("window.open did not return null: %s", raw)
		}
	}
	action := func(p embeddedbrowser.WebPopup, a string) string {
		t.Helper()
		u, e := reg.Popup(ctx, peer, "window", slot, p, a)
		if e != nil {
			t.Fatal(a, e)
		}
		return u
	}
	destination := server.URL + "/target"
	open(destination, false)
	if read().Popup != nil {
		t.Fatal("no-gesture popup queued")
	}
	open("javascript:alert(1)", true)
	if read().Popup != nil {
		t.Fatal("unknown scheme popup queued")
	}
	open(destination, true)
	state = read()
	for state.Popup == nil {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
		state = read()
	}
	if state.Popup == nil {
		t.Fatal("gesture popup missing")
	}
	p := *state.Popup
	if p.Origin != server.URL || p.SourceURL != server.URL+"/" || p.URL != destination || !p.UserGesture || p.Frame == "" || p.Browser == "" || p.View != state.Lease || p.Document != state.Document {
		t.Fatalf("wrong real source identity %+v state=%+v", p, state)
	}
	wrong := p
	wrong.URL = server.URL + "/forged"
	if _, e := reg.Popup(ctx, peer, "window", slot, wrong, "suspend"); e == nil {
		t.Fatal("forged target accepted")
	}
	action(p, "suspend")
	action(p, "ready")
	if !read().PopupVisible {
		t.Fatal("Host card not admitted after native Hide")
	}
	if i, e := target.OwnedIdentity(ctx); e != nil || i.ViewVisible {
		t.Fatal("popup overlay exposed underlying guest", i, e)
	}
	// Same source/window remains valid across visible resize; old presentation
	// snapshots cannot answer the new presentation but the request is retained.
	state, err = reg.Update(ctx, peer, "window", parent, admission.Lease, slot, "", embeddedbrowser.SlotBounds{Width: 200, Height: 180}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if state.Popup == nil || state.Popup.ID != p.ID {
		t.Fatal("resize silently lost request", state.Popup)
	}
	t.Logf("visible resize presentation %s -> %s, pixels=%+v", p.Presentation, state.Popup.Presentation, state.Bounds)
	if _, e := reg.Popup(ctx, peer, "window", slot, p, "allow"); e == nil {
		t.Fatal("stale presentation answered")
	}
	p = *state.Popup
	action(p, "suspend")
	action(p, "ready")
	if u := action(p, "allow"); u != destination {
		t.Fatal("wrong approved dispatch URL", u)
	}
	if _, e := reg.Popup(ctx, peer, "window", slot, p, "allow"); e == nil {
		t.Fatal("request dispatched twice")
	}
	state = read()
	if state.Popup != nil || state.URL != server.URL+"/" {
		t.Fatal("dispatch navigated source", state)
	}
	open(destination, true)
	state = read()
	for state.Popup == nil {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
		state = read()
	}
	p = *state.Popup
	action(p, "suspend")
	action(p, "ready")
	if e := reg.Hide(ctx, peer, "window"); e != nil {
		t.Fatal(e)
	}
	if _, e := reg.Popup(ctx, peer, "window", slot, p, "allow"); e == nil {
		t.Fatal("hidden source dispatched")
	}
	state, err = reg.Update(ctx, peer, "window", parent, admission.Lease, slot, "", embeddedbrowser.SlotBounds{Width: 200, Height: 180}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	open(destination, true)
	state = read()
	for state.Popup == nil {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
		state = read()
	}
	p = *state.Popup
	if e := target.Navigate(ctx, server.URL+"/next"); e != nil {
		t.Fatal(e)
	}
	for read().URL != server.URL+"/next" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, e := reg.Popup(ctx, peer, "window", slot, p, "allow"); e == nil {
		t.Fatal("navigated source dispatched")
	}
	for state = read(); state.State != "ready"; state = read() {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	open(destination, true)
	state = read()
	for state.Popup == nil {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
		state = read()
	}
	p = *state.Popup
	// Finish the existing pending request before entering iframe-only native
	// cases; ordinary source input and document URL remain the product invariant.
	action(p, "suspend")
	action(p, "ready")
	action(p, "deny")
	user32 := windows.NewLazySystemDLL("user32.dll")
	clickOwnGuest := func() {
		t.Helper()
		var render uintptr
		var classes []string
		callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
			var name [256]uint16
			user32.NewProc("GetClassNameW").Call(window, uintptr(unsafe.Pointer(&name[0])), 256)
			class := windows.UTF16ToString(name[:])
			classes = append(classes, class)
			if strings.HasPrefix(class, "Chrome_WidgetWin_") {
				var pid uint32
				user32.NewProc("GetWindowThreadProcessId").Call(window, uintptr(unsafe.Pointer(&pid)))
				root, _, _ := user32.NewProc("GetAncestor").Call(window, 2)
				if root == parent && pid == nativeAdapter.worker.Diagnostics().PID {
					render = window
				}
			}
			return 1
		})
		user32.NewProc("EnumChildWindows").Call(parent, callback, 0)
		t.Logf("fixture native input own parent=%x guest=%x classes=%s", parent, render, strings.Join(classes, ","))
		if render == 0 {
			t.Fatal("own direct CEF guest HWND missing", strings.Join(classes, ","))
		}
		dpi, _, _ := user32.NewProc("GetDpiForWindow").Call(parent)
		xy := uintptr(30 * dpi / 96)
		position := xy | (xy << 16)
		// Direct messages to this fixture's verified child only. Never SendInput,
		// SetCursorPos, foreground activation, or another app/window's HWND.
		for _, m := range []struct{ id, w uintptr }{{0x200, 0}, {0x201, 1}, {0x202, 0}} {
			if ok, _, e := user32.NewProc("PostMessageW").Call(render, m.id, m.w, position); ok == 0 {
				t.Fatal(e)
			}
		}
	}
	waitFor := func(label string, predicate func() bool) {
		t.Helper()
		deadline := time.Now().Add(4 * time.Second)
		for !predicate() {
			if ctx.Err() != nil || time.Now().After(deadline) {
				t.Fatal("timeout", label, ctx.Err())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	for _, opaque := range []bool{false, true} {
		oldLoaded, oldAd, oldClicked := sourceLoaded.Load(), adLoaded.Load(), clicked.Load()
		url := server.URL + "/frames"
		if opaque {
			url += "?opaque=1"
		}
		if e := target.Navigate(ctx, url); e != nil {
			t.Fatal(e)
		}
		waitFor("both real frames loaded", func() bool { return sourceLoaded.Load() > oldLoaded && adLoaded.Load() > oldAd })
		clickOwnGuest()
		time.Sleep(80 * time.Millisecond)
		t.Logf("fixture input received down=%d up=%d clicked=%d", mouseDown.Load(), mouseUp.Load(), clicked.Load())
		waitFor("real iframe gesture/null", func() bool { return clicked.Load() > oldClicked })
		if opaque {
			time.Sleep(120 * time.Millisecond)
			if read().Popup != nil {
				t.Fatal("opaque sandbox misattributed URL origin", read().Popup)
			}
			if opaqueReports.Load() == 0 {
				t.Fatal("fixture did not observe opaque Origin:null")
			}
			t.Log("opaque sandbox frame denied despite same-site HTTP source URL and spoofed window.origin")
			continue
		}
		var child embeddedbrowser.WebPopup
		waitFor("normal source frame trusted origin", func() bool {
			v := read()
			if v.Popup != nil {
				child = *v.Popup
				return true
			}
			return false
		})
		if child.Origin != server.URL || child.SourceURL != server.URL+"/child" || child.Context == "" || child.FrameDocument == "" {
			t.Fatalf("source iframe misbound %+v", child)
		}
		t.Logf("normal iframe native captured origin=%s while page replaced window.origin; frame=%s context=%s", child.Origin, child.Frame, child.Context)
		action(child, "suspend")
		action(child, "ready")
		oldAd = adLoaded.Load()
		adReload.Add(1)
		waitFor("unrelated ad same-URL reload", func() bool { return adLoaded.Load() > oldAd })
		v := read()
		if v.Popup == nil || v.Popup.ID != child.ID || v.Popup.Context != child.Context {
			t.Fatal("unrelated frame revoked source request", v.Popup)
		}
		oldLoaded = sourceLoaded.Load()
		sourceReload.Add(1)
		waitFor("source iframe same-URL reload", func() bool { return sourceLoaded.Load() > oldLoaded })
		waitFor("source request invalidated", func() bool { return read().Popup == nil })
		if _, e := reg.Popup(ctx, peer, "window", slot, child, "allow"); e == nil {
			t.Fatal("old source document/context challenge dispatched after same-URL reload")
		}
		t.Log("source same-URL reload invalidated old request/context; unrelated ad reload preserved it")
	}
	// Closing a real frame-bearing source also rejects stale requests. Reopen a
	// fresh normal source request using its verified fixture-only HWND.
	oldLoaded := sourceLoaded.Load()
	if e := target.Navigate(ctx, server.URL+"/frames"); e != nil {
		t.Fatal(e)
	}
	waitFor("normal frame again", func() bool { return sourceLoaded.Load() > oldLoaded && read().State == "ready" })
	oldClicked := clicked.Load()
	clickOwnGuest()
	waitFor("last source click", func() bool { return clicked.Load() > oldClicked })
	waitFor("last source popup", func() bool {
		v := read()
		if v.Popup != nil {
			p = *v.Popup
			return true
		}
		return false
	})
	if e := reg.Detach(ctx, peer, "window", slot); e != nil {
		t.Fatal(e)
	}
	if _, e := reg.Popup(ctx, peer, "window", slot, p, "allow"); e == nil {
		t.Fatal("closed source dispatched")
	}
	t.Log("actual CEF: null/no opener window; gesture/source identity; scheme deny; native Hide/Host ready; resize retained request; one-shot dispatch; source preserved; hidden/navigated/closed stale denied")
}
