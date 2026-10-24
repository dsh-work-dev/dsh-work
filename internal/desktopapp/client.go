package desktopapp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/storagepaths"
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
	if err := logging.Attach(); err != nil {
		logging.Module(logging.ModApp).Warn("log files unavailable", logging.ErrAttrs(err, "")...)
	}
	defer listener.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	client, state, err := ensureDaemon(ctx, identity)
	cancel()
	if err != nil {
		return err
	}
	logging.SetVerbose(state.Preferences.VerboseLogging)
	paths, err := storagepaths.DefaultDirectories()
	if err != nil {
		return fmt.Errorf("resolve dsh-work cache directory: %w", err)
	}
	webviewRoot := paths.CacheRoot
	if root := os.Getenv("DSH_WORK_DESKTOP_ROOT"); root != "" && os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		webviewRoot = filepath.Join(root, "cache")
	}
	webviewDataPath := filepath.Join(webviewRoot, "webview")
	defer client.Close()
	manager := remoteManagerSnapshot{client: client}
	var desktop *application.App
	var workspace application.Window
	windows := make(map[string]application.Window)
	var windowMu sync.Mutex
	var mu sync.Mutex
	var protocolMu sync.Mutex
	applicationStarted, protocolOpenPending := false, false
	var applicationShuttingDown atomic.Bool
	current := state
	// One frameless workbench window hosts the trusted shell document; DSH runs
	// in a frame on its own authority (docs/architecture.md, Windows and menus).
	var workerSurface *desktopbridge.Surface
	workerSurface = &desktopbridge.Surface{Host: shellWorkerHost, Window: func() application.Window {
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
		return &desktopbridge.Bridge{Client: &http.Client{Transport: uiWorkerTransport{base: client.HTTP.Transport, generation: snapshot.Status.GenerationID}}, Origin: daemon.Origin, Generation: snapshot.Status.GenerationID, OpenExternal: func(value string) error { return desktop.Browser.OpenURL(value) },
			ReportBoot: func(ctx context.Context, generation, detail string) error {
				ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				return client.Call(ctx, "HostService", "ReportWebBoot", "workspace", []any{generation, detail}, nil)
			},
			SaveSessionExport: func(ctx context.Context, filename string, write func(io.Writer) error) (bool, error) {
				return nativeui.SaveSessionLogArchive(desktop, filename, write)
			},
		}
	}}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" && os.Getenv("DSH_WORK_UI_HOLD") != "1" {
		workerSurface.Assets = func(b *desktopbridge.Bridge) http.Handler { return desktopprobe.Assets(b) }
	}
	var open func(string)
	commands := newCommandDispatcher(func(name string, data any) {
		if desktop != nil {
			desktop.Event.Emit(name, data)
		}
	}, func(name string, err error) {
		attrs := append([]any{"command", name}, logging.ErrAttrs(err, "")...)
		logging.Module(logging.ModIPC).Warn("window command delivery failed", attrs...)
	}, func(window, command string) {
		logging.Module(logging.ModIPC).Warn("pet command queue full; dropped oldest command", "window", window, "command", command)
	})
	shellService := &desktopclient.ShellService{Open: func(section string) { open(section) }, Pet: func(ctx context.Context, visible *bool) (app.PetPanel, error) {
		window, ok := ctx.Value(application.WindowKey).(application.Window)
		if !ok || window == nil {
			return app.PetPanel{}, fmt.Errorf("trusted desktop window required")
		}
		var panel app.PetPanel
		if visible == nil {
			return panel, client.Call(ctx, "PetSettingsService", "GetPetPanel", window.Name(), nil, &panel)
		}
		return panel, client.Call(ctx, "PetSettingsService", "SetPetVisibility", window.Name(), []any{*visible}, &panel)
	}, SaveZoomLevel: func(ctx context.Context, zoom float64) error {
		window, ok := ctx.Value(application.WindowKey).(application.Window)
		if !ok || window == nil {
			return fmt.Errorf("trusted desktop window required")
		}
		return client.Call(ctx, "WindowService", "SaveZoom", window.Name(), []any{zoom}, nil)
	}, OnBuiltinComponentsChanged: func(surface string, statuses []desktopclient.BuiltinComponentStatus) {
		ready := false
		for _, component := range statuses {
			if component.ID == "pet" && component.State == "loaded" {
				ready = true
				break
			}
		}
		commands.setPetReady(surface, ready)
		windowMu.Lock()
		window := windows[surface]
		windowMu.Unlock()
		if window != nil {
			if window.EmitEvent("builtin-components", statuses) {
				logging.Module(logging.ModIPC).Warn("built-in component state delivery was cancelled")
			}
		}
	}}
	hostService := &desktopclient.HostService{Client: client, LocalUpdateState: func() daemon.UpdateSnapshot {
		mu.Lock()
		defer mu.Unlock()
		return current.Update
	}}
	desktop = application.New(application.Options{Name: "dsh-work", Icon: resources.AppIcon, Logger: logging.WailsLogger(), ErrorHandler: logging.WailsErrorHandler(),
		Windows: application.WindowsOptions{WebviewUserDataPath: webviewDataPath, DisableQuitOnLastWindowClosed: true, AdditionalBrowserArgs: webviewDebugArgs()},
		Mac:     application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: false},
		Services: []application.Service{
			application.NewService(hostService), application.NewService(&desktopclient.ManagerService{Client: client}),
			application.NewService(&desktopclient.SettingsService{Client: client}), application.NewService(&desktopclient.StorageService{Client: client}), application.NewService(&desktopclient.PetSettingsService{Client: client}),
			application.NewService(&desktopclient.DiagnosticsService{Client: client}),
			application.NewService(shellService),
			application.NewService(&desktopclient.LogService{}),
		}, Assets: application.AssetOptions{Middleware: workerSurface.Middleware, Handler: shellState(func() string {
			mu.Lock()
			defer mu.Unlock()
			return shellWorkerURL(current.URL)
		}, uiAssets(client, application.AssetFileServerFS(resources.Assets)))},
	})
	desktop.HandleStream("worker-fetch", workerSurface.Fetch)
	desktop.HandleStream("worker-websocket", workerSurface.WebSocket)
	ledger := lifecycle.NewWindowLedger("workspace")
	remember := func(window application.Window, options application.WebviewWindowOptions) {
		store := remoteGeometry{client: client, values: state.Preferences}
		flush := nativeui.RememberWindowGeometry(window, options, store)
		desktop.OnShutdown(flush)
	}
	var reportFocus func(string, bool)
	closeWindow := func(name string, window application.Window, event *application.WindowEvent) {
		windowMu.Lock()
		currentWindow := windows[name]
		if currentWindow == nil || window.ID() != currentWindow.ID() {
			windowMu.Unlock()
			return
		}
		shuttingDown := applicationShuttingDown.Load()
		if !shuttingDown {
			event.Cancel()
		}
		ledger.SetVisible(name, false)
		if shuttingDown {
			delete(windows, name)
			if workspace != nil && window.ID() == workspace.ID() {
				workspace = nil
			}
			windowMu.Unlock()
			commands.removeWindow(name)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			if err := client.Call(ctx, "WindowService", "Closed", name, nil, nil); err != nil {
				logging.Module(logging.ModIPC).Warn("window close report failed", logging.ErrAttrs(err, "")...)
			}
			cancel()
			return
		}
		windowMu.Unlock()
		reportFocus(name, false)
		// Keep the native window and WebView alive while hidden.
		window.Hide()
	}
	reportFocus = func(surface string, focused bool) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := client.Call(ctx, "WindowService", "SetFocus", surface, []any{focused}, nil); err != nil {
			attrs := append([]any{"surface", surface, "focused", focused}, logging.ErrAttrs(err, "")...)
			logging.Module(logging.ModIPC).Warn("window focus report failed", attrs...)
		}
	}
	registerWorkspaceWindow := func(name string, window application.Window) {
		// createWorkspace is called with windowMu held so the window pointer and
		// its routing entry become visible as one operation.
		windows[name] = window
		commands.addWindow(name, wailsCommandWindow{window: window})
		window.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) { go reportFocus(name, true) })
		window.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) { go reportFocus(name, false) })
		if window.IsFocused() {
			go reportFocus(name, true)
		}
	}
	newOptions := func(name string) application.WebviewWindowOptions {
		options := application.WebviewWindowOptions{Name: name, Title: "dsh-work", Width: 1180, Height: 760, MinWidth: 720, MinHeight: 480, URL: "/", InitialPosition: application.WindowCentered, Hidden: true, BackgroundColour: application.NewRGB(31, 37, 44)}
		options.Permissions = webviewPermissions(name)
		mu.Lock()
		options.Windows.Theme = hostWindowTheme(current.Preferences.Appearance.Mode)
		mu.Unlock()
		return nativeui.RestoreWindowGeometry(options, remoteGeometry{client: client, values: state.Preferences})
	}
	createWorkspace := func(section string) {
		if workspace != nil {
			return
		}
		workOptions := newOptions("workspace")
		workOptions.Frameless = true
		workOptions.URL = workspaceURL(manager, section)
		// Composition hosting moves hit-testing to the native window: edge
		// resizing and the caption/caption-button regions (Snap layouts) no
		// longer depend on which document is under the pointer. Without it,
		// Wails detects edges in the top document, which the DSH frame covers.
		workOptions.Windows.WebView2CompositionHosting = true
		workOptions.Zoom = state.Preferences.WorkspaceZoom
		workspaceWindow := desktop.Window.NewWithOptions(workOptions)
		workspace = workspaceWindow
		registerWorkspaceWindow("workspace", workspaceWindow)
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
		created := workspace == nil
		if created {
			createWorkspace(section)
		}
		if !ledger.TryShow("workspace") {
			return
		}
		workspace.Show().Focus()
		// A new workbench reads its section from the URL; a live one switches panels.
		if !created {
			if section == "" {
				desktop.Event.Emit("settings-close", struct{}{})
			} else {
				desktop.Event.Emit("settings-section", section)
			}
		}
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
		if section == "settings" {
			desktop.Event.Emit("settings-close", struct{}{})
			return
		}
		windowMu.Lock()
		window := workspace
		windowMu.Unlock()
		if window != nil {
			window.Close()
		}
	}, desktop.Quit))
	defer ipcServer.Close()
	streamCtx, stopStream := context.WithCancel(context.Background())
	defer stopStream()
	desktop.OnShutdown(func() {
		applicationShuttingDown.Store(true)
		ledger.BeginQuit()
		stopStream()
	})
	desktop.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		protocolMu.Lock()
		applicationStarted = true
		openRequestedByProtocol := protocolOpenPending
		protocolOpenPending = false
		protocolMu.Unlock()
		// Every launch creates the workbench, hidden until opened: its DSH frame
		// completes boot, and a Settings launch starts with that panel open.
		launchSection := section
		if launchSection == "__boot" {
			launchSection = ""
		}
		windowMu.Lock()
		createWorkspace(launchSection)
		windowMu.Unlock()
		open(section)
		if openRequestedByProtocol {
			open("")
		}
		go func() { _ = ipcServer.Serve(listener) }()
		go func() {
			body, err := client.Stream(streamCtx, "/stream")
			if err == nil {
				processor := &streamProcessor{
					current:   &current,
					currentMu: &mu,
					emitEvent: func(name string, data any) { desktop.Event.Emit(name, data) },
					commands:  commands,
				}
				err = consumeDaemonStream(streamCtx, body, nil, processor.handle)
			}
			if streamCtx.Err() != nil || applicationShuttingDown.Load() {
				return
			}
			logging.Module(logging.ModIPC).Warn("daemon stream ended", logging.ErrAttrs(err, "")...)
			desktop.Quit()
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
	err = s.client.Call(ctx, "ManagerService", "GetSnapshot", "workspace", nil, &value)
	return
}

func (s remoteGeometry) Snapshot(context.Context) (settings.Values, error) { return s.values, nil }
func (s remoteGeometry) SetWindowGeometry(ctx context.Context, window string, g settings.WindowGeometry) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return s.client.Call(ctx, "WindowService", "SaveGeometry", window, []any{g}, nil)
}
