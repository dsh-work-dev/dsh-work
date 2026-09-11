package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/local/dsh-work/internal/workspacecontext"
)

type testDSH struct {
	server       *httptest.Server
	announcedURL string
}

type testChannel struct {
	server *httptest.Server
}

func (g *testChannel) Prepare(context.Context, string, string, string) (workerchannel.Session, error) {
	return testChannelSession{url: "/?generation=test-session"}, nil
}

type testChannelSession struct {
	url string
}

func (testChannelSession) Generation() string   { return "test-session" }
func (s testChannelSession) URL() string        { return s.url }
func (testChannelSession) Patch() string        { return "host.patch.json" }
func (testChannelSession) Client() *http.Client { return http.DefaultClient }
func (testChannelSession) Activate(string)      {}
func (testChannelSession) Close() error         { return nil }

type hostRuntimeInstaller struct {
	path  string
	calls int
}

func (i *hostRuntimeInstaller) Install(ctx context.Context, version string) (dshmanager.RuntimeInfo, error) {
	return i.InstallWithProgress(ctx, version, nil)
}

func (i *hostRuntimeInstaller) InstallWithProgress(_ context.Context, version string, observer dshmanager.RuntimeInstallObserver) (dshmanager.RuntimeInfo, error) {
	i.calls++
	if observer != nil {
		observer(lifecycle.RuntimePreparation{
			State:         lifecycle.RuntimePreparationAcquiringDSH,
			Operation:     lifecycle.RuntimePreparationOperationInstallDSH,
			TargetVersion: version,
			Toolchain:     string(dshmanager.RuntimeToolchainSystemNPM),
			Source:        lifecycle.RuntimePreparationSourceOfficial,
			CanCancel:     true,
		})
	}
	if err := os.MkdirAll(filepath.Dir(i.path), 0o700); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	if err := os.WriteFile(i.path, []byte("test runtime"), 0o600); err != nil {
		return dshmanager.RuntimeInfo{}, err
	}
	if observer != nil {
		observer(lifecycle.RuntimePreparation{
			State:         lifecycle.RuntimePreparationInstalled,
			TargetVersion: version,
			Toolchain:     string(dshmanager.RuntimeToolchainSystemNPM),
			Source:        lifecycle.RuntimePreparationSourceOfficial,
			CanCancel:     false,
		})
	}
	return dshmanager.RuntimeInfo{
		ID: "dsh-test", Version: version, Path: i.path,
		Source: dshmanager.RuntimeSourceManaged, Installed: true, Removable: true,
	}, nil
}

func newTestDSH() *testDSH {
	dsh := &testDSH{server: httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>test workspace</body></html>"))
	}))}
	dsh.announcedURL = dsh.server.URL + "/?token=test-launch-token"
	return dsh
}

func newHostTestManager(t *testing.T, profile string) *dshmanager.Manager {
	t.Helper()
	root := t.TempDir()
	dataDirectory := filepath.Join(root, "dsh-work")
	if err := os.MkdirAll(filepath.Join(dataDirectory, "profiles", profile), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath: filepath.Join(root, "manager.json"),
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "dsh-work", Name: "dsh-work", Path: dataDirectory, Ownership: dshmanager.DataDirectoryOwnershipDSHWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-test", Version: dshadapter.SupportedVersion, Path: runtimePath,
		}},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-test", Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: profile},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func (d *testDSH) Discover(context.Context) (dshadapter.Runtime, error) {
	return dshadapter.Runtime{Path: "test-dsh", Version: dshadapter.SupportedVersion}, nil
}

