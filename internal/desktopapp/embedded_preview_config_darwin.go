//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/wailsapp/wails/v3/pkg/application"
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
