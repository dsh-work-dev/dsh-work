package daemon

import (
	"bytes"
	"context"
	"encoding/json"
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
	for _, path := range []string{"/open", "/geometry", "/worker"} {
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
