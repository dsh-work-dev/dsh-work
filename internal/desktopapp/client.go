package desktopapp

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/accountcallback"
	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/desktopprobe"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func webviewPermissions(surface string) map[application.PermissionType]application.Permission {
	microphone := application.PermissionDeny
	if surface == "workspace" {
		// The interactive surface uses DSH Voice Input. Wails maps this to the
		// platform permission contract (including macOS TCC).
		microphone = application.PermissionAllow
	}
	return map[application.PermissionType]application.Permission{
		application.PermissionMicrophone:    microphone,
		application.PermissionCamera:        application.PermissionDeny,
		application.PermissionGeolocation:   application.PermissionDeny,
		application.PermissionNotifications: application.PermissionDeny,
		application.PermissionClipboardRead: application.PermissionDeny,
	}
}

func runDesktopClient(identity string, resources Resources) error {
	section := ""
	if len(os.Args) > 2 && os.Args[1] == "--ui" {
		section = os.Args[2]
	}
	existing := daemon.NewClient(identity + "-ui")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	err := existing.JSON(ctx, "/open", section, nil)
	cancel()
	existing.Close()
	if err == nil {
		return nil
	}
	listener, err := daemon.Listen(identity + "-ui")
	if err != nil {
		return err
	}
	defer listener.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	client, state, err := ensureDaemon(ctx, identity)
	cancel()
	if err != nil {
		return err
	}
	defer client.Close()
	manager := remoteManagerSnapshot{client: client}
	var desktop *application.App
	var workspace, settingsWindow application.Window
	var windowMu sync.Mutex
	var mu sync.Mutex
	var protocolMu sync.Mutex
	applicationStarted, protocolOpenPending := false, false
	var applicationShuttingDown atomic.Bool
	current := state
	standardHTTP := os.Getenv("DSH_WORK_STANDARD_HTTP") == "1"
	// One frameless workbench window hosts the trusted shell document; DSH runs
	// in a frame on its own authority (ADR-0024).
	var workerSurface *desktopbridge.Surface
	workerSurface = &desktopbridge.Surface{Host: shellWorkerHost, StandardHTTP: standardHTTP, Window: func() application.Window {
		windowMu.Lock()
		defer windowMu.Unlock()
		return workspace
	}, Current: func() *desktopbridge.Bridge {
		mu.Lock()
		snapshot := current
		mu.Unlock()
		if snapshot.URL == "" {
			return nil
		}
		return &desktopbridge.Bridge{Client: &http.Client{Transport: uiWorkerTransport{base: client.HTTP.Transport, generation: snapshot.Status.GenerationID}}, Origin: daemon.Origin, Generation: snapshot.Status.GenerationID, OpenExternal: func(value string) error { return desktop.Browser.OpenURL(value) }, StandardHTTP: standardHTTP,
			ReportBoot: func(ctx context.Context, generation, detail string) error {
				ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				return client.JSON(ctx, "/web-boot", struct {
					Generation string
					Detail     string
				}{generation, detail}, nil)
			},
		}
	}}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" && os.Getenv("DSH_WORK_UI_HOLD") != "1" {
		workerSurface.Assets = func(b *desktopbridge.Bridge) http.Handler { return desktopprobe.Assets(b) }
	}
	var open func(string)
	shellService := &desktopclient.ShellService{Open: func(section string) { open(section) }, Pet: func(ctx context.Context, visible *bool) (app.PetPanel, error) {
		var panel app.PetPanel
		if visible == nil {
			return panel, client.Call(ctx, "PetSettingsService", "GetPetPanel", "settings", nil, &panel)
		}
		return panel, client.Call(ctx, "PetSettingsService", "SetPetVisibility", "settings", []any{*visible}, &panel)
	}, SaveZoomLevel: func(ctx context.Context, zoom float64) error {
		return client.JSON(ctx, "/zoom", zoom, nil)
	}}
	desktop = application.New(application.Options{Name: "dsh-work", Icon: resources.AppIcon,
		Windows: application.WindowsOptions{WebviewUserDataPath: filepath.Join(state.Root, "webview"), DisableQuitOnLastWindowClosed: true, AdditionalBrowserArgs: webviewDebugArgs()},
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Services: []application.Service{
			application.NewService(&desktopclient.HostService{Client: client}), application.NewService(&desktopclient.ManagerService{Client: client}),
			application.NewService(&desktopclient.SettingsService{Client: client}), application.NewService(&desktopclient.StorageService{Client: client}), application.NewService(&desktopclient.PetSettingsService{Client: client}),
			application.NewService(shellService),
		}, Assets: application.AssetOptions{Middleware: workerSurface.Middleware, Handler: shellState(func() string {
			mu.Lock()
			defer mu.Unlock()
			return shellWorkerURL(current.URL)
		}, uiAssets(client, application.AssetFileServerFS(resources.Assets)))},
	})
	desktop.HandleStream("worker-fetch", workerSurface.Fetch)
	desktop.HandleStream("worker-websocket", workerSurface.WebSocket)
	ledger := lifecycle.NewWindowLedger("workspace", "settings")
	remember := func(window application.Window, options application.WebviewWindowOptions) {
		store := remoteGeometry{client: client, values: state.Preferences}
		flush := nativeui.RememberWindowGeometry(window, options, store)
		desktop.OnShutdown(flush)
	}
	closeWindow := func(name string, window application.Window, event *application.WindowEvent) {
		windowMu.Lock()
		defer windowMu.Unlock()
		if name == "workspace" && (workspace == nil || window.ID() != workspace.ID()) {
			return
		}
		if name == "settings" && (settingsWindow == nil || window.ID() != settingsWindow.ID()) {
			return
		}
		if !applicationShuttingDown.Load() {
			event.Cancel()
		}
		ledger.SetVisible(name, false)
		if applicationShuttingDown.Load() {
			return
		}
		// Keep the native window and WebView alive while hidden.
		window.Hide()
	}
	newOptions := func(name string) application.WebviewWindowOptions {
		options := application.WebviewWindowOptions{Name: name, Title: "dsh-work", Width: 1180, Height: 760, MinWidth: 720, MinHeight: 480, URL: "/", InitialPosition: application.WindowCentered, Hidden: true, BackgroundColour: application.NewRGB(31, 37, 44)}
		options.Permissions = webviewPermissions(name)
		mu.Lock()
		options.Windows.Theme = hostWindowTheme(current.Preferences.Appearance.Mode)
		mu.Unlock()
		if name == "settings" {
			options.Width, options.Height = 980, 720
			options.MinWidth = 680
			mu.Lock()
			options.Title = nativeui.LabelsFor(current.Preferences.Locale).Settings
			mu.Unlock()
		}
		return nativeui.RestoreWindowGeometry(options, remoteGeometry{client: client, values: state.Preferences})
	}
	createWorkspace := func() {
		if workspace != nil {
			return
		}
		workOptions := newOptions("workspace")
		workOptions.Frameless = true
		workOptions.URL = "/?shell=1"
		// Composition hosting moves hit-testing to the native window: edge
		// resizing and the caption/caption-button regions (Snap layouts) no
		// longer depend on which document is under the pointer. Without it,
		// Wails detects edges in the top document, which the DSH frame covers.
		workOptions.Windows.WebView2CompositionHosting = true
		workOptions.Zoom = state.Preferences.WorkspaceZoom
		workspaceWindow := desktop.Window.NewWithOptions(workOptions)
		workspace = workspaceWindow
		remember(workspace, workOptions)
		if report := os.Getenv("DSH_WORK_DESKTOP_REPORT"); report != "" && os.Getenv("DSH_WORK_UI_HOLD") != "1" {
			desktopprobe.InstallClient(desktop, workspace, report)
		}
		workspace.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) { closeWindow("workspace", workspaceWindow, event) })
	}
	open = func(section string) {
		if section == "__boot" {
			return
		}
		if applicationShuttingDown.Load() || ledger.IsQuitting() {
			return
		}
		if maintenance.InstallerInProgress() {
			maintenance.ShowInstallerBusy()
			return
		}
		windowMu.Lock()
		defer windowMu.Unlock()
		if applicationShuttingDown.Load() || ledger.IsQuitting() {
			return
		}
		if section != "" {
			if settingsWindow == nil {
				options := newOptions("settings")
				options.URL = settingsURL(manager, section)
				newWindow := desktop.Window.NewWithOptions(options)
				settingsWindow = newWindow
				window := settingsWindow
				remember(window, options)
				window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) { closeWindow("settings", window, event) })
			} else {
				desktop.Event.Emit("settings-section", section)
			}
			if !ledger.TryShow("settings") {
				return
			}
			settingsWindow.Show().Focus()
			return
		}
		if workspace == nil {
			createWorkspace()
		}
		if !ledger.TryShow("workspace") {
			return
		}
		workspace.Show().Focus()
	}
	desktop.Event.OnApplicationEvent(events.Common.ApplicationLaunchedWithUrl, func(event *application.ApplicationEvent) {
		if !isWorkspaceOpenURL(event.Context().URL()) {
			return
		}
		protocolMu.Lock()
		started := applicationStarted
		if !started {
			protocolOpenPending = true
		}
		protocolMu.Unlock()
		if started {
			open("")
		}
	})
	ipcServer := daemon.HTTPServer(uiHandler(open, func(section string) {
		windowMu.Lock()
		window := workspace
		if section == "settings" {
			window = settingsWindow
		}
		windowMu.Unlock()
		if window != nil {
			window.Close()
		}
	}, desktop.Quit))
	defer ipcServer.Close()
	pollCtx, stopPoll := context.WithCancel(context.Background())
	defer stopPoll()
	desktop.OnShutdown(func() {
		applicationShuttingDown.Store(true)
		ledger.BeginQuit()
		stopPoll()
	})
	desktop.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		protocolMu.Lock()
		applicationStarted = true
		openRequestedByProtocol := protocolOpenPending
		protocolOpenPending = false
		protocolMu.Unlock()
		// Settings-only launches also need the hidden workbench, whose DSH frame completes boot.
		windowMu.Lock()
		createWorkspace()
		windowMu.Unlock()
		open(section)
		if openRequestedByProtocol {
			open("")
		}
		go func() { _ = ipcServer.Serve(listener) }()
		go func() {
			cursor := state.Cursor
			lastURL := state.URL
			lastStatus, _ := json.Marshal(state.Status)
			lastUpdate := state.Update
			desktop.Event.Emit("update-state", state.Update)
			ticker := time.NewTicker(350 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-pollCtx.Done():
					return
				case <-ticker.C:
					var next daemon.Snapshot
					ctx, cancel := context.WithTimeout(pollCtx, 3*time.Second)
					windowMu.Lock()
					focused := workspace != nil && workspace.IsFocused()
					windowMu.Unlock()
					err := client.JSON(ctx, "/snapshot", struct {
						Cursor  uint64
						Focused bool
					}{cursor, focused}, &next)
					cancel()
					if err != nil {
						if pollCtx.Err() == nil {
							log.Printf("background disconnected: %v", err)
							desktop.Quit()
						}
						return
					}
					if next.PID != state.PID || next.Protocol != daemon.Protocol {
						desktop.Quit()
						return
					}
					mu.Lock()
					localeChanged := current.Preferences.Locale != next.Preferences.Locale
					current = next
					mu.Unlock()
					windowMu.Lock()
					if localeChanged && settingsWindow != nil {
						settingsWindow.SetTitle(nativeui.LabelsFor(next.Preferences.Locale).Settings)
					}
					windowMu.Unlock()
					cursor = next.Cursor
					for _, event := range next.Events {
						if err := replayEvent(event, func(e *application.CustomEvent) error {
							desktop.Event.EmitEvent(e)
							return nil
						}); err != nil {
							log.Printf("decode background event %s: %v", event.Name, err)
						}
					}
					statusJSON, _ := json.Marshal(next.Status)
					if string(statusJSON) != string(lastStatus) {
						desktop.Event.Emit("lifecycle", next.Status)
						lastStatus = statusJSON
					}
					if next.URL != lastURL {
						// The shell swaps its frame; the native window stays put.
						desktop.Event.Emit("worker-url", shellWorkerURL(next.URL))
						lastURL = next.URL
					}
					if next.Update != lastUpdate {
						desktop.Event.Emit("update-state", next.Update)
						lastUpdate = next.Update
					}
				}
			}
		}()
	})
	return desktop.Run()
}

