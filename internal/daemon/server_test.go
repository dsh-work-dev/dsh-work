package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/dshinteraction"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/presence"
	"github.com/local/dsh-work/internal/settings"
)

type maintenanceTestService struct {
	mutated bool
	quit    bool
}

type drainingTestService struct {
	started  chan struct{}
	canceled chan struct{}
	quit     bool
}

func (s *drainingTestService) Mutate(ctx context.Context) error {
	close(s.started)
	<-ctx.Done()
	close(s.canceled)
	return ctx.Err()
}

func (s *drainingTestService) Quit(context.Context) error {
	s.quit = true
	return nil
}

func (s *maintenanceTestService) Mutate(context.Context) error {
	s.mutated = true
	return nil
}

func (s *maintenanceTestService) GetStatus(context.Context) string { return "ready" }

func (s *maintenanceTestService) Quit(context.Context) error {
	s.quit = true
	return nil
}

func TestServerBlocksMutableCallsDuringInstallerMaintenance(t *testing.T) {
	service := &maintenanceTestService{}
	server := &Server{
		Services:    map[string]any{"HostService": service},
		Maintenance: func() bool { return true },
	}

	result := postCall(t, server, Call{Service: "HostService", Method: "Mutate", Surface: "workspace"})
	if !strings.Contains(result.Error, string(lifecycle.ErrorManagerOperationBusy)) {
		t.Fatalf("maintenance error = %q, want MANAGER_OPERATION_BUSY", result.Error)
	}
	if service.mutated {
		t.Fatal("mutable service was invoked during maintenance")
	}

	result = postCall(t, server, Call{Service: "HostService", Method: "GetStatus", Surface: "workspace"})
	if result.Error != "" || string(result.Value) != `"ready"` {
		t.Fatalf("read-only call result = %+v, want ready", result)
	}

	result = postCall(t, server, Call{Service: "HostService", Method: "Quit", Surface: "workspace"})
	if result.Error != "" || !service.quit {
		t.Fatalf("quit call result = %+v, quit=%v", result, service.quit)
	}
}

func TestServerBlocksUIAndWorkerRoutesDuringInstallerMaintenance(t *testing.T) {
	server := &Server{Maintenance: func() bool { return true }}
	for _, path := range []string{"/open", "/worker"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader([]byte(`{}`)))
		server.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want %d", path, recorder.Code, http.StatusServiceUnavailable)
		}
		if !strings.Contains(recorder.Body.String(), "MANAGER_OPERATION_BUSY") {
			t.Fatalf("%s body = %q, want typed maintenance error", path, recorder.Body.String())
		}
	}
}

func TestServerStartsUpdateAction(t *testing.T) {
	var called UpdateAction
	server := &Server{
		Services: map[string]any{"HostService": &HostService{
			UpdateCommand: func(_ context.Context, action UpdateAction) error { called = action; return nil },
			UpdateState:   func() UpdateSnapshot { return UpdateSnapshot{Phase: UpdateChecking} },
		}},
	}
	result := postCall(t, server, Call{Service: "HostService", Method: "UpdateAction", Surface: "workspace", Args: []json.RawMessage{json.RawMessage(`"check"`)}})
	if result.Error != "" || called != UpdateActionCheck {
		t.Fatalf("update action result=%+v called=%q", result, called)
	}
	if !strings.Contains(string(result.Value), `"phase":"checking"`) {
		t.Fatalf("update action value = %s, want checking state", result.Value)
	}
}

func TestServerDoesNotKeepLegacyUpdateRoute(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/update/check", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("legacy update route status = %d, want 404", recorder.Code)
	}
}

