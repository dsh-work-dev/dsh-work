//go:build darwin || linux

package main

/*
#cgo linux LDFLAGS: -ldl
#include "loader.h"
*/
import "C"
import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

type lab struct {
	app                     *application.App
	main, float             *application.WebviewWindow
	root, token, generation string
	selection, presentation uint64
	started                 atomic.Bool
	created                 atomic.Bool
	quitting                atomic.Bool
	unauthorized            atomic.Uint64
	results                 sync.Map
}

func cstr(s string) *C.char { return C.CString(s) }
func nativeCommand(action string, index int) error {
	a := cstr(action)
	defer C.free(unsafe.Pointer(a))
	if C.lab_command(a, C.int(index)) == 0 {
		return fmt.Errorf("native %s: %s", action, C.GoString(C.lab_error()))
	}
	return nil
}
func (l *lab) state() map[string]any {
	var s map[string]any
	if err := json.Unmarshal([]byte(C.GoString(C.lab_state())), &s); err != nil {
		panic(err)
	}
	ready := l.created.Load() && s["guestRunning"] == true
	tabs, _ := s["tabs"].([]any)
	if len(tabs) != 2 {
		ready = false
	}
	selected, _ := s["selected"].(string)
	visible, fault := false, false
	for _, v := range tabs {
		t := v.(map[string]any)
		if t["hwnd"] == float64(0) {
			ready = false
		}
		if t["name"] == selected {
			visible = t["visible"] == true
			fault = t["fault"] == true
		}
	}
	s["ready"] = ready
	s["starting"] = s["guestRunning"] == true && !ready
	s["interactable"] = ready && visible && !fault && s["covered"] != true
	s["viewGeneration"] = l.generation
	s["selectionEpoch"] = l.selection
	s["presentationEpoch"] = l.presentation
	s["monitors"] = l.app.Screen.GetAll()
	s["unauthorizedRequests"] = l.unauthorized.Load()
	return s
}
func (l *lab) action(action string, q url.Values) error {
	index := 0
	if q.Get("tab") == "B" {
		index = 1
	}
	switch action {
	case "tab":
		l.selection++
		l.float.Hide()
	case "float":
		l.float.Show()
		if C.lab_float_parent(l.float.NativeWindow()) == 0 {
			return fmt.Errorf("floating surface not realized")
		}
	case "dock":
		l.float.Hide()
	case "cover", "uncover", "renderer-fault", "renderer-recovered":
		l.presentation++
	case "restart-broker":
		l.generation = rand.Text()
		l.selection++
		l.presentation++
	case "bounds":
		width, e := strconv.Atoi(q.Get("width"))
		height, f := strconv.Atoi(q.Get("height"))
		if e != nil || f != nil || width < 400 || height < 300 || width > 2000 || height > 1600 {
			return fmt.Errorf("invalid bounds")
		}
		target := l.main
		if q.Get("window") == "float" {
			target = l.float
		}
		target.SetSize(width, height)
		return nativeCommand("layout", index)
	case "quit":
		go l.quit()
		return nil
	}
	return nativeCommand(action, index)
}
func (l *lab) quit() {
	if l.quitting.Swap(true) {
		return
	}
	application.InvokeSync(func() { C.lab_close() })
	for i := 0; i < 300; i++ {
		if C.lab_closed() != 0 {
			application.InvokeSync(func() { C.lab_shutdown() })
			if e := os.WriteFile(filepath.Join(l.root, "evidence", "host-closed.json"), []byte("{\"createdClosed\":true,\"cefShutdownReturned\":true}"), 0600); e != nil {
				log.Fatal(e)
			}
			l.app.Quit()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	log.Print("CEF close did not complete; leave failure evidence")
}

const shellHTML = "<!doctype html><html lang=\"zh-CN\"><meta charset=\"utf-8\"><script type=\"module\" src=\"/wails/runtime.js\"></script><style>*{box-sizing:border-box}body{margin:0;background:#16273f;color:white;font:18px system-ui}header{height:90px;padding:12px}i{display:inline-block;width:24px;height:24px;background:#39e2b5}button,input{font:inherit;padding:7px;margin:4px}aside{width:230px;padding:12px}output{display:block;white-space:pre-wrap}#cover{position:absolute;left:240px;top:100px;right:20px;bottom:20px;background:#16273f;z-index:10}#placeholder{width:100%;height:100%;object-fit:contain;opacity:.35}#cover section{position:absolute;left:20px;right:20px;top:20px;background:white;color:#18273e;padding:20px}</style><header><i></i> Browser Lab<div><button onclick=\"act('tab',{tab:'A'})\">标签 A</button><button onclick=\"act('tab',{tab:'B'})\">标签 B</button><button onclick=\"act('float')\">浮动</button><button onclick=\"act('dock')\">停靠</button></div></header><aside><label>宿主输入区<input id=\"shell-input\" style=\"width:200px\"></label></aside><template id=\"grant-template\">" + businessControls + "</template><section id=\"fault-panel\" hidden style=\"position:absolute;left:260px;top:150px\"><h2 id=\"fault-title\"></h2><button id=\"recover-renderer\" onclick=\"recover('renderer')\">重新加载页面</button><button id=\"recover-broker\" onclick=\"recover('broker')\">重新启动浏览器</button><output id=\"recovery-message\"></output></section><div id=\"cover\" hidden><img id=\"placeholder\" alt=\"页面暂停画面\"><section><b>页面已暂停</b><button id=\"close-cover\" onclick=\"act('uncover')\">返回页面</button></section></div>"

func (l *lab) assets(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/lab/ui/result/") {
		l.receiveResult(w, r, strings.TrimPrefix(r.URL.Path, "/lab/ui/result/"))
		return
	}
	if businessProxy(w, r, l.token) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/lab/") {
		if r.Header.Get("X-Browser-Lab-Token") != l.token {
			http.Error(w, "forbidden", 403)
			return
		}
		l.routes(w, r, true)
		return
	}
	if r.URL.Path == "/overlay-placeholder" {
		if r.Header.Get("X-Browser-Lab-Token") != l.token {
			http.Error(w, "forbidden", 403)
			return
		}
		http.ServeFile(w, r, filepath.Join(l.root, "evidence", "placeholder.png"))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, shellHTML, "<script>(async()=>{const token=", strconv.Quote(l.token), ";window.lab={events:[]};async function act(action,args={}){await fetch('/lab/command?'+new URLSearchParams({action,...args}),{method:'POST',headers:{'X-Browser-Lab-Token':token}})}window.act=act;if(location.pathname==='/float')document.querySelector('aside').style.display='none';", businessJS, recoveryJS, overlayJS, "})();</script>")
}

