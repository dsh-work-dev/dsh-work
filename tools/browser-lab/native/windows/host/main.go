// Browser Lab overlay-control lab. All HWND state is owned by the Wails UI thread.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"golang.org/x/sys/windows"
)

var user = windows.NewLazySystemDLL("user32.dll")
var enumChildren = user.NewProc("EnumChildWindows")
var parentOf = user.NewProc("GetParent")
var processOf = user.NewProc("GetWindowThreadProcessId")
var titleOf = user.NewProc("GetWindowTextW")
var classOf = user.NewProc("GetClassNameW")
var moveWindow = user.NewProc("MoveWindow")
var showWindow = user.NewProc("ShowWindow")
var setParent = user.NewProc("SetParent")
var getClient = user.NewProc("GetClientRect")
var getDPI = user.NewProc("GetDpiForWindow")
var getLong = user.NewProc("GetWindowLongPtrW")
var setLong = user.NewProc("SetWindowLongPtrW")
var getNativeRole = user.NewProc("GetPropW")
var nativeRoleName = syscall.StringToUTF16Ptr("BrowserLab.NativeRole")

type winRect struct{ Left, Top, Right, Bottom int32 }
type labTab struct {
	Name  string
	HWND  uintptr
	Fault bool
}
type lab struct {
	guestRunning, starting                       bool
	guestExit                                    string
	viewGeneration                               string
	presentationEpoch                            uint64
	covered                                      bool
	selectionEpoch                               uint64
	unauthorizedRequests                         int
	app                                          *application.App
	main, float                                  *application.WebviewWindow
	mainHWND, floatHWND                          uintptr
	initialSlots                                 [2]uintptr
	nativeSlots                                  bool
	tabs                                         [2]labTab
	selected                                     int
	floating, ready, manualDone, closing, closed bool
	guest                                        *exec.Cmd
	guestPID                                     uint32
	guestDone                                    chan error
	transitions                                  []map[string]any
}

func textAt(h uintptr) string {
	buf := make([]uint16, 512)
	titleOf.Call(h, uintptr(unsafe.Pointer(&buf[0])), 512)
	return windows.UTF16ToString(buf)
}
func (l *lab) record(kind string, detail any) {
	l.transitions = append(l.transitions, map[string]any{"time": time.Now().Format(time.RFC3339Nano), "kind": kind, "detail": detail})
	if len(l.transitions) > 500 {
		l.transitions = l.transitions[len(l.transitions)-500:]
	}
}
func showQuiet(w *application.WebviewWindow, h uintptr) {
	style, _, _ := getLong.Call(h, ^uintptr(19))
	setLong.Call(h, ^uintptr(19), style|0x08000000)
	w.Show()
	showWindow.Call(h, 4)
	setLong.Call(h, ^uintptr(19), style)
}
func (l *lab) layout() {
	for i, t := range l.tabs {
		if t.HWND == 0 {
			continue
		}
		p, _, _ := parentOf.Call(t.HWND)
		var r winRect
		getClient.Call(p, uintptr(unsafe.Pointer(&r)))
		d, _, _ := getDPI.Call(p)
		s := float64(d) / 96
		x, y := int32(280*s), int32(110*s)
		if p == l.floatHWND {
			x, y = int32(20*s), int32(110*s)
		}
		width, height := r.Right-x-int32(20*s), r.Bottom-y-int32(20*s)
		if width > 0 && height > 0 {
			moveWindow.Call(t.HWND, uintptr(x), uintptr(y), uintptr(width), uintptr(height), 1)
		}
		flag := uintptr(0)
		if i == l.selected && !l.covered && !t.Fault {
			flag = 4
		}
		showWindow.Call(t.HWND, flag)
	}
}

