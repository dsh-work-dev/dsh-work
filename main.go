package main

import (
	"context"
	"embed"
	"errors"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	dshworknotifications "github.com/local/dsh-work/internal/notifications"
	"github.com/local/dsh-work/internal/platform"
	dshworksettings "github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/workergateway"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed frontend/public/branding/dsh-work-appicon.png
var dshWorkAppIcon []byte

//go:embed frontend/public/branding/dsh-work-tray.png
var dshWorkTrayIcon []byte

//go:embed frontend/public/branding/dsh-work-tray-dark.png
var dshWorkTrayDarkIcon []byte

//go:embed frontend/public/branding/dsh-work-tray-template.png
var dshWorkTrayTemplateIcon []byte

func main() {
	application.RegisterEvent[lifecycle.Status]("lifecycle")
	application.RegisterEvent[dshworksettings.Locale]("locale")
	application.RegisterEvent[bool]("notification-failure")

	dependencies := platform.New()
	config := dshworkapp.DefaultConfig(currentDiscoveryRoot())
	managerLock, lockErr := dshworkapp.AcquireManagerProcessLock(config.SettingsPath)
	if lockErr != nil {
		var failure lifecycle.Failure
		if errors.As(lockErr, &failure) && failure.Code == lifecycle.ErrorManagerOperationBusy {
			log.Printf("dsh-work is already running: %v", lockErr)
			os.Exit(1)
		}
		log.Fatalf("dsh-work could not acquire its manager process lock: %v", lockErr)
	}
	defer func() {
		if err := managerLock.Close(); err != nil {
			log.Printf("dsh-work manager process lock release: %v", err)
		}
	}()
	dsh := dshadapter.New(dependencies.CommandExecutor, config.ExpectedDSHVersion)
	dsh.SetDiscoveryRoot(config.DiscoveryRoot)
	runtimeHint := dsh.RuntimeHint()
	var managerRunner dshmanager.CommandRunner
	if dependencies.CommandExecutor != nil {
		managerRunner = managerCommandRunner{executor: dependencies.CommandExecutor}
	}
	runtimeStore := filepath.Join(filepath.Dir(config.DSHDataDirectory), "runtimes")
	manager, managerErr := dshmanager.New(dshmanager.Config{
		CommandRunner:    managerRunner,
		PluginCommands:   dshadapter.NewPluginCommands(),
		RuntimeInstaller: platform.NewRuntimeInstaller(runtimeStore),
		RuntimeVerifier:  dsh,
		ProfileCatalog:   dsh,
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "dsh-work", Name: "dsh-work DSH data directory", Path: config.DSHDataDirectory, Ownership: dshmanager.DataDirectoryOwnershipDSHWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-" + runtimeHint.Version, Version: runtimeHint.Version, Path: runtimeHint.Path,
			Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: executableExists(runtimeHint.Path),
		}},
		DefaultRunContext: dshmanager.RunContext{
			RuntimeID: "dsh-" + runtimeHint.Version,
			Profile:   dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	})
	if managerErr != nil {
		log.Printf("dsh-work manager state unavailable: %v", managerErr)
		manager = nil
	}
	gateway := workergateway.New()
	host := dshworkapp.NewHost(dshworkapp.Dependencies{
		DSH:           dsh,
		Manager:       manager,
		ManagerError:  managerErr,
		Supervisor:    dependencies.Supervisor,
		Gateway:       gateway,
		PlatformError: dependencies.Err,
	}, config)
	var workspaceTrusted atomic.Bool
	workspaceTrusted.Store(true)
	settingsManager, settingsErr := dshworksettings.New(dshworksettings.Config{
		Path:     config.SettingsPath,
		Replacer: dependencies.FileReplacer,
	})
	closeToTray := true
	localePreference := dshworksettings.DefaultLocale
	notificationPreference := dshworknotifications.DefaultPreferences()
	if settingsErr != nil {
		log.Printf("dsh-work settings unavailable: %v", settingsErr)
	} else if values, err := settingsManager.Snapshot(context.Background()); err != nil {
		log.Printf("dsh-work settings could not be loaded: %v", err)
	} else {
		closeToTray = values.CloseToTray
		localePreference = values.Locale
		notificationPreference = values.Notifications
	}
	var activeLocale atomic.Value
	activeLocale.Store(string(localePreference))
	windowLedger := lifecycle.NewWindowLedger(closeToTray, "workspace", "settings")
	var workspaceWindow application.Window
	var settingsWindow application.Window
	var settingsWindowMu sync.Mutex
	var windowActionsMu sync.Mutex
	var showWorkspace func()
	var openSettings func(string)
	var quitFlow lifecycle.QuitFlow
	var applicationShuttingDown atomic.Bool
	nativeNotification := wailsnotifications.New()
	nativeNotificationHost := &nativeNotificationService{service: nativeNotification}
	notificationRouter := dshworknotifications.NewRouter(
		nativeNotificationDelivery{service: nativeNotification, host: nativeNotificationHost},
		func() bool {
			return workspaceWindow != nil && workspaceWindow.IsFocused()
		},
	)
	notificationRouter.SetPreferences(notificationPreference)
	hostService := dshworkapp.NewHostService(host, workspaceTrusted.Load, func() dshworksettings.Locale {
		if settingsManager == nil {
			return localePreference
		}
		values, err := settingsManager.Snapshot(context.Background())
		if err != nil || !values.Locale.Valid() {
			return localePreference
		}
		return values.Locale
	})
	managerService := dshworkapp.NewManagerService(manager, host)
	var publishLocale func(dshworksettings.Locale)
	settingsService := dshworkapp.NewSettingsService(
		settingsManager,
		windowLedger.SetCloseToTray,
		func(locale dshworksettings.Locale) {
			if publishLocale != nil {
				publishLocale(locale)
			}
		},
		notificationRouter.SetPreferences,
	)
	nativeTheme := dshWindowTheme(manager)

	desktop := application.New(application.Options{
		Name:        "dsh-work",
		Description: "A local desktop shell for DSH workspaces.",
		Icon:        dshWorkAppIcon,
		Services: []application.Service{
			application.NewService(hostService),
			application.NewService(managerService),
			application.NewService(settingsService),
			application.NewService(nativeNotificationHost),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	gateway.SetOpenExternal(desktop.Browser.OpenURL)
	notificationRouter.SetDeliveryFailureHandler(func(dshworknotifications.Event) {
		desktop.Event.Emit("notification-failure", true)
	})

	handleWindowClosing := func(name string, window application.Window, event *application.WindowEvent) {
		if windowLedger.IsQuitting() {
			return
		}
		windowActionsMu.Lock()
		decision := windowLedger.RequestClose(name)
		if decision.Action != lifecycle.WindowCloseQuit {
			window.Hide()
		}
		windowActionsMu.Unlock()
		if decision.Action == lifecycle.WindowCloseQuit {
			host.Quit()
			return
		}
		event.Cancel()
	}
	hideDshWorkWindows := func() {
		windowActionsMu.Lock()
		defer windowActionsMu.Unlock()
		windowLedger.SetVisible("workspace", false)
		if workspaceWindow != nil {
			workspaceWindow.Hide()
		}
		settingsWindowMu.Lock()
		window := settingsWindow
		settingsWindowMu.Unlock()
		windowLedger.SetVisible("settings", false)
		if window != nil {
			window.Hide()
		}
	}
	showWorkspace = func() {
		windowActionsMu.Lock()
		defer windowActionsMu.Unlock()
		if workspaceWindow == nil || !windowLedger.TryShow("workspace") {
			return
		}
		workspaceWindow.Show().Focus()
	}
	nativeNotification.OnNotificationResponse(func(result wailsnotifications.NotificationResult) {
		if result.Error != nil {
			log.Printf("desktop notification response: %v", result.Error)
			return
		}
		if result.Response.ID != "" && showWorkspace != nil {
			showWorkspace()
		}
	})

	tray := desktop.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(dshWorkTrayTemplateIcon)
	} else {
		tray.SetIcon(dshWorkTrayIcon).SetDarkModeIcon(dshWorkTrayDarkIcon)
	}
	initialNative := nativeLocaleCopyFor(localePreference)
	tray.SetTooltip(initialNative.trayTooltip)
	trayMenu := desktop.NewMenu()
	trayStatus := trayMenu.Add(nativeTrayStatus(localePreference, lifecycle.StateStarting)).SetEnabled(false)
	trayOpenWorkspace := trayMenu.Add(initialNative.openWorkspace).OnClick(func(*application.Context) {
		showWorkspace()
	})
	traySettings := trayMenu.Add(initialNative.settings).OnClick(func(*application.Context) {
		openSettings("settings")
	})
	trayMenu.AddSeparator()
	trayRestartDSH := trayMenu.Add(initialNative.restartDSH).OnClick(func(*application.Context) {
		if quitFlow.InProgress() || windowLedger.IsQuitting() {
			return
		}
		host.Restart()
	})
	trayQuit := trayMenu.Add(initialNative.quit).OnClick(func(*application.Context) {
		host.Quit()
	})
	tray.SetMenu(trayMenu).OnClick(func() {
		showWorkspace()
	})

	menu := desktop.NewMenu()
	menuSettings := menu.Add(initialNative.settings).OnClick(func(*application.Context) {
		openSettings("settings")
	})
	helpMenu := menu.AddSubmenu(initialNative.help)
	checkUpdates := helpMenu.Add(initialNative.checkUpdates).OnClick(func(*application.Context) {
		labels := nativeLocaleCopyFor(loadNativeLocale(&activeLocale))
		desktop.Dialog.Info().SetTitle(labels.updateTitle).SetMessage(labels.updateMessage).Show()
	})
	aboutDshWork := helpMenu.Add(initialNative.about).OnClick(func(*application.Context) {
		labels := nativeLocaleCopyFor(loadNativeLocale(&activeLocale))
		desktop.Dialog.Info().SetTitle(labels.aboutTitle).SetMessage(labels.aboutMessage).Show()
	})
	desktop.Menu.Set(menu)

	updateNativeLocale := func(locale dshworksettings.Locale) {
		if !locale.Valid() {
			locale = dshworksettings.DefaultLocale
		}
		activeLocale.Store(string(locale))
		labels := nativeLocaleCopyFor(locale)
		tray.SetTooltip(labels.trayTooltip)
		trayStatus.SetLabel(nativeTrayStatus(locale, host.Status().State))
		trayOpenWorkspace.SetLabel(labels.openWorkspace)
		traySettings.SetLabel(labels.settings)
		trayRestartDSH.SetLabel(labels.restartDSH)
		trayQuit.SetLabel(labels.quit)
		menuSettings.SetLabel(labels.settings)
		helpMenu.SetLabel(labels.help)
		checkUpdates.SetLabel(labels.checkUpdates)
		aboutDshWork.SetLabel(labels.about)
	}
	publishLocale = func(locale dshworksettings.Locale) {
		updateNativeLocale(locale)
		desktop.Event.Emit("locale", locale)
	}

	openSettings = func(section string) {
		windowActionsMu.Lock()
		defer windowActionsMu.Unlock()
		if !windowLedger.TryShow("settings") {
			return
		}
		settingsWindowMu.Lock()
		window := settingsWindow
		if window == nil {
			window = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
				Name:               "settings",
				Title:              "设置",
				Width:              980,
				Height:             720,
				MinWidth:           680,
				MinHeight:          480,
				BackgroundColour:   application.NewRGB(31, 37, 44),
				Windows:            application.WindowsWindow{Theme: nativeTheme},
				URL:                settingsURL(manager, section),
				InitialPosition:    application.WindowCentered,
				Hidden:             true,
				UseApplicationMenu: false,
			})
			window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
				handleWindowClosing("settings", window, event)
			})
			settingsWindow = window
		}
		settingsWindowMu.Unlock()
		window.SetURL(settingsURL(manager, section)).Show().Focus()
	}

	workspaceWindow = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:               "workspace",
		Title:              "dsh-work",
		Width:              1180,
		Height:             760,
		MinWidth:           720,
		MinHeight:          480,
		BackgroundColour:   application.NewRGB(31, 37, 44),
		Windows:            application.WindowsWindow{Theme: nativeTheme},
		URL:                "/",
		InitialPosition:    application.WindowCentered,
		UseApplicationMenu: true,
	})
	workspaceWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		handleWindowClosing("workspace", workspaceWindow, event)
	})
	windowLedger.SetVisible("workspace", true)

	host.SetPublish(func(status lifecycle.Status) {
		desktop.Event.Emit("lifecycle", status)
		trayStatus.SetLabel(nativeTrayStatus(loadNativeLocale(&activeLocale), status.State))
		if status.State == lifecycle.StateStopping && workspaceWindow != nil {
			workspaceTrusted.Store(true)
			workspaceWindow.SetURL("/")
		}
		eventID := status.CorrelationID
		if eventID == "" {
			eventID = status.GenerationID
		}
		if eventID == "" {
			return
		}
		locale := loadNativeLocale(&activeLocale)
		var notification dshworknotifications.Event
		switch status.State {
		case lifecycle.StateFailed:
			if status.Error == nil {
				return
			}
			copy := nativeNotificationCopyFor(locale)
			notification = dshworknotifications.Event{
				ID:     "dsh-work-host-failure:" + eventID,
				Class:  dshworknotifications.ClassError,
				Title:  copy.title,
				Body:   copy.body,
				Target: "workspace",
			}
		default:
			copy := nativeLifecycleNotificationCopyFor(locale, status.State)
			if copy.title == "" || copy.body == "" {
				return
			}
			notification = dshworknotifications.Event{
				ID:     "dsh-work-host-lifecycle:" + eventID + ":" + string(status.State),
				Class:  dshworknotifications.ClassLifecycle,
				Title:  copy.title,
				Body:   copy.body,
				Target: "workspace",
			}
		}
		if err := notificationRouter.Publish(context.Background(), notification); err != nil {
			log.Printf("desktop notification delivery: %v", err)
		}
	})
	host.SetReadyHandler(func(workspaceURL string) {
		workspaceTrusted.Store(false)
		workspaceWindow.SetURL(workspaceURL)
	})
	host.SetRecoveryHandler(func() {
		workspaceTrusted.Store(true)
		workspaceWindow.SetURL("/")
	})
	host.SetQuitHandler(func() {
		windowLedger.BeginQuit()
		quitFlow.Begin(
			hideDshWorkWindows,
			host.ShutdownForApp,
			func() {
				if !applicationShuttingDown.Load() {
					desktop.Quit()
				}
			},
			func(err error) {
				log.Printf("dsh-work host shutdown: %v", err)
				if applicationShuttingDown.Load() {
					return
				}
				windowLedger.CancelQuit()
				if openSettings != nil {
					openSettings("overview")
				}
			},
		)
	})
	desktop.OnShutdown(func() {
		applicationShuttingDown.Store(true)
		windowLedger.BeginQuit()
		hideDshWorkWindows()
		if err := host.ShutdownForApp(); err != nil {
			log.Printf("dsh-work host shutdown: %v", err)
		}
	})
	desktop.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if nativeNotificationHost.startupFailed.Load() {
			desktop.Event.Emit("notification-failure", true)
		}
		host.Start()
	})

	if err := desktop.Run(); err != nil {
		log.Fatal(err)
	}
}

func executableExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dshWindowTheme(manager *dshmanager.Manager) application.Theme {
	if manager == nil {
		return application.SystemDefault
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		return application.SystemDefault
	}
	switch snapshot.Theme {
	case dshmanager.ThemePreferenceLight:
		return application.Light
	case dshmanager.ThemePreferenceDark:
		return application.Dark
	default:
		return application.SystemDefault
	}
}

func loadNativeLocale(value *atomic.Value) dshworksettings.Locale {
	if value == nil {
		return dshworksettings.DefaultLocale
	}
	raw, ok := value.Load().(string)
	if !ok {
		return dshworksettings.DefaultLocale
	}
	locale := dshworksettings.Locale(raw)
	if !locale.Valid() {
		return dshworksettings.DefaultLocale
	}
	return locale
}

type managerCommandRunner struct {
	executor dshadapter.CommandExecutor
}

func (r managerCommandRunner) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshmanager.CommandResult, error) {
	result, err := r.executor.Run(ctx, executable, args, env, dir)
	return dshmanager.CommandResult{Stdout: result.Stdout, Stderr: result.Stderr}, err
}

func settingsURL(manager *dshmanager.Manager, section string) string {
	values := url.Values{}
	values.Set("surface", "settings")
	if section != "" {
		values.Set("section", section)
	}
	if manager != nil {
		if snapshot, err := manager.Snapshot(context.Background()); err == nil {
			selection := snapshot.Current
			if selection == nil {
				selection = snapshot.Configured
			}
			if selection != nil {
				values.Set("data-directory", selection.Profile.DataDirectoryID)
				values.Set("profile", selection.Profile.Name)
			}
		}
	}
	return "/?" + values.Encode()
}

func currentDiscoveryRoot() string {
	root, err := os.Getwd()
	if err != nil {
		return "."
	}
	return root
}
