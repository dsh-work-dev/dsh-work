package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/supervisor"
	"github.com/local/work/internal/workergateway"
)

type testDSH struct {
	server       *httptest.Server
	announcedURL string
}

type testGateway struct {
	server *httptest.Server
}

func (g *testGateway) Start(context.Context, string) (workergateway.Session, error) {
	return testGatewaySession{origin: g.server.URL + "/", url: g.server.URL + "/__work/bootstrap?session=test-session"}, nil
}

type testGatewaySession struct {
	origin string
	url    string
}

func (s testGatewaySession) URL() string    { return s.url }
func (s testGatewaySession) Origin() string { return s.origin }
func (testGatewaySession) Close() error     { return nil }

func newTestDSH() *testDSH {
	dsh := &testDSH{server: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>test workspace</body></html>"))
	}))}
	dsh.announcedURL = dsh.server.URL + "/?token=test-launch-token"
	return dsh
}

func (d *testDSH) Discover(context.Context) (dshadapter.Runtime, error) {
	return dshadapter.Runtime{Path: "test-dsh", Version: dshadapter.SupportedVersion}, nil
}

func (d *testDSH) BuildLaunchPlan(_ dshadapter.Runtime, generationID, workspace, dshHome string, _ int) (supervisor.LaunchPlan, error) {
	u, err := url.Parse(d.server.URL)
	if err != nil {
		return supervisor.LaunchPlan{}, err
	}
	return supervisor.LaunchPlan{
		GenerationID:     generationID,
		Executable:       "test-dsh",
		WorkingDirectory: workspace,
		Env:              map[string]string{"DSH_HOME": dshHome},
		ExpectedOrigin:   d.server.URL,
		ExpectedHost:     u.Hostname(),
		ExpectedPort:     portFromURL(u),
	}, nil
}

func (d *testDSH) ParseReadyAnnouncement(text string) (dshadapter.ReadyAnnouncement, bool) {
	if text == "ready" {
		return dshadapter.ReadyAnnouncement{URL: d.announcedURL}, true
	}
	return dshadapter.ReadyAnnouncement{}, false
}

func (*testDSH) ValidateReady(announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan) error {
	parsed, err := url.Parse(announcement.URL)
	if err != nil || parsed.Hostname() != plan.ExpectedHost || parsed.Port() != portString(plan.ExpectedPort) {
		return lifecycle.Failure{Code: lifecycle.ErrorDSHInvalidReadiness, Summary: "invalid test readiness"}
	}
	return nil
}

func (d *testDSH) Probe(ctx context.Context, announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan) error {
	parsed, err := url.Parse(announcement.URL)
	if err != nil || parsed.Hostname() != plan.ExpectedHost || parsed.Port() != portString(plan.ExpectedPort) {
		return lifecycle.Failure{Code: lifecycle.ErrorDSHInvalidReadiness, Summary: "invalid test readiness"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, announcement.URL, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("test workspace is not ready")
	}
	return nil
}

func (*testDSH) RequestShutdown(ctx context.Context, worker supervisor.Worker) error {
	return worker.RequestStop(ctx)
}

type testSupervisor struct {
	worker *testWorker
	starts int
}

func (s *testSupervisor) Start(_ context.Context, _ supervisor.LaunchPlan, rawHandler supervisor.RawOutputHandler) (supervisor.Worker, error) {
	s.starts++
	s.worker = newTestWorker()
	if rawHandler != nil {
		rawHandler(supervisor.StreamStdout, "ready")
	}
	return s.worker, nil
}

type testWorker struct {
	events            chan supervisor.OutputEvent
	exited            chan struct{}
	once              sync.Once
	closeOnce         sync.Once
	waitEmptyFailures int
	closed            bool
}

func newTestWorker() *testWorker {
	return &testWorker{events: make(chan supervisor.OutputEvent, 4), exited: make(chan struct{})}
}

func (w *testWorker) Events() <-chan supervisor.OutputEvent { return w.events }
func (w *testWorker) Exited() <-chan struct{}               { return w.exited }
func (w *testWorker) ExitResult() supervisor.ExitResult     { return supervisor.ExitResult{Started: true} }
func (w *testWorker) RequestStop(context.Context) error {
	w.once.Do(func() { close(w.exited) })
	return nil
}
func (w *testWorker) ForceStop(context.Context) error {
	w.once.Do(func() { close(w.exited) })
	return nil
}
func (w *testWorker) WaitEmpty(context.Context) error {
	if w.waitEmptyFailures > 0 {
		w.waitEmptyFailures--
		return errors.New("test cleanup boundary is not empty yet")
	}
	return nil
}
func (*testWorker) Diagnostics() supervisor.Diagnostics { return supervisor.Diagnostics{} }
func (w *testWorker) Close() error {
	w.closeOnce.Do(func() {
		w.closed = true
		close(w.events)
	})
	return nil
}

func TestHostStartsReadyAndCleansUpToStopped(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{DSH: dsh, Supervisor: supervisorAdapter, Gateway: &testGateway{server: dsh.server}}, Config{
		WorkspaceRoot:       t.TempDir(),
		DSHHome:             t.TempDir(),
		ReadinessTimeout:    time.Second,
		ProbeTimeout:        time.Second,
		GracefulStopTimeout: time.Second,
		ForceStopTimeout:    time.Second,
		EmptyTimeout:        time.Second,
		ShutdownTimeout:     time.Second,
	})
	statuses := make(chan lifecycle.Status, 16)
	var handoffURL string
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.SetReadyHandler(func(value string) { handoffURL = value })
	if status := host.Start(); status.State != lifecycle.StateStarting {
		t.Fatalf("Start() status = %+v", status)
	}
	ready := waitForStatus(t, statuses, lifecycle.StateReady)
	if ready.WorkspaceURL == "" || ready.Error != nil {
		t.Fatalf("unexpected ready status: %+v", ready)
	}
	if ready.WorkspaceURL != dsh.server.URL+"/" || handoffURL != dsh.server.URL+"/__work/bootstrap?session=test-session" {
		t.Fatalf("gateway session was projected incorrectly: status=%q handoff=%q", ready.WorkspaceURL, handoffURL)
	}
	if status := host.Cancel(); status.State != lifecycle.StateStopping {
		t.Fatalf("Cancel() status = %+v", status)
	}
	stopped := waitForStatus(t, statuses, lifecycle.StateStopped)
	if stopped.WorkspaceURL != "" || stopped.Error != nil {
		t.Fatalf("unexpected stopped status: %+v", stopped)
	}
	if supervisorAdapter.worker == nil {
		t.Fatal("supervisor did not receive a worker start")
	}
}

