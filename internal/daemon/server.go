package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
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
	Update        UpdateSnapshot
	Events        []Event
	Cursor        uint64
	Diagnostics   supervisor.Diagnostics
}
type Server struct {
	Services map[string]any
	Host     *app.Host
	Channel  *workerchannel.Adapter
	Settings *settings.Manager
	Root     string
	PID      int
	OpenUI   func(string) error
	// UpdateState and UpdateCommand expose the daemon-owned updater to trusted
	// local clients. The UI renders UpdateState and sends explicit actions;
	// provider and staging details remain inside the daemon process.
	UpdateState   func() UpdateSnapshot
	UpdateCommand func(context.Context, UpdateAction) error
	Media         http.Handler
	// Maintenance reports whether the current-user installer owns the
	// maintenance boundary. Read-only snapshots remain available while the
	// boundary is held; all mutable service calls are rejected at this daemon
	// authority. HostService.Quit is the one installer stop handshake.
	Maintenance func() bool
	mu          sync.Mutex
	events      []Event
	cursor      uint64
	focused     bool
	focusSeen   time.Time
	draining    atomic.Bool
	activeMu    sync.Mutex
	active      map[uint64]context.CancelFunc
	activeNext  uint64
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
	if r.Body != nil && r.Body != http.NoBody {
		// Request bodies can remain active while a duplex Worker response is
		// being delivered. Do not let the HTTP/1 server reuse this pipe until
		// its body reader has been torn down.
		w.Header().Set("Connection", "close")
	}
	// Browser requests cannot supply this transport: the listener is an ACL-
	// restricted local named pipe. Reject forwarded Web origins nonetheless.
	if r.Header.Get("Origin") != "" && r.URL.Path != "/worker" {
		http.Error(w, "forbidden", 403)
		return
	}
	switch r.URL.Path {
	case "/web-boot":
		var input struct {
			Generation string
			Detail     string
		}
		if r.Method != http.MethodPost || json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&input) != nil {
			http.Error(w, "invalid web boot report", 400)
			return
		}
		if err := s.Host.ReportWebBoot(input.Generation, input.Detail); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		writeJSON(w, true)
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
		} else if state.Status.State == lifecycle.StateStarting {
			state.URL = s.Host.WebBootURL()
		}
		if s.Settings != nil {
			state.Preferences, _ = s.Settings.Snapshot(r.Context())
		}
		if s.UpdateState != nil {
			state.Update = s.UpdateState()
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
		if isQuitCall(call) {
			// Quit is the installer's stop handshake. Mark the daemon draining
			// before invoking it so a concurrent mutable call cannot pass the
			// maintenance check, and cancel calls that were already in flight.
			s.beginDrain()
		}
		if s.maintenanceBusy() && !maintenanceCallAllowed(call) {
			writeJSON(w, Result{Error: maintenanceFailure().Error()})
			return
		}
		value, err := s.invoke(r.Context(), call)
		if isQuitCall(call) && err != nil {
			s.ResetDrain()
		}
		result := Result{Value: value}
		if err != nil {
			result.Error = err.Error()
		}
		writeJSON(w, result)
	case "/open":
		if s.maintenanceBusy() {
			http.Error(w, maintenanceFailure().Error(), http.StatusServiceUnavailable)
			return
		}
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
	case "/update/action":
		if s.maintenanceBusy() {
			http.Error(w, maintenanceFailure().Error(), http.StatusServiceUnavailable)
			return
		}
		if s.UpdateCommand == nil {
			http.Error(w, "update controls unavailable", http.StatusServiceUnavailable)
			return
		}
		var input struct {
			Action UpdateAction `json:"action"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&input); err != nil {
			http.Error(w, "invalid update action", http.StatusBadRequest)
			return
		}
		if input.Action != UpdateActionCheck && input.Action != UpdateActionDownload && input.Action != UpdateActionInstall {
			http.Error(w, "invalid update action", http.StatusBadRequest)
			return
		}
		if err := s.UpdateCommand(r.Context(), input.Action); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		if s.UpdateState != nil {
			writeJSON(w, s.UpdateState())
		} else {
			writeJSON(w, true)
		}
	case "/geometry":
		if s.maintenanceBusy() {
			http.Error(w, maintenanceFailure().Error(), http.StatusServiceUnavailable)
			return
		}
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
			if s.maintenanceBusy() {
				http.Error(w, maintenanceFailure().Error(), http.StatusServiceUnavailable)
				return
			}
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

func (s *Server) maintenanceBusy() bool {
	return s != nil && (s.draining.Load() || s.Maintenance != nil && s.Maintenance())
}

func isQuitCall(call Call) bool {
	return call.Service == "HostService" && call.Method == "Quit" && call.Surface == "workspace" && len(call.Args) == 0
}

func (s *Server) beginDrain() {
	if s == nil || s.draining.Swap(true) {
		return
	}
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	for _, cancel := range s.active {
		cancel()
	}
}

// BeginDrain starts the daemon stop boundary for trusted in-process controls.
// Remote HostService.Quit calls enter the same path through ServeHTTP.
func (s *Server) BeginDrain() {
	s.beginDrain()
}

// ResetDrain reopens mutable calls after a requested quit failed its cleanup
// phase. A successful quit tears down the daemon, so only that recovery path
// needs to clear the transient drain state.
func (s *Server) ResetDrain() {
	if s != nil {
		s.draining.Store(false)
	}
}

func (s *Server) trackCall(cancel context.CancelFunc) func() {
	s.activeMu.Lock()
	if s.active == nil {
		s.active = make(map[uint64]context.CancelFunc)
	}
	s.activeNext++
	id := s.activeNext
	s.active[id] = cancel
	draining := s.draining.Load()
	s.activeMu.Unlock()
	if draining {
		cancel()
	}
	return func() {
		s.activeMu.Lock()
		delete(s.active, id)
		s.activeMu.Unlock()
	}
}

func maintenanceCallAllowed(call Call) bool {
	if call.Service == "HostService" {
		switch call.Method {
		case "GetStatus", "GetWorkspaceStatus", "GetTheme", "GetLocale", "GetStartupOutput", "Quit":
			return true
		}
	}
	if call.Service == "ManagerService" {
		switch call.Method {
		case "GetSnapshot", "GetTheme", "ListProfileBackups", "PreviewRestorePoint":
			return true
		}
	}
	if call.Service == "SettingsService" && call.Method == "GetSettings" {
		return true
	}
	if call.Service == "StorageService" {
		return call.Method == "GetLocations" || call.Method == "DefaultUserDataPath"
	}
	if call.Service == "PetSettingsService" {
		switch call.Method {
		case "GetPetPanel", "GetPetOverlay", "GetPetPresentation", "GetPetPreview", "GetPetPlayback":
			return true
		}
	}
	return false
}

func maintenanceFailure() lifecycle.Failure {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorManagerOperationBusy,
		Summary:       "dsh-work is being updated.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Wait for the installer to finish, then retry the command.",
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
	if s.draining.Load() && !maintenanceCallAllowed(call) {
		return nil, maintenanceFailure()
	}
	// Accepted manager operations belong to the daemon, not to the lifetime of
	// the HTTP connection. Keep that property while retaining a daemon-level
	// cancellation path for the installer stop handshake.
	base := app.LocalClientContext(context.WithoutCancel(ctx), call.Surface)
	operationCtx, cancel := context.WithCancel(base)
	defer cancel()
	stopTracking := s.trackCall(cancel)
	defer stopTracking()
	args := []reflect.Value{reflect.ValueOf(operationCtx)}
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
	if isWorkerUpgrade(r) {
		forwardWorkerUpgrade(w, r, current, decodedPath, path)
		return
	}
	forwardWorkerRequest(w, r, current, decodedPath, path)
}

func isWorkerUpgrade(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

func forwardWorkerUpgrade(w http.ResponseWriter, r *http.Request, current workerchannel.Session, decodedPath, rawPath string) {
	proxy := httputil.ReverseProxy{Director: func(out *http.Request) {
		out.URL.Scheme = "http"
		out.URL.Host = "127.0.0.1:1"
		out.Host = out.URL.Host
		out.URL.Path = decodedPath
		out.URL.RawPath = rawPath
		out.Header.Del("X-DSH-Path")
		out.Header.Del("X-DSH-Generation")
		out.Header.Del("Cookie")
		out.Header.Del("Authorization")
		out.Header.Set("Origin", workeripc.Origin)
	}, Transport: workerTransport{client: current.Client()}, FlushInterval: -1, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "Worker unavailable", http.StatusBadGateway)
	}}
	proxy.ServeHTTP(w, r)
}

// forwardWorkerRequest owns both sides of a full-duplex HTTP/1 request. The
// standard ReverseProxy can return after the upstream response ends while its
// transport goroutine is still reading the incoming request body. net/http then
// starts its keep-alive read on the same connection and panics with
// "invalid concurrent Body.Read call". Keeping the body read under our control
// lets cancellation close it and wait for that read before the handler returns.
func forwardWorkerRequest(w http.ResponseWriter, r *http.Request, current workerchannel.Session, decodedPath, rawPath string) {
	ctx, cancel := context.WithCancel(r.Context())

	var requestBody *trackedRequestBody
	defer func() {
		cancel()
		if requestBody != nil {
			// A full-duplex response may finish before the browser closes its
			// upload stream. Interrupt an in-flight server-body read before
			// closing it; Request.Body.Close itself waits for that read's mutex.
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now())
			_ = requestBody.Close()
			requestBody.Wait()
			_ = controller.SetReadDeadline(time.Time{})
		}
	}()
	out := r.Clone(ctx)
	out.RequestURI = ""
	out.URL = &url.URL{
		Scheme:   "http",
		Host:     "127.0.0.1:1",
		Path:     decodedPath,
		RawPath:  rawPath,
		RawQuery: r.URL.RawQuery,
	}
	out.Host = out.URL.Host
	out.Close = false
	for _, name := range []string{
		"X-DSH-Path", "X-DSH-Generation", "Cookie", "Authorization",
		"Connection", "Upgrade", "Transfer-Encoding", "Content-Length",
		"Proxy-Authorization", "Proxy-Connection",
	} {
		out.Header.Del(name)
	}
	out.Header.Set("Origin", workeripc.Origin)
	if r.Body == nil || r.Body == http.NoBody {
		out.Body = nil
		out.ContentLength = 0
	} else {
		requestBody = newTrackedRequestBody(r.Body)
		out.Body = requestBody
		out.GetBody = nil
		// The incoming named-pipe HTTP/1 connection cannot be reused while a
		// browser upload is being cancelled. Mark it for close so net/http does
		// not start its next keep-alive read before the body teardown settles.
		w.Header().Set("Connection", "close")
	}

	response, err := (workerTransport{client: current.Client()}).RoundTrip(out)
	if err != nil {
		// RoundTrip can fail while the upload is still open (for example when
		// the Worker is being replaced). Keep this daemon connection one-shot
		// before writing the error so net/http never starts its next request
		// read while the request body teardown is in progress.
		w.Header().Set("Connection", "close")
		http.Error(w, "Worker unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if err := writeWorkerResponse(w, r, response); err != nil {
		// The downstream UI may have cancelled after receiving enough of a
		// stream. The deferred body close still synchronizes the upload reader.
		return
	}
}

func writeWorkerResponse(w http.ResponseWriter, request *http.Request, response *http.Response) error {
	copyWorkerResponseHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	if request.Method == http.MethodHead || response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusResetContent || response.StatusCode == http.StatusNotModified {
		return nil
	}
	flush := http.NewResponseController(w).Flush
	buffer := make([]byte, 32<<10)
	for {
		n, err := response.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := w.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			_ = flush()
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func copyWorkerResponseHeaders(dst, src http.Header) {
	hopByHop := map[string]struct{}{
		"Connection": {}, "Keep-Alive": {}, "Proxy-Authenticate": {},
		"Proxy-Authorization": {}, "Te": {}, "Trailer": {},
		"Transfer-Encoding": {}, "Upgrade": {},
	}
	for _, value := range src.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			hopByHop[http.CanonicalHeaderKey(strings.TrimSpace(name))] = struct{}{}
		}
	}
	for name, values := range src {
		if _, skip := hopByHop[http.CanonicalHeaderKey(name)]; skip {
			continue
		}
		dst[name] = append([]string(nil), values...)
	}
}

type trackedRequestBody struct {
	src       io.ReadCloser
	done      chan struct{}
	doneOnce  sync.Once
	closeOnce sync.Once
	mu        sync.Mutex
	closed    bool
	reading   bool
}

func newTrackedRequestBody(src io.ReadCloser) *trackedRequestBody {
	return &trackedRequestBody{src: src, done: make(chan struct{})}
}

func (b *trackedRequestBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	b.reading = true
	b.mu.Unlock()

	n, err := b.src.Read(p)
	b.mu.Lock()
	b.reading = false
	closed := b.closed
	b.mu.Unlock()
	if err != nil || closed {
		b.finish()
	}
	return n, err
}

func (b *trackedRequestBody) Close() error {
	b.mu.Lock()
	wasClosed := b.closed
	b.closed = true
	reading := b.reading
	b.mu.Unlock()
	var closeErr error
	if !wasClosed {
		b.closeOnce.Do(func() { closeErr = b.src.Close() })
	}
	if !reading {
		b.finish()
	}
	return closeErr
}

func (b *trackedRequestBody) finish() { b.doneOnce.Do(func() { close(b.done) }) }

func (b *trackedRequestBody) Wait() { <-b.done }

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