func TestServerQuitCancelsInFlightCalls(t *testing.T) {
	service := &drainingTestService{started: make(chan struct{}), canceled: make(chan struct{})}
	server := &Server{Services: map[string]any{"HostService": service}}

	body, err := json.Marshal(Call{Service: "HostService", Method: "Mutate", Surface: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	mutateDone := make(chan struct{})
	go func() {
		request := httptest.NewRequest(http.MethodPost, "/control", bytes.NewReader(body))
		server.ServeHTTP(httptest.NewRecorder(), request)
		close(mutateDone)
	}()
	select {
	case <-service.started:
	case <-time.After(time.Second):
		t.Fatal("mutable call did not start")
	}

	result := postCall(t, server, Call{Service: "HostService", Method: "Quit", Surface: "workspace"})
	if result.Error != "" || !service.quit {
		t.Fatalf("quit call result = %+v, quit=%v", result, service.quit)
	}
	select {
	case <-service.canceled:
	case <-time.After(time.Second):
		t.Fatal("in-flight call was not canceled by Quit")
	}
	select {
	case <-mutateDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight call did not return")
	}
}

func TestForwardWorkerWaitsForEarlyResponseUpload(t *testing.T) {
	const upload = "body that remains open while the worker answers"
	// The Worker consumes the bytes already sent before answering, as a real
	// streaming upload would. Bytes left unread on the loopback socket make
	// Windows reset the connection when the frontend closes it, and the reset
	// can discard the early response before the client reads it.
	workerClient := &http.Client{Transport: earlyResponseTransport{consume: len(upload)}}
	session := testWorkerSession{client: workerClient, generation: "test-generation"}

	var serverLog bytes.Buffer
	frontend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := http.NewResponseController(w).EnableFullDuplex(); err != nil {
			t.Errorf("frontend full duplex: %v", err)
			return
		}
		forwardWorkerRequest(w, r, session, "/early", "/early")
	}))
	frontend.Config.ErrorLog = log.New(&serverLog, "", 0)
	defer frontend.Close()

	reader, writer := io.Pipe()
	request, err := http.NewRequest(http.MethodPost, frontend.URL, reader)
	if err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte(upload))
		writeDone <- err
	}()
	type roundTrip struct {
		response *http.Response
		err      error
	}
	done := make(chan roundTrip, 1)
	go func() {
		response, err := frontend.Client().Do(request)
		done <- roundTrip{response, err}
	}()
	var response *http.Response
	select {
	case result := <-done:
		response, err = result.response, result.err
	case <-time.After(10 * time.Second):
		// A failed round trip waits for the request writer, which waits for
		// more upload bytes; end the upload so the test fails instead of hanging.
		_ = writer.CloseWithError(errors.New("early response did not arrive"))
		t.Fatal("early response did not arrive while the upload was open")
	}
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "early" {
		t.Fatalf("body = %q, want early response", body)
	}
	_ = writer.Close()
	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("upload body did not close after early response")
	}
	time.Sleep(50 * time.Millisecond)
	if logText := serverLog.String(); strings.Contains(logText, "invalid concurrent Body.Read call") || strings.Contains(logText, "ReverseProxy read error") {
		t.Fatalf("unexpected proxy lifecycle error: %s", logText)
	}
}

func TestForwardWorkerPreservesAccountCompletionRedirect(t *testing.T) {
	const completionURL = "https://chat.deepseek.com/desktop/complete"
	session := testWorkerSession{client: &http.Client{Transport: completionRedirectTransport{location: completionURL}}, generation: "test-generation"}
	request := httptest.NewRequest(http.MethodGet, "/worker?code=code&state=state", nil)
	recorder := httptest.NewRecorder()
	forwardWorkerRequest(recorder, request, session, "/oauth/callback", "/oauth/callback")
	if recorder.Code != http.StatusFound || recorder.Header().Get("Location") != completionURL {
		t.Fatalf("callback redirect returned status %d with location %q", recorder.Code, recorder.Header().Get("Location"))
	}
}

type testWorkerSession struct {
	client     *http.Client
	generation string
}

func (s testWorkerSession) URL() string          { return "http://127.0.0.1:1" }
func (s testWorkerSession) Generation() string   { return s.generation }
func (s testWorkerSession) Patch() string        { return "" }
func (s testWorkerSession) Client() *http.Client { return s.client }
func (s testWorkerSession) Activate(string)      {}
func (s testWorkerSession) Close() error         { return nil }

// earlyResponseTransport answers before the upload ends, after reading the
// first consume bytes of the request body.
type earlyResponseTransport struct{ consume int }

func (t earlyResponseTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if t.consume > 0 && request.Body != nil {
		if _, err := io.ReadFull(request.Body, make([]byte, t.consume)); err != nil {
			return nil, err
		}
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Length": []string{"5"}},
		Body:          io.NopCloser(strings.NewReader("early")),
		ContentLength: 5,
	}, nil
}

type completionRedirectTransport struct{ location string }

func (t completionRedirectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{t.location}},
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

