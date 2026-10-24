package desktopapp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/selfupdate"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/version"
	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

const (
	updateFeedEnv                 = "DSH_WORK_UPDATE_FEED_URL"
	updateChannelEnv              = "DSH_WORK_UPDATE_CHANNEL"
	updateProgressPublishInterval = 250 * time.Millisecond
	// updateManifestPath is where each channel's feed document lives below
	// the feed root: one fixed release tag per channel, holding one asset that
	// the pipeline replaces on every promotion.
	updateManifestPath    = "updater-%s/update.json"
	updateDownloadTimeout = 30 * time.Minute
	// A channel switch waits for the running operation, which is bounded by the
	// download it is already doing.
	updateSwitchTimeout = 30 * time.Second
	updateSwitchPoll    = 25 * time.Millisecond
)

// manifestURL is the feed document of one channel. Each channel is served from
// its own release tag, so the client never has to filter a shared feed.
func manifestURL(feedRoot, channel string) string {
	return strings.TrimSuffix(feedRoot, "/") + "/" + fmt.Sprintf(updateManifestPath, channel)
}

var (
	errUpdateUnavailable = errors.New("update controls unavailable")
	errUpdateNoRelease   = errors.New("no update is available")
)

// updateBackend is the release source and installer handoff the daemon owns.
// The provider only offers a release; every authenticity decision is made by
// this package through internal/selfupdate, because the vendored updater
// accepts a release that carries no verification block.
type updateBackend interface {
	Check(context.Context) (*pendingRelease, error)
	Stage(context.Context, *pendingRelease, func(written, total int64)) error
	Recheck() error
	Install(context.Context) error
	// WithChannel returns a backend that follows another feed, and Discard drops
	// everything the previous channel produced.
	WithChannel(channel string) (updateBackend, error)
	Discard() error
}

// pendingRelease pairs the provider record, needed to fetch the artifact, with
// the identity the feed claims, needed to authenticate it.
type pendingRelease struct {
	provider *wailsupdater.Release
	feed     *selfupdate.FeedRelease
}

// installerBackend downloads the advertised installer, authenticates it, keeps
// a durable verified copy, and hands that copy to the operating system.
type installerBackend struct {
	provider wailsupdater.Provider
	feedRoot string
	channel  string
	staging  *selfupdate.Staging

	pending *pendingRelease
	staged  *selfupdate.Installer
}

// WithChannel returns a backend that reads another channel's manifest. Each
// channel has its own feed file, so a switch cannot keep following the previous
// channel's releases.
func (b *installerBackend) WithChannel(channel string) (updateBackend, error) {
	provider, err := endpoint.New(endpoint.Config{
		URL:        manifestURL(b.feedRoot, channel),
		Channel:    channel,
		HTTPClient: &http.Client{Timeout: updateDownloadTimeout},
	})
	if err != nil {
		return nil, err
	}
	return &installerBackend{
		provider: provider,
		feedRoot: b.feedRoot,
		channel:  channel,
		staging:  b.staging,
	}, nil
}

// Discard removes the staged candidate and the release it came from, so a
// download from the previous channel cannot become an installation.
func (b *installerBackend) Discard() error {
	b.pending = nil
	b.staged = nil
	if b.staging == nil {
		return nil
	}
	return b.staging.Clear()
}

func (b *installerBackend) Check(ctx context.Context) (*pendingRelease, error) {
	release, err := b.provider.Check(ctx, wailsupdater.CheckRequest{
		CurrentVersion: version.String(),
		Platform:       "windows",
		Arch:           runtime.GOARCH,
	})
	if err != nil {
		return nil, err
	}
	if release == nil {
		return nil, nil
	}
	if !strings.EqualFold(strings.TrimSpace(release.Channel), b.channel) {
		return nil, fmt.Errorf("update feed advertised release channel %q, this installation follows %q", release.Channel, b.channel)
	}
	feed := selfupdate.ReleaseFrom(release)
	if err := selfupdate.ValidateRelease(feed); err != nil {
		return nil, err
	}
	return &pendingRelease{provider: release, feed: feed}, nil
}