func (d *testDSH) BuildLaunchPlan(launch dshadapter.LaunchContext) (supervisor.LaunchPlan, error) {
	workingDirectory := launch.BootstrapDirectory
	if launch.Workspace.State == workspacecontext.StateSelected {
		workingDirectory = launch.Workspace.Path
	}
	return supervisor.LaunchPlan{
		GenerationID:     launch.GenerationID,
		Executable:       "test-dsh",
		Args:             []string{"--profile", launch.Profile},
		WorkingDirectory: workingDirectory,
		Env:              map[string]string{"DSH_HOME": launch.DataDirectory},
		ExpectedOrigin:   d.server.URL,
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
	if err != nil || parsed.Scheme+"://"+parsed.Host != plan.ExpectedOrigin {
		return lifecycle.Failure{Code: lifecycle.ErrorDSHInvalidReadiness, Summary: "invalid test readiness"}
	}
	return nil
}

func (d *testDSH) Probe(ctx context.Context, announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan, client *http.Client) error {
	parsed, err := url.Parse(announcement.URL)
	if err != nil || parsed.Scheme+"://"+parsed.Host != plan.ExpectedOrigin {
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
	plan   supervisor.LaunchPlan
}

func (s *testSupervisor) Start(_ context.Context, plan supervisor.LaunchPlan, rawHandler supervisor.RawOutputHandler) (supervisor.Worker, error) {
	s.starts++
	s.plan = plan
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
	manager := newHostTestManager(t, "web")
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server}}, Config{
		BootstrapDirectory:  t.TempDir(),
		DSHDataDirectory:    t.TempDir(),
		ReadinessTimeout:    time.Second,
		ProbeTimeout:        time.Second,
		GracefulStopTimeout: time.Second,
		ForceStopTimeout:    time.Second,
		EmptyTimeout:        time.Second,
		ShutdownTimeout:     time.Second,
	})
	statuses := make(chan lifecycle.Status, 16)
	var workspaceURL string
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.SetReadyHandler(func(value string) { workspaceURL = value })
	if status := host.Start(); status.State != lifecycle.StateStarting {
		t.Fatalf("Start() status = %+v", status)
	}
	ready := waitForStatus(t, statuses, lifecycle.StateReady)
	if ready.WorkspaceURL == "" || ready.Error != nil {
		t.Fatalf("unexpected ready status: %+v", ready)
	}
	if ready.Workspace == nil || ready.Workspace.State != workspacecontext.StateSelectionRequired || ready.Workspace.GenerationID != ready.GenerationID {
		t.Fatalf("workspace context was not resolved for the generation: %+v", ready.Workspace)
	}
	if ready.WorkspaceURL != "/?generation=test-session" || workspaceURL != "/?generation=test-session" {
		t.Fatalf("channel session was projected incorrectly: status=%q workspace=%q", ready.WorkspaceURL, workspaceURL)
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

func TestHostDoesNotPullMissingRuntimeDuringStartup(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "managed", "dsh.cmd")
	installer := &hostRuntimeInstaller{path: runtimePath}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath:        filepath.Join(root, "manager.json"),
		RuntimeInstaller: installer,
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: dshmanager.DataDirectoryOwnershipDSHWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-test", Version: dshadapter.SupportedVersion, Path: runtimePath,
		}},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-test", Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(chan lifecycle.Status, 32)
	host := NewHost(Dependencies{
		DSH: dsh, Manager: manager,
		Supervisor: &testSupervisor{}, Channel: &testChannel{server: dsh.server},
	}, Config{
		BootstrapDirectory: t.TempDir(),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
		EmptyTimeout:       time.Second,
	})
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	failureStatus := waitForStatus(t, statuses, lifecycle.StateFailed)
	if installer.calls != 0 {
		t.Fatalf("startup invoked runtime installer %d time(s)", installer.calls)
	}
	if failureStatus.Error == nil || failureStatus.Error.Code != lifecycle.ErrorDSHRuntimeNotFound || !failureStatus.CanRetry {
		t.Fatalf("missing runtime failure = %+v", failureStatus)
	}
	if _, err := os.Stat(runtimePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("startup created a runtime file, stat error = %v", err)
	}
}

func TestHostUsesManagerRunContextAndClearsCurrentState(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	workspacePath := filepath.Join(root, "project")
	runtimePath := filepath.Join(root, "test-dsh")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "coding"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath: filepath.Join(root, "manager.json"),
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: dshmanager.DataDirectoryOwnershipDSHWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-test", Version: dshadapter.SupportedVersion, Path: runtimePath,
		}},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-test", Profile: dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "coding"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{
		DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server},
		WorkspaceResolver: workspacecontext.ResolverFunc(func(_ context.Context, generation string, _ workspacecontext.Request) (workspacecontext.Context, error) {
			return workspacecontext.NewSelected(generation, "workspace-1", workspacePath, "Project")
		}),
	}, Config{
		BootstrapDirectory: filepath.Join(root, "bootstrap"),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
		EmptyTimeout:       time.Second,
	})
	statuses := make(chan lifecycle.Status, 16)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	waitForStatus(t, statuses, lifecycle.StateReady)
	if got := strings.Join(supervisorAdapter.plan.Args, " "); got != "--profile coding" {
		t.Fatalf("launch args = %q, want explicit coding profile", got)
	}
	if supervisorAdapter.plan.Env["DSH_HOME"] != homePath {
		t.Fatalf("DSH_HOME = %q, want %q", supervisorAdapter.plan.Env["DSH_HOME"], homePath)
	}
	if supervisorAdapter.plan.WorkingDirectory != workspacePath {
		t.Fatalf("working directory = %q, want explicitly selected Workspace path", supervisorAdapter.plan.WorkingDirectory)
	}
	active, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if active.Current == nil || active.Current.Profile.Name != "coding" {
		t.Fatalf("current Run context = %#v, want coding", active.Current)
	}
	host.Cancel()
	waitForStatus(t, statuses, lifecycle.StateStopped)
	cleared, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cleared.Current != nil {
		t.Fatalf("current Run context after cleanup = %#v, want nil", cleared.Current)
	}
}

func TestHostDoesNotCreateMissingUserDSHHome(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	root := t.TempDir()
	missingHome := filepath.Join(root, "user-dsh-home")
	runtimePath := filepath.Join(root, "test-dsh")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath: filepath.Join(root, "manager.json"),
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "personal", Name: "Personal DSH", Path: missingHome, Ownership: dshmanager.DataDirectoryOwnershipUser,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-test", Version: dshadapter.SupportedVersion, Path: runtimePath,
		}},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-test", Profile: dshmanager.ProfileRef{DataDirectoryID: "personal", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(chan lifecycle.Status, 8)
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{
		DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server},
	}, Config{
		BootstrapDirectory: filepath.Join(root, "bootstrap"),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
		EmptyTimeout:       time.Second,
	})
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorProfileNotFound {
		t.Fatalf("unexpected missing-user-home status: %+v", failed)
	}
	if _, statErr := os.Stat(missingHome); !os.IsNotExist(statErr) {
		t.Fatalf("missing user DSH home was created or returned another error: %v", statErr)
	}
	if supervisorAdapter.starts != 0 {
		t.Fatalf("worker started for missing user DSH home: %d", supervisorAdapter.starts)
	}
}