func postCall(t *testing.T, server *Server, call Call) Result {
	t.Helper()
	body, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/control", bytes.NewReader(body))
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("control status = %d, body=%q", recorder.Code, recorder.Body.String())
	}
	var result Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestServerSavesWorkspaceZoom(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := settings.New(settings.Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	service := &WindowService{Settings: manager}
	server := &Server{Services: map[string]any{"WindowService": service}}
	if result := postCall(t, server, Call{Service: "WindowService", Method: "SaveZoom", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`1.2`)}}); result.Error != "" {
		t.Fatalf("workspace-2 zoom result = %+v", result)
	}
	if result := postCall(t, server, Call{Service: "WindowService", Method: "SaveZoom", Surface: "workspace", Args: []json.RawMessage{json.RawMessage(`0.5`)}}); result.Error == "" {
		t.Fatal("invalid zoom was accepted")
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil || values.WorkspaceZoom != 1.2 {
		t.Fatalf("saved zoom = %v error=%v", values.WorkspaceZoom, err)
	}
}

func TestWindowServiceSavesGeometryOnlyForWorkspace(t *testing.T) {
	manager, err := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{Services: map[string]any{"WindowService": &WindowService{Settings: manager}}}
	geometry := settings.WindowGeometry{X: -1280, Y: 40, Positioned: true, Width: 1280, Height: 800, Maximised: true}
	encoded, err := json.Marshal(geometry)
	if err != nil {
		t.Fatal(err)
	}
	if result := postCall(t, server, Call{Service: "WindowService", Method: "SaveGeometry", Surface: "workspace-2", Args: []json.RawMessage{encoded}}); result.Error != "" {
		t.Fatalf("non-primary geometry result = %+v", result)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil || values.WorkspaceWindow != (settings.WindowGeometry{}) {
		t.Fatalf("non-primary window saved geometry: %+v error=%v", values.WorkspaceWindow, err)
	}
	if result := postCall(t, server, Call{Service: "WindowService", Method: "SaveGeometry", Surface: "workspace", Args: []json.RawMessage{encoded}}); result.Error != "" {
		t.Fatalf("workspace geometry result = %+v", result)
	}
	values, err = manager.Snapshot(context.Background())
	if err != nil || values.WorkspaceWindow != geometry {
		t.Fatalf("workspace geometry = %+v error=%v", values.WorkspaceWindow, err)
	}
}

type surfaceEchoService struct{}

func (surfaceEchoService) Read(ctx context.Context) string { return SurfaceFrom(ctx) }

func TestInvokeAcceptsOnlyCanonicalWorkspaceNamesAndPassesSurface(t *testing.T) {
	server := &Server{Services: map[string]any{"SurfaceEchoService": surfaceEchoService{}}}
	for _, surface := range []string{"workspace", "workspace-2", "workspace-12"} {
		result := postCall(t, server, Call{Service: "SurfaceEchoService", Method: "Read", Surface: surface})
		if result.Error != "" || string(result.Value) != `"`+surface+`"` {
			t.Fatalf("surface %q result = %+v", surface, result)
		}
	}
	for _, surface := range []string{"workspace-1", "workspace-02", "worker", "workspace-x"} {
		result := postCall(t, server, Call{Service: "SurfaceEchoService", Method: "Read", Surface: surface})
		if result.Error == "" {
			t.Errorf("invalid surface %q was accepted", surface)
		}
	}
}

type webBootReportService struct{ got string }

func (s *webBootReportService) ReportWebBoot(ctx context.Context, generation, detail string) error {
	s.got = SurfaceFrom(ctx) + ":" + generation + ":" + detail
	return nil
}

func TestHostServiceReportWebBootAndWindowFocusAreMaintenanceAllowed(t *testing.T) {
	tracker := presence.New()
	report := &webBootReportService{}
	server := &Server{
		Services: map[string]any{
			"HostService":    report,
			"WindowService":  &WindowService{Presence: tracker},
			"DSHPageService": &DSHPageService{Activity: dshactivity.New(), Interactions: dshinteraction.New()},
		},
		Maintenance: func() bool { return true },
	}
	for _, call := range []Call{
		{Service: "HostService", Method: "ReportWebBoot", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`"g1"`), json.RawMessage(`"ready"`)}},
		{Service: "WindowService", Method: "SetFocus", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`true`)}},
		{Service: "WindowService", Method: "Closed", Surface: "workspace-2"},
		{Service: "DSHPageService", Method: "ReportSessionOpened", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`"7"`), json.RawMessage(`""`)}},
		{Service: "DSHPageService", Method: "ReportInteraction", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`{"instance":"page-1","sessionId":"","kind":"","key":"","toolName":"","current":"session-1","visible":true}`)}},
		{Service: "DSHPageService", Method: "ResetInteractions", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`"page-1"`)}},
	} {
		if result := postCall(t, server, call); result.Error != "" {
			t.Errorf("maintenance-allowed call %s.%s failed: %+v", call.Service, call.Method, result)
		}
	}
	if report.got != "workspace-2:g1:ready" {
		t.Fatalf("web boot report = %q", report.got)
	}
	if !maintenanceCallAllowed(Call{Service: "WindowService", Method: "SetFocus"}) || !maintenanceCallAllowed(Call{Service: "WindowService", Method: "Closed"}) || !maintenanceCallAllowed(Call{Service: "HostService", Method: "ReportWebBoot"}) || !maintenanceCallAllowed(Call{Service: "DSHPageService", Method: "ReportSessionOpened"}) || !maintenanceCallAllowed(Call{Service: "DSHPageService", Method: "ReportInteraction"}) || !maintenanceCallAllowed(Call{Service: "DSHPageService", Method: "ResetInteractions"}) {
		t.Fatal("required maintenance calls are not allow-listed")
	}
	for _, call := range []Call{{Service: "WindowService", Method: "SaveZoom"}, {Service: "WindowService", Method: "SaveGeometry"}, {Service: "HostService", Method: "UpdateAction"}} {
		if maintenanceCallAllowed(call) {
			t.Errorf("unexpected maintenance allow-list entry: %s.%s", call.Service, call.Method)
		}
	}
}

