package desktopapp

import (
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/version"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
	"github.com/wailsapp/wails/v3/pkg/updater/providers/endpoint"
)

const (
	updateFeedEnv       = "DSH_WORK_UPDATE_FEED_URL"
	updateChannelEnv    = "DSH_WORK_UPDATE_CHANNEL"
	updatePublicKeyEnv  = "DSH_WORK_UPDATE_PUBLIC_KEY"
	updatePublicKeyFile = "DSH_WORK_UPDATE_PUBLIC_KEY_FILE"
)

// updateBackend is the small part of the Wails updater used by the desktop
// flow. Keeping the orchestration behind this interface makes the prompt,
// cancellation and restart ordering testable without starting a WebView.
type updateBackend interface {
	Check(context.Context) (*wailsupdater.Release, error)
	DownloadAndInstall(context.Context) error
	Restart(context.Context) error
	DownloadedPath() string
}

type wailsUpdateBackend struct{ updater *wailsupdater.Updater }

func (b wailsUpdateBackend) Check(ctx context.Context) (*wailsupdater.Release, error) {
	return b.updater.Check(ctx)
}
func (b wailsUpdateBackend) DownloadAndInstall(ctx context.Context) error {
	return b.updater.DownloadAndInstall(ctx)
}
func (b wailsUpdateBackend) Restart(ctx context.Context) error { return b.updater.Restart(ctx) }
func (b wailsUpdateBackend) DownloadedPath() string            { return b.updater.DownloadedPath() }

type updatePrompter interface {
	Confirm(*wailsupdater.Release) bool
	Info(title, message string)
	Error(title, message string)
}

type nativeUpdatePrompter struct {
	desktop *application.App
	locale  func() settings.Locale
}

var updatePromptSequence atomic.Uint64

func (p nativeUpdatePrompter) labels() nativeui.Labels {
	locale := settings.DefaultLocale
	if p.locale != nil {
		locale = p.locale()
	}
	return nativeui.LabelsFor(locale)
}

func (p nativeUpdatePrompter) Confirm(release *wailsupdater.Release) bool {
	labels := p.labels()
	version := ""
	if release != nil {
		version = release.Version
	}
	message := fmt.Sprintf(labels.UpdateAvailableMessage, version)
	sequence := updatePromptSequence.Add(1)
	applyEvent := fmt.Sprintf("dsh-work:update:%d:apply", sequence)
	cancelEvent := fmt.Sprintf("dsh-work:update:%d:cancel", sequence)
	choice := make(chan bool, 1)
	var choiceOnce sync.Once
	choose := func(value bool) { choiceOnce.Do(func() { choice <- value }) }

	prompt := p.desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:                 fmt.Sprintf("dsh-work-update-prompt-%d", sequence),
		Title:                labels.UpdateAvailableTitle,
		Width:                460,
		Height:               250,
		MinWidth:             460,
		MinHeight:            250,
		MaxWidth:             460,
		MaxHeight:            250,
		AlwaysOnTop:          true,
		DisableResize:        true,
		InitialPosition:      application.WindowCentered,
		BackgroundColour:     application.NewRGB(31, 37, 44),
		HTML:                 updatePromptHTML(labels.UpdateAvailableTitle, message, labels.UpdateNow, labels.UpdateCancel, applyEvent, cancelEvent),
		AllowSimpleEventEmit: true,
	})
	stopApply := p.desktop.Event.On(applyEvent, func(*application.CustomEvent) { choose(true) })
	stopCancel := p.desktop.Event.On(cancelEvent, func(*application.CustomEvent) { choose(false) })
	stopClose := prompt.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { choose(false) })
	prompt.Show().Focus()
	selected := <-choice
	stopApply()
	stopCancel()
	stopClose()
	prompt.Close()
	return selected
}