func TestHostReportsUnsupportedPlatformWithoutStartingAWorker(t *testing.T) {
	host := NewHost(Dependencies{PlatformError: errors.New("darwin native adapter is intentionally not present")}, Config{
		BootstrapDirectory: t.TempDir(),
		DSHDataDirectory:   t.TempDir(),
	})
	statuses := make(chan lifecycle.Status, 8)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorPlatformUnsupported {
		t.Fatalf("unexpected unsupported-platform status: %+v", failed)
	}
}

func TestHostDoesNotStartWhenTheRunContextManagerCannotLoad(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{
		DSH:          dsh,
		ManagerError: errors.New("manager state is corrupt"),
		Supervisor:   supervisorAdapter,
		Channel:      &testChannel{server: dsh.server},
	}, Config{
		BootstrapDirectory: t.TempDir(),
		DSHDataDirectory:   t.TempDir(),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
	})
	statuses := make(chan lifecycle.Status, 8)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorManagerStateInvalid {
		t.Fatalf("unexpected manager-load failure: %+v", failed)
	}
	if supervisorAdapter.starts != 0 {
		t.Fatalf("worker started with an unavailable Run context manager: %d", supervisorAdapter.starts)
	}
}

func TestHostDoesNotUseAnImplicitRunContextWithoutManager(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{
		DSH:        dsh,
		Supervisor: supervisorAdapter,
		Channel:    &testChannel{server: dsh.server},
	}, Config{
		BootstrapDirectory: t.TempDir(),
		DSHDataDirectory:   t.TempDir(),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
	})
	statuses := make(chan lifecycle.Status, 8)
	host.SetPublish(func(status lifecycle.Status) { statuses <- status })
	host.Start()
	failed := waitForStatus(t, statuses, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorManagerStateInvalid {
		t.Fatalf("unexpected implicit-context failure: %+v", failed)
	}
	if supervisorAdapter.starts != 0 {
		t.Fatalf("worker started without a Run context manager: %d", supervisorAdapter.starts)
	}
}

func TestHostRestoresRecoverySurfaceAfterReadyWorkerExit(t *testing.T) {
	dsh := newTestDSH()
	defer dsh.server.Close()
	manager := newHostTestManager(t, "web")
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server}}, Config{
		BootstrapDirectory: t.TempDir(),
		DSHDataDirectory:   t.TempDir(),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
		EmptyTimeout:       time.Second,
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
	manager := newHostTestManager(t, "web")
	supervisorAdapter := &testSupervisor{}
	host := NewHost(Dependencies{DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server}}, Config{
		BootstrapDirectory: t.TempDir(),
		DSHDataDirectory:   t.TempDir(),
		ReadinessTimeout:   time.Second,
		ProbeTimeout:       time.Second,
		EmptyTimeout:       time.Second,
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

func TestHostBlocksPluginMutationWhileStopping(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)
	if status := fixture.host.Cancel(); status.State != lifecycle.StateStopping {
		t.Fatalf("Cancel() status = %+v, want Stopping", status)
	}
	_, err := fixture.manager.InstallPlugin(context.Background(), dshmanager.PluginInstallRequest{
		Target:  dshmanager.PluginTarget{Profile: fixture.target("alpha").Profile},
		Package: "@example/plugin",
	})
	assertFailureCode(t, err, lifecycle.ErrorManagerOperationBusy)
	fixture.waitForHostState(t, lifecycle.StateStopped)
}

func TestHostBlocksPluginMutationDuringUnexpectedWorkerCleanup(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)

	worker := fixture.supervisor.workers[0]
	waitEmptyStarted := make(chan struct{})
	waitEmptyGate := make(chan struct{})
	worker.waitEmptyStarted = waitEmptyStarted
	worker.waitEmptyGate = waitEmptyGate
	worker.exitUnexpected()
	pluginResult := make(chan error, 1)
	go func() {
		_, err := fixture.host.InstallPlugin(context.Background(), dshmanager.PluginInstallRequest{
			Target:  dshmanager.PluginTarget{Profile: fixture.target("alpha").Profile},
			Package: "@example/plugin",
		})
		pluginResult <- err
	}()
	var pluginErr error
	select {
	case <-waitEmptyStarted:
		close(waitEmptyGate)
	case <-time.After(time.Second):
		close(waitEmptyGate)
	}
	if pluginErr == nil {
		select {
		case pluginErr = <-pluginResult:
		case <-time.After(time.Second):
			t.Fatal("plugin mutation did not return")
		}
	} else {
		close(waitEmptyGate)
	}
	assertFailureCode(t, pluginErr, lifecycle.ErrorManagerOperationBusy)
	failed := fixture.waitForHostState(t, lifecycle.StateFailed)
	if failed.Error == nil || failed.Error.Code != lifecycle.ErrorDSHEarlyExit {
		t.Fatalf("unexpected unexpected-exit status: %+v", failed)
	}
}

