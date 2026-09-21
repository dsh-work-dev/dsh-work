package desktopapp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
)

type updateBackendFake struct {
	release  *wailsupdater.Release
	checkErr error

	started chan struct{}
	finish  chan struct{}
	onStart func()

	mu        sync.Mutex
	canceled  bool
	restarted bool
	staged    string
}

func (f *updateBackendFake) Check(context.Context) (*wailsupdater.Release, error) {
	return f.release, f.checkErr
}

func (f *updateBackendFake) DownloadAndInstall(ctx context.Context) error {
	select {
	case <-f.started:
	default:
		close(f.started)
	}
	if f.onStart != nil {
		f.onStart()
	}
	select {
	case <-f.finish:
		return nil
	case <-ctx.Done():
		f.mu.Lock()
		f.canceled = true
		f.mu.Unlock()
		return ctx.Err()
	}
}

func (f *updateBackendFake) Restart(context.Context) error {
	f.mu.Lock()
	f.restarted = true
	f.mu.Unlock()
	return nil
}

func (f *updateBackendFake) DownloadedPath() string { return f.staged }

type updatePrompterFake struct {
	choice chan bool
	asked  chan struct{}
	mu     sync.Mutex
	infos  []string
	errors []string
}

func (p *updatePrompterFake) Confirm(*wailsupdater.Release) bool {
	close(p.asked)
	return <-p.choice
}

func (p *updatePrompterFake) Info(_, message string) {
	p.mu.Lock()
	p.infos = append(p.infos, message)
	p.mu.Unlock()
}

func (p *updatePrompterFake) Error(_, message string) {
	p.mu.Lock()
	p.errors = append(p.errors, message)
	p.mu.Unlock()
}

func TestUpdateRunnerWaitsForStagedDownloadBeforeRestart(t *testing.T) {
	backend := &updateBackendFake{
		release: &wailsupdater.Release{Version: "2.0.0"},
		started: make(chan struct{}),
		finish:  make(chan struct{}),
	}
	prompt := &updatePrompterFake{choice: make(chan bool, 1), asked: make(chan struct{})}
	prompt.choice <- true
	runner := &updateRunner{backend: backend, prompt: prompt}
	backend.onStart = runner.notifyDownloadStarted

	runner.Trigger(context.Background(), true)
	select {
	case <-prompt.asked:
	case <-time.After(time.Second):
		t.Fatal("update prompt did not open while download was staging")
	}
	backend.mu.Lock()
	if backend.restarted {
		backend.mu.Unlock()
		t.Fatal("restart happened before the staged download completed")
	}
	backend.mu.Unlock()
	close(backend.finish)
	waitForUpdate(t, func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.restarted
	})
}

func TestUpdateRunnerCancelStopsDownloadAndDoesNotRestart(t *testing.T) {
	backend := &updateBackendFake{
		release: &wailsupdater.Release{Version: "2.0.0"},
		started: make(chan struct{}),
		finish:  make(chan struct{}),
	}
	prompt := &updatePrompterFake{choice: make(chan bool, 1), asked: make(chan struct{})}
	prompt.choice <- false
	runner := &updateRunner{backend: backend, prompt: prompt}
	backend.onStart = runner.notifyDownloadStarted

	runner.Trigger(context.Background(), true)
	select {
	case <-prompt.asked:
	case <-time.After(time.Second):
		t.Fatal("update prompt did not open")
	}
	waitForUpdate(t, func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.canceled
	})
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.restarted {
		t.Fatal("cancelled update restarted the application")
	}
}

func TestUpdateRunnerRemovesCompletedStagingOnCancel(t *testing.T) {
	artifactDir, err := os.MkdirTemp("", "wails-update-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(artifactDir) })
	artifact := filepath.Join(artifactDir, "dsh-work.exe")
	if err := os.WriteFile(artifact, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := &updateBackendFake{
		release: &wailsupdater.Release{Version: "2.0.0"},
		started: make(chan struct{}),
		finish:  make(chan struct{}),
		staged:  artifact,
	}
	prompt := &updatePrompterFake{choice: make(chan bool, 1), asked: make(chan struct{})}
	prompt.choice <- false
	runner := &updateRunner{backend: backend, prompt: prompt}
	backend.onStart = runner.notifyDownloadStarted

	runner.Trigger(context.Background(), true)
	select {
	case <-prompt.asked:
	case <-time.After(time.Second):
		t.Fatal("update prompt did not open")
	}
	close(backend.finish)
	waitForUpdate(t, func() bool { _, err := os.Stat(artifactDir); return errors.Is(err, os.ErrNotExist) })
}

func TestUpdateRunnerDoesNotRestartWhenUIStopFails(t *testing.T) {
	backend := &updateBackendFake{
		release: &wailsupdater.Release{Version: "2.0.0"},
		started: make(chan struct{}),
		finish:  make(chan struct{}),
	}
	prompt := &updatePrompterFake{choice: make(chan bool, 1), asked: make(chan struct{})}
	prompt.choice <- true
	runner := &updateRunner{
		backend: backend,
		prompt:  prompt,
		stopUI:  func() error { return errors.New("ui still running") },
	}
	backend.onStart = runner.notifyDownloadStarted

	runner.Trigger(context.Background(), true)
	select {
	case <-prompt.asked:
	case <-time.After(time.Second):
		t.Fatal("update prompt did not open")
	}
	close(backend.finish)
	waitForUpdate(t, func() bool {
		prompt.mu.Lock()
		defer prompt.mu.Unlock()
		return len(prompt.errors) > 0
	})
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.restarted {
		t.Fatal("update restarted while the UI was still running")
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
