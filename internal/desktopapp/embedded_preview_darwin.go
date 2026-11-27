//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/notifications"
	"github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed embedded_preview_consumer.js
var macPreviewConsumerJS string

//go:embed embedded_preview_shell.js
var macPreviewShellJS string

type macPreviewScenario struct {
	Nonce                 string `json:"nonce"`
	URL                   string `json:"url"`
	Workspace             string `json:"workspace"`
	Evidence              string `json:"evidence"`
	ManualFiles           string `json:"manualFiles,omitempty"`
	ManualNotification    bool   `json:"manualNotification,omitempty"`
	Download              bool   `json:"download,omitempty"`
	NativeDownload        bool   `json:"nativeDownload,omitempty"`
	ManualDownload        bool   `json:"manualDownload,omitempty"`
	ManualOverwrite       bool   `json:"manualOverwrite,omitempty"`
	CrashDownload         bool   `json:"crashDownload,omitempty"`
	InvalidateDownload    bool   `json:"invalidateDownload,omitempty"`
	ResizeDownload        bool   `json:"resizeDownload,omitempty"`
	FailNavigation        bool   `json:"failNavigation,omitempty"`
	ContentRisk           string `json:"contentRisk,omitempty"`
	DownloadQuit          string `json:"downloadQuit,omitempty"`
	DownloadWindowClose   bool   `json:"downloadWindowClose,omitempty"`
	IndicatorDependencies string `json:"indicatorDependencies,omitempty"`
	DockReopen            bool   `json:"dockReopen,omitempty"`
	PetGeometry           bool   `json:"petGeometry,omitempty"`
	RetirePicker          bool   `json:"retirePicker,omitempty"`
}

var macPreviewScenarioOnce sync.Once
var macPreviewScenarioValue *macPreviewScenario
var macPreviewScenarioError error

func macPreviewConfig() (*macPreviewScenario, error) {
	macPreviewScenarioOnce.Do(func() {
		path := os.Getenv("DSH_WORK_MAC_PREVIEW_SCENARIO")
		if path == "" {
			return
		}
		root := embeddedDevelopmentRoot()
		if os.Getenv("CI") != "true" || root == "" || filepath.Clean(path) != filepath.Join(root, "scenario.json") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 8192 {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		data, err := os.ReadFile(path)
		var cfg macPreviewScenario
		if err != nil || json.Unmarshal(data, &cfg) != nil || len(cfg.Nonce) < 16 || len(cfg.Nonce) > 128 || filepath.Clean(cfg.Workspace) != filepath.Join(root, "workspace") || !filepath.IsAbs(cfg.Evidence) {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.ManualFiles != "" && cfg.ManualFiles != "both" && cfg.ManualFiles != "artifact" {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.ManualOverwrite && (!cfg.Download || !cfg.NativeDownload || !cfg.ManualDownload) {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.ContentRisk != "" && ((cfg.ContentRisk != "accept" && cfg.ContentRisk != "decline") ||
			!cfg.Download || cfg.NativeDownload || cfg.ManualDownload || cfg.CrashDownload) {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.DownloadQuit != "" && ((cfg.DownloadQuit != "wait" && cfg.DownloadQuit != "cancel" && cfg.DownloadQuit != "back") ||
			!cfg.Download || cfg.CrashDownload || cfg.NativeDownload || cfg.ManualDownload || cfg.ContentRisk != "") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.DownloadWindowClose && (!cfg.Download || cfg.DownloadQuit != "" || cfg.CrashDownload ||
			cfg.NativeDownload || cfg.ManualDownload || cfg.ContentRisk != "") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.ResizeDownload && (!cfg.Download || cfg.NativeDownload || cfg.CrashDownload || cfg.InvalidateDownload ||
			cfg.ContentRisk != "" || cfg.DownloadQuit != "" || cfg.DownloadWindowClose) {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.IndicatorDependencies != "" && (!filepath.IsAbs(cfg.IndicatorDependencies) || cfg.Download || cfg.ManualFiles != "") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.DockReopen && (cfg.Download || cfg.ManualFiles != "" || cfg.IndicatorDependencies != "") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.ManualNotification && !cfg.DockReopen {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		if cfg.PetGeometry && (cfg.DockReopen || cfg.Download || cfg.ManualFiles != "" || cfg.IndicatorDependencies != "") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		parsed, err := url.Parse(cfg.URL)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		macPreviewScenarioValue = &cfg
	})
	return macPreviewScenarioValue, macPreviewScenarioError
}

func configureEmbeddedPreviewPet(config *pet.CatalogConfig, capabilities *pet.OverlayCapabilities, root string) bool {
	if cfg, err := macPreviewConfig(); err == nil && cfg != nil && cfg.PetGeometry {
		config.CodexHome = filepath.Join(root, "probe-pets")
		config.CommunityHome = ""
		config.Env = func(string) string { return "" }
		// Exercise geometry on the real overlay before qualifying Mac drag.
		capabilities.Interaction = true
		capabilities.Drag = true
		return true
	}
	return false
}

func configureEmbeddedPreviewServices(server *daemon.Server, browser *embeddedBrowserService, app *application.App, labels nativeui.Labels, petPointerHit func(float64, float64) bool) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return
	}
	if cfg.IndicatorDependencies != "" {
		server.Services["MacPreviewIndicator"] = &macPreviewIndicator{cfg, browser}
	}
	if cfg.DockReopen {
		server.Services["MacPreviewWorkspace"] = &macPreviewWorkspace{app: app, browser: browser, labels: labels, cfg: cfg}
	}
	if cfg.PetGeometry {
		server.Services["MacPreviewPet"] = &macPreviewPet{app: app, settings: server.Settings, hitTest: petPointerHit}
	}
}

type macPreviewPet struct {
	app      *application.App
	settings *settings.Manager
	hitTest  func(float64, float64) bool
	mu       sync.Mutex
}

func (p *macPreviewPet) window(ctx context.Context) (application.Window, error) {
	if _, ok := daemon.ProcessPeerFrom(ctx); !ok {
		return nil, errors.New("native Pet fixture peer required")
	}
	window, ok := p.app.Window.GetByName("pet")
	if !ok || !window.IsVisible() {
		return nil, errors.New("Pet window is not visible")
	}
	return window, nil
}

func (p *macPreviewPet) Input(ctx context.Context) (map[string]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	window, err := p.window(ctx)
	if err != nil {
		return nil, err
	}
	return nativeui.CheckMacPreviewPetPointer(window, p.hitTest)
}

func (p *macPreviewPet) Click(ctx context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	window, err := p.window(ctx)
	if err != nil {
		return false, err
	}
	return nativeui.CheckMacPreviewPetClick(window)
}

func (p *macPreviewPet) Geometry(ctx context.Context, move bool) (desktopclient.MacPreviewPetFrame, error) {
	window, err := p.window(ctx)
	if err != nil {
		return desktopclient.MacPreviewPetFrame{}, err
	}
	frame, err := desktopclient.MacPreviewObservePetFrame(window)
	if err == nil && move {
		window.SetPosition(frame.AreaX+73, frame.AreaY+91)
	}
	return frame, err
}

func (p *macPreviewPet) DisplayChange(ctx context.Context, change string) error {
	window, err := p.window(ctx)
	if err != nil {
		return err
	}
	var event int
	switch change {
	case "screen":
		event = 0
	case "backing":
		event = 1
	case "topology":
		event = 2
	default:
		return errors.New("unknown fixture display event")
	}
	values, err := p.settings.Snapshot(ctx)
	if err != nil {
		return err
	}
	// Use a stale saved monitor and scale while the real window stays still.
	// Only the notification handler can reconcile the profile to AppKit.
	values.Pet.Position.MonitorID = "fixture-previous-display"
	values.Pet.Position.AnchorX, values.Pet.Position.AnchorY = 0.95, 0.9
	values.Pet.Position.Scale = 1.125
	if _, err := p.settings.SetPetPreference(ctx, values.Pet); err != nil {
		return err
	}
	return desktopclient.MacPreviewPetDisplayChange(window, event)
}

type macPreviewWorkspace struct {
	app              *application.App
	browser          *embeddedBrowserService
	labels           nativeui.Labels
	cfg              *macPreviewScenario
	notificationMu   sync.Mutex
	notificationSent bool
}

type macPreviewWindowCounts struct {
	Total              int  `json:"total"`
	Visible            int  `json:"visible"`
	NotificationPosted bool `json:"notificationPosted,omitempty"`
}

func (d *macPreviewWorkspace) Observe(ctx context.Context, action string) (macPreviewWindowCounts, error) {
	if _, _, err := d.browser.peer(ctx); err != nil {
		return macPreviewWindowCounts{}, err
	}
	windows := d.app.Window.GetAll()
	counts := macPreviewWindowCounts{Total: len(windows)}
	for _, window := range windows {
		if window.IsVisible() {
			counts.Visible++
		}
	}
	switch action {
	case "dock":
		return counts, desktopclient.MacPreviewApplicationReopen()
	case "notification-default":
		return counts, desktopclient.MacPreviewNotificationDefault(dshApprovalCategoryID, d.labels.ApprovalAllowOnce, d.labels.ApprovalReject, true)
	case "notification-manual":
		if !d.cfg.ManualNotification || counts.Visible != 0 {
			return counts, errors.New("manual notification fixture is unavailable")
		}
		posted, err := d.postNotification()
		counts.NotificationPosted = posted
		return counts, err
	case "tray-button":
		return counts, desktopclient.MacPreviewTrayAction(ctx, "", false)
	case "tray-menu-open":
		return counts, desktopclient.MacPreviewTrayAction(ctx, d.labels.OpenWorkspace, true)
	case "tray-menu":
		return counts, desktopclient.MacPreviewTrayAction(ctx, d.labels.OpenWorkspace, false)
	case "":
		return counts, nil
	default:
		return counts, errors.New("unknown native workbench entry")
	}
}

func (d *macPreviewWorkspace) postNotification() (bool, error) {
	d.notificationMu.Lock()
	defer d.notificationMu.Unlock()
	if d.notificationSent {
		return true, nil
	}
	center := wailsnotifications.New()
	authorized, err := center.CheckNotificationAuthorization()
	if err != nil || !authorized {
		return false, err
	}
	if desktopclient.MacPreviewNotificationDefault(dshApprovalCategoryID, d.labels.ApprovalAllowOnce, d.labels.ApprovalReject, false) != nil {
		return false, nil
	}
	if err := center.SendNotificationWithActions(wailsnotifications.NotificationOptions{
		ID:         d.cfg.notificationID(),
		Title:      "DSH Work 通知测试",
		Body:       "点击通知正文，恢复测试工作台。",
		CategoryID: dshApprovalCategoryID,
	}); err != nil {
		return false, err
	}
	d.notificationSent = true
	return true, nil
}

func (c *macPreviewScenario) notificationID() string {
	return "dsh-preview-notification-" + c.Nonce
}

func recordMacPreviewNotificationResponse(result wailsnotifications.NotificationResult) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || !cfg.ManualNotification || result.Error != nil || result.Response.ID != cfg.notificationID() {
		return
	}
	data, err := json.Marshal(map[string]string{"action": result.Response.ActionIdentifier})
	if err == nil {
		_ = os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-notification-response.json"), data, 0600)
	}
}

func clearMacPreviewNotification() {
	cfg, err := macPreviewConfig()
	if err == nil && cfg != nil && cfg.ManualNotification {
		_ = wailsnotifications.New().RemoveNotification(cfg.notificationID())
	}
}

type macPreviewIndicator struct {
	cfg     *macPreviewScenario
	browser *embeddedBrowserService
}

type macPreviewIndicatorRoute struct{ target *embeddedbrowser.OwnedTarget }

func (r macPreviewIndicatorRoute) Select(context.Context, automation.Request) (browserruntime.RouteSelection, error) {
	return browserruntime.RouteSelection{Protocol: "owned-cef-v1", Open: r.target.OpenOwnedRoute}, nil
}

type macPreviewIndicatorSupervisor struct {
	owner  *macowner.MCPAdapter
	worker supervisor.Worker
}

func (s *macPreviewIndicatorSupervisor) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	worker, err := s.owner.Start(ctx, plan, raw)
	s.worker = worker
	return worker, err
}

const macPreviewIndicatorClicks = 3

// Click is registered only for the isolated scenario and uses the real
// workbench owner and locked MCP to click a fixed loopback fixture element.
func (p *macPreviewIndicator) Click(ctx context.Context, parent uint64, windowLease, slot, lease string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	peer, surface, err := p.browser.peer(ctx)
	if err != nil {
		return err
	}
	target, err := p.browser.Registry.OwnedWindowViewTarget(peer, surface, uintptr(parent), windowLease, slot, lease)
	if err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return err
	}
	publication, err := ownedMacPublication()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(p.cfg.Workspace), "indicator-runtime")
	owner, err := macowner.NewMCPAdapter(publication, node, p.cfg.IndicatorDependencies, root)
	if err != nil {
		return fmt.Errorf("configure locked Browser MCP: %w", err)
	}
	supervised := &macPreviewIndicatorSupervisor{owner: owner}
	defer func() {
		if err != nil && supervised.worker != nil {
			phase, code, signal, events := macowner.PreviewMCPOutcome(supervised.worker)
			fmt.Fprintf(os.Stderr, "mac-preview-indicator: owner phase=%s exit=%d signal=%d events=%v\n", phase, code, signal, events)
		}
	}()
	factory := browserruntime.NewFactory(browserruntime.Config{
		Routes: macPreviewIndicatorRoute{target}, Supervisor: supervised, Root: root,
		ObserveStage: func(stage string) {
			fmt.Fprintln(os.Stderr, "mac-preview-indicator:", stage)
			if stage == "failure-adapter-read" {
				if worker, ok := supervised.worker.(interface{ PrivateConnectionProcess() (uint32, uint64) }); ok {
					pid, started := worker.PrivateConnectionProcess()
					actual, err := localipc.ProcessStarted(pid)
					fmt.Fprintf(os.Stderr, "mac-preview-indicator: adapter-live-at-read-failure=%t\n", err == nil && started != 0 && actual == started)
				}
			}
		},
		Resolve: func(context.Context) (browserruntime.Runtime, error) {
			return browserruntime.Runtime{
				Node: node, Dependencies: p.cfg.IndicatorDependencies,
				Environment: map[string]string{"DSH_WORK_BROWSER_DIAGNOSTICS": "1"},
			}, nil
		},
	})
	guard := func() (func() error, error) {
		identity, err := target.OwnedIdentity(ctx)
		if err != nil || identity.URL != p.cfg.URL || identity.ViewLease != lease || !identity.ViewVisible || !identity.FrameScopeReady {
			return nil, fmt.Errorf("indicator requires the authenticated visible fixture document: %v", err)
		}
		end, err := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: rand.Text()})
		if err != nil {
			return nil, fmt.Errorf("begin guarded fixture call: %w", err)
		}
		return func() error {
			if err := end(); err != nil {
				return fmt.Errorf("end guarded fixture call: %w", err)
			}
			return nil
		}, nil
	}
	end, err := guard()
	if err != nil {
		return err
	}
	resource, startErr := factory(ctx, automation.Request{Session: "mac-indicator-fixture", Lifetime: "one"})
	if resource != nil {
		defer func() {
			if closeErr := resource.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close locked Browser MCP: %w", closeErr))
			}
		}()
	}
	if startErr != nil {
		startErr = fmt.Errorf("start locked Browser MCP: %w", startErr)
	}
	if err := errors.Join(startErr, end()); err != nil {
		return err
	}
	for click := 1; click <= macPreviewIndicatorClicks; click++ {
		end, err = guard()
		if err != nil {
			return err
		}
		value, callErr := resource.Execute(ctx, "browser_click", map[string]any{"target": "getByRole('button', { name: 'Agent feedback target' })"})
		if err = errors.Join(callErr, end()); err != nil {
			return fmt.Errorf("execute guarded fixture click %d: %w", click, err)
		}
		data, err := json.Marshal(value)
		if err != nil || bytes.Contains(data, []byte(`"isError":true`)) {
			return fmt.Errorf("locked MCP fixture click %d failed: %v", click, err)
		}
	}
	return nil
}