// Stage writes the offered artifact into a private temporary file, lets the
// provider stream and digest it, and stages the authenticated copy. The
// provider's own staging directory is temporary by design, so the verified
// bytes are copied before they can be discarded.
func (b *installerBackend) Stage(ctx context.Context, pending *pendingRelease, progress func(written, total int64)) error {
	if pending == nil || pending.provider == nil {
		return errUpdateNoRelease
	}
	dir, err := os.MkdirTemp("", "dsh-work-update-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	download := filepath.Join(dir, "installer.exe")
	file, err := os.OpenFile(download, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	streamErr := b.provider.Download(ctx, pending.provider, file, progress)
	closeErr := file.Close()
	if streamErr != nil {
		return streamErr
	}
	if closeErr != nil {
		return closeErr
	}
	installer, err := b.staging.Stage(download, pending.feed)
	if err != nil {
		return err
	}
	b.pending = pending
	b.staged = installer
	return nil
}

// Recheck re-authenticates the staged bytes immediately before the installer
// runs, so a payload swapped on disk after staging cannot be executed.
func (b *installerBackend) Recheck() error {
	if b.staged == nil || b.pending == nil {
		return errUpdateNoRelease
	}
	return b.staging.Recheck(b.staged, b.pending.feed)
}

// Install runs the authenticated installer detached from this process. The
// installer takes the maintenance boundary, stops the running workbench,
// replaces the whole release unit, releases the boundary and starts the new
// workbench; this process is expected to be stopped by that handshake.
func (b *installerBackend) Install(ctx context.Context) error {
	if b.staged == nil {
		return errUpdateNoRelease
	}
	outcome := b.staged.RunInstaller(ctx)
	return outcome.Err
}

// updateRunner is the daemon's single-flight update coordinator. Automatic
// checks download silently; only an explicit install action stops the UI and
// hands the authenticated installer to the operating system.
type updateRunner struct {
	backend  updateBackend
	current  string
	selected string
	stopUI   func() error
	publish  func(daemon.UpdateSnapshot)

	busy           atomic.Bool
	mu             sync.RWMutex
	state          daemon.UpdateSnapshot
	pending        *pendingRelease
	publishClock   trailingClock
	statePublisher *trailingPublisher[daemon.UpdateSnapshot]

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
	publisher, drain := r.queueStatePublishLocked(next, changed)
	r.mu.Unlock()
	if drain {
		publisher.drain()
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
	publisher, drain := r.queueStatePublishLocked(next, changed)
	r.mu.Unlock()
	if drain {
		publisher.drain()
	}
}

func (r *updateRunner) queueStatePublishLocked(next daemon.UpdateSnapshot, changed bool) (*trailingPublisher[daemon.UpdateSnapshot], bool) {
	if !changed || r.publish == nil {
		return nil, false
	}
	if r.statePublisher == nil {
		r.statePublisher = newTrailingPublisher(updateProgressPublishInterval, r.publishClock, r.publish, func(_, latest daemon.UpdateSnapshot) daemon.UpdateSnapshot {
			return latest
		})
	}
	return r.statePublisher, r.statePublisher.enqueue(next, next.Phase == daemon.UpdateDownloading)
}

func (r *updateRunner) closeStatePublisher() {
	if r == nil {
		return
	}
	r.mu.Lock()
	publisher := r.statePublisher
	r.mu.Unlock()
	if publisher != nil {
		publisher.close()
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
	pending, err := r.backend.Check(ctx)
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.LastCheckedAt = time.Now().UTC().Format(time.RFC3339)
	})
	if err != nil {
		logging.Module(logging.ModUpdate).Warn("update check failed", logging.ErrAttrs(err, "")...)
		r.setError("check")
		return
	}
	if pending == nil {
		r.updateState(func(state *daemon.UpdateSnapshot) {
			state.Phase = daemon.UpdateUpToDate
			state.TargetVersion = ""
			state.ErrorCode = ""
			state.InstallMode = daemon.UpdateInstallNone
		})
		return
	}
	r.mu.Lock()
	r.pending = pending
	r.mu.Unlock()
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateAvailable
		state.TargetVersion = pending.feed.Version
		state.TotalBytes = pending.feed.Artifact.Size
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
	pending := r.pending
	r.mu.Unlock()
	defer func() {
		cancel()
		r.mu.Lock()
		r.activeCancel = nil
		r.mu.Unlock()
	}()
	if pending == nil {
		r.setError("download")
		return
	}
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateDownloading
		state.TargetVersion = pending.feed.Version
		state.ErrorCode = ""
		state.InstallMode = daemon.UpdateInstallNone
	})
	// The artifact streams first; anything after the last byte is the
	// authentication of the copy that was written, so the phase reported to the
	// user follows the same order.
	progress := func(written, total int64) {
		r.updateState(func(state *daemon.UpdateSnapshot) {
			if total > 0 && written >= total {
				state.Phase = daemon.UpdateVerifying
			} else {
				state.Phase = daemon.UpdateDownloading
			}
			state.ReceivedBytes = written
			if total > 0 {
				state.TotalBytes = total
			}
		})
	}
	if err := r.backend.Stage(downloadCtx, pending, progress); err != nil {
		if errors.Is(err, context.Canceled) && ctx.Err() != nil {
			return
		}
		logging.Module(logging.ModUpdate).Error("update download failed", logging.ErrAttrs(err, lifecycle.ErrorUpdateDownloadFailed)...)
		r.setError("download")
		return
	}
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateReady
		state.ReceivedBytes = state.TotalBytes
		state.ErrorCode = ""
		state.InstallMode = daemon.UpdateInstallInstaller
	})
}

