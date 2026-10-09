package desktopbridge

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestHostSurfaceSeparatesWorkerAndShellByAuthority(t *testing.T) {
	var reached string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = r.Host + r.URL.Path })
	s := &Surface{Host: "wails.localhost:48217", Window: func() application.Window { return nil }, Current: func() *Bridge { return nil }}
	handler := s.Middleware(next)
	cases := []struct {
		name, host, path, origin, referer string
		status                            int
		reaches                           bool
		framing                           bool
	}{
		{name: "shell page", host: "wails.localhost", path: "/", status: 200, reaches: true, framing: true},
		{name: "shell binding", host: "wails.localhost", path: "/wails/runtime", status: 200, reaches: true, framing: true},
		{name: "Worker calls shell binding", host: "wails.localhost", path: "/wails/runtime", origin: "http://wails.localhost:48217", status: 403},
		{name: "Worker navigates to shell", host: "wails.localhost", path: "/", referer: "http://wails.localhost:48217/?generation=g", status: 403},
		{name: "shell opens a Stream", host: "wails.localhost", path: "/wails/stream/poll", status: 403},
		{name: "Worker privileged runtime", host: "wails.localhost:48217", path: "/wails/runtime", status: 403},
		{name: "Worker runtime script", host: "wails.localhost:48217", path: "/wails/runtime.js", status: 200, reaches: true},
		{name: "Worker Stream", host: "wails.localhost:48217", path: "/wails/stream/poll", status: 200, reaches: true},
		{name: "Worker document without generation", host: "wails.localhost:48217", path: "/", status: 503},
	}
	for _, c := range cases {
		reached = ""
		r := httptest.NewRequest("POST", "http://"+c.host+c.path, nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.referer != "" {
			r.Header.Set("Referer", c.referer)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != c.status || (reached != "") != c.reaches {
			t.Fatalf("%s: status %d reached %q", c.name, w.Code, reached)
		}
		if got := w.Header().Get("Content-Security-Policy") == "frame-ancestors 'none'"; got != c.framing {
			t.Fatalf("%s: framing policy %v", c.name, got)
		}
	}
}

func TestWorkerMiddlewareUsesFiniteHTTPForOrdinaryRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/echo" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_, _ = io.Copy(w, r.Body)
	}))
	defer upstream.Close()
	bridge := &Bridge{Client: upstream.Client(), Origin: upstream.URL, Generation: "g1"}
	s := &Surface{Host: "wails.localhost:48217", Current: func() *Bridge { return bridge }}
	request := httptest.NewRequest(http.MethodPost, "/api/echo", strings.NewReader("finite body"))
	request.Host = "wails.localhost:48217"
	response := httptest.NewRecorder()
	s.Middleware(http.NotFoundHandler()).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "finite body" {
		t.Fatalf("finite HTTP proxy: status=%d body=%q", response.Code, response.Body.String())
	}
}
