package main

import (
	"context"
	"embed"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	workapp "github.com/local/work/internal/app"
	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/dshmanager"
	"github.com/local/work/internal/lifecycle"
	worknotifications "github.com/local/work/internal/notifications"
	"github.com/local/work/internal/platform"
	worksettings "github.com/local/work/internal/settings"
	"github.com/local/work/internal/workergateway"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed frontend/public/branding/work-appicon.png
var workAppIcon []byte

//go:embed frontend/public/branding/work-tray.png
var workTrayIcon []byte

//go:embed frontend/public/branding/work-tray-dark.png
var workTrayDarkIcon []byte

//go:embed frontend/public/branding/work-tray-template.png
var workTrayTemplateIcon []byte

func main() {
	application.RegisterEvent[lifecycle.Status]("lifecycle")
	application.RegisterEvent[worksettings.Locale]("locale")
	application.RegisterEvent[bool]("notification-failure")

	dependencies := platform.New()
	config := workapp.DefaultConfig(currentDiscoveryRoot())
	dsh := dshadapter.New(dependencies.CommandExecutor, config.ExpectedDSHVersion)
	dsh.SetDiscoveryRoot(config.DiscoveryRoot)
	runtimeHint := dsh.RuntimeHint()
	var managerRunner dshmanager.CommandRunner
	if dependencies.CommandExecutor != nil {
		managerRunner = managerCommandRunner{executor: dependencies.CommandExecutor}
	}
	runtimeStore := filepath.Join(filepath.Dir(config.DSHDataDirectory), "dsh-work", "runtimes")
	manager, managerErr := dshmanager.New(dshmanager.Config{
		CommandRunner:    managerRunner,
		PluginCommands:   dshadapter.NewPluginCommands(),
		RuntimeInstaller: platform.NewRuntimeInstaller(runtimeStore),
		RuntimeVerifier:  dsh,
		ProfileCatalog:   dsh,
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "work", Name: "Work DSH data directory", Path: config.DSHDataDirectory, Ownership: dshmanager.DataDirectoryOwnershipWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-" + runtimeHint.Version, Version: runtimeHint.Version, Path: runtimeHint.Path,
			Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: executableExists(runtimeHint.Path),
		}},
		DefaultTarget: dshmanager.LaunchTarget{
			RuntimeID: "dsh-" + runtimeHint.Version,
			Profile:   dshmanager.ProfileRef{DataDirectoryID: "work", Name: "web"},
		},
	})
	if managerErr != nil {
		log.Printf("Work manager state unavailable: %v", managerErr)
		manager = nil
	}
	gateway := workergateway.New()
	host := workapp.NewHost(workapp.Dependencies{
		DSH:           dsh,
		Manager:       manager,
		Supervisor:    dependencies.Supervisor,
		Gateway:       gateway,
		PlatformError: dependencies.Err,
	}, config)
	var workspaceTrusted atomic.Bool
	workspaceTrusted.Store(true)
	settingsManager, settingsErr := worksettings.New(worksettings.Config{
		Path:     config.SettingsPath,
		Replacer: dependencies.FileReplacer,
	})
	closeToTray := true
	localePreference := worksettings.DefaultLocale
	notificationPreference := worknotifications.DefaultPreferences()
	if settingsErr != nil {
		log.Printf("Work settings unavailable: %v", settingsErr)
	} else if values, err := settingsManager.Snapshot(context.Background()); err != nil {
		log.Printf("Work settings could not be loaded: %v", err)
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
	var showWorkspace func()
	var openSettings func(string)
	nativeNotification := wailsnotifications.New()
	nativeNotificationHost := &nativeNotificationService{service: nativeNotification}
	notificationRouter := worknotifications.NewRouter(
		nativeNotificationDelivery{service: nativeNotification, host: nativeNotificationHost},
		func() bool {
			return workspaceWindow != nil && workspaceWindow.IsFocused()
		},
	)
	notificationRouter.SetPreferences(notificationPreference)
	hostService := workapp.NewHostService(host, workspaceTrusted.Load, func() worksettings.Locale {
		if settingsManager == nil {
			return localePreference
		}
		values, err := settingsManager.Snapshot(context.Background())
		if err != nil || !values.Locale.Valid() {
			return localePreference
		}
		return values.Locale
	})
	managerService := workapp.NewManagerService(manager)
	var publishLocale func(worksettings.Locale)
	settingsService := workapp.NewSettingsService(
		settingsManager,
		windowLedger.SetCloseToTray,
		func(locale worksettings.Locale) {
			if publishLocale != nil {
				publishLocale(locale)
			}
		},
		notificationRouter.SetPreferences,
	)
	nativeTheme := dshWindowTheme(manager)

	desktop := application.New(application.Options{
		Name:        "Work",
		Description: "A local desktop shell for DSH workspaces.",
		Icon:        workAppIcon,
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
	notificationRouter.SetDeliveryFailureHandler(func(worknotifications.Event) {
		desktop.Event.Emit("notification-failure", true)
	})

	handleWindowClosing := func(name string, window application.Window, event *application.WindowEvent) {
		if windowLedger.IsQuitting() {
			return
		}
		decision := windowLedger.RequestClose(name)
		if decision.Action == lifecycle.WindowCloseQuit {
			host.Quit()
			return
		}
		window.Hide()
		event.Cancel()
	}
	showWorkspace = func() {
		if windowLedger.IsQuitting() || workspaceWindow == nil {
			return
		}
		windowLedger.SetVisible("workspace", true)
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
		tray.SetTemplateIcon(workTrayTemplateIcon)
	} else {
		tray.SetIcon(workTrayIcon).SetDarkModeIcon(workTrayDarkIcon)
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
	aboutWork := helpMenu.Add(initialNative.about).OnClick(func(*application.Context) {
		labels := nativeLocaleCopyFor(loadNativeLocale(&activeLocale))
		desktop.Dialog.Info().SetTitle(labels.aboutTitle).SetMessage(labels.aboutMessage).Show()
	})
	desktop.Menu.Set(menu)

	updateNativeLocale := func(locale worksettings.Locale) {
		if !locale.Valid() {
			locale = worksettings.DefaultLocale
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
		aboutWork.SetLabel(labels.about)
	}
	publishLocale = func(locale worksettings.Locale) {
		updateNativeLocale(locale)
		desktop.Event.Emit("locale", locale)
	}

	openSettings = func(section string) {
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
		if windowLedger.IsQuitting() {
			return
		}
		windowLedger.SetVisible("settings", true)
		window.SetURL(settingsURL(manager, section)).Show().Focus()
	}

	workspaceWindow = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:               "workspace",
		Title:              "Work",
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
		eventID := status.CorrelationID
		if eventID == "" {
			eventID = status.GenerationID
		}
		if eventID == "" {
			return
		}
		locale := loadNativeLocale(&activeLocale)
		var notification worknotifications.Event
		switch status.State {
		case lifecycle.StateFailed:
			if status.Error == nil {
				return
			}
			copy := nativeNotificationCopyFor(locale)
			notification = worknotifications.Event{
				ID:     "work-host-failure:" + eventID,
				Class:  worknotifications.ClassError,
				Title:  copy.title,
				Body:   copy.body,
				Target: "workspace",
			}
		default:
			copy := nativeLifecycleNotificationCopyFor(locale, status.State)
			if copy.title == "" || copy.body == "" {
				return
			}
			notification = worknotifications.Event{
				ID:     "work-host-lifecycle:" + eventID + ":" + string(status.State),
				Class:  worknotifications.ClassLifecycle,
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
		desktop.Quit()
	})
	desktop.OnShutdown(func() {
		if err := host.ShutdownForApp(); err != nil {
			log.Printf("Work host shutdown: %v", err)
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

func loadNativeLocale(value *atomic.Value) worksettings.Locale {
	if value == nil {
		return worksettings.DefaultLocale
	}
	raw, ok := value.Load().(string)
	if !ok {
		return worksettings.DefaultLocale
	}
	locale := worksettings.Locale(raw)
	if !locale.Valid() {
		return worksettings.DefaultLocale
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
			selection := snapshot.Active
			if selection == nil {
				selection = snapshot.Desired
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