func TestHostReportsUnsupportedPlatformWithoutStartingAWorker(t *testing.T) {
	host := NewHost(Dependencies{PlatformError: errors.New("darwin native adapter is intentionally not present")}, Config{
		WorkspaceRoot: t.TempDir(),
		DSHHome:       t.TempDir(),
	})
	statuses := make(chan lifecycle.Status, 8)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorPlatformUnsupported {
		t.Fatalf("unexpected unsupported-platform status: %+v", failed)
	}
}

func TestHostRestoresRecoverySurfaceAfterReadyWorkerExit(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{DSH: dsh, Supervisor: supervisorAdapter, Gateway: &testGateway{server: dsh.server}}, Config{
		WorkspaceRoot:    t.TempDir(),
		DSHHome:          t.TempDir(),
		ReadinessTimeout: time.Second,
		ProbeTimeout:     time.Second,
		EmptyTimeout:     time.Second,
	})
	statuses := make(chan lifecycle.Status, 16)
	recovered := make(chan struct{}, 1)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.SetRecoveryHandler(func() { recovered <- struct{}{} })
	host.Start()
	waitForStatus(t, statuses, lifecycle.StateReady)
	supervisorAdapter.worker.once.Do(func() { close(supervisorAdapter.worker.exited) })
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorDSHEarlyExit {
		t.Fatalf("unexpected post-ready failure: %+v", failed)
	}
	select {
	case <-recovered:
	case <-time.After(time.Second):
		t.Fatal("recovery surface was not requested after Worker exit")
	}
}

func TestHostDoesNotOverlapAWorkerWhenCleanupIsUnverified(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{DSH: dsh, Supervisor: supervisorAdapter, Gateway: &testGateway{server: dsh.server}}, Config{
		WorkspaceRoot:    t.TempDir(),
		DSHHome:          t.TempDir(),
		ReadinessTimeout: time.Second,
		ProbeTimeout:     time.Second,
		EmptyTimeout:     time.Second,
	})
	statuses := make(chan lifecycle.Status, 16)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	waitForStatus(t, statuses, lifecycle.StateReady)
	firstWorker := supervisorAdapter.worker
	firstWorker.waitEmptyFailures = 1
	if status := host.Cancel(); status.State != lifecycle.StateStopping {
		t.Fatalf("unexpected cancel status: %+v", status)
	}
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorProcessCleanupFailed {
		t.Fatalf("unexpected cleanup failure: %+v", failed)
	}
	if status := host.Start(); status.State != lifecycle.StateStarting {
		t.Fatalf("cleanup retry did not permit a new generation: %+v", status)
	}
	waitForStatus(t, statuses, lifecycle.StateReady)
	if supervisorAdapter.starts != 2 {
		t.Fatalf("unexpected Worker start count after cleanup retry: starts=%d", supervisorAdapter.starts)
	}
	if !firstWorker.closed {
		t.Fatal("new generation replaced the cleanup owner before it closed")
	}
}

func waitForStatus(t *testing.T, statuses <-chan lifecycle.Status, state lifecycle.State) lifecycle.Status {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case status := <-statuses:
			if status.State == state {
				return status
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", state)
		}
	}
}

func portFromURL(value *url.URL) int {
	var port int
	_, _ = fmt.Sscanf(value.Port(), "%d", &port)
	return port
}

func portString(port int) string {
	return strconv.Itoa(port)
}