type evaluation struct {
	Surface string
	Source  string
	Arg     json.RawMessage
}

func (l *lab) receiveResult(w http.ResponseWriter, r *http.Request, id string) {
	v, ok := l.results.LoadAndDelete(id)
	if !ok || r.Method != "POST" {
		http.Error(w, "invalid return ticket", 403)
		return
	}
	var raw json.RawMessage
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&raw) != nil {
		http.Error(w, "invalid result", 400)
		return
	}
	v.(chan json.RawMessage) <- raw
	w.WriteHeader(204)
}

func (l *lab) routes(w http.ResponseWriter, r *http.Request, asset bool) {
	path := strings.TrimPrefix(r.URL.Path, "/lab")
	if path == "/state" {
		if !l.started.Load() {
			http.Error(w, "starting", 503)
			return
		}
		var s map[string]any
		application.InvokeSync(func() { s = l.state() })
		json.NewEncoder(w).Encode(s)
		return
	}
	if path == "/close" {
		go l.quit()
		fmt.Fprint(w, "{\"closing\":true}")
		return
	}
	if path == "/command" {
		var e error
		application.InvokeSync(func() { e = l.action(r.URL.Query().Get("action"), r.URL.Query()) })
		if e != nil {
			http.Error(w, e.Error(), 409)
			return
		}
		fmt.Fprint(w, "{\"ok\":true}")
		return
	}
	if path == "/ui/navigate" && !asset {
		var v struct{ Surface, URL string }
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&v) != nil {
			http.Error(w, "invalid navigation", 400)
			return
		}
		u, e := url.Parse(v.URL)
		if e != nil || (u.Scheme != "wails" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
			http.Error(w, "fixture or Wails URL required", 403)
			return
		}
		target := l.main
		if v.Surface == "float" {
			target = l.float
		} else if v.Surface != "main" {
			http.Error(w, "surface required", 400)
			return
		}
		application.InvokeSync(func() { target.SetURL(v.URL) })
		fmt.Fprint(w, "{\"ok\":true}")
		return
	}
	if path == "/ui/eval" && !asset {
		var v evaluation
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&v) != nil || v.Source == "" {
			http.Error(w, "invalid evaluation", 400)
			return
		}
		target := l.main
		if v.Surface == "float" {
			target = l.float
		} else if v.Surface != "main" {
			http.Error(w, "invalid surface", 400)
			return
		}
		id := rand.Text()
		ch := make(chan json.RawMessage, 1)
		l.results.Store(id, ch)
		defer l.results.Delete(id)
		arg := string(v.Arg)
		if arg == "" {
			arg = "null"
		}
		// One-shot return ticket, not the control token, enters the owned WebView.
		script := fmt.Sprintf("Promise.resolve().then(()=> (%s)(%s)).then(value=>({value}),e=>({error:String(e)})).then(value=>fetch((location.protocol==='wails:'?'/lab':'http://127.0.0.1:19481')+'/ui/result/%s',{method:'POST',body:JSON.stringify(value)})).catch(()=>{})", v.Source, arg, id)
		application.InvokeSync(func() { target.ExecJS(script) })
		select {
		case out := <-ch:
			w.Header().Set("Content-Type", "application/json")
			w.Write(out)
		case <-time.After(8 * time.Second):
			http.Error(w, "WebView evaluation timeout", 504)
		case <-r.Context().Done():
		}
		return
	}
	http.NotFound(w, r)
}
func main() {
	runtime.LockOSThread()
	root := os.Getenv("BROWSER_LAB_RUN_ROOT")
	token := os.Getenv("BROWSER_LAB_TOKEN")
	if root == "" || len(token) < 24 {
		log.Fatal("private lab root/token required")
	}
	lib := cstr(os.Getenv("BROWSER_LAB_BRIDGE"))
	defer C.free(unsafe.Pointer(lib))
	if e := C.load_bridge(lib); e != nil {
		log.Fatal(C.GoString(e))
	}
	cache, helper, logPath := cstr(filepath.Join(root, "profile")), cstr(os.Getenv("BROWSER_LAB_HELPER")), cstr(filepath.Join(root, "evidence", "cef.log"))
	defer C.free(unsafe.Pointer(cache))
	defer C.free(unsafe.Pointer(helper))
	defer C.free(unsafe.Pointer(logPath))
	if C.lab_initialize(cache, helper, logPath) == 0 {
		log.Fatal(C.GoString(C.lab_error()))
	}
	l := &lab{root: root, token: token, generation: rand.Text(), selection: 1, presentation: 1}
	artifact, e := startArtifactServer(filepath.Join(root, "data"))
	if e != nil {
		log.Fatal(e)
	}
	defer artifact.Close()
	l.app = application.New(application.Options{Name: "Browser Lab POSIX", Assets: application.AssetOptions{Handler: http.HandlerFunc(l.assets)}})
	l.main = l.app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Browser Lab native embedding", Width: 1150, Height: 800, URL: "/"})
	l.float = l.app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Browser Lab floating", Width: 1000, Height: 750, URL: "/float", Hidden: true})
	for _, window := range []*application.WebviewWindow{l.main, l.float} {
		window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
			if !l.quitting.Load() {
				e.Cancel()
				go l.quit()
			}
		})
	}
	l.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		l.started.Store(true)
		go func() {
			for i := 0; i < 200; i++ {
				done := false
				application.InvokeSync(func() {
					if p := l.main.NativeWindow(); p != nil {
						u := cstr("http://localhost:19478/interactions?tab=A")
						defer C.free(unsafe.Pointer(u))
						if C.lab_create(p, u) == 0 {
							log.Fatal(C.GoString(C.lab_error()))
						}
						l.created.Store(true)
						if f := l.float.NativeWindow(); f != nil {
							C.lab_float_parent(f)
						}
						done = true
					}
				})
				if done {
					return
				}
				time.Sleep(100 * time.Millisecond)
			}
		}()
	})
	listener, e := net.Listen("tcp", "127.0.0.1:19481")
	if e != nil {
		log.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/ui/result/") {
			l.receiveResult(w, r, strings.TrimPrefix(r.URL.Path, "/ui/result/"))
			return
		}
		if r.Header.Get("X-Browser-Lab-Token") != token || r.Header.Get("Origin") != "" {
			l.unauthorized.Add(1)
			http.Error(w, "forbidden", 403)
			return
		}
		l.routes(w, r, false)
	}), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	if e := l.app.Run(); e != nil {
		log.Fatal(e)
	}
	server.Close()
}