func TestDSHPageInteractionMethodsRequireAWorkspaceSurface(t *testing.T) {
	store := dshinteraction.New()
	server := &Server{Services: map[string]any{"DSHPageService": &DSHPageService{Interactions: store}}}
	contextReport := `{"instance":"page-1","sessionId":"","kind":"","key":"","toolName":"","current":"session-1","visible":true}`
	if result := postCall(t, server, Call{Service: "DSHPageService", Method: "ReportInteraction", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(contextReport)}}); result.Error != "" {
		t.Fatalf("workspace-2 interaction report failed: %+v", result)
	}
	if instance, current, visible, ok := store.Page("workspace-2"); !ok || instance != "page-1" || current != "session-1" || !visible {
		t.Fatalf("workspace-2 page = %q %q %t %t", instance, current, visible, ok)
	}
	if result := postCall(t, server, Call{Service: "DSHPageService", Method: "ReportInteraction", Surface: "pet", Args: []json.RawMessage{json.RawMessage(contextReport)}}); result.Error == "" {
		t.Fatal("accepted an interaction report from a non-workspace surface")
	}
	if result := postCall(t, server, Call{Service: "DSHPageService", Method: "ResetInteractions", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`"page-1"`)}}); result.Error != "" {
		t.Fatalf("workspace-2 interaction reset failed: %+v", result)
	}
	if _, ok := store.Owner("page-1"); ok {
		t.Fatal("page reset did not remove the reported instance")
	}
	if result := postCall(t, server, Call{Service: "DSHPageService", Method: "ResetInteractions", Surface: "pet", Args: []json.RawMessage{json.RawMessage(`"page-1"`)}}); result.Error == "" {
		t.Fatal("accepted an interaction reset from a non-workspace surface")
	}
}

