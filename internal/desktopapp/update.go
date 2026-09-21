package desktopapp

import (
	"context"
	"encoding/base64"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

const (
	updateFeedEnv                 = "DSH_WORK_UPDATE_FEED_URL"
	updateChannelEnv              = "DSH_WORK_UPDATE_CHANNEL"
	updatePublicKeyEnv            = "DSH_WORK_UPDATE_PUBLIC_KEY"
	updatePublicKeyFile           = "DSH_WORK_UPDATE_PUBLIC_KEY_FILE"
	updateProgressPublishInterval = 250 * time.Millisecond
)

var (
	errUpdateUnavailable = errors.New("update controls unavailable")
	errUpdateNoRelease   = errors.New("no update is available")
)

// updateBackend is the small part of the official Wails updater used by the
// daemon-owned flow. UI state is maintained by updateRunner so it can be
// projected over IPC without exposing provider or staging details.
type updateBackend interface {
	Check(context.Context) (*wailsupdater.Release, error)
	DownloadAndInstall(context.Context) error
	Restart(context.Context) error
}

type wailsUpdateBackend struct{ updater *wailsupdater.Updater }

func (b wailsUpdateBackend) Check(ctx context.Context) (*wailsupdater.Release, error) {
	return b.updater.Check(ctx)
}
func (b wailsUpdateBackend) DownloadAndInstall(ctx context.Context) error {
	return b.updater.DownloadAndInstall(ctx)
}
func (b wailsUpdateBackend) Restart(ctx context.Context) error { return b.updater.Restart(ctx) }

// updateRunner is the daemon's single-flight update coordinator. Automatic
// checks download silently; only an explicit install action stops the UI and
// asks the Wails helper to restart into the staged executable.
type updateRunner struct {
	backend updateBackend
	current string
	stopUI  func() error
	publish func(daemon.UpdateSnapshot)

	busy        atomic.Bool
	mu          sync.RWMutex
	state       daemon.UpdateSnapshot
	release     *wailsupdater.Release
	lastPublish time.Time

	activeCancel context.CancelFunc
	startCancel  context.CancelFunc
}

func (r *updateRunner) Snapshot() daemon.UpdateSnapshot {
	if r == nil {
		return daemon.UpdateSnapshot{Phase: daemon.UpdateUnconfigured}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.state
}

func (r *updateRunner) setState(next daemon.UpdateSnapshot) {
	if r == nil {
		return
	}
	if next.CurrentVersion == "" {
		next.CurrentVersion = r.current
	}
	r.mu.Lock()
	changed := r.state != next
	r.state = next
	publish := r.publish
	if changed && publish != nil {
		now := time.Now()
		if next.Phase == daemon.UpdateDownloading && !r.lastPublish.IsZero() && now.Sub(r.lastPublish) < updateProgressPublishInterval {
			publish = nil
		} else {
			r.lastPublish = now
		}
	}
	r.mu.Unlock()
	if changed && publish != nil {
		publish(next)
	}
}

func (r *updateRunner) updateState(mutator func(*daemon.UpdateSnapshot)) {
	if r == nil {
		return
	}
	r.mu.Lock()
	next := r.state
	mutator(&next)
	if next.CurrentVersion == "" {
		next.CurrentVersion = r.current
	}
	changed := r.state != next
	r.state = next
	publish := r.publish
	if changed && publish != nil {
		now := time.Now()
		if next.Phase == daemon.UpdateDownloading && !r.lastPublish.IsZero() && now.Sub(r.lastPublish) < updateProgressPublishInterval {
			publish = nil
		} else {
			r.lastPublish = now
		}
	}
	r.mu.Unlock()
	if changed && publish != nil {
		publish(next)
	}
}

func (r *updateRunner) setError(code string) {
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateError
		state.ErrorCode = code
		state.InstallMode = daemon.UpdateInstallNone
	})
}