func macPreviewRecordBroker(owner *macowner.Adapter) error {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || !cfg.CrashDownload {
		return err
	}
	target, err := owner.PreviewBrokerTarget()
	if err != nil {
		return err
	}
	proof, err := json.Marshal(target)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-broker.json"), proof, 0600)
}

func macPreviewDownloadConsent() embeddedbrowser.DownloadConsent {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || !cfg.Download || cfg.NativeDownload {
		return nil
	}
	return func(ctx context.Context, request embeddedbrowser.DownloadConsentRequest) (embeddedbrowser.DownloadConsentDecision, error) {
		if err := ctx.Err(); err != nil {
			return embeddedbrowser.DownloadConsentDecision{}, err
		}
		if request.SourceOrigin+"/" != cfg.URL || (request.URL != cfg.URL+"complete.txt" && request.URL != cfg.URL+"cancel.txt") {
			return embeddedbrowser.DownloadConsentDecision{}, errors.New("MAC_PREVIEW_DOWNLOAD_REJECTED")
		}
		if (cfg.InvalidateDownload || cfg.ResizeDownload) && request.URL == cfg.URL+"cancel.txt" {
			if err := os.WriteFile(filepath.Join(cfg.Evidence, "download-consent-started"), nil, 0600); err != nil {
				return embeddedbrowser.DownloadConsentDecision{}, err
			}
			if cfg.InvalidateDownload {
				<-ctx.Done()
				err := os.WriteFile(filepath.Join(cfg.Evidence, "download-consent-canceled"), nil, 0600)
				return embeddedbrowser.DownloadConsentDecision{}, errors.Join(ctx.Err(), err)
			}
			if err := waitMacPreviewDownloadMarker(ctx, cfg.Evidence, "download-resize-complete"); err != nil {
				return embeddedbrowser.DownloadConsentDecision{}, err
			}
		}
		destination, err := downloads.PrepareDestination(filepath.Join(cfg.Workspace, filepath.Base(request.URL)), false)
		return embeddedbrowser.DownloadConsentDecision{Prepared: destination}, err
	}
}

func macPreviewDownloadShutdownChoice() int {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return 0
	}
	switch cfg.DownloadQuit {
	case "wait":
		return 1
	case "cancel":
		return 2
	case "back":
		return 3
	default:
		return 0
	}
}

func macPreviewRecordDownloadShutdown(expected, actual int) error {
	if expected == 0 {
		return nil
	}
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil || actual != expected {
		return fmt.Errorf("fixture native exit button: got %d, expected %d (%v)", actual, expected, err)
	}
	return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-quit-decision"), []byte(cfg.Nonce), 0600)
}

func (cfg *macPreviewScenario) recordDownloadQuitReady() error {
	return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-quit-ready"), []byte(cfg.Nonce), 0600)
}

func previewScript(source string, cfg *macPreviewScenario) string {
	data, _ := json.Marshal(struct {
		Nonce     string `json:"nonce"`
		URL       string `json:"url"`
		Workspace string `json:"workspace"`
	}{cfg.Nonce, cfg.URL, cfg.Workspace})
	return strings.Replace(source, "__MAC_PREVIEW_CONFIG__", string(data), 1)
}
func configureEmbeddedPreviewAssets(surface *desktopbridge.Surface) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return
	}
	surface.Assets = func(bridge *desktopbridge.Bridge) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				bridge.Assets(w, r)
				return
			}
			response := httptest.NewRecorder()
			bridge.Assets(response, r)
			for k, values := range response.Header() {
				for _, value := range values {
					w.Header().Add(k, value)
				}
			}
			body := strings.Replace(response.Body.String(), "<head>", "<head><script>"+previewScript(macPreviewConsumerJS, cfg)+"</script>", 1)
			w.Header().Del("Content-Length")
			w.WriteHeader(response.Code)
			_, _ = io.WriteString(w, body)
		})
	}
}