func TestHostShutdownWaitsForContextSwitch(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)

	releaseReady := make(chan struct{})
	fixture.supervisor.readyGates["beta"] = releaseReady
	switchResult := make(chan error, 1)
	go func() {
		_, err := fixture.host.SwitchRunContext(context.Background(), fixture.target("beta"))
		switchResult <- err
	}()
	fixture.supervisor.waitForStart(t, "beta")

	shutdownResult := make(chan error, 1)
	go func() { shutdownResult <- fixture.host.ShutdownForApp() }()
	select {
	case err := <-shutdownResult:
		t.Fatalf("ShutdownForApp returned before the context switch completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}

	close(releaseReady)
	select {
	case err := <-switchResult:
		if err != nil {
			t.Fatalf("context switch failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("context switch did not complete")
	}
	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("ShutdownForApp failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ShutdownForApp did not stop the post-switch Worker")
	}
	fixture.supervisor.assertNoOverlap(t)
	if status := fixture.host.Status(); status.State != lifecycle.StateStopped {
		t.Fatalf("host status after shutdown = %+v, want Stopped", status)
	}
}

func TestHostSwitchesRunContextImmediatelyAfterVerifiedStop(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()

	fixture.startReady(t)
	target := fixture.target("beta")
	snapshot, err := fixture.host.SwitchRunContext(context.Background(), target)
	if err != nil {
		t.Fatalf("SwitchRunContext() error = %v", err)
	}
	assertRunContext(t, snapshot.Current, target, "current")
	assertRunContext(t, snapshot.Configured, target, "configured")
	assertRunContext(t, snapshot.KnownGood, target, "known-good")
	fixture.supervisor.assertNoOverlap(t)
	fixture.supervisor.assertEventOrder(t, "start:alpha", "stop:alpha", "start:beta")
	if fixture.supervisor.starts != 2 {
		t.Fatalf("Worker starts = %d, want 2", fixture.supervisor.starts)
	}
}

func TestHostCommitsCandidateOnlyAfterReady(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)

	releaseReady := make(chan struct{})
	fixture.supervisor.readyGates["beta"] = releaseReady
	target := fixture.target("beta")
	result := make(chan struct {
		snapshot dshmanager.Snapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := fixture.host.SwitchRunContext(context.Background(), target)
		result <- struct {
			snapshot dshmanager.Snapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	fixture.supervisor.waitForStart(t, "beta")
	deadline := time.Now().Add(time.Second)
	for {
		snapshot, err := fixture.manager.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Current == nil {
			if snapshot.Configured == nil || snapshot.Configured.Profile.Name != "alpha" {
				t.Fatalf("configured context changed before candidate readiness: %#v", snapshot.Configured)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("candidate became current before readiness: %#v", snapshot.Current)
		}
		time.Sleep(time.Millisecond)
	}
	close(releaseReady)
	select {
	case outcome := <-result:
		if outcome.err != nil {
			t.Fatalf("SwitchRunContext() error = %v", outcome.err)
		}
		assertRunContext(t, outcome.snapshot.Current, target, "current")
	case <-time.After(time.Second):
		t.Fatal("context switch did not finish after candidate readiness")
	}
}

func TestHostCancellingCandidateSwitchStopsWithoutAutomaticRestore(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)

	releaseReady := make(chan struct{})
	fixture.supervisor.readyGates["beta"] = releaseReady
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan struct {
		snapshot dshmanager.Snapshot
		err      error
	}, 1)
	go func() {
		snapshot, err := fixture.host.SwitchRunContext(ctx, fixture.target("beta"))
		result <- struct {
			snapshot dshmanager.Snapshot
			err      error
		}{snapshot: snapshot, err: err}
	}()
	fixture.supervisor.waitForStart(t, "beta")
	cancel()

	select {
	case outcome := <-result:
		assertFailureCode(t, outcome.err, lifecycle.ErrorCancelled)
		if outcome.snapshot.Current != nil {
			t.Fatal("cancelled switch restarted a Worker")
		}
		if outcome.snapshot.LastSwitchAttempt == nil || outcome.snapshot.LastSwitchAttempt.Rollback != dshmanager.RollbackDisabled {
			t.Fatal("cancellation did not suppress automatic restore")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled context switch did not finish")
	}
	fixture.supervisor.assertNoOverlap(t)
	fixture.supervisor.assertEventOrder(t, "start:alpha", "stop:alpha", "start:beta", "stop:beta")
}

func TestHostRollsBackCandidateFailureWithoutWorkerOverlap(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.dsh.setFailure("beta", true)
	fixture.startReady(t)

	target := fixture.target("beta")
	snapshot, err := fixture.host.SwitchRunContext(context.Background(), target)
	if err == nil {
		t.Fatal("SwitchRunContext() error = nil, want candidate failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorDSHReadinessTimeout)
	assertRunContext(t, snapshot.Current, fixture.target("alpha"), "rolled-back current")
	assertRunContext(t, snapshot.Configured, fixture.target("alpha"), "rolled-back configured")
	assertRunContext(t, snapshot.KnownGood, fixture.target("alpha"), "rolled-back known-good")
	if snapshot.LastSwitchAttempt == nil || snapshot.LastSwitchAttempt.Target != target || snapshot.LastSwitchAttempt.Rollback != dshmanager.RollbackRestored {
		t.Fatalf("retained failed attempt = %#v", snapshot.LastSwitchAttempt)
	}
	if status := fixture.host.Status(); status.State != lifecycle.StateReady {
		t.Fatalf("host status after successful rollback = %+v, want Ready", status)
	}
	fixture.supervisor.assertNoOverlap(t)
	fixture.supervisor.assertEventOrder(t, "start:alpha", "stop:alpha", "start:beta", "stop:beta", "start:alpha")
}

func TestHostRetainsFailedTargetWithoutRollbackWhenPreferenceIsDisabled(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.host.deps.AutomaticRuntimeRollback = func() bool { return false }
	fixture.dsh.setFailure("beta", true)
	fixture.startReady(t)

	target := fixture.target("beta")
	snapshot, err := fixture.host.SwitchRunContext(context.Background(), target)
	if err == nil {
		t.Fatal("SwitchRunContext() error = nil")
	}
	if snapshot.Current != nil {
		t.Fatalf("failed target became current: %#v", snapshot.Current)
	}
	assertRunContext(t, snapshot.Configured, target, "configured failed target")
	assertRunContext(t, snapshot.KnownGood, fixture.target("alpha"), "retained known-good")
	if snapshot.LastSwitchAttempt == nil || snapshot.LastSwitchAttempt.Rollback != dshmanager.RollbackDisabled || snapshot.LastSwitchAttempt.AutomaticRollback {
		t.Fatalf("failed attempt = %#v", snapshot.LastSwitchAttempt)
	}
	fixture.supervisor.assertEventOrder(t, "start:alpha", "stop:alpha", "start:beta", "stop:beta")

	restored, err := fixture.host.RestoreKnownGood(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertRunContext(t, restored.Current, fixture.target("alpha"), "restored current")
	if restored.LastSwitchAttempt != nil {
		t.Fatalf("successful restore retained failure: %#v", restored.LastSwitchAttempt)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if guardErr := fixture.manager.BeginRunContextSwitch(context.Background()); guardErr == nil {
			if endErr := fixture.manager.EndRunContextSwitch(context.Background()); endErr != nil {
				t.Fatal(endErr)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("manager switch guard was not released after restore")
		}
		time.Sleep(time.Millisecond)
	}

	snapshot, err = fixture.host.SwitchRunContext(context.Background(), target)
	if err == nil || snapshot.LastSwitchAttempt == nil {
		t.Fatalf("second failed attempt = snapshot %#v error=%v", snapshot, err)
	}
	fixture.dsh.setFailure("beta", false)
	retried, err := fixture.host.RetryLastSwitch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertRunContext(t, retried.Current, target, "retried current")
	if retried.LastSwitchAttempt != nil {
		t.Fatalf("successful retry retained failure: %#v", retried.LastSwitchAttempt)
	}
}

func TestHostKeepsCurrentWhenSwitchTargetFailsResolution(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)
	target := fixture.target("beta")
	target.RuntimeID = "missing"

	snapshot, err := fixture.host.SwitchRunContext(context.Background(), target)
	if err == nil {
		t.Fatal("SwitchRunContext() error = nil")
	}
	assertRunContext(t, snapshot.Current, fixture.target("alpha"), "current")
	if snapshot.LastSwitchAttempt == nil || snapshot.LastSwitchAttempt.Stage != dshmanager.SwitchAttemptResolving || snapshot.LastSwitchAttempt.Target != target {
		t.Fatalf("resolution attempt = %#v", snapshot.LastSwitchAttempt)
	}
	fixture.supervisor.assertEventOrder(t, "start:alpha")
}

func TestHostRollsBackCandidateStartFailureWithoutWorkerOverlap(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.startReady(t)
	fixture.supervisor.startErrors["beta"] = errors.New("candidate process boundary could not be created")

	snapshot, err := fixture.host.SwitchRunContext(context.Background(), fixture.target("beta"))
	if err == nil {
		t.Fatal("SwitchRunContext() error = nil, want candidate start failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorProcessStartFailed)
	assertRunContext(t, snapshot.Current, fixture.target("alpha"), "start-failure current")
	assertRunContext(t, snapshot.Configured, fixture.target("alpha"), "start-failure configured")
	assertRunContext(t, snapshot.KnownGood, fixture.target("alpha"), "start-failure known-good")
	if status := fixture.host.Status(); status.State != lifecycle.StateReady {
		t.Fatalf("host status after candidate start rollback = %+v, want Ready", status)
	}
	fixture.supervisor.assertNoOverlap(t)
	fixture.supervisor.assertEventOrder(t, "start:alpha", "stop:alpha", "start:alpha")
}

func TestHostEntersRetryableFailedWhenRollbackFails(t *testing.T) {
	fixture := newRunContextSwitchFixture(t)
	defer fixture.close()
	fixture.supervisor.onStart = func(profile string) {
		if profile == "beta" {
			fixture.dsh.setFailure("alpha", true)
		}
	}
	fixture.dsh.setFailure("beta", true)
	fixture.startReady(t)

	snapshot, err := fixture.host.SwitchRunContext(context.Background(), fixture.target("beta"))
	if err == nil {
		t.Fatal("SwitchRunContext() error = nil, want rollback failure")
	}
	assertFailureCode(t, err, lifecycle.ErrorDSHReadinessTimeout)
	if snapshot.Current != nil {
		t.Fatalf("failed rollback published current context: %#v", snapshot.Current)
	}
	assertRunContext(t, snapshot.Configured, fixture.target("alpha"), "failed-switch configured")
	assertRunContext(t, snapshot.KnownGood, fixture.target("alpha"), "failed-switch known-good")
	status := fixture.host.Status()
	if status.State != lifecycle.StateFailed || status.Error == nil || !status.CanRetry {
		t.Fatalf("rollback failure status = %+v, want retryable Failed", status)
	}
	fixture.supervisor.assertNoOverlap(t)

	fixture.dsh.setFailure("alpha", false)
	fixture.dsh.setFailure("beta", false)
	if _, err := fixture.host.RestoreKnownGood(context.Background()); err != nil {
		t.Fatalf("explicit version recovery retry failed: %v", err)
	}
	if ready := fixture.host.Status(); ready.State != lifecycle.StateReady || ready.Error != nil {
		t.Fatalf("recovery retry did not reach Ready: %+v", ready)
	}
	recovered, err := fixture.manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertRunContext(t, recovered.Current, fixture.target("alpha"), "recovered current")
}

type runContextSwitchFixture struct {
	dsh        *switchTestDSH
	manager    *dshmanager.Manager
	supervisor *switchTestSupervisor
	host       *Host
	statuses   chan lifecycle.Status
}

// Lifecycle fixtures supply version inputs independently of real package installation.
// Real frozen-lock and package repair behavior is covered by version recovery tests.
type switchVersionCommands struct{ dshadapter.PluginCommands }

func (switchVersionCommands) CaptureVersions(context.Context, string) (dshmanager.ProfileVersionInput, error) {
	return dshmanager.ProfileVersionInput{PackageManager: "pnpm"}, nil
}
func (switchVersionCommands) CheckVersionProfile(context.Context, string) error { return nil }
func (switchVersionCommands) ApplyVersions(context.Context, string, dshmanager.ProfileVersionInput) error {
	return nil
}
func (switchVersionCommands) ResetVersions(context.Context, string) ([]string, error) {
	return nil, nil
}

func newRunContextSwitchFixture(t *testing.T, configure ...func(*dshmanager.Config)) *runContextSwitchFixture {
	t.Helper()
	root := t.TempDir()
	dsh := newSwitchTestDSH()
	homeAlpha := filepath.Join(root, "dsh-alpha")
	homeBeta := filepath.Join(root, "dsh-beta")
	for _, home := range []string{homeAlpha, homeBeta} {
		for _, profile := range []string{"alpha", "beta"} {
			if err := os.MkdirAll(filepath.Join(home, "profiles", profile), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	runtimeAlpha := filepath.Join(root, "dsh-alpha.cmd")
	runtimeBeta := filepath.Join(root, "dsh-beta.cmd")
	for _, path := range []string{runtimeAlpha, runtimeBeta} {
		if err := os.WriteFile(path, []byte("test runtime"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	managerConfig := dshmanager.Config{
		StatePath: filepath.Join(root, "manager.json"),
		DataDirectories: []dshmanager.DataDirectoryInfo{
			{ID: "alpha-home", Name: "Alpha DSH", Path: homeAlpha, Ownership: dshmanager.DataDirectoryOwnershipDSHWork},
			{ID: "beta-home", Name: "Beta DSH", Path: homeBeta, Ownership: dshmanager.DataDirectoryOwnershipDSHWork},
		},
		Runtimes: []dshmanager.RuntimeInfo{
			{ID: "dsh-alpha", Version: dshadapter.SupportedVersion, Path: runtimeAlpha},
			{ID: "dsh-beta", Version: dshadapter.SupportedVersion, Path: runtimeBeta},
		},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-alpha",
			Profile:   dshmanager.ProfileRef{DataDirectoryID: "alpha-home", Name: "alpha"},
		},
	}
	managerConfig.PluginCommands = switchVersionCommands{}
	managerConfig.CommandRunner = versionRecoveryFixtureRunner{}
	managerConfig.RuntimeInstaller = &versionRecoveryFixtureInstaller{runtime: managerConfig.Runtimes[0]}
	for _, apply := range configure {
		apply(&managerConfig)
	}
	manager, err := dshmanager.New(managerConfig)
	if err != nil {
		t.Fatal(err)
	}
	supervisorAdapter := &switchTestSupervisor{
		readyGates:   make(map[string]<-chan struct{}),
		startErrors:  make(map[string]error),
		startSignals: make(map[string]chan struct{}),
	}
	host := NewHost(Dependencies{
		DSH: dsh, Manager: manager, Supervisor: supervisorAdapter, Channel: &testChannel{server: dsh.server},
	}, Config{
		BootstrapDirectory:  filepath.Join(root, "bootstrap"),
		ReadinessTimeout:    45 * time.Millisecond,
		ProbeTimeout:        5 * time.Millisecond,
		GracefulStopTimeout: 50 * time.Millisecond,
		ForceStopTimeout:    50 * time.Millisecond,
		EmptyTimeout:        50 * time.Millisecond,
		ShutdownTimeout:     time.Second,
	})
	fixture := &runContextSwitchFixture{
		dsh: dsh, manager: manager, supervisor: supervisorAdapter, host: host,
		statuses: make(chan lifecycle.Status, 64),
	}
	host.SetPublish(func(status lifecycle.Status) { fixture.statuses <- status })
	return fixture
}

func (f *runContextSwitchFixture) startReady(t *testing.T) {
	t.Helper()
	if status := f.host.Start(); status.State != lifecycle.StateStarting {
		t.Fatalf("initial Start() status = %+v", status)
	}
	f.waitForHostState(t, lifecycle.StateReady)
}

func (f *runContextSwitchFixture) waitForHostState(t *testing.T, state lifecycle.State) lifecycle.Status {
	t.Helper()
	return waitForStatus(t, f.statuses, state)
}

func (f *runContextSwitchFixture) target(profile string) dshmanager.RunContext {
	if profile == "beta" {
		return dshmanager.RunContext{RuntimeID: "dsh-beta", Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "beta-home", Name: "beta"}}
	}
	return dshmanager.RunContext{RuntimeID: "dsh-alpha", Node: dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, Profile: dshmanager.ProfileRef{DataDirectoryID: "alpha-home", Name: "alpha"}}
}

func (f *runContextSwitchFixture) close() {
	if f.host != nil {
		_ = f.host.ShutdownForApp()
	}
	if f.dsh != nil && f.dsh.server != nil {
		f.dsh.server.Close()
	}
}

func TestRuntimeAdapterUsesResolvedNodeEnvironmentInsteadOfRuntimeToolchainPath(t *testing.T) {
	runtime := dshmanager.RuntimeInfo{Path: "dsh.cmd", Version: "1.2.3", ToolchainPath: "legacy-node"}
	node := dshmanager.ResolvedNode{ChildEnvironment: map[string]string{"PATH": "selected-node", "NODE_OPTIONS": "--fixture"}}
	adapted := runtimeAdapterValue(runtime, node)
	if adapted.Env["PATH"] != "selected-node" || adapted.Env["NODE_OPTIONS"] != "--fixture" {
		t.Fatalf("adapter environment = %#v", adapted.Env)
	}
	if adapted.Env["PATH"] == runtime.ToolchainPath {
		t.Fatal("runtime ToolchainPath still controls launch")
	}
}

func assertRunContext(t *testing.T, got *dshmanager.RunContext, want dshmanager.RunContext, label string) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("%s Run context = %#v, want %#v", label, got, want)
	}
}

func assertFailureCode(t *testing.T, err error, want lifecycle.ErrorCode) {
	t.Helper()
	var failure lifecycle.Failure
	if errors.As(err, &failure) {
		if failure.Code != want {
			t.Fatalf("failure code = %s, want %s", failure.Code, want)
		}
		return
	}
	var failurePointer *lifecycle.Failure
	if !errors.As(err, &failurePointer) || failurePointer == nil {
		t.Fatalf("error %v is not a lifecycle failure", err)
	}
	if failurePointer.Code != want {
		t.Fatalf("failure code = %s, want %s", failurePointer.Code, want)
	}
}

type switchTestDSH struct {
	server       *httptest.Server
	announcedURL string
	mu           sync.Mutex
	failProfiles map[string]bool
}

func newSwitchTestDSH() *switchTestDSH {
	dsh := &switchTestDSH{failProfiles: make(map[string]bool)}
	dsh.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>switch test workspace</body></html>"))
	}))
	dsh.announcedURL = dsh.server.URL + "/?token=switch-test-token"
	return dsh
}

func (d *switchTestDSH) BuildLaunchPlan(launch dshadapter.LaunchContext) (supervisor.LaunchPlan, error) {
	return supervisor.LaunchPlan{
		GenerationID:     launch.GenerationID,
		Executable:       launch.Runtime.Path,
		Args:             []string{"--profile", launch.Profile},
		WorkingDirectory: launch.BootstrapDirectory,
		Env:              map[string]string{"DSH_HOME": launch.DataDirectory},
		ExpectedOrigin:   d.server.URL,
	}, nil
}

func (d *switchTestDSH) Discover(context.Context) (dshadapter.Runtime, error) {
	return dshadapter.Runtime{Path: "switch-test-dsh", Version: dshadapter.SupportedVersion}, nil
}

func (d *switchTestDSH) ParseReadyAnnouncement(text string) (dshadapter.ReadyAnnouncement, bool) {
	if text == "ready" {
		return dshadapter.ReadyAnnouncement{URL: d.announcedURL}, true
	}
	return dshadapter.ReadyAnnouncement{}, false
}

func (d *switchTestDSH) ValidateReady(announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan) error {
	parsed, err := url.Parse(announcement.URL)
	if err != nil || parsed.Scheme+"://"+parsed.Host != plan.ExpectedOrigin {
		return lifecycle.Failure{Code: lifecycle.ErrorDSHInvalidReadiness, Summary: "invalid switch test readiness"}
	}
	return nil
}

func (d *switchTestDSH) Probe(ctx context.Context, announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan, client *http.Client) error {
	if len(plan.Args) >= 2 {
		d.mu.Lock()
		failed := d.failProfiles[plan.Args[1]]
		d.mu.Unlock()
		if failed {
			return errors.New("switch test profile is not ready")
		}
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
		return errors.New("switch test workspace is not ready")
	}
	return nil
}

func (d *switchTestDSH) RequestShutdown(ctx context.Context, worker supervisor.Worker) error {
	return worker.RequestStop(ctx)
}

func (d *switchTestDSH) setFailure(profile string, failed bool) {
	d.mu.Lock()
	d.failProfiles[profile] = failed
	d.mu.Unlock()
}

type switchTestSupervisor struct {
	mu           sync.Mutex
	active       int
	maxActive    int
	starts       int
	plans        []supervisor.LaunchPlan
	events       []string
	readyGates   map[string]<-chan struct{}
	startErrors  map[string]error
	startSignals map[string]chan struct{}
	workers      []*switchTestWorker
	onStart      func(string)
}

func (s *switchTestSupervisor) Start(ctx context.Context, plan supervisor.LaunchPlan, rawHandler supervisor.RawOutputHandler) (supervisor.Worker, error) {
	profile := switchTestProfile(plan)
	s.mu.Lock()
	if err := s.startErrors[profile]; err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.starts++
	s.plans = append(s.plans, plan)
	s.events = append(s.events, "start:"+profile)
	if signal := s.startSignals[profile]; signal != nil {
		close(signal)
		s.startSignals[profile] = nil
	}
	onStart := s.onStart
	gate := s.readyGates[profile]
	s.mu.Unlock()
	if onStart != nil {
		onStart(profile)
	}
	worker := &switchTestWorker{events: make(chan supervisor.OutputEvent, 4), exited: make(chan struct{}), onStop: func() {
		s.mu.Lock()
		s.active--
		s.events = append(s.events, "stop:"+profile)
		s.mu.Unlock()
	}}
	s.mu.Lock()
	s.workers = append(s.workers, worker)
	s.mu.Unlock()
	if rawHandler != nil {
		if gate == nil {
			rawHandler(supervisor.StreamStdout, "ready")
		} else {
			go func() {
				select {
				case <-gate:
					rawHandler(supervisor.StreamStdout, "ready")
				case <-ctx.Done():
				}
			}()
		}
	}
	return worker, nil
}

func switchTestProfile(plan supervisor.LaunchPlan) string {
	if len(plan.Args) >= 2 {
		return plan.Args[1]
	}
	return "unknown"
}

func (s *switchTestSupervisor) waitForStart(t *testing.T, profile string) {
	t.Helper()
	signal := make(chan struct{})
	s.mu.Lock()
	for _, plan := range s.plans {
		if switchTestProfile(plan) == profile {
			s.mu.Unlock()
			return
		}
	}
	s.startSignals[profile] = signal
	s.mu.Unlock()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s Worker start", profile)
	}
}

func (s *switchTestSupervisor) assertNoOverlap(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	maxActive := s.maxActive
	s.mu.Unlock()
	if maxActive > 1 {
		t.Fatalf("overlapping Worker generations observed: max active = %d", maxActive)
	}
}

func (s *switchTestSupervisor) assertEventOrder(t *testing.T, expected ...string) {
	t.Helper()
	s.mu.Lock()
	events := append([]string(nil), s.events...)
	s.mu.Unlock()
	position := 0
	for _, want := range expected {
		for position < len(events) && events[position] != want {
			position++
		}
		if position == len(events) {
			t.Fatalf("Worker events = %#v, missing ordered event %q", events, want)
		}
		position++
	}
}

type switchTestWorker struct {
	events           chan supervisor.OutputEvent
	exited           chan struct{}
	exitOnce         sync.Once
	stopOnce         sync.Once
	closeOnce        sync.Once
	onStop           func()
	waitEmptyStarted chan struct{}
	waitEmptyOnce    sync.Once
	waitEmptyGate    <-chan struct{}
}

func (w *switchTestWorker) Events() <-chan supervisor.OutputEvent { return w.events }
func (w *switchTestWorker) Exited() <-chan struct{}               { return w.exited }
func (*switchTestWorker) ExitResult() supervisor.ExitResult {
	return supervisor.ExitResult{Started: true}
}
func (w *switchTestWorker) exitUnexpected() {
	w.exitOnce.Do(func() { close(w.exited) })
}
func (w *switchTestWorker) RequestStop(context.Context) error {
	w.stopOnce.Do(func() {
		w.exitOnce.Do(func() { close(w.exited) })
		if w.onStop != nil {
			w.onStop()
		}
	})
	return nil
}
func (w *switchTestWorker) ForceStop(ctx context.Context) error { return w.RequestStop(ctx) }
func (w *switchTestWorker) WaitEmpty(ctx context.Context) error {
	if w.waitEmptyStarted != nil {
		w.waitEmptyOnce.Do(func() { close(w.waitEmptyStarted) })
	}
	if w.waitEmptyGate == nil {
		return nil
	}
	select {
	case <-w.waitEmptyGate:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (*switchTestWorker) Diagnostics() supervisor.Diagnostics { return supervisor.Diagnostics{} }
func (w *switchTestWorker) Close() error {
	w.closeOnce.Do(func() {
		if w.events == nil {
			w.events = make(chan supervisor.OutputEvent)
		}
		close(w.events)
	})
	return nil
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

func TestReadinessFailureKeepsSafeProbeDiagnostic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "private upstream detail", http.StatusUnauthorized)
	}))
	defer server.Close()
	adapter := dshadapter.New(nil, dshadapter.SupportedVersion)
	err := adapter.Probe(context.Background(), dshadapter.ReadyAnnouncement{URL: server.URL + "/?token=private-launch-token"}, supervisor.LaunchPlan{ExpectedOrigin: server.URL}, server.Client())
	if err == nil {
		t.Fatal("probe unexpectedly succeeded")
	}
	host := NewHost(Dependencies{}, Config{})
	failure := host.failureFor(err, lifecycle.ErrorDSHReadinessTimeout, "DSH not ready", true)
	if !strings.Contains(failure.Detail, "HTTP 401") || strings.Contains(failure.Detail, "private") || strings.Contains(failure.Detail, server.URL) {
		t.Fatalf("unsafe or missing probe diagnostic: %s", failure.Detail)
	}
}