// Action starts the requested operation and returns quickly. Progress and
// terminal state are projected asynchronously through Snapshot and events.
func (r *updateRunner) Action(ctx context.Context, action daemon.UpdateAction) error {
	if r == nil || r.backend == nil {
		if r != nil {
			r.setState(daemon.UpdateSnapshot{Phase: daemon.UpdateUnconfigured, CurrentVersion: r.current})
		}
		if action == daemon.UpdateActionCheck {
			return nil
		}
		return errUpdateUnavailable
	}
	// HTTP action handlers return before the asynchronous operation completes;
	// detach the operation from the request cancellation while Stop still owns
	// the coordinator's explicit shutdown cancellation.
	operationCtx := context.WithoutCancel(ctx)
	switch action {
	case daemon.UpdateActionCheck:
		if phase := r.Snapshot().Phase; phase == daemon.UpdateReady || phase == daemon.UpdateInstalling {
			return nil
		}
		r.Trigger(operationCtx, false)
		return nil
	case daemon.UpdateActionDownload:
		phase := r.Snapshot().Phase
		if phase == daemon.UpdateReady || phase == daemon.UpdateDownloading || phase == daemon.UpdateVerifying {
			return nil
		}
		if !r.busy.CompareAndSwap(false, true) {
			return nil
		}
		if r.Snapshot().Phase != daemon.UpdateAvailable {
			r.busy.Store(false)
			return errUpdateNoRelease
		}
		go func() {
			defer r.busy.Store(false)
			r.runDownload(operationCtx)
		}()
		return nil
	case daemon.UpdateActionInstall:
		phase := r.Snapshot().Phase
		if phase == daemon.UpdateInstalling {
			return nil
		}
		if phase != daemon.UpdateReady {
			return errUpdateNoRelease
		}
		if !r.busy.CompareAndSwap(false, true) {
			return nil
		}
		go func() {
			defer r.busy.Store(false)
			r.runInstall(operationCtx)
		}()
		return nil
	default:
		return errors.New("invalid update action")
	}
}

// Trigger starts a check. Automatic checks continue into download when a
// release is found; manual checks stop at available so the About button can
// explicitly start the download.
func (r *updateRunner) Trigger(ctx context.Context, automatic bool) {
	if r == nil {
		return
	}
	if r.backend == nil {
		r.setState(daemon.UpdateSnapshot{Phase: daemon.UpdateUnconfigured, CurrentVersion: r.current})
		return
	}
	if phase := r.Snapshot().Phase; phase == daemon.UpdateReady || phase == daemon.UpdateInstalling {
		return
	}
	if !r.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.busy.Store(false)
		r.runCheck(ctx, automatic)
	}()
}

func (r *updateRunner) runCheck(ctx context.Context, automatic bool) {
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateChecking
		state.TargetVersion = ""
		state.ErrorCode = ""
		state.ReceivedBytes = 0
		state.TotalBytes = 0
		state.InstallMode = daemon.UpdateInstallNone
	})
	release, err := r.backend.Check(ctx)
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	})
	if err != nil {
		log.Printf("update check: %v", err)
		r.setError("check")
		return
	}
	if release == nil {
		r.updateState(func(state *daemon.UpdateSnapshot) {
			state.Phase = daemon.UpdateUpToDate
			state.TargetVersion = ""
			state.ErrorCode = ""
			state.InstallMode = daemon.UpdateInstallNone
		})
		return
	}
	r.mu.Lock()
	r.release = release
	r.mu.Unlock()
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateAvailable
		state.TargetVersion = release.Version
		state.TotalBytes = release.Artifact.Size
		state.ReceivedBytes = 0
		state.ErrorCode = ""
		state.InstallMode = daemon.UpdateInstallNone
	})
	if automatic {
		r.runDownload(ctx)
	}
}

func (r *updateRunner) runDownload(ctx context.Context) {
	downloadCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	r.activeCancel = cancel
	release := r.release
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		r.activeCancel = nil
		r.mu.Unlock()
	}()
	if release == nil {
		r.setError("download")
		return
	}
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateDownloading
		state.TargetVersion = release.Version
		state.ErrorCode = ""
		state.InstallMode = daemon.UpdateInstallNone
	})
	if err := r.backend.DownloadAndInstall(downloadCtx); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return
		}
		log.Printf("update download: %v", err)
		r.setError("download")
		return
	}
	// The Wails updater normally emits EventUpdateReady after verification. A
	// backend that completes without that event still receives a truthful ready
	// projection here.
	if r.Snapshot().Phase != daemon.UpdateReady {
		r.updateState(func(state *daemon.UpdateSnapshot) {
			state.Phase = daemon.UpdateReady
			state.ReceivedBytes = state.TotalBytes
			state.InstallMode = daemon.UpdateInstallStagedRestart
		})
	}
}

