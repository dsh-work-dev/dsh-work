package desktopapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
)

type updateBackendFake struct {
	release  *wailsupdater.Release
	checkErr error

	started chan struct{}
	finish  chan struct{}

	mu            sync.Mutex
	downloadCalls int
	restarted     bool
	checkCalls    int
}

func (f *updateBackendFake) Check(context.Context) (*wailsupdater.Release, error) {
	f.mu.Lock()
	f.checkCalls++
	f.mu.Unlock()
	return f.release, f.checkErr
}

func (f *updateBackendFake) DownloadAndInstall(ctx context.Context) error {
	f.mu.Lock()
	f.downloadCalls++
	f.mu.Unlock()
	if f.started != nil {
		select {
		case <-f.started:
		default:
			close(f.started)
		}
	}
	if f.finish == nil {
		return nil
	}
	select {
	case <-f.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *updateBackendFake) Restart(context.Context) error {
	f.mu.Lock()
	f.restarted = true
	f.mu.Unlock()
	return nil
}

func TestUpdateRunnerManualCheckStopsAtAvailable(t *testing.T) {
	backend := &updateBackendFake{release: &wailsupdater.Release{Version: "2.0.0", Artifact: wailsupdater.Artifact{Size: 100}}}
	runner := &updateRunner{backend: backend, current: "1.0.0", state: daemon.UpdateSnapshot{Phase: daemon.UpdateIdle, CurrentVersion: "1.0.0"}}
	if err := runner.Action(context.Background(), daemon.UpdateActionCheck); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateAvailable })
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.downloadCalls != 0 {
		t.Fatalf("manual check started %d downloads", backend.downloadCalls)
	}
	if got := runner.Snapshot().TargetVersion; got != "2.0.0" {
		t.Fatalf("target version = %q, want 2.0.0", got)
	}
}

func TestUpdateRunnerAutomaticCheckDownloadsWithoutRestart(t *testing.T) {
	backend := &updateBackendFake{
		release: &wailsupdater.Release{Version: "2.0.0", Artifact: wailsupdater.Artifact{Size: 100}},
		started: make(chan struct{}),
		finish:  make(chan struct{}),
	}
	runner := &updateRunner{backend: backend, current: "1.0.0", state: daemon.UpdateSnapshot{Phase: daemon.UpdateIdle, CurrentVersion: "1.0.0"}}
	runner.Trigger(context.Background(), true)
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("automatic update did not start downloading")
	}
	backend.mu.Lock()
	if backend.restarted {
		backend.mu.Unlock()
		t.Fatal("automatic download restarted the application")
	}
	backend.mu.Unlock()
	close(backend.finish)
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateReady })
	if got := runner.Snapshot().InstallMode; got != daemon.UpdateInstallStagedRestart {
		t.Fatalf("install mode = %q, want staged-restart", got)
	}
}

func TestUpdateRunnerManualDownloadThenExplicitInstall(t *testing.T) {
	backend := &updateBackendFake{release: &wailsupdater.Release{Version: "2.0.0"}}
	var stopUICalled bool
	runner := &updateRunner{
		backend: backend,
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateAvailable, CurrentVersion: "1.0.0", TargetVersion: "2.0.0"},
		release: backend.release,
		stopUI:  func() error { stopUICalled = true; return nil },
	}
	if err := runner.Action(context.Background(), daemon.UpdateActionDownload); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateReady })
	if err := runner.Action(context.Background(), daemon.UpdateActionInstall); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.restarted
	})
	if !stopUICalled {
		t.Fatal("explicit install did not stop the UI")
	}
}

func TestUpdateRunnerDoesNotEnterInstallingWhenUIStopFails(t *testing.T) {
	backend := &updateBackendFake{}
	runner := &updateRunner{
		backend: backend,
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateReady, CurrentVersion: "1.0.0", TargetVersion: "2.0.0", InstallMode: daemon.UpdateInstallStagedRestart},
		stopUI:  func() error { return errors.New("ui still running") },
	}
	if err := runner.Action(context.Background(), daemon.UpdateActionInstall); err != nil {
		t.Fatal(err)
	}
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateError })
	backend.mu.Lock()
	restarted := backend.restarted
	backend.mu.Unlock()
	if restarted {
		t.Fatal("update restarted after the UI stop failed")
	}
}

func TestUpdateRunnerCheckFailureIsVisibleWithoutRawError(t *testing.T) {
	runner := &updateRunner{
		backend: &updateBackendFake{checkErr: errors.New("private feed details")},
		current: "1.0.0",
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateIdle, CurrentVersion: "1.0.0"},
	}
	runner.Trigger(context.Background(), false)
	waitForUpdate(t, func() bool { return runner.Snapshot().Phase == daemon.UpdateError })
	state := runner.Snapshot()
	if state.ErrorCode != "check" {
		t.Fatalf("error code = %q, want check", state.ErrorCode)
	}
	if state.ErrorCode == "private feed details" {
		t.Fatal("raw provider error leaked into update state")
	}
}

func waitForUpdate(t *testing.T, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("update flow did not reach the expected state")
}