type macPreviewControlCount struct {
	Mounted int            `json:"mounted"`
	Hit     int            `json:"hit"`
	Sample  map[string]any `json:"sample,omitempty"`
}

type macPreviewBrowserSidebar struct {
	Address macPreviewControlCount `json:"address"`
	Guide   macPreviewControlCount `json:"guide"`
	Expand  macPreviewControlCount `json:"expand"`
}

type macPreviewReport struct {
	Stage, Nonce, Slot, Document, Session, Step, WorkerOrigin, ShellOrigin string
	BrowserSidebar                                                         *macPreviewBrowserSidebar `json:"browserSidebar"`
}

var macPreviewReports = make(chan macPreviewReport, 16)

func embeddedPreviewShellAssets(next http.Handler) http.Handler {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return next
	}
	// The fixture reports through the shell asset handler without importing a
	// second Wails runtime or opening a Stream from the shell authority.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/__mac-preview/report" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Host != "localhost" {
			http.Error(w, "invalid preview authority", http.StatusForbidden)
			return
		}
		var report macPreviewReport
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&report) != nil || report.Nonce != cfg.Nonce {
			http.Error(w, "invalid preview report", http.StatusBadRequest)
			return
		}
		select {
		case macPreviewReports <- report:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.Error(w, "preview report queue full", http.StatusTooManyRequests)
		}
	})
}