func TestDSHPageInteractionValidationRejectsInvalidReportsWithoutChangingStore(t *testing.T) {
	store := dshinteraction.New()
	added, removed := 0, 0
	store.OnAdded(func(string, string, string, dshinteraction.Interaction) { added++ })
	store.OnRemoved(func(string, string, dshinteraction.Interaction) { removed++ })
	if err := store.Report("workspace", dshinteraction.Report{Instance: "page-1", SessionID: "", Current: "current-1", Visible: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Report("workspace", dshinteraction.Report{Instance: "page-1", SessionID: "session-1", Kind: "approval", Key: "approval:1", ToolName: "shell.exec"}); err != nil {
		t.Fatal(err)
	}
	added, removed = 0, 0
	server := &Server{Services: map[string]any{"DSHPageService": &DSHPageService{Interactions: store}}}
	valid := dshinteraction.Report{Instance: "page-1", SessionID: "session-1", Kind: "question", Key: "replacement", Current: "ignored", Visible: false}
	cases := []struct {
		name   string
		mutate func(*dshinteraction.Report)
	}{
		{name: "empty instance", mutate: func(report *dshinteraction.Report) { report.Instance = "" }},
		{name: "long instance", mutate: func(report *dshinteraction.Report) { report.Instance = strings.Repeat("i", 257) }},
		{name: "long session ID", mutate: func(report *dshinteraction.Report) { report.SessionID = strings.Repeat("s", 257) }},
		{name: "unsupported kind", mutate: func(report *dshinteraction.Report) { report.Kind = "completion" }},
		{name: "missing key", mutate: func(report *dshinteraction.Report) { report.Key = "" }},
		{name: "long key", mutate: func(report *dshinteraction.Report) { report.Key = strings.Repeat("k", 257) }},
		{name: "long tool name", mutate: func(report *dshinteraction.Report) { report.ToolName = strings.Repeat("t", 121) }},
		{name: "long current", mutate: func(report *dshinteraction.Report) {
			report.SessionID, report.Kind, report.Key, report.Current = "", "", "", strings.Repeat("c", 257)
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			report := valid
			test.mutate(&report)
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			added, removed = 0, 0
			result := postCall(t, server, Call{Service: "DSHPageService", Method: "ReportInteraction", Surface: "workspace", Args: []json.RawMessage{encoded}})
			if result.Error == "" {
				t.Fatal("accepted an invalid interaction report")
			}
			instance, current, visible, ok := store.Page("workspace")
			if !ok || instance != "page-1" || current != "current-1" || !visible || added != 0 || removed != 0 {
				t.Fatalf("invalid report changed state: page=%q current=%q visible=%t ok=%t added=%d removed=%d", instance, current, visible, ok, added, removed)
			}
		})
	}
}

func TestDSHPageInteractionResetValidationRejectsInvalidInstancesWithoutChangingStore(t *testing.T) {
	store := dshinteraction.New()
	removed := 0
	store.OnRemoved(func(string, string, dshinteraction.Interaction) { removed++ })
	if err := store.Report("workspace", dshinteraction.Report{Instance: "page-1", SessionID: "session-1", Kind: "question", Key: "question:1"}); err != nil {
		t.Fatal(err)
	}
	server := &Server{Services: map[string]any{"DSHPageService": &DSHPageService{Interactions: store}}}
	for _, instance := range []string{"", strings.Repeat("i", 257)} {
		encoded, err := json.Marshal(instance)
		if err != nil {
			t.Fatal(err)
		}
		result := postCall(t, server, Call{Service: "DSHPageService", Method: "ResetInteractions", Surface: "workspace", Args: []json.RawMessage{encoded}})
		if result.Error == "" {
			t.Errorf("accepted invalid instance of length %d", len(instance))
		}
		if owner, ok := store.Owner("page-1"); !ok || owner != "workspace" || removed != 0 {
			t.Fatalf("invalid reset changed state: owner=%q ok=%t removed=%d", owner, ok, removed)
		}
	}
}

func TestWindowClosedClearsPendingInteractions(t *testing.T) {
	store := dshinteraction.New()
	var removed []string
	store.OnRemoved(func(instance, sessionID string, _ dshinteraction.Interaction) {
		removed = append(removed, instance+":"+sessionID)
	})
	if err := store.Report("workspace-2", dshinteraction.Report{Instance: "page-1", SessionID: "session-1", Kind: "question", Key: "question:1"}); err != nil {
		t.Fatal(err)
	}
	server := &Server{Services: map[string]any{"WindowService": &WindowService{Interactions: store}}}
	if result := postCall(t, server, Call{Service: "WindowService", Method: "Closed", Surface: "workspace-2"}); result.Error != "" {
		t.Fatalf("closing workspace-2 failed: %+v", result)
	}
	if _, ok := store.Owner("page-1"); ok || len(removed) != 1 || removed[0] != "page-1:session-1" {
		t.Fatalf("close cleanup owner=%t removals=%v", ok, removed)
	}
}

func TestDSHPageReportSessionOpenedUsesAWorkspaceSurface(t *testing.T) {
	server := &Server{Services: map[string]any{"DSHPageService": &DSHPageService{Activity: dshactivity.New()}}}
	if result := postCall(t, server, Call{Service: "DSHPageService", Method: "ReportSessionOpened", Surface: "workspace-2", Args: []json.RawMessage{json.RawMessage(`"7"`), json.RawMessage(`""`)}}); result.Error != "" {
		t.Fatalf("workspace-2 report failed: %+v", result)
	}
	if result := postCall(t, server, Call{Service: "DSHPageService", Method: "ReportSessionOpened", Surface: "pet", Args: []json.RawMessage{json.RawMessage(`"7"`), json.RawMessage(`""`)}}); result.Error == "" {
		t.Fatalf("pet surface report was accepted: %+v", result)
	}
}