func updatePromptHTML(title, message, apply, cancel, applyEvent, cancelEvent string) string {
	return fmt.Sprintf(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title><style>
:root{color-scheme:dark;font-family:"Segoe UI",system-ui,sans-serif;background:#1f252c;color:#f5f7fa}
body{margin:0;min-height:250px;display:flex;align-items:center;justify-content:center;background:#1f252c}
main{box-sizing:border-box;width:100%%;padding:28px 32px}h1{font-size:20px;font-weight:600;margin:0 0 16px}
p{font-size:14px;line-height:1.5;margin:0 0 28px;color:#d7dde5;white-space:pre-wrap}
footer{display:flex;justify-content:flex-end;gap:10px}button{border:1px solid #586575;border-radius:6px;padding:9px 18px;font:inherit;color:#f5f7fa;background:#303946;cursor:pointer}
button:focus{outline:2px solid #76a9fa;outline-offset:2px}button.primary{border-color:#76a9fa;background:#3478d4}
</style></head><body><main role="dialog" aria-labelledby="title" aria-describedby="message">
<h1 id="title">%s</h1><p id="message">%s</p><footer>
<button id="cancel" type="button">%s</button><button id="apply" class="primary" type="button" autofocus>%s</button>
</footer></main><script>
(function(){var apply=%s,cancel=%s;function emit(name){if(window.wails&&window.wails.Events){window.wails.Events.Emit(name)}}
document.getElementById("apply").addEventListener("click",function(){emit(apply)});
document.getElementById("cancel").addEventListener("click",function(){emit(cancel)});
document.addEventListener("keydown",function(e){if(e.key==="Escape"){emit(cancel)}})})();
</script></body></html>`, html.EscapeString(title), html.EscapeString(title), html.EscapeString(message), html.EscapeString(cancel), html.EscapeString(apply), strconv.Quote(applyEvent), strconv.Quote(cancelEvent))
}

func (p nativeUpdatePrompter) Info(title, message string) {
	p.desktop.Dialog.Info().SetTitle(title).SetMessage(message).Show()
}

func (p nativeUpdatePrompter) Error(title, message string) {
	p.desktop.Dialog.Error().SetTitle(title).SetMessage(message).Show()
}

// updateRunner owns one application update flow. It starts staging as soon
// as a release is found, then asks whether the user wants to continue. A
// cancel interrupts the temporary download and removes a completed staging
// directory; an update choice waits for verification/staging before closing
// the UI and asking the Wails helper to restart the daemon into the new app.
type updateRunner struct {
	backend updateBackend
	prompt  updatePrompter
	stopUI  func() error
	locale  func() settings.Locale

	busy atomic.Bool
	mu   sync.Mutex
	// downloadStarted is replaced for each run. Wails emits its start event
	// before the provider begins streaming, which lets the prompt appear while
	// the artifact is being staged instead of after the network transfer.
	downloadStarted chan<- struct{}
	activeCancel    context.CancelFunc
	startCancel     context.CancelFunc
}

func (r *updateRunner) notifyDownloadStarted() {
	r.mu.Lock()
	started := r.downloadStarted
	r.mu.Unlock()
	if started == nil {
		return
	}
	select {
	case started <- struct{}{}:
	default:
	}
}

func (r *updateRunner) Trigger(ctx context.Context, manual bool) {
	if r == nil || r.backend == nil {
		if manual && r != nil && r.prompt != nil {
			labels := r.labels()
			r.prompt.Info(labels.UpdateTitle, labels.UpdateMessage)
		}
		return
	}
	if !r.busy.CompareAndSwap(false, true) {
		if manual && r.prompt != nil {
			labels := r.labels()
			r.prompt.Info(labels.UpdateTitle, labels.UpdateBusyMessage)
		}
		return
	}
	go r.run(ctx, manual)
}

func (r *updateRunner) run(ctx context.Context, manual bool) {
	defer r.busy.Store(false)
	release, err := r.backend.Check(ctx)
	if err != nil {
		log.Printf("update check: %v", err)
		if manual && r.prompt != nil {
			labels := r.labels()
			r.prompt.Error(labels.UpdateFailureTitle, labels.UpdateFailureMessage)
		}
		return
	}
	if release == nil {
		if manual && r.prompt != nil {
			labels := r.labels()
			r.prompt.Info(labels.UpdateTitle, labels.UpdateNoUpdateMessage)
		}
		return
	}

	downloadCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	started := make(chan struct{}, 1)
	r.downloadStarted = started
	r.activeCancel = cancel
	r.mu.Unlock()
	done := make(chan error, 1)
	go func() { done <- r.backend.DownloadAndInstall(downloadCtx) }()

	// Give the updater a chance to emit DownloadStarted. The timeout keeps a
	// slow or unusual provider from blocking the user prompt indefinitely.
	finished := false
	var downloadErr error
	startTimer := time.NewTimer(2 * time.Second)
	select {
	case <-started:
	case downloadErr = <-done:
		finished = true
	case <-startTimer.C:
	}
	if !startTimer.Stop() {
		select {
		case <-startTimer.C:
		default:
		}
	}

	accepted := r.prompt != nil && r.prompt.Confirm(release)
	if !accepted {
		cancel()
		if !finished {
			downloadErr = <-done
		}
		r.discardStaged()
		r.clearActive()
		return
	}
	if !finished {
		downloadErr = <-done
	}
	r.clearActive()
	if downloadErr != nil {
		log.Printf("update download: %v", downloadErr)
		if r.prompt != nil {
			labels := r.labels()
			r.prompt.Error(labels.UpdateFailureTitle, labels.UpdateFailureMessage)
		}
		return
	}

	// The UI is a separate process using the same installed executable. Close
	// it before the daemon's updater helper swaps the executable on Windows.
	if r.stopUI != nil {
		if err := r.stopUI(); err != nil {
			log.Printf("stop UI before update: %v", err)
			r.discardStaged()
			if r.prompt != nil {
				labels := r.labels()
				r.prompt.Error(labels.UpdateFailureTitle, labels.UpdateFailureMessage)
			}
			return
		}
	}
	if err := r.backend.Restart(context.Background()); err != nil {
		log.Printf("restart after update: %v", err)
		if r.prompt != nil {
			labels := r.labels()
			r.prompt.Error(labels.UpdateFailureTitle, labels.UpdateFailureMessage)
		}
	}
}

func (r *updateRunner) labels() nativeui.Labels {
	locale := settings.DefaultLocale
	if r != nil && r.locale != nil {
		locale = r.locale()
	}
	return nativeui.LabelsFor(locale)
}

func (r *updateRunner) clearActive() {
	r.mu.Lock()
	// The runner serializes flows with busy, so the active cancellation belongs
	// to this run when it reaches either terminal path.
	r.activeCancel = nil
	r.downloadStarted = nil
	r.mu.Unlock()
}

func (r *updateRunner) discardStaged() {
	path := r.backend.DownloadedPath()
	if path == "" {
		return
	}
	dir := filepath.Dir(path)
	tmp := filepath.Clean(os.TempDir())
	rel, err := filepath.Rel(tmp, dir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || !strings.HasPrefix(filepath.Base(dir), "wails-update-") {
		return
	}
	_ = os.RemoveAll(dir)
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
	r.downloadStarted = nil
	r.mu.Unlock()
	if startCancel != nil {
		startCancel()
	}
	if cancel != nil {
		cancel()
	}
}

// Start schedules the quiet startup check and the periodic background checks
// used by the resident daemon. Manual menu requests share the same runner and
// are serialized with these checks.
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
			r.Trigger(ctx, false)
		case <-ctx.Done():
			return
		}
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				r.Trigger(ctx, false)
			case <-ctx.Done():
				return
			}
		}
	}()
}

// newUpdateRunner configures the official Wails endpoint provider when a
// feed URL is supplied. Release signing remains optional for local/testing
// feeds, while production feeds should provide both a digest and a pinned key.
func newUpdateRunner(desktop *application.App, locale func() settings.Locale, stopUI func() error) *updateRunner {
	prompt := nativeUpdatePrompter{desktop: desktop, locale: locale}
	feed := strings.TrimSpace(os.Getenv(updateFeedEnv))
	if feed == "" {
		return &updateRunner{prompt: prompt, stopUI: stopUI, locale: locale}
	}
	channel := strings.TrimSpace(os.Getenv(updateChannelEnv))
	provider, err := endpoint.New(endpoint.Config{URL: feed, Channel: channel})
	if err != nil {
		log.Printf("update feed unavailable: %v", err)
		return &updateRunner{prompt: prompt, stopUI: stopUI, locale: locale}
	}
	publicKey, err := updatePublicKey()
	if err != nil {
		log.Printf("update public key unavailable: %v", err)
		return &updateRunner{prompt: prompt, stopUI: stopUI, locale: locale}
	}
	if err := desktop.Updater.Init(wailsupdater.Config{
		CurrentVersion: version.String(),
		Providers:      []wailsupdater.Provider{provider},
		PublicKey:      publicKey,
		Window:         wailsupdater.WindowNone,
	}); err != nil {
		log.Printf("update initializer unavailable: %v", err)
		return &updateRunner{prompt: prompt, stopUI: stopUI, locale: locale}
	}
	runner := &updateRunner{
		backend: wailsUpdateBackend{updater: desktop.Updater},
		prompt:  prompt,
		stopUI:  stopUI,
		locale:  locale,
	}
	desktop.Event.On(wailsupdater.EventDownloadStarted, func(*application.CustomEvent) {
		runner.notifyDownloadStarted()
	})
	return runner
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