func installEmbeddedPreviewConsumer(app *application.App, window application.Window, service *desktopclient.EmbeddedBrowserService, visibleWindows func() int) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return
	}
	go func() {
		timeout := 100 * time.Second
		if cfg.ManualFiles != "" || cfg.ManualDownload || cfg.ManualNotification {
			timeout = 10 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		ctx = context.WithValue(ctx, application.WindowKey, window)
		result := map[string]any{"qualification": "actual-Wails/DSH-native-view-normal-consumer", "screenshotSource": "AppKit-offscreen-actual-owned-NSView-tree-plus-CEF-CALayer", "globalScreenCapture": "not-requested", "passed": false, "closeInstanceReturned": false}
		save := func(name string, value any) error {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(cfg.Evidence, name+".json"), append(data, '\n'), 0600)
		}
		fail := func(stage string, err error) {
			result["failureStage"] = stage
			result["failureCode"] = fmt.Sprint(err)
			_ = save("mac-preview-consumer", result)
			app.Quit()
		}
		if err := os.MkdirAll(cfg.Evidence, 0700); err != nil {
			return
		}
		script := previewScript(macPreviewShellJS, cfg)
		inject := time.NewTicker(250 * time.Millisecond)
		defer inject.Stop()
		window.ExecJS(script)
		await := func(stage string) (macPreviewReport, error) {
			for {
				select {
				case value := <-macPreviewReports:
					if value.Stage == "shell-ready" {
						inject.Stop()
						// The initial Show can precede AppKit key-window eligibility.
						// Focus the realized fixture window before DSH opens its sidebar.
						if !window.IsFocused() {
							window.Focus()
						}
						if err := save("mac-preview-shell-boot", map[string]string{"shellOrigin": value.ShellOrigin, "entry": "actual-Wails-window"}); err != nil {
							return value, err
						}
						if value.ShellOrigin == "" || value.ShellOrigin == "null" {
							return value, errors.New("MAC_PREVIEW_SHELL_ORIGIN_OPAQUE")
						}
					}
					if value.Stage == "failure" {
						if value.BrowserSidebar != nil {
							_ = save("mac-preview-browser-sidebar", value.BrowserSidebar)
						}
						return value, fmt.Errorf("MAC_PREVIEW_DOM_FAILED_%s", value.Step)
					}
					if value.Stage == stage {
						return value, nil
					}
				case <-inject.C:
					window.ExecJS(script)
				case <-ctx.Done():
					return macPreviewReport{}, ctx.Err()
				}
			}
		}
		requested, err := await("slot-requested")
		if err != nil {
			fail("real-DSH-slot", err)
			return
		}
		if !strings.HasPrefix(requested.Session, "session:session-") || !strings.HasPrefix(requested.Slot, strings.TrimPrefix(requested.Session, "session:")+"/") || requested.Document == "" || requested.WorkerOrigin == "" || requested.WorkerOrigin == "null" || requested.ShellOrigin == "" || requested.ShellOrigin == "null" || requested.WorkerOrigin == requested.ShellOrigin {
			fail("actual-session-origin", errors.New("MAC_PREVIEW_REAL_SESSION_REQUIRED"))
			return
		}
		result["actualSession"] = requested.Session
		result["slot"] = requested.Slot
		result["workerOrigin"] = requested.WorkerOrigin
		result["shellOrigin"] = requested.ShellOrigin
		var state embeddedbrowser.ViewState
		waitFor := func(ctx context.Context, check func() bool) error {
			for {
				if check() {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		wait := func(check func() bool) error { return waitFor(ctx, check) }
		if cfg.FailNavigation {
			failureCtx, stopFailure := context.WithTimeout(ctx, 12*time.Second)
			err = waitFor(failureCtx, func() bool {
				var stateErr error
				state, stateErr = service.State(failureCtx, requested.Document, requested.Slot)
				return stateErr == nil && state.State == "navigationFailed" &&
					state.Failure == "CEF_NAVIGATION_FAILED" && state.URL == cfg.URL && state.Frame == ""
			})
			stopFailure()
			if err != nil {
				fail("network-failure-state", fmt.Errorf("%s error=%s frame=%t: %w", state.State, state.Failure, state.Frame != "", err))
				return
			}
			if err = save("mac-preview-navigation-failed", state); err != nil {
				fail("network-failure-evidence", err)
				return
			}
			if err = os.WriteFile(filepath.Join(cfg.Evidence, "navigation-retry"), []byte(cfg.Nonce), 0600); err == nil {
				err = service.Action(ctx, requested.Document, requested.Slot, "reload")
			}
			if err != nil {
				fail("network-failure-reload", err)
				return
			}
		}
		var lastSlotObservation string
		err = wait(func() bool {
			var stateErr error
			state, stateErr = service.State(ctx, requested.Document, requested.Slot)
			observation := fmt.Sprintf("%s visible=%t frame=%t failure=%s error=%v", state.State, state.Visible, state.Frame != "", state.Failure, stateErr)
			if observation != lastSlotObservation {
				lastSlotObservation = observation
				_ = save("mac-preview-slot", map[string]any{"state": state, "error": fmt.Sprint(stateErr)})
			}
			return stateErr == nil && state.State == "ready" && state.Visible && state.URL == cfg.URL && state.Document != "" && state.Frame != "" && state.Lease != ""
		})
		if err != nil {
			fail("native-slot-ready", fmt.Errorf("%w: %s", err, lastSlotObservation))
			return
		}
		before := state
		result["before"] = before
		if cfg.FailNavigation {
			result["navigationRetryPassed"] = before.Failure == ""
		}
		var native map[string]any
		err = wait(func() bool {
			var e error
			native, e = desktopclient.MacPreviewObserve(window, before.Lease)
			paint, _ := native["paintSequence"].(float64)
			return e == nil && native["visible"] == true && native["nativeHitTestIsCEF"] == true && paint > 0
		})
		if err != nil {
			fail("actual-native-view", err)
			return
		}
		type pageState struct {
			Nonce, Input, Prompt       string
			PopupLoads, ChooserCancels int
			PopupClicks                int
			PopupTargetAtPoint         string
			PopupClickTrusted          bool
			PopupClickActive           bool
			PromptStarts               int
			PromptDown, PromptUp       int
			PromptTargetAtPoint        string
			Clicks, Loads, Tick        int
			Composition                []string
			ScrollY                    float64
			FixtureFileRead            bool
			Focused                    bool
			IndicatorEvents            []string
		}
		httpClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer httpClient.CloseIdleConnections()
		readPage := func() (pageState, error) {
			var value pageState
			request, e := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"report", nil)
			if e != nil {
				return value, e
			}
			response, e := httpClient.Do(request)
			if e != nil {
				return value, e
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				return value, errors.New("MAC_PREVIEW_PAGE_OBSERVATION_FAILED")
			}
			e = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&value)
			return value, e
		}
		if cfg.FailNavigation {
			if err = wait(func() bool {
				page, err := readPage()
				return err == nil && page.Nonce == cfg.Nonce && page.Tick > 0
			}); err != nil {
				fail("network-retry-page-report", err)
				return
			}
			if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-visible.png")); err != nil {
				fail("network-retry-render", err)
				return
			}
			cleanup, stop := context.WithTimeout(ctx, 15*time.Second)
			err = service.Client.Call(cleanup, "EmbeddedBrowserService", "CloseInstance", window.Name(), nil, nil)
			stop()
			if err != nil {
				fail("network-retry-close", err)
				return
			}
			result["closeInstanceReturned"] = true
			result["passed"] = true
			if err = save("mac-preview-consumer", result); err != nil {
				fail("network-retry-evidence", err)
				return
			}
			app.Quit()
			return
		}
		// The initial show can run before the native window is eligible to
		// become key. Focus this owned window once it has a visible native view.
		if native["keyWindow"] != true {
			window.Focus()
		}
		focusCtx, stopFocus := context.WithTimeout(ctx, 5*time.Second)
		focusErr := waitFor(focusCtx, func() bool {
			if observation, e := desktopclient.MacPreviewObserve(window, before.Lease); e == nil {
				native = observation
				frame, _ := native["frame"].(map[string]any)
				width, _ := frame["width"].(float64)
				height, _ := frame["height"].(float64)
				// The first painted view can still be clipped by the opening
				// sidebar. Wait until the fixture's input targets fit.
				return native["keyWindow"] == true && native["visible"] == true && native["nativeHitTestIsCEF"] == true &&
					width >= 280 && height >= 200
			}
			return false
		})
		stopFocus()
		native["pid"] = os.Getpid()
		started, e := localipc.ProcessStarted(uint32(os.Getpid()))
		if e != nil {
			fail("actual-native-process-instance", e)
			return
		}
		native["started"] = started
		if err = save("mac-preview-native-before", native); err != nil {
			fail("native-before-evidence", err)
			return
		}
		if focusErr != nil {
			fail("actual-native-input-ready", fmt.Errorf("key=%v visible=%v hit=%v frame=%v: %w", native["keyWindow"], native["visible"], native["nativeHitTestIsCEF"], native["frame"], focusErr))
			return
		}
		if err = desktopclient.MacPreviewInteract(window, before.Lease); err != nil {
			fail("actual-NSEvent-input", err)
			return
		}
		var changed pageState
		err = wait(func() bool {
			var e error
			changed, e = readPage()
			return e == nil && changed.Nonce == cfg.Nonce && changed.Focused && changed.Input == "mac-ui-dirty中" && changed.Clicks == 1 && changed.Loads == 1 && changed.ScrollY > 0 &&
				slices.Contains(changed.Composition, "compositionupdate:zhong") && slices.Contains(changed.Composition, "compositionend:中")
		})
		if err != nil {
			fail("input-page-readback", fmt.Errorf("%w: input=%q clicks=%d loads=%d scroll=%v composition=%v", err, changed.Input, changed.Clicks, changed.Loads, changed.ScrollY, changed.Composition))
			return
		}
		result["afterAppKitInput"] = changed
		originalPaint := native["paintSequence"].(float64)
		err = wait(func() bool {
			value, e := desktopclient.MacPreviewObserve(window, before.Lease)
			if e == nil && value["visible"] == true && value["paintSequence"].(float64) > originalPaint {
				native = value
				return true
			}
			return false
		})
		if err != nil {
			fail("physical-input-new-paint", err)
			return
		}
		if err = save("mac-preview-native-after-input", native); err != nil {
			fail("native-input-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-visible.png")); err != nil {
			fail("AppKit-visible-render", err)
			return
		}
		if cfg.DockReopen || cfg.IndicatorDependencies != "" {
			if cfg.DockReopen {
				counts, err := verifyMacPreviewWorkspaceRestore(ctx, service, window, requested, before, visibleWindows)
				if err != nil {
					fail("native-dock-reopen", err)
					return
				}
				if err := desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-dock-reopened.png")); err != nil {
					fail("render-after-dock-reopen", err)
					return
				}
				result["dockReopenPassed"] = true
				result["trayActionsPassed"] = true
				result["trayMenuTrackingPassed"] = true
				result["notificationDefaultResponsePassed"] = true
				result["dockDaemonWindows"] = counts
				if cfg.ManualNotification {
					err = verifyMacPreviewManualNotification(ctx, cfg, service, window, requested, before, visibleWindows)
					if err != nil {
						fail("native-notification-center", err)
						return
					}
					result["manualNotificationPassed"] = true
				}
			} else if err = verifyMacPreviewIndicator(ctx, cfg, service, window, requested, before.Lease); err != nil {
				fail("agent-indicator-native-paint", err)
				return
			} else if err = wait(func() bool {
				value, e := readPage()
				return e == nil && value.Nonce == cfg.Nonce && slices.Equal(value.IndicatorEvents,
					slices.Repeat([]string{"mousedown:true", "mouseup:true", "click:true"}, macPreviewIndicatorClicks))
			}); err != nil {
				fail("agent-indicator-input-order", err)
				return
			} else {
				result["agentIndicatorPassed"] = true
				result["agentIndicatorClicks"] = macPreviewIndicatorClicks
			}
			cleanup, stop := context.WithTimeout(ctx, 15*time.Second)
			err = service.Client.Call(cleanup, "EmbeddedBrowserService", "CloseInstance", window.Name(), nil, nil)
			stop()
			if err != nil {
				fail("fixture-instance-close", err)
				return
			}
			result["closeInstanceReturned"] = true
			result["passed"] = true
			if err = save("mac-preview-consumer", result); err != nil {
				fail("evidence-write", err)
				return
			}
			app.Quit()
			return
		}
		if cfg.Download {
			downloadTimeout := 25 * time.Second
			if cfg.ManualDownload {
				downloadTimeout = 4 * time.Minute
			}
			downloadCtx, stopDownload := context.WithTimeout(ctx, downloadTimeout)
			err = verifyMacPreviewDownloads(downloadCtx, cfg, service, window, requested, before.Lease)
			stopDownload()
			if err != nil {
				fail("download-network-and-cancel", err)
				return
			}
			result["downloadsPassed"] = true
			if cfg.DownloadQuit == "wait" || cfg.DownloadQuit == "cancel" {
				result["passed"] = true
				if err := save("mac-preview-consumer", result); err != nil {
					fail("download-quit-evidence", err)
					return
				}
				if err := cfg.recordDownloadQuitReady(); err != nil {
					fail("download-quit-evidence", err)
				}
				return // The requested quit or stop closes this UI.
			}
			if cfg.CrashDownload {
				if err = wait(func() bool {
					state, e = service.State(ctx, requested.Document, requested.Slot)
					return e == nil && state.State == "NeedsReload"
				}); err != nil {
					fail("crashed-slot-reload-admission", err)
					return
				}
				recovered, recoveryErr := service.Recover(ctx, requested.Document, requested.Slot, "")
				if recoveryErr != nil || recovered.Instance != before.Instance ||
					recovered.BrokerGeneration == "" || recovered.BrokerGeneration == before.BrokerGeneration ||
					recovered.Lease == "" || recovered.Lease == before.Lease {
					fail("crashed-slot-recover", fmt.Errorf("fresh broker and lease for the same browser instance required: %v", recoveryErr))
					return
				}
				bounds := embeddedbrowser.SlotBounds{
					X: float64(before.Bounds.X), Y: float64(before.Bounds.Y),
					Width: float64(before.Bounds.Width), Height: float64(before.Bounds.Height),
				}
				if _, err = service.Update(ctx, requested.Document, requested.Slot, "", bounds, true); err != nil {
					fail("crashed-slot-present", err)
					return
				}
				if err = wait(func() bool {
					state, e = service.State(ctx, requested.Document, requested.Slot)
					if e != nil || state.Lease != recovered.Lease || state.State != "ready" || !state.Visible || state.URL != cfg.URL {
						return false
					}
					native, nativeErr := desktopclient.MacPreviewObserve(window, recovered.Lease)
					paint, _ := native["paintSequence"].(float64)
					return nativeErr == nil && native["visible"] == true && paint > 0
				}); err != nil {
					fail("crashed-slot-repaint", err)
					return
				}
				result["crashedSlotReopened"] = true
			}
			cleanup, stop := context.WithTimeout(ctx, 15*time.Second)
			err = service.Client.Call(cleanup, "EmbeddedBrowserService", "CloseInstance", window.Name(), nil, nil)
			stop()
			if err != nil && (!cfg.CrashDownload || !strings.HasPrefix(err.Error(), "CEF_CLEANUP_COMPLETED_ABNORMALLY:")) {
				fail("download-instance-close", err)
				return
			}
			result["closeInstanceReturned"] = err == nil
			result["passed"] = true
			_ = save("mac-preview-consumer", result)
			app.Quit()
			return
		}
		command := func(value string) {
			data, _ := json.Marshal(map[string]string{"nonce": cfg.Nonce, "command": value})
			window.ExecJS("globalThis.__MAC_PREVIEW_COMMAND__?.(" + string(data) + ")")
		}
		finishConsumer := func() {
			// CloseInstance completes while Cocoa still pumps, before UI exit.
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			err := service.Client.Call(cleanup, "EmbeddedBrowserService", "CloseInstance", window.Name(), nil, nil)
			stop()
			if err != nil {
				fail("normal-instance-close", err)
				return
			}
			result["closeInstanceReturned"] = true
			result["passed"] = true
			if err = save("mac-preview-consumer", result); err != nil {
				fail("evidence-write", err)
				return
			}
			app.Quit()
		}
		command("open-workspace-picker")
		folderCtx, stopFolder := context.WithTimeout(ctx, 12*time.Second)
		err = waitFor(folderCtx, func() bool { return desktopclient.MacPreviewCancelWorkspacePicker(window) })
		if err == nil {
			err = waitFor(folderCtx, func() bool { return !desktopclient.MacPreviewFilePickerVisible(window) })
		}
		stopFolder()
		if err != nil {
			fail("workspace-directory-picker", err)
			return
		}
		result["workspaceDirectoryPickerCanceled"] = true
		command("open-shell-overlay")
		if _, err = await("shell-overlay-opened"); err != nil {
			fail("shell-overlay-hide-ack", err)
			return
		}
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != false {
			fail("shell-overlay-native-hide", fmt.Errorf("expected hidden native view: %v", err))
			return
		}
		command("open-provider-overlay")
		if _, err = await("provider-overlay-opened"); err != nil {
			fail("provider-overlay-ack", err)
			return
		}
		command("close-shell-overlay")
		if _, err = await("shell-overlay-still-blocked"); err != nil {
			fail("shell-overlay-partial-release-ack", err)
			return
		}
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != false {
			fail("shell-overlay-partial-release", fmt.Errorf("remaining overlay did not keep the native view hidden: %v", err))
			return
		}
		if err = desktopclient.MacPreviewClick(window, before.Lease, 200, 20); err == nil {
			fail("shell-overlay-input-admitted", errors.New("AppKit delivered input while the provider overlay was active"))
			return
		}
		result["overlappingOverlaysBlockedInput"] = true
		command("close-provider-overlay")
		if _, err = await("shell-overlay-closed"); err != nil {
			fail("shell-overlay-resume-ack", err)
			return
		}
		err = wait(func() bool {
			var stateErr error
			state, stateErr = service.State(ctx, requested.Document, requested.Slot)
			if stateErr != nil || !state.Visible || state.State != "ready" || state.Lease != before.Lease || state.Document != before.Document || state.Target != before.Target || state.Epoch == before.Epoch {
				return false
			}
			native, stateErr = desktopclient.MacPreviewObserve(window, before.Lease)
			return stateErr == nil && native["visible"] == true
		})
		if err != nil {
			fail("shell-overlay-fresh-layout", err)
			return
		}
		result["shellOverlayRestored"] = true
		before = state
		var unfocused pageState
		err = wait(func() bool {
			var pageErr error
			unfocused, pageErr = readPage()
			return pageErr == nil && unfocused.Nonce == cfg.Nonce && unfocused.Tick > changed.Tick && !unfocused.Focused && native["firstResponderIsCEF"] == false
		})
		if err != nil {
			fail("shell-overlay-focus-preserved", err)
			return
		}
		if err = desktopclient.MacPreviewClick(window, before.Lease, 200, 20); err != nil {
			fail("shell-overlay-focus-click", err)
			return
		}
		err = wait(func() bool {
			value, pageErr := readPage()
			return pageErr == nil && value.Nonce == cfg.Nonce && value.Tick > unfocused.Tick && value.Focused
		})
		if err != nil {
			fail("shell-overlay-user-focus", err)
			return
		}
		result["focusLifecyclePassed"] = true
		command("send-stale-layout")
		if _, err = await("stale-layout-rejected"); err != nil {
			fail("shell-stale-layout", err)
			return
		}
		state, err = service.State(ctx, requested.Document, requested.Slot)
		if err != nil || !state.Visible || state.Epoch != before.Epoch || state.Bounds != before.Bounds {
			fail("shell-stale-layout-state", fmt.Errorf("stale input changed the restored view: %v", err))
			return
		}
		result["staleLayoutRejected"] = true
		// Drive Wails' backing-property listener on the owned fixture window.
		// The physical display stays the same, so the CSS slot must not move.
		window.HandleWindowEvent(uint(events.Mac.WindowDidChangeBackingProperties))
		err = wait(func() bool {
			var stateErr error
			state, stateErr = service.State(ctx, requested.Document, requested.Slot)
			if stateErr != nil || !state.Visible || state.State != "ready" || state.Lease != before.Lease || state.Document != before.Document || state.Bounds != before.Bounds || state.Epoch == before.Epoch {
				return false
			}
			native, stateErr = desktopclient.MacPreviewObserve(window, before.Lease)
			return stateErr == nil && native["visible"] == true && native["layoutEpoch"] == state.Epoch
		})
		if err != nil {
			fail("mac-backing-properties-refresh", err)
			return
		}
		result["displayRefreshPassed"] = true
		before = state
		var backingFrames []map[string]any
		checkBacking := func(base embeddedbrowser.ViewState, growBuffer bool) error {
			backingCtx, stop := context.WithTimeout(ctx, 8*time.Second)
			defer stop()
			var last map[string]any
			err := waitFor(backingCtx, func() bool {
				current, stateErr := service.State(backingCtx, requested.Document, requested.Slot)
				state = current
				currentNative, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
				last = currentNative
				if stateErr != nil || state.State != "ready" || !state.Visible || state.Lease != before.Lease ||
					state.Target != before.Target || state.Document != before.Document || state.Epoch == base.Epoch {
					return false
				}
				if growBuffer {
					if state.Bounds.Height <= base.Bounds.Height {
						return false
					}
				} else {
					// Wails returns the native window size in whole Cocoa points.
					// A restored fractional frame can round by one point per edge.
					got, original := state.Bounds, before.Bounds
					if math.Abs(float64(got.X-original.X)) > 1 || math.Abs(float64(got.Y-original.Y)) > 1 ||
						math.Abs(float64(got.Width-original.Width)) > 1 || math.Abs(float64(got.Height-original.Height)) > 1 {
						return false
					}
				}
				scale, _ := last["scale"].(float64)
				frameBytes, _ := last["frameBytes"].(float64)
				previousBytes, _ := native["frameBytes"].(float64)
				return observeErr == nil && scale >= 1 && last["layerScale"] == scale && last["layoutEpoch"] == state.Epoch &&
					last["pixelWidth"] == math.Ceil(float64(state.Bounds.Width)*scale) &&
					last["pixelHeight"] == math.Ceil(float64(state.Bounds.Height)*scale) &&
					(!growBuffer || frameBytes > previousBytes) && last["nativeHitTestIsCEF"] == true
			})
			if err != nil {
				actualWidth, actualHeight := window.Size()
				return fmt.Errorf("window=%dx%d previous=%+v current=%+v native=%v: %w",
					actualWidth, actualHeight, base.Bounds, state.Bounds, last, err)
			}
			backingFrames = append(backingFrames, last)
			return nil
		}
		width, height := window.Size()
		const growBy = 120
		window.SetSize(width, height+growBy)
		err = checkBacking(before, true)
		grown := state
		window.SetSize(width, height)
		if err != nil {
			fail("mac-backing-pixels-grow", err)
			return
		}
		if err = checkBacking(grown, false); err != nil {
			fail("mac-backing-pixels-restore", err)
			return
		}
		result["backingFramesAfterWindowResize"] = backingFrames
		before = state
		previews, err := service.SetOcclusion(ctx, requested.Document, true)
		if err != nil || len(previews) != 1 {
			fail("cover-visible-view", fmt.Errorf("expected one native preview, got %d: %v", len(previews), err))
			return
		}
		preview := previews[0]
		if preview.Slot != requested.Slot || preview.Lease != before.Lease || preview.Document != before.Document || preview.Frame != before.Frame || preview.URL != before.URL {
			fail("cover-preview-identity", errors.New("MAC_PREVIEW_COVER_IDENTITY_CHANGED"))
			return
		}
		encoded, decodeErr := base64.StdEncoding.DecodeString(preview.Data)
		image, imageErr := png.DecodeConfig(bytes.NewReader(encoded))
		if decodeErr != nil || imageErr != nil || image.Width < 100 || image.Height < 100 {
			fail("cover-preview-png", errors.New("MAC_PREVIEW_COVER_PNG_INVALID"))
			return
		}
		if err = os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-cover.png"), encoded, 0600); err != nil {
			fail("cover-preview-evidence", err)
			return
		}
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != false {
			fail("cover-native-hide", errors.New("MAC_PREVIEW_COVER_VIEW_STILL_VISIBLE"))
			return
		}
		command("hide")
		if _, err = await("hidden-requested"); err != nil {
			fail("collapse-real-sidebar", err)
			return
		}
		err = wait(func() bool {
			var e error
			state, e = service.State(ctx, requested.Document, requested.Slot)
			return e == nil && !state.Visible && state.Document == before.Document && state.Target == before.Target
		})
		if err != nil {
			fail("native-hide-ACK", err)
			return
		}
		result["hidden"] = state
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != false {
			fail("actual-hidden-native-view", errors.New("MAC_PREVIEW_NATIVE_HIDE_NOT_OBSERVED"))
			return
		}
		if err = save("mac-preview-native-hidden", native); err != nil {
			fail("native-hidden-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-hidden.png")); err != nil {
			fail("AppKit-hidden-render", err)
			return
		}
		if _, err = service.SetOcclusion(ctx, requested.Document, false); err != nil {
			fail("cover-resume", err)
			return
		}
		command("show")
		if _, err = await("sidebar-reopened"); err != nil {
			fail("reopen-real-sidebar", err)
			return
		}
		err = wait(func() bool {
			var e error
			state, e = service.State(ctx, requested.Document, requested.Slot)
			return e == nil && state.State == "ready" && state.Visible && state.BrokerGeneration == before.BrokerGeneration && state.Target == before.Target && state.Frame == before.Frame && state.Document == before.Document && state.Lease == before.Lease
		})
		if err != nil {
			fail("same-document-reopen", err)
			return
		}
		result["after"] = state
		err = wait(func() bool {
			value, e := readPage()
			if e == nil && value.Nonce == cfg.Nonce && value.Input == changed.Input && value.Clicks == changed.Clicks && value.Loads == 1 && value.ScrollY > 0 && value.Tick > changed.Tick {
				result["afterReopen"] = value
				return true
			}
			return false
		})
		if err != nil {
			fail("same-live-page-readback", err)
			return
		}
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != true || native["nativeHitTestIsCEF"] != true {
			fail("actual-reopened-native-view", errors.New("MAC_PREVIEW_NATIVE_REOPEN_NOT_OBSERVED"))
			return
		}
		if err = save("mac-preview-native-reopened", native); err != nil {
			fail("native-reopen-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-reopened.png")); err != nil {
			fail("AppKit-reopened-render", err)
			return
		}
		command("switch-to-guide")
		if _, err = await("guide-tab-selected"); err != nil {
			fail("select-guide-tab", err)
			return
		}
		if err = wait(func() bool {
			native, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
			return observeErr == nil && native["visible"] == false
		}); err != nil {
			fail("inactive-browser-tab-hidden", err)
			return
		}
		command("switch-to-browser")
		if _, err = await("browser-tab-selected"); err != nil {
			fail("select-browser-tab", err)
			return
		}
		if err = wait(func() bool {
			state, stateErr := service.State(ctx, requested.Document, requested.Slot)
			native, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
			return stateErr == nil && state.Visible && state.State == "ready" &&
				state.Lease == before.Lease && state.Target == before.Target && state.Document == before.Document &&
				observeErr == nil && native["visible"] == true && native["nativeHitTestIsCEF"] == true
		}); err != nil {
			fail("active-browser-tab-restored", err)
			return
		}
		result["browserTabSwitchPassed"] = true
		if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 190); err != nil {
			fail("upload-picker-click", err)
			return
		}
		if err = wait(func() bool { return desktopclient.MacPreviewFilePickerVisible(window) }); err != nil {
			fail("upload-picker-owned-sheet", err)
			return
		}
		if err = desktopclient.MacPreviewCheckFilePickerFilter(window); err != nil {
			fail("upload-picker-file-type-filter", err)
			return
		}
		result["uploadPickerFileFilterPassed"] = true
		if cfg.RetirePicker {
			pickerClose, cancelPickerClose := context.WithTimeout(ctx, 12*time.Second)
			defer cancelPickerClose()
			if err = service.Detach(pickerClose, requested.Document, requested.Slot); err != nil {
				fail("retire-browser-with-picker", err)
				return
			}
			if err = waitFor(pickerClose, func() bool {
				_, observeErr := desktopclient.MacPreviewObserve(window, before.Lease)
				return !desktopclient.MacPreviewFilePickerVisible(window) &&
					observeErr != nil && observeErr.Error() == "MAC_FIXTURE_NATIVE_VIEW_MISSING"
			}); err != nil {
				fail("retire-picker-and-input-lease", err)
				return
			}
			if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 190); err == nil {
				fail("retire-picker-and-input-lease", errors.New("detached browser accepted input"))
				return
			}
			result["browserRetiredWithPicker"] = true
			finishConsumer()
			return
		}
		command("hide")
		if _, err = await("hidden-requested"); err != nil {
			fail("upload-picker-hide-sidebar", err)
			return
		}
		var coveredFrames float64
		if err = wait(func() bool {
			if desktopclient.MacPreviewFilePickerVisible(window) {
				return false
			}
			frame, frameErr := desktopclient.MacPreviewObserve(window, before.Lease)
			var validCount bool
			coveredFrames, validCount = frame["displayedFrames"].(float64)
			return frameErr == nil && validCount && frame["visible"] == false
		}); err != nil {
			fail("upload-picker-drain-on-hide", err)
			return
		}
		command("show")
		if _, err = await("sidebar-reopened"); err != nil {
			fail("upload-picker-reopen-sidebar", err)
			return
		}
		var reopenedFrames float64
		err = wait(func() bool {
			value, e := readPage()
			state, stateErr := service.State(ctx, requested.Document, requested.Slot)
			frame, frameErr := desktopclient.MacPreviewObserve(window, before.Lease)
			reopenedFrames, _ = frame["displayedFrames"].(float64)
			return e == nil && value.ChooserCancels == 1 && value.PromptTargetAtPoint == "prompt" &&
				stateErr == nil && state.Visible && state.Lease == before.Lease && state.Document == before.Document &&
				frameErr == nil && frame["visible"] == true && reopenedFrames > coveredFrames
		})
		if err != nil {
			fail("upload-picker-cancel-readback", err)
			return
		}
		result["uploadPickerCanceled"] = true
		result["postPickerFrame"] = map[string]float64{"coveredDisplayedFrames": coveredFrames, "reopenedDisplayedFrames": reopenedFrames}
		if cfg.ManualFiles == "both" {
			if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 190); err != nil {
				fail("manual-upload-click", err)
				return
			}
			if err = wait(func() bool { return desktopclient.MacPreviewFilePickerVisible(window) }); err != nil {
				fail("manual-upload-sheet", err)
				return
			}
			_ = save("mac-preview-manual-upload", map[string]string{"choose": filepath.Join(cfg.Workspace, "dsh-parity-upload.txt")})
			err = wait(func() bool {
				value, e := readPage()
				return e == nil && value.FixtureFileRead
			})
			if err != nil {
				fail("manual-upload-file-readback", err)
				return
			}
			result["uploadFixtureFileRead"] = true
		}
		promptCtx, stopPrompt := context.WithTimeout(ctx, 12*time.Second)
		beforePromptInput, _ := desktopclient.MacPreviewObserve(window, before.Lease)
		var clickErr error
		if err = waitFor(promptCtx, func() bool {
			clickErr = desktopclient.MacPreviewClick(window, before.Lease, 260, 70)
			if clickErr != nil {
				result["promptClickWaitReason"] = clickErr.Error()
			}
			return clickErr == nil
		}); err != nil {
			stopPrompt()
			fail("prompt-click", errors.Join(err, clickErr))
			return
		}
		afterPromptInput, _ := desktopclient.MacPreviewObserve(window, before.Lease)
		result["promptNativeInput"] = map[string]any{"before": beforePromptInput, "after": afterPromptInput}
		var promptPending bool
		var promptLastEpoch string
		err = waitFor(promptCtx, func() bool {
			var stateErr error
			state, stateErr = service.State(promptCtx, requested.Document, requested.Slot)
			if stateErr == nil {
				promptLastEpoch = state.Epoch
			}
			promptPending = promptPending || (state.Dialog != nil && state.Dialog.Kind == "prompt")
			return stateErr == nil && state.Dialog != nil && state.Dialog.Kind == "prompt" && state.DialogVisible
		})
		stopPrompt()
		if err != nil {
			page, pageErr := readPage()
			lastPromptInput, _ := desktopclient.MacPreviewObserve(window, before.Lease)
			result["promptTransportAfterTimeout"] = lastPromptInput["inputTransport"]
			result["promptLastBrokerEpoch"] = promptLastEpoch
			fail("prompt-overlay-ready", fmt.Errorf("%w: page-down=%d page-up=%d page-starts=%d page-result=%q point-target=%q native-down=%v→%v native-up=%v→%v key=%v→%v pending-seen=%t state=%s view-visible=%t readback=%v",
				err, page.PromptDown, page.PromptUp, page.PromptStarts, page.Prompt, page.PromptTargetAtPoint,
				beforePromptInput["fixtureLeftDown"], afterPromptInput["fixtureLeftDown"], beforePromptInput["fixtureLeftUp"], afterPromptInput["fixtureLeftUp"],
				beforePromptInput["keyWindow"], afterPromptInput["keyWindow"], promptPending, state.State, state.Visible, pageErr))
			return
		}
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != false || native["nativeHitTestIsCEF"] != false {
			fail("prompt-native-hit-test", errors.New("MAC_PREVIEW_DIALOG_DID_NOT_SUSPEND_VIEW"))
			return
		}
		command("answer-prompt")
		if _, err = await("prompt-answered"); err != nil {
			fail("prompt-answer-from-workbench", err)
			return
		}
		err = wait(func() bool {
			value, e := readPage()
			native, nativeErr := desktopclient.MacPreviewObserve(window, before.Lease)
			return e == nil && value.PromptStarts == 1 && value.Prompt == "已确认" && value.Loads == 1 && nativeErr == nil && native["visible"] == true
		})
		if err != nil {
			fail("prompt-response-and-native-restore", err)
			return
		}
		result["promptPassed"] = true
		result["promptStartedOnce"] = true

		for index, action := range []string{"deny-popup", "allow-popup"} {
			beforeClick, _ := desktopclient.MacPreviewObserve(window, before.Lease)
			if err = desktopclient.MacPreviewClick(window, before.Lease, 260, 130); err != nil {
				fail(action+"-native-click", err)
				return
			}
			afterClick, _ := desktopclient.MacPreviewObserve(window, before.Lease)
			popupCtx, stopPopup := context.WithTimeout(ctx, 12*time.Second)
			var popupPending bool
			err = waitFor(popupCtx, func() bool {
				observed, stateErr := service.State(popupCtx, requested.Document, requested.Slot)
				if stateErr != nil {
					return false
				}
				state = observed
				popupPending = popupPending || state.Popup != nil
				return state.PopupVisible && state.Popup != nil && state.Popup.URL == cfg.URL+"popup" && state.Popup.Origin+"/" == cfg.URL
			})
			stopPopup()
			if err != nil {
				page, pageErr := readPage()
				fail(action+"-overlay-ready", fmt.Errorf("%w: page-clicks=%d trusted=%t activation=%t point-target=%q native-down=%v→%v native-up=%v→%v key=%v→%v pending-seen=%t visible=%t epoch=%q readback=%v",
					err, page.PopupClicks, page.PopupClickTrusted, page.PopupClickActive, page.PopupTargetAtPoint,
					beforeClick["fixtureLeftDown"], afterClick["fixtureLeftDown"], beforeClick["fixtureLeftUp"], afterClick["fixtureLeftUp"],
					beforeClick["keyWindow"], afterClick["keyWindow"], popupPending, state.Visible, state.Epoch, pageErr))
				return
			}
			command(action)
			if _, err = await("popup-answered"); err != nil {
				fail(action+"-from-workbench", err)
				return
			}
			err = wait(func() bool {
				value, e := readPage()
				if e != nil || value.PopupClicks != index+1 || !value.PopupClickTrusted || !value.PopupClickActive {
					return false
				}
				if action == "allow-popup" {
					return value.PopupLoads == 1
				}
				state, stateErr := service.State(ctx, requested.Document, requested.Slot)
				native, nativeErr := desktopclient.MacPreviewObserve(window, before.Lease)
				return value.PopupLoads == 0 && stateErr == nil && state.Popup == nil && nativeErr == nil && native["visible"] == true
			})
			if err != nil {
				fail(action+"-effect", err)
				return
			}
		}
		result["popupPassed"] = true

		pickerCtx, stopPicker := context.WithCancel(ctx)
		picked := make(chan error, 1)
		go func() {
			_, e := service.OpenArtifact(pickerCtx, requested.Document, requested.Slot+"/artifact-fixture")
			picked <- e
		}()
		pickerDeadline := time.NewTimer(12 * time.Second)
		pickerPolling := time.NewTicker(100 * time.Millisecond)
		var pickerErr error
		for !desktopclient.MacPreviewFilePickerVisible(window) && pickerErr == nil {
			select {
			case pickerErr = <-picked:
				if pickerErr == nil {
					pickerErr = errors.New("MAC_PREVIEW_PICKER_COMPLETED_BEFORE_USER_ACTION")
				}
			case <-pickerDeadline.C:
				pickerErr = errors.New("MAC_PREVIEW_PICKER_SHEET_NOT_VISIBLE")
			case <-pickerPolling.C:
			}
		}
		pickerDeadline.Stop()
		pickerPolling.Stop()
		stopPicker()
		if pickerErr != nil {
			fail("artifact-picker-owned-sheet", pickerErr)
			return
		}
		if e := <-picked; e == nil {
			fail("artifact-picker-cancel", errors.New("MAC_PREVIEW_PICKER_ACCEPTED_AFTER_CANCEL"))
			return
		}
		if err = wait(func() bool { return !desktopclient.MacPreviewFilePickerVisible(window) }); err != nil {
			fail("artifact-picker-drained", err)
			return
		}
		result["artifactPickerCanceled"] = true
		if cfg.ManualFiles != "" {
			_ = save("mac-preview-manual-artifact", map[string]string{"choose": filepath.Join(cfg.Workspace, "dsh-parity-artifact.html")})
			artifactSlot := requested.Slot + "/artifact-fixture"
			artifact, e := service.OpenArtifact(ctx, requested.Document, artifactSlot)
			if e != nil || artifact.ArtifactName != "dsh-parity-artifact.html" || artifact.Lease == "" {
				fail("manual-artifact-file-open", fmt.Errorf("artifact lease=%t selected=%t: %v", artifact.Lease != "", artifact.ArtifactName == "dsh-parity-artifact.html", e))
				return
			}
			result["artifactFixtureSelected"] = true
			if _, err = service.Update(ctx, requested.Document, requested.Slot, "", embeddedbrowser.SlotBounds{}, false); err != nil {
				fail("manual-artifact-hide-web-view", err)
				return
			}
			bounds := embeddedbrowser.SlotBounds{X: 96, Y: 96, Width: 400, Height: 280}
			lastView, err := service.Update(ctx, requested.Document, artifactSlot, "", bounds, true)
			if err != nil {
				fail("manual-artifact-present", err)
				return
			}
			loadCtx, finishLoad := context.WithTimeout(ctx, 15*time.Second)
			tick := time.NewTicker(100 * time.Millisecond)
			var lastPaint float64
			for {
				value, e := service.State(loadCtx, requested.Document, artifactSlot)
				native, nativeErr := desktopclient.MacPreviewObserve(window, artifact.Lease)
				paint, _ := native["paintSequence"].(float64)
				if e == nil {
					lastView = value
				}
				if nativeErr == nil {
					lastPaint = paint
				}
				if e == nil && value.Lease == artifact.Lease && value.State == "ready" && value.Target != "" &&
					value.Title == "DSH Work fixture artifact" && value.Visible &&
					nativeErr == nil && native["visible"] == true && paint > 0 {
					break
				}
				select {
				case <-loadCtx.Done():
					failure := fmt.Errorf("last artifact state=%s title=%q target=%t visible=%t paint=%g failure=%q: %w",
						lastView.State, lastView.Title, lastView.Target != "", lastView.Visible, lastPaint, lastView.Failure, errors.Join(loadCtx.Err(), e, nativeErr))
					tick.Stop()
					finishLoad()
					fail("manual-artifact-document-ready", failure)
					return
				case <-tick.C:
				}
			}
			tick.Stop()
			finishLoad()
			result["artifactFixtureOpened"] = true
		}
		finishConsumer()
	}()
}