func (r *updateRunner) runInstall(ctx context.Context) {
	// The staged bytes are authenticated again here, before anything is stopped:
	// a payload that changed on disk after staging is refused while the running
	// workbench can still be kept.
	if err := r.backend.Recheck(); err != nil {
		logging.Module(logging.ModUpdate).Warn("staged installer recheck failed", logging.ErrAttrs(err, "")...)
		r.setError("install")
		return
	}
	if r.stopUI != nil {
		if err := r.stopUI(); err != nil {
			logging.Module(logging.ModUpdate).Warn("ui stop before update failed", logging.ErrAttrs(err, "")...)
			r.setError("install")
			return
		}
	}
	// The UI stop is the daemon's handover. The installer then takes the
	// maintenance boundary, stops the remaining workbench processes itself,
	// replaces the whole release unit, releases the boundary and starts the new
	// workbench, so nothing here has to survive this process.
	r.updateState(func(state *daemon.UpdateSnapshot) {
		state.Phase = daemon.UpdateInstalling
		state.InstallMode = daemon.UpdateInstallInstaller
		state.ErrorCode = ""
	})
	if err := r.backend.Install(ctx); err != nil {
		logging.Module(logging.ModUpdate).Error("update install failed", logging.ErrAttrs(err, lifecycle.ErrorUpdateInstallFailed)...)
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

// newUpdateRunner wires the release feed this installation follows.
//
// The feed root, and the installer publisher when there is one, are compiled
// into a released build, so an installed copy can only be updated from its own
// feed, and only with an installer Authenticode-signed by that publisher once a
// publisher is named. A development build may point at another feed through the
// environment, but only when it has no compiled-in identity at all. A build
// with no feed stays unconfigured.
func newUpdateRunner(stagingBase string, selected settings.UpdateChannel, stopUI func() error, publish func(daemon.UpdateSnapshot)) *updateRunner {
	channel := resolveUpdateChannel(selected)
	runner := &updateRunner{
		current:  version.String(),
		selected: string(channel),
		stopUI:   stopUI,
		publish:  publish,
		state: daemon.UpdateSnapshot{
			Phase:           daemon.UpdateUnconfigured,
			CurrentVersion:  version.String(),
			BuildChannel:    version.Channel(),
			SelectedChannel: string(channel),
		},
	}
	feedRoot := resolveUpdateFeed()
	if feedRoot == "" {
		logging.Module(logging.ModUpdate).Info("update feed not configured")
		return runner
	}
	staging, err := selfupdate.NewStaging(stagingBase)
	if err != nil {
		logging.Module(logging.ModUpdate).Warn("update staging unavailable", logging.ErrAttrs(err, "")...)
		return runner
	}
	manifest := manifestURL(feedRoot, string(channel))
	provider, err := endpoint.New(endpoint.Config{
		URL:        manifest,
		Channel:    string(channel),
		HTTPClient: &http.Client{Timeout: updateDownloadTimeout},
	})
	if err != nil {
		logging.Module(logging.ModUpdate).Warn("update feed unavailable", logging.ErrAttrs(err, "")...)
		return runner
	}
	runner.backend = &installerBackend{
		provider: provider,
		feedRoot: feedRoot,
		channel:  string(channel),
		staging:  staging,
	}
	runner.state.Phase = daemon.UpdateIdle
	return runner
}

// SetChannel applies the channel the user selected.
//
// The running operation is cancelled and awaited, the staged candidate and the
// remembered release are dropped, and the new channel is checked. Switching is
// refused while an update is installing, because the installer is already
// replacing this installation.
func (r *updateRunner) SetChannel(ctx context.Context, channel settings.UpdateChannel) error {
	if r == nil {
		return errUpdateUnavailable
	}
	if !channel.Valid() {
		return fmt.Errorf("unsupported update channel %q", channel)
	}
	r.mu.RLock()
	backend := r.backend
	selected := r.selected
	r.mu.RUnlock()
	if backend == nil {
		return errUpdateUnavailable
	}
	if phase := r.Snapshot().Phase; phase == daemon.UpdateInstalling {
		return errors.New("the update channel cannot change while an update is installing")
	}
	if string(channel) == selected {
		return nil
	}
	r.Stop()
	if !r.waitIdle(updateSwitchTimeout) {
		return errors.New("the previous update operation did not stop in time")
	}
	next, err := backend.WithChannel(string(channel))
	if err != nil {
		return err
	}
	if err := backend.Discard(); err != nil {
		logging.Module(logging.ModUpdate).Warn("staged update discard failed", logging.ErrAttrs(err, "")...)
	}
	r.mu.Lock()
	r.backend = next
	r.pending = nil
	r.selected = string(channel)
	r.mu.Unlock()
	r.setState(daemon.UpdateSnapshot{
		Phase:           daemon.UpdateIdle,
		CurrentVersion:  r.current,
		BuildChannel:    version.Channel(),
		SelectedChannel: string(channel),
	})
	// The new channel is checked at once, but the result waits for the user:
	// switching channels must never install anything on its own.
	r.Trigger(ctx, false)
	return nil
}

// waitIdle reports whether the single-flight operation finished in time.
func (r *updateRunner) waitIdle(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !r.busy.Load() {
			return true
		}
		time.Sleep(updateSwitchPoll)
	}
	return !r.busy.Load()
}

// resolveUpdateChannel reports the channel this installation follows. The
// selected channel is the user's choice and is the only source for a released
// build; a development build without a compiled-in channel may take the
// environment override so a local feed can be exercised.
func resolveUpdateChannel(selected settings.UpdateChannel) settings.UpdateChannel {
	if !version.IsDevelopment() || version.Channel() != "" {
		if !selected.Valid() {
			return settings.DefaultUpdateChannel
		}
		return selected
	}
	override := settings.UpdateChannel(strings.ToLower(strings.TrimSpace(os.Getenv(updateChannelEnv))))
	if override.Valid() {
		return override
	}
	if !selected.Valid() {
		return settings.DefaultUpdateChannel
	}
	return selected
}

// resolveUpdateFeed resolves the feed this installation follows. A released
// build follows only the feed compiled into it; a development build may point
// at a local feed instead, which is how the update path is exercised without a
// published release.
func resolveUpdateFeed() string {
	if feed := strings.TrimSpace(version.Feed()); feed != "" {
		return feed
	}
	if !version.IsDevelopment() {
		return ""
	}
	return strings.TrimSpace(os.Getenv(updateFeedEnv))
}