func isWorkspaceOpenURL(raw string) bool {
	launched, err := url.Parse(raw)
	if err != nil || launched.User != nil || launched.Port() != "" || launched.Path != "" || launched.RawQuery != "" || launched.Fragment != "" || !strings.EqualFold(launched.Hostname(), "open") {
		return false
	}
	return strings.EqualFold(launched.Scheme, "dsh") || strings.EqualFold(launched.Scheme, accountcallback.DesktopReturnScheme)
}

// shellWorkerHost is the fixed authority of the framed Worker document. Wails
// intercepts any wails.localhost authority inside the WebView, so the port is
// an origin label only and nothing listens on it. It stays fixed so DSH's
// browser storage survives Worker generations.
const shellWorkerHost = "wails.localhost:48217"

func shellWorkerURL(relative string) string {
	if relative == "" {
		return ""
	}
	return "http://" + shellWorkerHost + relative
}

// shellState answers the shell's initial frame address; later changes arrive
// as worker-url events.
func shellState(workerURL func() string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__shell/worker" {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			URL string `json:"url"`
		}{workerURL()})
	})
}

type uiWorkerTransport struct {
	base       http.RoundTripper
	generation string
}

func (t uiWorkerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	out := r.Clone(r.Context())
	out.Header = r.Header.Clone()
	out.Header.Set("X-DSH-Path", r.URL.EscapedPath())
	out.Header.Set("X-DSH-Generation", t.generation)
	out.URL.Scheme = "http"
	out.URL.Host = "daemon.local"
	out.URL.Path = "/worker"
	out.URL.RawPath = ""
	out.Host = "daemon.local"
	return t.base.RoundTrip(out)
}
func uiAssets(client *daemon.Client, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 13 && r.URL.Path[:13] == "/__pet_media/" {
			out := r.Clone(r.Context())
			out.RequestURI = ""
			out.URL.Scheme = "http"
			out.URL.Host = "daemon.local"
			out.Header.Del("Origin")
			resp, err := client.HTTP.Do(out)
			if err != nil {
				http.Error(w, "background unavailable", 503)
				return
			}
			defer resp.Body.Close()
			for key, values := range resp.Header {
				for _, value := range values {
					w.Header().Add(key, value)
				}
			}
			w.WriteHeader(resp.StatusCode)
			_, _ = io.Copy(w, resp.Body)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type remoteGeometry struct {
	client *daemon.Client
	values settings.Values
}

type remoteManagerSnapshot struct{ client *daemon.Client }

func (s remoteManagerSnapshot) Snapshot(ctx context.Context) (value dshmanager.Snapshot, err error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err = s.client.Call(ctx, "ManagerService", "GetSnapshot", "settings", nil, &value)
	return
}

func (s remoteGeometry) Snapshot(context.Context) (settings.Values, error) { return s.values, nil }
func (s remoteGeometry) SetWindowGeometry(ctx context.Context, window string, g settings.WindowGeometry) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.client.JSON(ctx, "/geometry", struct {
		Window   string
		Geometry settings.WindowGeometry
	}{window, g}, nil)
}
