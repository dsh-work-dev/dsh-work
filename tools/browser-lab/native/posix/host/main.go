//go:build darwin || linux

package main

/*
#cgo linux LDFLAGS: -ldl
#include "loader.h"
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func cstr(value string) *C.char { return C.CString(value) }
func main() {
	runtime.LockOSThread()
	root := os.Getenv("BROWSER_LAB_RUN_ROOT")
	token := os.Getenv("BROWSER_LAB_TOKEN")
	if root == "" || len(token) < 24 {
		log.Fatal("Private run root/token required")
	}
	library := cstr(os.Getenv("BROWSER_LAB_BRIDGE"))
	defer C.free(unsafe.Pointer(library))
	if err := C.load_bridge(library); err != nil {
		log.Fatal(C.GoString(err))
	}
	cache, helper, logPath := cstr(filepath.Join(root, "profile")), cstr(os.Getenv("BROWSER_LAB_HELPER")), cstr(filepath.Join(root, "evidence", "cef.log"))
	defer C.free(unsafe.Pointer(cache))
	defer C.free(unsafe.Pointer(helper))
	defer C.free(unsafe.Pointer(logPath))
	if C.lab_initialize(cache, helper, logPath) == 0 {
		log.Fatal(C.GoString(C.lab_error()))
	}
	log.Print("CEF initialized on locked main thread")
	app := application.New(application.Options{Name: "Browser Lab POSIX", Assets: application.AssetOptions{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<!doctype html><meta charset="utf-8"><style>body{margin:0;background:#16273f;color:white;font:22px system-ui}header{height:80px;padding:12px}i{display:inline-block;width:24px;height:24px;background:#39e2b5}aside{width:220px;padding:12px}</style><header><i></i> Browser Lab</header><aside>Wails 宿主</aside>`)
	})}})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Browser Lab native embedding", Width: 1000, Height: 700, URL: "http://wails.localhost/"})
	var once sync.Once
	var started atomic.Bool
	var createError string
	startNative := func() {
		log.Print("Waiting for Wails native window")
		for i := 0; i < 200; i++ {
			ready := false
			application.InvokeSync(func() {
				if p := window.NativeWindow(); p != nil {
					once.Do(func() {
						log.Print("Wails native window found; creating CEF child")
						url := cstr("http://127.0.0.1:19478/interactions?tab=A")
						defer C.free(unsafe.Pointer(url))
						if C.lab_create(p, url) == 0 {
							createError = C.GoString(C.lab_error())
						}
						log.Printf("CEF child request returned; error=%q", createError)
					})
					ready = true
				}
			})
			if ready {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		started.Store(true)
		log.Print("Wails application started")
		go startNative()
	})
	listener, err := net.Listen("tcp", "127.0.0.1:19481")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /state", func(w http.ResponseWriter, r *http.Request) {
		if !started.Load() {
			http.Error(w, "Wails is starting", 503)
			return
		}
		var raw string
		application.InvokeSync(func() { raw = C.GoString(C.lab_state()); log.Print("Native state: ", raw) })
		if createError != "" {
			w.WriteHeader(500)
			json.NewEncoder(w).Encode(map[string]string{"error": createError})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, raw)
	})
	mux.HandleFunc("POST /close", func(w http.ResponseWriter, r *http.Request) {
		application.InvokeSync(func() { C.lab_close() })
		fmt.Fprint(w, `{"closing":true}`)
		go func() {
			for i := 0; i < 200; i++ {
				if C.lab_closed() != 0 {
					application.InvokeSync(func() { C.lab_shutdown() })
					app.Quit()
					return
				}
				time.Sleep(50 * time.Millisecond)
			}
			log.Print("CEF close did not reach OnBeforeClose")
		}()
	})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Browser-Lab-Token") != token || r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", 403)
			return
		}
		mux.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second}
	go server.Serve(listener)
	log.Print("Entering Wails run loop")
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
	server.Close()
	os.WriteFile(filepath.Join(root, "evidence", "host-closed.json"), []byte(fmt.Sprintf(`{"createdClosed":%t,"cefShutdownReturned":true}`, C.lab_closed() != 0)), 0600)
}
