package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/local/dsh-work/internal/workeripc"
)

type Event struct {
	ID   uint64
	Name string
	Data json.RawMessage
}
type Snapshot struct {
	Protocol, PID int
	Root, URL     string
	Status        lifecycle.Status
	Preferences   settings.Values
	Events        []Event
	Cursor        uint64
	Diagnostics   supervisor.Diagnostics
}
type Server struct {
	Services  map[string]any
	Host      *app.Host
	Channel   *workerchannel.Adapter
	Settings  *settings.Manager
	Root      string
	PID       int
	OpenUI    func(string) error
	Media     http.Handler
	mu        sync.Mutex
	events    []Event
	cursor    uint64
	focused   bool
	focusSeen time.Time
}

func (s *Server) Publish(name string, data any) {
	raw, err := json.Marshal(data)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor++
	s.events = append(s.events, Event{ID: s.cursor, Name: name, Data: raw})
	if len(s.events) > 256 {
		s.events = append([]Event(nil), s.events[len(s.events)-256:]...)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Browser requests cannot supply this transport: the listener is an ACL-
	// restricted local named pipe. Reject forwarded Web origins nonetheless.
	if r.Header.Get("Origin") != "" && r.URL.Path != "/worker" {
		http.Error(w, "forbidden", 403)
		return
	}
	switch r.URL.Path {
	case "/snapshot":
		var input struct {
			Cursor  uint64
			Focused *bool
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input)
		if input.Focused != nil {
			s.mu.Lock()
			s.focused = *input.Focused
			s.focusSeen = time.Now()
			s.mu.Unlock()
		}
		state := Snapshot{Protocol: Protocol, PID: s.PID, Root: s.Root, Status: s.Host.Status(), Diagnostics: s.Host.Diagnostics()}
		if state.Status.State == lifecycle.StateReady {
			state.URL = state.Status.WorkspaceURL
		}
		if s.Settings != nil {
			state.Preferences, _ = s.Settings.Snapshot(r.Context())
		}
		s.mu.Lock()
		state.Cursor = s.cursor
		for _, event := range s.events {
			if event.ID > input.Cursor {
				state.Events = append(state.Events, event)
			}
		}
		s.mu.Unlock()
		writeJSON(w, state)
	case "/control":
		var call Call
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&call); err != nil {
			http.Error(w, "invalid call", 400)
			return
		}
		value, err := s.invoke(r.Context(), call)
		result := Result{Value: value}
		if err != nil {
			result.Error = err.Error()
		}
		writeJSON(w, result)
	case "/open":
		var section string
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&section) != nil {
			http.Error(w, "invalid section", 400)
			return
		}
		if err := s.OpenUI(section); err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		writeJSON(w, true)
	case "/geometry":
		var input struct {
			Window   string
			Geometry settings.WindowGeometry
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input) != nil || (input.Window != "workspace" && input.Window != "worker" && input.Window != "settings") {
			http.Error(w, "invalid geometry", 400)
			return
		}
		if input.Window == "worker" {
			input.Window = "workspace"
		}
		if err := s.Settings.SetWindowGeometry(r.Context(), input.Window, input.Geometry); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		writeJSON(w, true)
	default:
		if r.URL.Path == "/worker" {
			s.forwardWorker(w, r)
			return
		}
		if s.Media != nil {
			s.Media.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	}
}

// Only the existing typed service binding methods are exported. Fields,
// constructors, Host internals and methods without a context are inaccessible.
func (s *Server) invoke(ctx context.Context, call Call) (data json.RawMessage, err error) {
	defer func() {
		if recover() != nil {
			data = nil
			err = errors.New("background call failed")
		}
	}()
	if call.Surface != "workspace" && call.Surface != "settings" {
		return nil, errors.New("invalid client surface")
	}
	service, ok := s.Services[call.Service]
	if !ok {
		return nil, errors.New("unknown service")
	}
	method := reflect.ValueOf(service).MethodByName(call.Method)
	if !method.IsValid() {
		return nil, errors.New("unknown method")
	}
	t := method.Type()
	if t.NumIn() == 0 || t.In(0) != reflect.TypeOf((*context.Context)(nil)).Elem() || t.NumIn() != len(call.Args)+1 {
		return nil, errors.New("invalid arguments")
	}
	// Accepted manager operations belong to the daemon, not to the lifetime of
	// the HTTP connection. Existing service timeouts and explicit cancel methods
	// bound them; loss of a UI cannot interrupt an in-flight restore/install.
	ctx = app.LocalClientContext(context.WithoutCancel(ctx), call.Surface)
	args := []reflect.Value{reflect.ValueOf(ctx)}
	for i, raw := range call.Args {
		value := reflect.New(t.In(i + 1))
		if err := json.Unmarshal(raw, value.Interface()); err != nil {
			return nil, err
		}
		args = append(args, value.Elem())
	}
	results := method.Call(args)
	if len(results) > 0 && t.Out(len(results)-1) == reflect.TypeOf((*error)(nil)).Elem() {
		last := results[len(results)-1]
		results = results[:len(results)-1]
		if !last.IsNil() {
			return nil, last.Interface().(error)
		}
	}
	if len(results) == 0 {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(results[0].Interface())
}

func (s *Server) forwardWorker(w http.ResponseWriter, r *http.Request) {
	// The Worker may produce response chunks before consuming the upload.
	// Go HTTP/1 otherwise drains the request on the first response write,
	// deadlocking the existing Wails upload acknowledgements/download credits.
	if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
		http.Error(w, "duplex transport unavailable", http.StatusInternalServerError)
		return
	}
	current := s.Channel.Current()
	if current == nil || r.Header.Get("X-DSH-Generation") != current.Generation() {
		http.Error(w, "Worker expired", 410)
		return
	}
	path := r.Header.Get("X-DSH-Path")
	if len(path) == 0 || path[0] != '/' {
		http.Error(w, "invalid Worker path", 400)
		return
	}
	decodedPath, err := url.PathUnescape(path)
	if err != nil {
		http.Error(w, "invalid Worker path", 400)
		return
	}
	proxy := httputil.ReverseProxy{Director: func(out *http.Request) {
		out.URL.Scheme = "http"
		out.URL.Host = "127.0.0.1:1"
		out.Host = out.URL.Host
		out.URL.Path = decodedPath
		out.URL.RawPath = path
		out.Header.Del("X-DSH-Path")
		out.Header.Del("X-DSH-Generation")
		out.Header.Del("Cookie")
		out.Header.Del("Authorization")
		out.Header.Set("Origin", workeripc.Origin)
	}, Transport: workerTransport{client: current.Client()}, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "Worker unavailable", 502) }}
	proxy.ServeHTTP(w, r)
}

type workerTransport struct{ client *http.Client }

func (t workerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.client.Jar != nil {
		for _, cookie := range t.client.Jar.Cookies(r.URL) {
			r.AddCookie(cookie)
		}
	}
	resp, err := t.client.Transport.RoundTrip(r)
	if err == nil && t.client.Jar != nil {
		t.client.Jar.SetCookies(r.URL, resp.Cookies())
		resp.Header.Del("Set-Cookie")
	}
	return resp, err
}
func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// HTTPServer retains streaming responses while bounding idle request headers.
func HTTPServer(handler http.Handler) *http.Server {
	return &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 64 << 10}
}

func (s *Server) UIFocused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.focused && time.Since(s.focusSeen) < 2*time.Second
}