var discoveryCallback = syscall.NewCallback(func(h, data uintptr) uintptr {
	l := (*lab)(unsafe.Pointer(data))
	var pid uint32
	processOf.Call(h, uintptr(unsafe.Pointer(&pid)))
	p, _, _ := parentOf.Call(h)
	buf := make([]uint16, 128)
	classOf.Call(h, uintptr(unsafe.Pointer(&buf[0])), 128)
	if pid == l.guestPID && windows.UTF16ToString(buf) == "CefBrowserWindow" {
		for i, name := range []string{"A", "B"} {
			role, _, _ := getNativeRole.Call(h, uintptr(unsafe.Pointer(nativeRoleName)))
			if (l.nativeSlots && p == l.initialSlots[i] && role == uintptr(i+1)) || (!l.nativeSlots && p == l.mainHWND && textAt(h) == "Browser Lab Tab "+name) {
				l.tabs[i] = labTab{Name: name, HWND: h}
			}
		}
	}
	return 1
})

func (l *lab) discover() {
	enumChildren.Call(l.mainHWND, discoveryCallback, uintptr(unsafe.Pointer(l)))
}
func (l *lab) dock() error {
	if !l.floating {
		return nil
	}
	child := l.tabs[l.selected].HWND
	old, _, err := setParent.Call(child, l.mainHWND)
	if old == 0 {
		return fmt.Errorf("dock SetParent: %v", err)
	}
	l.floating = false
	l.float.Hide()
	l.layout()
	l.record("dock", map[string]any{"tab": l.tabs[l.selected].Name, "hwnd": child, "previousParent": old})
	return nil
}
func (l *lab) action(action string, q url.Values) error {
	switch action {
	case "architecture-destroy-parent":
		if os.Getenv("BROWSER_LAB_ARCH_PROBE") != "1" {
			return fmt.Errorf("architecture probe not enabled")
		}
		l.presentationEpoch++
		l.record("architecture-parent-destroy", l.mainHWND)
		r, _, err := user.NewProc("DestroyWindow").Call(l.mainHWND)
		if r == 0 {
			return err
		}

	case "native-input", "native-focus":
		return l.nativeInput(q.Get("target"), action == "native-focus")
	case "renderer-fault", "renderer-recovered":
		index := 0
		if q.Get("tab") == "B" {
			index = 1
		} else if q.Get("tab") != "A" {
			return fmt.Errorf("invalid tab")
		}
		l.tabs[index].Fault = action == "renderer-fault"
		l.presentationEpoch++
		l.layout()
		l.record(action, l.tabs[index].Name)
	case "crash-broker":
		if !l.guestRunning || l.guest == nil {
			return fmt.Errorf("broker is not running")
		}
		l.record("injected-broker-kill", l.guestPID)
		return l.guest.Process.Kill()

	case "cover":
		if !l.covered {
			l.covered = true
			l.presentationEpoch++
			l.layout()
			l.record("cover", l.presentationEpoch)
		}
	case "uncover":
		if l.covered {
			l.covered = false
			l.presentationEpoch++
			l.layout()
			l.record("uncover", l.presentationEpoch)
		}
	case "tab":
		l.selectionEpoch++
		index := 0
		if q.Get("tab") == "B" {
			index = 1
		} else if q.Get("tab") != "A" {
			return fmt.Errorf("expected tab A or B")
		}
		if err := l.dock(); err != nil {
			return err
		}
		l.selected = index
		l.layout()
		l.record("tab", l.tabs[index].Name)
	case "float":
		if l.floating {
			return nil
		}
		child := l.tabs[l.selected].HWND
		old, _, err := setParent.Call(child, l.floatHWND)
		if old == 0 {
			return fmt.Errorf("float SetParent: %v", err)
		}
		l.floating = true
		l.layout()
		showQuiet(l.float, l.floatHWND)
		l.record("float", map[string]any{"tab": l.tabs[l.selected].Name, "hwnd": child, "previousParent": old})
	case "dock":
		return l.dock()
	case "monitor":
		index, err := strconv.Atoi(q.Get("index"))
		list := MonitorInventory()
		if err != nil || index < 0 || index >= len(list) {
			return fmt.Errorf("invalid monitor index")
		}
		work, ok := list[index]["workArea"].(map[string]any)
		if !ok {
			return fmt.Errorf("monitor geometry unavailable")
		}
		rect := application.Rect{X: int(work["left"].(int32)) + 20, Y: int(work["top"].(int32)) + 20, Width: min(1250, int(work["width"].(int32))-40), Height: min(850, int(work["height"].(int32))-40)}
		w := l.main
		if q.Get("window") == "float" {
			w = l.float
		}
		w.SetPhysicalBounds(rect)
		l.layout()
		l.record("monitor", map[string]any{"index": index, "window": q.Get("window"), "bounds": rect})
	case "bounds":
		x, e1 := strconv.Atoi(q.Get("x"))
		y, e2 := strconv.Atoi(q.Get("y"))
		w, e3 := strconv.Atoi(q.Get("width"))
		h, e4 := strconv.Atoi(q.Get("height"))
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || w < 400 || h < 300 {
			return fmt.Errorf("invalid bounds")
		}
		target := l.main
		if q.Get("window") == "float" {
			target = l.float
		}
		target.SetPhysicalBounds(application.Rect{X: x, Y: y, Width: w, Height: h})
		l.layout()
	case "manual-done":
		l.manualDone = true
		l.record("manual-done", nil)
	default:
		return fmt.Errorf("unknown action %q", action)
	}
	return nil
}
func (l *lab) state() map[string]any {
	tabs := []map[string]any{}
	for _, t := range l.tabs {
		row := NativeWindowState(t.HWND)
		row["name"] = t.Name
		row["fault"] = t.Fault
		row["title"] = textAt(t.HWND)
		row["focus"] = NativeFocus(t.HWND)
		tabs = append(tabs, row)
	}
	topWindows := []map[string]any{}
	if l.guestRunning {
		topWindows = GuestTopLevelWindows(l.guestPID)
	}
	return map[string]any{"guestTopLevelWindows": topWindows, "guestRunning": l.guestRunning, "starting": l.starting, "guestExit": l.guestExit, "covered": l.covered, "presentationEpoch": l.presentationEpoch, "interactable": l.ready && !l.closing && !l.closed && !l.covered && !l.tabs[l.selected].Fault, "viewGeneration": l.viewGeneration, "selectionEpoch": l.selectionEpoch, "unauthorizedRequests": l.unauthorizedRequests, "ready": l.ready, "main": NativeWindowState(l.mainHWND), "float": NativeWindowState(l.floatHWND), "mainFocus": NativeFocus(l.mainHWND), "floatFocus": NativeFocus(l.floatHWND), "tabs": tabs, "selected": l.tabs[l.selected].Name, "floating": l.floating, "manualDone": l.manualDone, "guestPID": l.guestPID, "monitors": MonitorInventory(), "transitions": l.transitions}
}
func (l *lab) quit() {
	duplicate := false
	running := false
	application.InvokeSync(func() {
		duplicate = l.closing
		running = l.guestRunning
		if !duplicate {
			l.closing = true
		}
	})
	if duplicate {
		return
	}
	fail := func(err error) {
		log.Printf("Close failed; demo kept alive: %v", err)
		application.InvokeSync(func() { l.closing = false })
	}
	if !running {
		l.closeHost()
		return
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://127.0.0.1:19479/json/list")
	if err != nil {
		fail(err)
		return
	}
	var targets []struct{ ID, Type string }
	err = json.NewDecoder(response.Body).Decode(&targets)
	response.Body.Close()
	// Remote iframe/worker targets are owned by their page lifecycle, not extra
	// CefBrowser windows. Close the two actual roots and let CEF tear them down.
	pages := targets[:0]
	for _, t := range targets {
		switch t.Type {
		case "page":
			pages = append(pages, t)
		case "iframe", "worker", "service_worker", "shared_worker":
		default:
			fail(fmt.Errorf("unexpected target type: %s", t.Type))
			return
		}
	}
	targets = pages
	if err != nil || (len(targets) != 2 && os.Getenv("BROWSER_LAB_ARCH_PROBE") != "1") || len(targets) > 2 {
		fail(fmt.Errorf("expected two targets: %d, %v", len(targets), err))
		return
	}
	for _, t := range targets {
		if t.Type != "page" {
			fail(fmt.Errorf("unexpected target type"))
			return
		}
		response, err = client.Get("http://127.0.0.1:19479/json/close/" + url.PathEscape(t.ID))
		if err != nil {
			fail(err)
			return
		}
		response.Body.Close()
		if response.StatusCode != 200 {
			fail(fmt.Errorf("close HTTP %d", response.StatusCode))
			return
		}
	}
	select {
	case err = <-l.guestDone:
		if err != nil {
			fail(err)
			return
		}
	case <-time.After(10 * time.Second):
		fail(fmt.Errorf("CEF timeout"))
		return
	}
	application.InvokeSync(func() { l.closed = true; l.record("CEF process exited", nil) })
	log.Println("Both CEF tabs closed; broker exited normally")
	l.closeHost()
}

func (l *lab) closeHost() {
	// Close() dispatches window events asynchronously. Let both real WebView2
	// windows finish their native close before ending Wails' message loop.
	application.InvokeSync(func() { l.closed = true; l.float.Close(); l.main.Close() })
	for i := 0; i < 100; i++ {
		a, _, _ := nativeProbeIsWindow.Call(l.mainHWND)
		b, _, _ := nativeProbeIsWindow.Call(l.floatHWND)
		if a == 0 && b == 0 {
			l.app.Quit()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Print("Native Wails windows did not close; leave the process for diagnosis")
}

func main() {
	root := os.Getenv("BROWSER_LAB_ROOT")
	data := os.Getenv("BROWSER_LAB_DATA_ROOT")
	exe := os.Getenv("BROWSER_LAB_CEF_EXE")
	if root == "" || data == "" || exe == "" {
		log.Fatal("Use the ordinary-terminal interaction launcher")
	}
	token := os.Getenv("BROWSER_LAB_TOKEN")
	if len(token) < 24 {
		log.Fatal("Missing private lab token")
	}
	artifactServer, err := startArtifactServer(data)
	if err != nil {
		log.Fatal(err)
	}
	defer artifactServer.Close()
	l := &lab{nativeSlots: os.Getenv("BROWSER_LAB_NATIVE_PROTOCOL") == "2", viewGeneration: strconv.FormatInt(time.Now().UnixNano(), 36), selectionEpoch: 1, presentationEpoch: 1, tabs: [2]labTab{{Name: "A"}, {Name: "B"}}, guestDone: make(chan error, 1)}
	var handle http.HandlerFunc
	shell := func(floating bool) string {
		heading := "BROWSER_LAB · Windows 集中收尾"
		if floating {
			heading = "BROWSER_LAB · 浮动页面"
		}
		return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>` + heading + `</title><style>*{box-sizing:border-box}body{margin:0;background:#eef2f6;color:#14243c;font:17px system-ui}header{height:90px;background:#19273f;color:white;padding:15px 20px}button,input{font:inherit;padding:8px;margin-right:8px}aside{width:260px;padding:18px}#slot{position:absolute;left:280px;top:110px;right:20px;bottom:20px;background:#dfe8ed;padding:20px}output{display:block;white-space:pre-wrap;padding:8px}p{line-height:1.5}</style><header><i id="shell-marker" style="display:inline-block;width:16px;height:16px;background:#39e2b5;margin-right:8px"></i><b>` + heading + `</b><div><button onclick="act('tab',{tab:'A'})">标签 A</button><button onclick="act('tab',{tab:'B'})">标签 B</button><button onclick="act('float')">浮动</button><button onclick="act('dock')">停靠</button></div></header><aside><label>宿主输入区<input id="shell-input" aria-label="宿主输入区" style="width:220px"></label><p>在 A 输入“中文甲”；切 B 并浮动，输入“中文乙”；停靠后在宿主输入区输入“宿主中文”。均请使用中文输入法选择候选词。</p><button onclick="act('manual-done')">完成输入验证</button><output id="status"></output></aside><div id="slot"><section id="fault-panel" hidden><h2 id="fault-title">页面发生异常</h2><p>恢复后需要重新授权。</p><button id="recover-renderer" onclick="recover('renderer')">重新加载页面</button><button id="recover-broker" onclick="recover('broker')">重新启动浏览器</button><output id="recovery-message"></output></section></div><div id="cover" hidden style="position:absolute;left:280px;top:110px;right:20px;bottom:20px;background:#19273f;z-index:10;overflow:hidden"><img id="placeholder" alt="页面暂停画面" style="width:100%;height:100%;object-fit:contain;opacity:.35;pointer-events:none"><section style="position:absolute;top:24px;left:24px;right:24px;background:white;padding:24px;border-radius:12px"><b>页面已暂停</b><p>关闭弹层以返回页面。</p><button id="close-cover" onclick="act('uncover')">返回页面</button></section></div><script>
if(location.pathname==='/float'){document.querySelector('aside').style.display='none';document.querySelector('#slot').style.left='20px'};
const token=` + strconv.Quote(token) + `;window.lab={events:[],instance:crypto.randomUUID()};
for(const type of ['focusin','focusout','keydown','keyup','compositionstart','compositionupdate','compositionend','beforeinput','input'])document.addEventListener(type,e=>{lab.events.push({time:Date.now(),type,target:e.target.id,key:e.key,code:e.code,data:e.data,inputType:e.inputType,composing:e.isComposing,trusted:e.isTrusted,value:e.target.value});if(lab.events.length>2000)lab.events.shift()},true);
async function act(action,args={}){const r=await fetch('/lab/command?'+new URLSearchParams({action,...args}),{method:'POST',headers:{'X-Browser-Lab-Token':token}});if(!r.ok)document.querySelector('#status').textContent=await r.text()};window.act=act;
setInterval(async()=>{try{const s=await(await fetch('/lab/state',{headers:{'X-Browser-Lab-Token':token}})).json();document.querySelector('#status').textContent='标签 '+s.selected+' · '+(s.floating?'已浮动':'已停靠')}catch{}},1000);
</script><template id="grant-template">` + businessControls + `</template><script>` + businessJS + recoveryJS + overlayJS + `</script></html>`
	}
	assets := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if businessProxy(w, r, token) {
			return
		}
		if r.URL.Path == "/overlay-placeholder" {
			if r.Header.Get("X-Browser-Lab-Token") != token {
				http.Error(w, "unauthorized", 403)
				return
			}
			bytes, err := os.ReadFile(filepath.Join(root, "evidence", "placeholder.png"))
			if err != nil {
				http.Error(w, "snapshot unavailable", 404)
				return
			}
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Cache-Control", "no-store")
			w.Write(bytes)
			return
		}
		if r.URL.Path == "/browser-lab-host-canary" {
			w.Write([]byte("browser-lab-host-only-resource-v1"))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/lab/") {
			r.URL.Path = strings.TrimPrefix(r.URL.Path, "/lab")
			handle(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(shell(r.URL.Path == "/float")))
	})
	l.app = application.New(application.Options{Name: "BROWSER_LAB interaction lab", Assets: application.AssetOptions{Handler: assets}, Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(data, "host-profile"), AdditionalBrowserArgs: []string{"--remote-debugging-port=19480"}, WndProcInterceptor: func(h uintptr, msg uint32, wp, lp uintptr) (uintptr, bool) {
		// Initialise composition with a real, visible parent, without taking
		// keyboard focus. Keeping the sole initial window hidden until after
		// Embed can strand the async controller callback on a cold desktop.
		// The floating host stays hidden; CEF/control readiness is unchanged.
		if msg == 1 && textAt(h) == "BROWSER_LAB · Windows 集中收尾" {
			showWindow.Call(h, 4)
			log.Printf("BrowserLab bootstrap: shown owned main HWND %x without activation", h)
		}
		if h == l.mainHWND || h == l.floatHWND {
			if msg == 5 || msg == 0x02e0 {
				if msg == 0x02e0 {
					l.record("WM_DPICHANGED", map[string]any{"hwnd": h, "dpiX": wp & 65535, "dpiY": (wp >> 16) & 65535})
				}
				l.layout()
			}
		}
		return 0, false
	}}})
	opts := application.WebviewWindowOptions{Title: "BROWSER_LAB · Windows 集中收尾", Width: 1150, Height: 800, URL: "http://wails.localhost/", Hidden: true, Windows: application.WindowsWindow{WebView2CompositionHosting: true}}
	l.main = l.app.Window.NewWithOptions(opts)
	opts.Title = "BROWSER_LAB · 浮动页面"
	opts.URL = "http://wails.localhost/float"
	opts.Width = 950
	opts.Height = 700
	l.float = l.app.Window.NewWithOptions(opts)
	l.main.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !l.closed {
			e.Cancel()
			go l.quit()
		}
	})
	l.float.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if !l.closed {
			e.Cancel()
			go func() {
				application.InvokeSync(func() {
					if err := l.dock(); err != nil {
						log.Println(err)
					}
				})
			}()
		}
	})
	handle = func(w http.ResponseWriter, r *http.Request) {
		if businessProxy(w, r, token) {
			return
		}
		if r.Header.Get("X-Browser-Lab-Token") != token {
			application.InvokeSync(func() { l.unauthorizedRequests++ })
			http.Error(w, "unauthorized", 403)
			return
		}
		if r.URL.Path == "/state" {
			var result map[string]any
			application.InvokeSync(func() { result = l.state() })
			json.NewEncoder(w).Encode(result)
			return
		}
		if r.URL.Path != "/command" || r.Method != "POST" {
			http.Error(w, "not found", 404)
			return
		}
		if r.URL.Query().Get("action") == "restart-broker" {
			if err := l.startBroker(root, data, exe, token); err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			w.Write([]byte("restarted"))
			return
		}
		if r.URL.Query().Get("action") == "quit" {
			w.Write([]byte("closing"))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			go l.quit()
			return
		}
		var err error
		application.InvokeSync(func() {
			if !l.ready && r.URL.Query().Get("action") != "crash-broker" {
				err = fmt.Errorf("not ready")
				return
			}
			err = l.action(r.URL.Query().Get("action"), r.URL.Query())
		})
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		w.Write([]byte("ok"))
	}
	server := &http.Server{Addr: "127.0.0.1:19481", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handle(w, r) }), ReadHeaderTimeout: 3 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Println(err)
		}
	}()
	l.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(e *application.ApplicationEvent) {
		go func() {
			for i := 0; i < 150; i++ {
				application.InvokeSync(func() { l.mainHWND = uintptr(l.main.NativeWindow()); l.floatHWND = uintptr(l.float.NativeWindow()) })
				if l.mainHWND != 0 && l.floatHWND != 0 {
					break
				}
				time.Sleep(100 * time.Millisecond)
			}
			if l.mainHWND == 0 || l.floatHWND == 0 {
				log.Println("Native Wails hosts not ready")
				return
			}
			if err := l.startBroker(root, data, exe, token); err != nil {
				log.Printf("Broker startup failed: %v", err)
			}
		}()
	})
	l.app.OnShutdown(func() { server.Close() })
	if err := l.app.Run(); err != nil {
		log.Fatal(err)
	}
}
