package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
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
	for _, path := range []string{"/open", "/update/action", "/geometry", "/worker"} {
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
		UpdateCommand: func(_ context.Context, action UpdateAction) error {
			called = action
			return nil
		},
		UpdateState: func() UpdateSnapshot { return UpdateSnapshot{Phase: UpdateChecking} },
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/update/action", strings.NewReader(`{"action":"check"}`))
	server.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("update action status = %d, body=%q", recorder.Code, recorder.Body.String())
	}
	if called != UpdateActionCheck {
		t.Fatalf("update action = %q, want check", called)
	}
	if got := strings.TrimSpace(recorder.Body.String()); !strings.Contains(got, `"phase":"checking"`) {
		t.Fatalf("update action body = %q, want checking state", got)
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
	workerClient := &http.Client{Transport: earlyResponseTransport{}}
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
		_, err := writer.Write([]byte("body that remains open while the worker answers"))
		writeDone <- err
	}()
	response, err := frontend.Client().Do(request)
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

type earlyResponseTransport struct{}

func (earlyResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        http.Header{"Content-Length": []string{"5"}},
		Body:          io.NopCloser(strings.NewReader("early")),
		ContentLength: 5,
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