func (r *updateRunner) runInstall(ctx context.Context) {
	if r.stopUI != nil {
		if err := r.stopUI(); err != nil {
			log.Printf("stop UI before update: %v", err)
			r.setError("install")
			return
		}
	}
	// The UI stop is the daemon's update boundary. Only after that handshake
	// succeeds do we expose installing and let the Wails helper replace the
	// staged executable.
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateInstalling
		state.ErrorCode = ""
	})
	if err := r.backend.Restart(ctx); err != nil {
		log.Printf("restart after update: %v", err)
		r.setError("install")
	}
}

func (r *updateRunner) Stop() {
	if r == nil {
		return
	}
	r.mu.Lock()
	cancel := r.activeCancel
	startCancel := r.startCancel
	r.activeCancel = nil
	r.startCancel = nil
	r.mu.Unlock()
	if startCancel != nil {
		startCancel()
	}
	if cancel != nil {
		cancel()
	}
}

// Start schedules the quiet startup check and the periodic background checks
// used by the resident daemon. Manual requests share the same single-flight
// coordinator.
func (r *updateRunner) Start() {
	if r == nil || r.backend == nil {
		return
	}
	r.mu.Lock()
	if r.startCancel != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.startCancel = cancel
	r.mu.Unlock()
	go func() {
		initial := time.NewTimer(10 * time.Second)
		defer initial.Stop()
		select {
		case <-initial.C:
			r.Trigger(ctx, true)
		case <-ctx.Done():
			return
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.Trigger(ctx, true)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// newUpdateRunner configures the official Wails endpoint provider when a
// feed URL is supplied. Release signing remains optional for local/testing
// feeds; production feeds should provide both a digest and a pinned key.
func newUpdateRunner(desktop *application.App, stopUI func() error, publish func(daemon.UpdateSnapshot)) *updateRunner {
	runner := &updateRunner{
		current: version.String(),
		stopUI:  stopUI,
		publish: publish,
		state:   daemon.UpdateSnapshot{Phase: daemon.UpdateUnconfigured, CurrentVersion: version.String()},
	}
	feed := strings.TrimSpace(os.Getenv(updateFeedEnv))
	if feed == "" {
		return runner
	}
	channel := strings.TrimSpace(os.Getenv(updateChannelEnv))
	provider, err := endpoint.New(endpoint.Config{URL: feed, Channel: channel})
	if err != nil {
		log.Printf("update feed unavailable: %v", err)
		return runner
	}
	publicKey, err := updatePublicKey()
	if err != nil {
		log.Printf("update public key unavailable: %v", err)
		return runner
	}
	if err := desktop.Updater.Init(wailsupdater.Config{
		CurrentVersion: version.String(),
		Providers:      []wailsupdater.Provider{provider},
		PublicKey:      publicKey,
		Window:         wailsupdater.WindowNone,
	}); err != nil {
		log.Printf("update initializer unavailable: %v", err)
		return runner
	}
	runner.backend = wailsUpdateBackend{updater: desktop.Updater}
	runner.state.Phase = daemon.UpdateIdle
	registerUpdaterEvents(desktop, runner)
	return runner
}

func registerUpdaterEvents(desktop *application.App, runner *updateRunner) {
	desktop.Event.On(wailsupdater.EventCheckStarted, func(*application.CustomEvent) {
		runner.updateState(func(state *daemon.UpdateSnapshot) { state.Phase = daemon.UpdateChecking })
	})
	desktop.Event.On(wailsupdater.EventUpdateAvailable, func(event *application.CustomEvent) {
		if release, ok := updaterRelease(event); ok {
			runner.mu.Lock()
			runner.release = release
			runner.mu.Unlock()
			runner.updateState(func(state *daemon.UpdateSnapshot) {
				state.Phase = daemon.UpdateAvailable
				state.TargetVersion = release.Version
				state.TotalBytes = release.Artifact.Size
			})
		}
	})
	desktop.Event.On(wailsupdater.EventNoUpdate, func(*application.CustomEvent) {
		runner.updateState(func(state *daemon.UpdateSnapshot) {
			state.Phase = daemon.UpdateUpToDate
			state.TargetVersion = ""
			state.ErrorCode = ""
		})
	})
	desktop.Event.On(wailsupdater.EventDownloadStarted, func(event *application.CustomEvent) {
		runner.updateState(func(state *daemon.UpdateSnapshot) {
			state.Phase = daemon.UpdateDownloading
			state.ReceivedBytes = 0
			if release, ok := updaterRelease(event); ok {
				state.TargetVersion = release.Version
				state.TotalBytes = release.Artifact.Size
			}
		})
	})
	desktop.Event.On(wailsupdater.EventDownloadProgress, func(event *application.CustomEvent) {
		if progress, ok := updaterProgress(event); ok {
			runner.updateState(func(state *daemon.UpdateSnapshot) {
				state.Phase = daemon.UpdateDownloading
				state.ReceivedBytes = progress.Written
				if progress.Total > 0 {
					state.TotalBytes = progress.Total
				}
			})
		}
	})
	desktop.Event.On(wailsupdater.EventDownloadComplete, func(*application.CustomEvent) {
		runner.updateState(func(state *daemon.UpdateSnapshot) { state.Phase = daemon.UpdateVerifying })
	})
	desktop.Event.On(wailsupdater.EventVerifying, func(*application.CustomEvent) {
		runner.updateState(func(state *daemon.UpdateSnapshot) { state.Phase = daemon.UpdateVerifying })
	})
	desktop.Event.On(wailsupdater.EventInstalling, func(*application.CustomEvent) {
		if runner.Snapshot().Phase == daemon.UpdateInstalling {
			return
		}
		runner.updateState(func(state *daemon.UpdateSnapshot) { state.Phase = daemon.UpdateVerifying })
	})
	desktop.Event.On(wailsupdater.EventUpdateReady, func(event *application.CustomEvent) {
		runner.updateState(func(state *daemon.UpdateSnapshot) {
			state.Phase = daemon.UpdateReady
			state.InstallMode = daemon.UpdateInstallStagedRestart
			state.ErrorCode = ""
			if release, ok := updaterRelease(event); ok {
				state.TargetVersion = release.Version
				state.TotalBytes = release.Artifact.Size
				state.ReceivedBytes = state.TotalBytes
			}
		})
	})
	desktop.Event.On(wailsupdater.EventError, func(event *application.CustomEvent) {
		code := "update"
		if info, ok := updaterErrorInfo(event); ok && info.Stage != "" {
			code = string(info.Stage)
		}
		runner.setError(code)
	})
}

func updaterRelease(event *application.CustomEvent) (*wailsupdater.Release, bool) {
	if event == nil {
		return nil, false
	}
	switch value := event.Data.(type) {
	case *wailsupdater.Release:
		return value, value != nil
	case wailsupdater.Release:
		copy := value
		return &copy, true
	default:
		return nil, false
	}
}

func updaterProgress(event *application.CustomEvent) (wailsupdater.Progress, bool) {
	if event == nil {
		return wailsupdater.Progress{}, false
	}
	switch value := event.Data.(type) {
	case wailsupdater.Progress:
		return value, true
	case *wailsupdater.Progress:
		return *value, value != nil
	default:
		return wailsupdater.Progress{}, false
	}
}

func updaterErrorInfo(event *application.CustomEvent) (wailsupdater.ErrorInfo, bool) {
	if event == nil {
		return wailsupdater.ErrorInfo{}, false
	}
	switch value := event.Data.(type) {
	case wailsupdater.ErrorInfo:
		return value, true
	case *wailsupdater.ErrorInfo:
		return *value, value != nil
	default:
		return wailsupdater.ErrorInfo{}, false
	}
}

func updatePublicKey() ([]byte, error) {
	if path := strings.TrimSpace(os.Getenv(updatePublicKeyFile)); path != "" {
		return os.ReadFile(path)
	}
	raw := strings.TrimSpace(os.Getenv(updatePublicKeyEnv))
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err == nil && len(decoded) > 0 {
		return decoded, nil
	}
	return []byte(raw), nil
}