func verifyMacPreviewManualNotification(ctx context.Context, cfg *macPreviewScenario, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, original embeddedbrowser.ViewState, visibleWindows func() int) error {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	mark := func(stage string) error {
		return os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-notification-"+stage), []byte(stage+"\n"), 0600)
	}
	if err := mark("permission"); err != nil {
		return err
	}
	if err := service.Client.Call(ctx, "SettingsService", "SetNotificationPreference", window.Name(), []any{string(notifications.PreferenceEnabled), true}, nil); err != nil {
		return err
	}
	window.Close()
	if err := waitMacPreviewWindowVisibility(ctx, window, false); err != nil {
		return err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var counts macPreviewWindowCounts
		if err := service.Client.Call(ctx, "MacPreviewWorkspace", "Observe", window.Name(), []any{"notification-manual"}, &counts); err != nil {
			return err
		}
		if counts.NotificationPosted {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for notification permission and approval category: %w", ctx.Err())
		}
	}
	if err := mark("posted"); err != nil {
		return err
	}
	for {
		var response struct{ Action string }
		data, err := os.ReadFile(filepath.Join(cfg.Evidence, "mac-preview-notification-response.json"))
		if err == nil && json.Unmarshal(data, &response) == nil {
			if response.Action != wailsnotifications.DefaultActionIdentifier {
				return fmt.Errorf("notification response was %q; click the notification body", response.Action)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for a Notification Center response: %w", ctx.Err())
		}
	}
	if err := waitMacPreviewWindowVisibility(ctx, window, true); err != nil {
		return err
	}
	for {
		state, stateErr := service.State(ctx, requested.Document, requested.Slot)
		frame, frameErr := desktopclient.MacPreviewObserve(window, original.Lease)
		if visibleWindows() == 1 && stateErr == nil && frameErr == nil && state.Visible && frame["visible"] == true &&
			state.Lease == original.Lease && state.Document == original.Document && state.BrokerGeneration == original.BrokerGeneration {
			return nil
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("waiting for the original workbench and browser: %w", ctx.Err())
		}
	}
}

func verifyMacPreviewWorkspaceRestore(ctx context.Context, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, original embeddedbrowser.ViewState, visibleWindows func() int) (macPreviewWindowCounts, error) {
	observeDaemon := func(ctx context.Context, action string) (macPreviewWindowCounts, error) {
		var counts macPreviewWindowCounts
		err := service.Client.Call(ctx, "MacPreviewWorkspace", "Observe", window.Name(), []any{action}, &counts)
		return counts, err
	}
	var daemonBefore macPreviewWindowCounts
	for _, action := range []string{"workbench-dock", "dock", "tray-button", "tray-menu", "notification-default"} {
		check, stop := context.WithTimeout(ctx, 12*time.Second)
		err := func() error {
			window.Close()
			if err := waitMacPreviewWindowVisibility(check, window, false); err != nil {
				return err
			}
			if visibleWindows() != 0 {
				return errors.New("hidden workbench still counted by the window ledger")
			}
			if action == "workbench-dock" {
				if err := desktopclient.MacPreviewApplicationReopen(); err != nil {
					return err
				}
			} else {
				if action == "tray-menu" {
					counts, err := observeDaemon(check, "tray-menu-open")
					if err != nil {
						return fmt.Errorf("native context menu opening and dismissal: %w", err)
					}
					if window.IsVisible() || visibleWindows() != 0 || counts.Total == 0 || counts.Visible != 0 {
						return fmt.Errorf("right-clicking the tray menu opened an app window: daemon=%+v", counts)
					}
				}
				counts, err := observeDaemon(check, action)
				if err != nil {
					return err
				}
				if counts.Total == 0 || counts.Visible != 0 {
					return fmt.Errorf("expected hidden daemon windows, found %+v", counts)
				}
				daemonBefore = counts
			}
			if err := waitMacPreviewWindowVisibility(check, window, true); err != nil {
				return err
			}
			if visibleWindows() != 1 {
				return errors.New("window ledger did not record the restored workbench")
			}
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				state, stateErr := service.State(check, requested.Document, requested.Slot)
				frame, frameErr := desktopclient.MacPreviewObserve(window, original.Lease)
				if stateErr == nil && frameErr == nil && state.Visible && frame["visible"] == true &&
					state.Lease == original.Lease && state.Document == original.Document && state.BrokerGeneration == original.BrokerGeneration {
					return nil
				}
				select {
				case <-ticker.C:
				case <-check.Done():
					return fmt.Errorf("same browser document did not return: %w", check.Err())
				}
			}
		}()
		stop()
		if err != nil {
			return macPreviewWindowCounts{}, fmt.Errorf("%s reopen: %w", action, err)
		}
	}
	after, err := observeDaemon(ctx, "")
	if err != nil {
		return macPreviewWindowCounts{}, err
	}
	if after != daemonBefore {
		return macPreviewWindowCounts{}, fmt.Errorf("Dock reopen changed daemon window visibility: before=%+v after=%+v", daemonBefore, after)
	}
	return after, nil
}

func verifyMacPreviewIndicator(ctx context.Context, cfg *macPreviewScenario, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, lease string) error {
	ctx, cancel := context.WithTimeout(ctx, 48*time.Second)
	defer cancel()
	admission, err := service.RegisterWindow(ctx)
	if err != nil {
		return err
	}
	parent, err := desktopclient.MacPreviewParent(window)
	if err != nil {
		return err
	}
	baseline, err := macPreviewIndicatorPixels(filepath.Join(cfg.Evidence, "mac-preview-visible.png"))
	if err != nil {
		return err
	}
	click := make(chan error, 1)
	go func() {
		click <- service.Client.Call(ctx, "MacPreviewIndicator", "Click", window.Name(), []any{parent, admission.Lease, requested.Slot, lease}, nil)
	}()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	sample := filepath.Join(cfg.Evidence, "mac-preview-indicator.png")
	painted := false
	for {
		select {
		case err := <-click:
			if err != nil {
				return err
			}
			if !painted {
				return errors.New("indicator pixels were not observed in the owned AppKit view")
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !painted {
				if err := desktopclient.MacPreviewRender(window, lease, sample); err != nil {
					return err
				}
				pixels, err := macPreviewIndicatorPixels(sample)
				if err != nil {
					return err
				}
				painted = pixels >= baseline+12
			}
		}
	}
}

func macPreviewIndicatorPixels(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	pixels, err := png.Decode(file)
	if err != nil {
		return 0, err
	}
	count := 0
	bounds := pixels.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X + bounds.Dx()/2; x < bounds.Max.X; x++ {
			r, g, b, _ := pixels.At(x, y).RGBA()
			if r < 110*257 && g > 140*257 && g > r+55*257 && b > g+28*257 {
				count++
			}
		}
	}
	return count, nil
}

func verifyMacPreviewDownloads(ctx context.Context, cfg *macPreviewScenario, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, lease string) error {
	waitFor := func(filename string, condition func(embeddedbrowser.DownloadView) bool) (embeddedbrowser.DownloadView, error) {
		var last []embeddedbrowser.DownloadView
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			state, err := service.State(ctx, requested.Document, requested.Slot)
			if err != nil {
				return embeddedbrowser.DownloadView{}, err
			}
			last = state.Downloads
			for _, download := range last {
				if download.Filename == filename && condition(download) {
					return download, nil
				}
			}
			select {
			case <-ctx.Done():
				return embeddedbrowser.DownloadView{}, fmt.Errorf("%s last state %+v: %w", filename, last, ctx.Err())
			case <-ticker.C:
			}
		}
	}
	if cfg.DownloadQuit == "" && !cfg.DownloadWindowClose {
		const complete = "complete.txt"
		target := filepath.Join(cfg.Workspace, complete)
		if cfg.ManualOverwrite {
			if err := desktopclient.MacPreviewPinDownloadDestination(window, target); err != nil {
				return err
			}
		}
		if err := desktopclient.MacPreviewClick(window, lease, 260, 250); err != nil {
			return fmt.Errorf("click complete download: %w", err)
		}
		if cfg.NativeDownload && !cfg.ManualDownload {
			return cancelMacPreviewSave(ctx, window)
		}
		if cfg.ManualDownload {
			if err := waitMacPreviewSave(ctx, window, true); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-save-target.txt"), []byte(cfg.Workspace+"\n"), 0600); err != nil {
				return err
			}
			if cfg.ManualOverwrite {
				if err := waitMacPreviewDownloadConfirmation(ctx, window); err != nil {
					return err
				}
				data, err := os.ReadFile(target)
				if err != nil || string(data) != "previous preview download\n" {
					return fmt.Errorf("existing fixture file changed before confirmation: %v", err)
				}
				if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-overwrite-confirmation"), nil, 0600); err != nil {
					return err
				}
			}
		}
		if cfg.ContentRisk != "" {
			if _, err := waitFor(complete, func(d embeddedbrowser.DownloadView) bool { return d.State == "finalizing" }); err != nil {
				return err
			}
			if err := respondMacPreviewContentRisk(ctx, window, target, cfg.ContentRisk == "accept"); err != nil {
				return err
			}
			if cfg.ContentRisk == "decline" {
				if _, err := waitFor(complete, func(d embeddedbrowser.DownloadView) bool { return d.State == "failed" }); err != nil {
					return err
				}
				if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("destination published after declining content risk: %v", err)
				}
				stages, err := filepath.Glob(filepath.Join(cfg.Workspace, ".dsh-download-*"))
				if err != nil || len(stages) != 0 {
					return fmt.Errorf("private download stage survived declining content risk: %v (%v)", stages, err)
				}
				return nil
			}
		}
		finished, err := waitFor(complete, func(d embeddedbrowser.DownloadView) bool { return d.State == "complete" })
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filepath.Join(cfg.Workspace, complete))
		expected := cfg.Nonce
		if cfg.ContentRisk == "accept" {
			expected = "\x89PNG\r\n\x1a\n" + expected
		}
		if err != nil || string(data) != expected || finished.Received != int64(len(data)) {
			return errors.Join(err, errors.New("MAC_PREVIEW_DOWNLOAD_BYTES_MISMATCH"))
		}
		if cfg.ManualDownload || cfg.ContentRisk != "" {
			return nil
		}
	}
	if err := desktopclient.MacPreviewClick(window, lease, 260, 310); err != nil {
		return fmt.Errorf("click interrupted download: %w", err)
	}
	const canceled = "cancel.txt"
	if cfg.InvalidateDownload || cfg.ResizeDownload {
		if err := waitMacPreviewDownloadMarker(ctx, cfg.Evidence, "download-consent-started"); err != nil {
			return err
		}
	}
	if cfg.InvalidateDownload {
		if err := service.Hide(ctx); err != nil {
			return err
		}
		if err := waitMacPreviewDownloadMarker(ctx, cfg.Evidence, "download-consent-canceled"); err != nil {
			return err
		}
		if _, err := os.Lstat(filepath.Join(cfg.Workspace, canceled)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("invalidated download created a destination: %v", err)
		}
		return nil
	}
	if cfg.ResizeDownload {
		before, err := service.State(ctx, requested.Document, requested.Slot)
		if err != nil {
			return err
		}
		bounds := embeddedbrowser.SlotBounds{
			X: float64(before.Bounds.X), Y: float64(before.Bounds.Y),
			Width: float64(before.Bounds.Width), Height: float64(before.Bounds.Height - 24),
		}
		after, err := service.Update(ctx, requested.Document, requested.Slot, "", bounds, true)
		if err != nil || !after.Visible || before.Lease != after.Lease || before.Document != after.Document ||
			before.Epoch == after.Epoch || before.Bounds == after.Bounds {
			return fmt.Errorf("same-view native resize was not acknowledged: %v", err)
		}
		native, err := desktopclient.MacPreviewObserve(window, lease)
		if err != nil || native["layoutPreviousEpoch"] != before.Epoch || native["layoutEpoch"] != after.Epoch {
			return fmt.Errorf("input epoch changed before the AppKit layout: previous=%v applied=%v want %s → %s: %v",
				native["layoutPreviousEpoch"], native["layoutEpoch"], before.Epoch, after.Epoch, err)
		}
		if err := os.WriteFile(filepath.Join(cfg.Evidence, "download-resize-complete"), nil, 0600); err != nil {
			return err
		}
	}
	active, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "transferring" && d.Received > 0 })
	if err != nil {
		return err
	}
	if cfg.CrashDownload {
		return verifyMacDownloadCrashCleanup(ctx, cfg, canceled)
	}
	if cfg.DownloadWindowClose {
		before, err := service.State(ctx, requested.Document, requested.Slot)
		if err != nil || before.Document == "" {
			return fmt.Errorf("active workbench download has no browser document: %v", err)
		}
		if err := cfg.recordDownloadQuitReady(); err != nil {
			return err
		}
		if err := waitMacPreviewWindowVisibility(ctx, window, false); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-window-hidden"), []byte(cfg.Nonce), 0600); err != nil {
			return err
		}
		if _, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "complete" }); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-window-complete"), []byte(cfg.Nonce), 0600); err != nil {
			return err
		}
		if err := waitMacPreviewWindowVisibility(ctx, window, true); err != nil {
			return err
		}
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			native, err := desktopclient.MacPreviewObserve(window, lease)
			if err == nil && native["visible"] == true {
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("browser did not become visible after reopening the workbench: %w", ctx.Err())
			case <-ticker.C:
			}
		}
		after, err := service.State(ctx, requested.Document, requested.Slot)
		if err != nil || after.Instance != before.Instance || after.BrokerGeneration != before.BrokerGeneration ||
			after.Lease != before.Lease || after.Document != before.Document {
			return fmt.Errorf("workbench close changed the active browser document: %v", err)
		}
		return nil
	}
	if cfg.DownloadQuit != "" {
		if cfg.DownloadQuit != "back" {
			return nil // The parent drives quit while this exact transfer is active.
		}
		if err := cfg.recordDownloadQuitReady(); err != nil {
			return err
		}
		_, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "complete" })
		return err
	}
	if err := service.CancelDownload(ctx, requested.Document, requested.Slot, active.ID); err != nil {
		return err
	}
	if _, err := waitFor(canceled, func(d embeddedbrowser.DownloadView) bool { return d.State == "canceled" }); err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(cfg.Workspace, canceled)); !errors.Is(err, os.ErrNotExist) {
		return errors.New("MAC_PREVIEW_CANCELED_DOWNLOAD_DESTINATION_EXISTS")
	}
	return nil
}

func waitMacPreviewWindowVisibility(ctx context.Context, window application.Window, visible bool) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for window.IsVisible() != visible {
		select {
		case <-ctx.Done():
			return fmt.Errorf("workbench did not reach visibility %t: %w", visible, ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}

func respondMacPreviewContentRisk(ctx context.Context, window application.Window, target string, accept bool) error {
	if err := waitMacPreviewDownloadConfirmation(ctx, window); err != nil {
		return err
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("destination already exists while confirmation is open: %v", err)
	}
	if !desktopclient.MacPreviewRespondDownloadConfirmation(window, accept) {
		return errors.New("MAC_PREVIEW_DOWNLOAD_CONFIRMATION_DISMISSED")
	}
	return nil
}

func waitMacPreviewDownloadConfirmation(ctx context.Context, window application.Window) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !desktopclient.MacPreviewDownloadConfirmationVisible(window) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("native download confirmation did not appear: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}

func waitMacPreviewDownloadMarker(ctx context.Context, evidence, marker string) error {
	for {
		if _, err := os.Stat(filepath.Join(evidence, marker)); !errors.Is(err, os.ErrNotExist) {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("missing %s: %w", marker, ctx.Err())
		case <-time.After(40 * time.Millisecond):
		}
	}
}

func verifyMacDownloadCrashCleanup(ctx context.Context, cfg *macPreviewScenario, filename string) error {
	stages, err := filepath.Glob(filepath.Join(cfg.Workspace, ".dsh-download-*", "partial"))
	if err != nil || len(stages) != 1 {
		return errors.Join(err, errors.New("MAC_PREVIEW_DOWNLOAD_STAGE_REQUIRED"))
	}
	info, err := os.Stat(stages[0])
	if err != nil || info.Size() == 0 {
		return errors.Join(err, errors.New("MAC_PREVIEW_DOWNLOAD_STAGE_EMPTY"))
	}
	if err := os.WriteFile(filepath.Join(cfg.Evidence, "mac-preview-download-crash-ready"), []byte(cfg.Nonce), 0600); err != nil {
		return err
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	stage := filepath.Dir(stages[0])
	for {
		if _, err := os.Lstat(stage); errors.Is(err, os.ErrNotExist) {
			if _, err := os.Lstat(filepath.Join(cfg.Workspace, filename)); !errors.Is(err, os.ErrNotExist) {
				return errors.New("MAC_PREVIEW_CRASHED_DOWNLOAD_DESTINATION_EXISTS")
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("native stage survived broker termination: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

func cancelMacPreviewSave(ctx context.Context, window application.Window) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for !desktopclient.MacPreviewCancelDownloadSave(window) {
		select {
		case <-ctx.Done():
			return fmt.Errorf("native download Save sheet did not accept cancellation: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return waitMacPreviewSave(ctx, window, false)
}

func waitMacPreviewSave(ctx context.Context, window application.Window, visible bool) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for desktopclient.MacPreviewDownloadSaveVisible(window) != visible {
		select {
		case <-ctx.Done():
			return fmt.Errorf("native download Save sheet did not update visibility: %w", ctx.Err())
		case <-ticker.C:
		}
	}
	return nil
}
