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

	"github.com/local/dsh-work/internal/acquisition"
	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/nativeui"
	dshworknotifications "github.com/local/dsh-work/internal/notifications"
	dshworkpet "github.com/local/dsh-work/internal/pet"
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

func petWindowOptions() application.WebviewWindowOptions {
	return application.WebviewWindowOptions{
		Name:        "pet",
		Title:       "dsh-work Pet",
		Width:       240,
		Height:      220,
		AlwaysOnTop: false,
		Frameless:   true,
		// The Settings size slider is the sole resize control. Keeping the
		// native border disabled prevents a second, unsaved resize path.
		DisableResize:    true,
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/?surface=pet",
		InitialPosition:  application.WindowXY,
		X:                0,
		Y:                0,
		Hidden:           true,
		// The overlay must receive hover and drag input for its explicit handle.
		IgnoreMouseEvents: false,
		Mac: application.MacWindow{
			Backdrop: application.MacBackdropTransparent,
		},
		Windows: application.WindowsWindow{
			HiddenOnTaskbar:                   true,
			DisableFramelessWindowDecorations: true,
		},
	}
}

func main() {
	application.RegisterEvent[lifecycle.Status]("lifecycle")
	application.RegisterEvent[acquisition.OperationStatus]("acquisition")
	application.RegisterEvent[dshworksettings.Locale]("locale")
	application.RegisterEvent[bool]("notification-failure")
	application.RegisterEvent[dshworkapp.PetOverlayState]("pet-state")

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
	petActivity := dshactivity.New(filepath.Join(filepath.Dir(config.SettingsPath), "pet-activity-bridge"))
	defer petActivity.Close()
	dsh.SetLaunchPatch(petActivity.Prepare)
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
		DSHCatalog:       platform.NewDSHReleaseCatalog(runtimeStore),
		NodeCatalog:      platform.NewNodeReleaseCatalog(runtimeStore),
		NodeInstaller:    platform.NewNodeInstaller(runtimeStore),
		NodeResolver:     platform.NewNodeResolver(runtimeStore),
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
			Node:      dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem},
			Profile:   dshmanager.ProfileRef{DataDirectoryID: "dsh-work", Name: "web"},
		},
	})
	if managerErr != nil {
		log.Printf("dsh-work manager state unavailable: %v", managerErr)
		manager = nil
	}
	gateway := workergateway.New()
	gateway.SetWorkerObserver(petActivity.Start)
	var automaticRuntimeRollback atomic.Bool
	automaticRuntimeRollback.Store(true)
	host := dshworkapp.NewHost(dshworkapp.Dependencies{
		DSH:                      dsh,
		Manager:                  manager,
		ManagerError:             managerErr,
		Supervisor:               dependencies.Supervisor,
		Gateway:                  gateway,
		PlatformError:            dependencies.Err,
		AutomaticRuntimeRollback: automaticRuntimeRollback.Load,
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
		automaticRuntimeRollback.Store(values.AutomaticRuntimeRollback)
	}
	var petCatalog dshworkpet.PetCatalog
	petCatalogRoot := filepath.Join(filepath.Dir(config.SettingsPath), "pet-cache")
	if catalog, err := dshworkpet.NewPetCatalog(dshworkpet.CatalogConfig{CacheRoot: petCatalogRoot}); err != nil {
		log.Printf("dsh-work Pet catalog unavailable: %v", err)
	} else {
		petCatalog = catalog
	}
	var activeLocale atomic.Value
	activeLocale.Store(string(localePreference))
	windowLedger := lifecycle.NewWindowLedger(closeToTray, "workspace", "settings")
	var workspaceWindow application.Window
	var settingsWindow application.Window
	var petWindow application.Window
	var settingsWindowMu sync.Mutex
	var petWindowMu sync.Mutex
	var windowActionsMu sync.Mutex
	var petMenuUpdateMu sync.Mutex
	petMenuRefreshRequests := make(chan struct{}, 1)
	petMenuActionRequests := make(chan bool, 1)
	petMenuRefreshStop := make(chan struct{})
	var petMenuRefreshWG sync.WaitGroup
	var stopPetMenuRefresh func()
	var stopPetMenuRefreshOnce sync.Once
	var petMenuActionBusy atomic.Bool
	var showWorkspace func()
	var openSettings func(string)
	var refreshPetMenu func(context.Context)
	var refreshLifecycleMenu func()
	var quitFlow lifecycle.QuitFlow
	var applicationShuttingDown atomic.Bool
	var restartActionBusy atomic.Bool
	var petWindowCloseAllowed atomic.Bool
	var petWindowRepositioning atomic.Bool
	var petWindowPositionRestored atomic.Bool
	nativeNotification := wailsnotifications.New()
	nativeNotificationHost := nativeui.NewNotificationService(nativeNotification)
	notificationRouter := dshworknotifications.NewRouter(
		nativeui.NewNotificationDelivery(nativeNotification, nativeNotificationHost),
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
	}, func(section string) {
		if openSettings != nil {
			openSettings(section)
		}
	})
	var desktop *application.App
	managerService := dshworkapp.NewManagerServiceWithRuntimeProgress(manager, host, func(status acquisition.OperationStatus) {
		if desktop != nil {
			desktop.Event.Emit("acquisition", status)
		}
	})
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
		automaticRuntimeRollback.Store,
	)
	petSettingsService := dshworkapp.NewPetSettingsService(settingsManager, petCatalog)
	dshworkapp.SetPetActivity(petSettingsService, petActivity, func() {
		if showWorkspace != nil {
			showWorkspace()
		}
	})
	dshworkapp.SetPetRendererFactory(petSettingsService, func() dshworkpet.PetRenderer {
		return dshworkpet.NewRasterRenderer()
	})
	petOverlayCapabilities := dshworkpet.CurrentOverlayCapabilities()
	dshworkapp.SetPetOverlayCapabilities(petSettingsService, petOverlayCapabilities)
	nativeTheme := dshWindowTheme(manager)

	desktop = application.New(application.Options{
		Name:        "dsh-work",
		Description: "A local desktop shell for DSH workspaces.",
		Icon:        dshWorkAppIcon,
		Services: []application.Service{
			application.NewService(hostService),
			application.NewService(managerService),
			application.NewService(settingsService),
			application.NewService(petSettingsService),
			application.NewService(nativeNotificationHost),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	var repositionPetWindow func()
	var persistPetWindowPosition func()
	if petOverlayCapabilities.Level != dshworkpet.OverlayFallback {
		petWindow = desktop.Window.NewWithOptions(petWindowOptions())
		petWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
			if petWindowCloseAllowed.Load() || applicationShuttingDown.Load() {
				return
			}
			event.Cancel()
			petWindow.Hide()
		})
	}
	repositionPetWindow = func() {
		petWindowMu.Lock()
		window := petWindow
		petWindowMu.Unlock()
		if window == nil || settingsManager == nil || applicationShuttingDown.Load() {
			return
		}
		values, err := settingsManager.Snapshot(context.Background())
		if err != nil {
			return
		}
		position := values.Pet.Position
		screen, screenErr := window.GetScreen()
		if screen == nil && screenErr == nil {
			// Wails has not created the native window yet; SetPosition would
			// be a no-op. Show retries restoration after native creation.
			return
		}
		if desktop.Screen != nil && position.MonitorID != "" {
			if stored := desktop.Screen.GetByID(position.MonitorID); stored != nil {
				screen = stored
				screenErr = nil
			}
		}
		if (screenErr != nil || screen == nil) && desktop.Screen != nil {
			screen = desktop.Screen.GetPrimary()
		}
		if screen == nil {
			return
		}
		scale := float64(screen.ScaleFactor)
		windowWidth, windowHeight := petActivityWindowSize(position.Width, position.Height)
		resolved := dshworkpet.ResolvePosition(
			position.AnchorX,
			position.AnchorY,
			windowWidth,
			windowHeight,
			scale,
			dshworkpet.WorkArea{X: screen.WorkArea.X, Y: screen.WorkArea.Y, Width: screen.WorkArea.Width, Height: screen.WorkArea.Height},
			12,
		)
		petWindowRepositioning.Store(true)
		defer petWindowRepositioning.Store(false)
		window.SetSize(resolved.Width, resolved.Height)
		window.SetPosition(resolved.X, resolved.Y)
		// Native creation emits move/resize events at the temporary (0, 0)
		// position. Accept persistence only after restoring the saved anchor.
		petWindowPositionRestored.Store(true)
	}
	persistPetWindowPosition = func() {
		if !petWindowPositionRestored.Load() || petWindowRepositioning.Load() {
			return
		}
		petWindowMu.Lock()
		window := petWindow
		petWindowMu.Unlock()
		if window == nil || settingsManager == nil || applicationShuttingDown.Load() {
			return
		}
		screen, err := window.GetScreen()
		if err != nil || screen == nil {
			return
		}
		x, y := window.Position()
		width, height := window.Size()
		area := dshworkpet.WorkArea{X: screen.WorkArea.X, Y: screen.WorkArea.Y, Width: screen.WorkArea.Width, Height: screen.WorkArea.Height}
		anchorX, anchorY := dshworkpet.AnchorForPosition(x, y, width, height, area, 12)
		scale := float64(screen.ScaleFactor)
		if scale <= 0 {
			scale = 1
		}
		position := dshworksettings.PetPosition{
			MonitorID: screen.ID,
			AnchorX:   anchorX,
			AnchorY:   anchorY,
			Width:     width,
			Height:    height,
			Scale:     scale,
		}
		// Persist the user's sprite size; activity chrome has a fixed readable area.
		if values, err := settingsManager.Snapshot(context.Background()); err == nil {
			position.Width, position.Height = values.Pet.Position.Width, values.Pet.Position.Height
		} else {
			return
		}
		if err := dshworkapp.PersistPetPosition(context.Background(), petSettingsService, position); err != nil {
			log.Printf("dsh-work Pet position: %v", err)
		}
	}
	dshworkapp.AttachPetOverlayHooks(petSettingsService, dshworkapp.PetOverlayHooks{
		AlwaysOnTop: func(enabled bool) error {
			petWindowMu.Lock()
			defer petWindowMu.Unlock()
			if petWindow != nil {
				petWindow.SetAlwaysOnTop(enabled)
			}
			return nil
		},
		Show: func() error {
			petWindowMu.Lock()
			window := petWindow
			petWindowMu.Unlock()
			if window == nil || applicationShuttingDown.Load() {
				return nil
			}
			repositionPetWindow()
			window.Show()
			if !petWindowPositionRestored.Load() {
				repositionPetWindow()
			}
			return nil
		},
		Hide: func() error {
			petWindowMu.Lock()
			defer petWindowMu.Unlock()
			if petWindow != nil {
				petWindow.Hide()
			}
			return nil
		},
		Close: func() error {
			petWindowMu.Lock()
			defer petWindowMu.Unlock()
			if petWindow != nil {
				petWindowCloseAllowed.Store(true)
				petWindow.Close()
			}
			return nil
		},
		Resize: func(width, height int) error {
			petWindowMu.Lock()
			window := petWindow
			petWindowMu.Unlock()
			if window == nil || applicationShuttingDown.Load() {
				return errors.New("pet overlay is unavailable")
			}
			if width <= 0 || height <= 0 {
				return errors.New("pet overlay size is invalid")
			}
			windowWidth, windowHeight := petActivityWindowSize(width, height)
			window.SetSize(windowWidth, windowHeight)
			repositionPetWindow()
			return nil
		},
		StateChanged: func() {
			select {
			case petMenuRefreshRequests <- struct{}{}:
			default:
			}
		},
	})
	if petWindow != nil {
		for _, eventType := range []events.WindowEventType{events.Common.WindowDidMove, events.Common.WindowDidResize} {
			petWindow.OnWindowEvent(eventType, func(*application.WindowEvent) {
				persistPetWindowPosition()
			})
		}
	}
	handlePetDisplayChange := func(*application.WindowEvent) {
		repositionPetWindow()
		_ = dshworkapp.PublishPetEvent(petSettingsService, dshworkpet.PetInputEvent{Type: "display.changed", Source: "host"})
	}
	if petWindow != nil {
		for _, eventType := range []events.WindowEventType{events.Common.WindowDPIChanged, events.Common.WindowFullscreen, events.Common.WindowUnFullscreen} {
			petWindow.OnWindowEvent(eventType, handlePetDisplayChange)
		}
	}
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
		petWindowMu.Lock()
		if petWindow != nil {
			petWindow.Hide()
		}
		petWindowMu.Unlock()
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
	initialNative := nativeui.LabelsFor(localePreference)
	tray.SetTooltip(initialNative.TrayTooltip)
	restartSettled := func(status lifecycle.Status) bool {
		return status.State == lifecycle.StateReady || status.State == lifecycle.StateFailed || status.State == lifecycle.StateStopped
	}
	restartDSH := func() {
		if applicationShuttingDown.Load() || quitFlow.InProgress() || windowLedger.IsQuitting() || !restartActionBusy.CompareAndSwap(false, true) {
			return
		}
		if refreshLifecycleMenu != nil {
			refreshLifecycleMenu()
		}
		status := host.Restart()
		if restartSettled(status) {
			restartActionBusy.Store(false)
		}
		if refreshLifecycleMenu != nil {
			refreshLifecycleMenu()
		}
	}
	quitDSHWork := func() {
		if applicationShuttingDown.Load() || quitFlow.InProgress() || windowLedger.IsQuitting() {
			return
		}
		host.Quit()
		if refreshLifecycleMenu != nil {
			refreshLifecycleMenu()
		}
	}
	trayMenu := desktop.NewMenu()
	trayStatus := trayMenu.Add(nativeui.TrayStatus(localePreference, lifecycle.StateStarting)).SetEnabled(false)
	trayOpenWorkspace := trayMenu.Add(initialNative.OpenWorkspace).OnClick(func(*application.Context) {
		showWorkspace()
	})
	traySettings := trayMenu.Add(initialNative.Settings).OnClick(func(*application.Context) {
		openSettings("settings")
	})
	trayMenu.AddSeparator()
	trayRestartDSH := trayMenu.Add(initialNative.RestartDSH).OnClick(func(*application.Context) {
		restartDSH()
	})
	trayQuit := trayMenu.Add(initialNative.Quit).OnClick(func(*application.Context) {
		quitDSHWork()
	})
	tray.SetMenu(trayMenu).OnClick(func() {
		showWorkspace()
	})

	menu := desktop.NewMenu()
	actionsMenu := menu.AddSubmenu(initialNative.Actions)
	menuPetVisibility := actionsMenu.AddCheckbox(initialNative.ShowPet, false).SetEnabled(false)
	actionsMenu.AddSeparator()
	appMenuRestartDSH := actionsMenu.Add(initialNative.RestartDSH).OnClick(func(*application.Context) {
		restartDSH()
	})
	appMenuQuit := actionsMenu.Add(initialNative.Quit).OnClick(func(*application.Context) {
		quitDSHWork()
	})
	refreshLifecycleMenu = func() {
		enabled := !applicationShuttingDown.Load() && !quitFlow.InProgress() && !windowLedger.IsQuitting() && !restartActionBusy.Load()
		trayRestartDSH.SetEnabled(enabled)
		trayQuit.SetEnabled(enabled)
		appMenuRestartDSH.SetEnabled(enabled)
		appMenuQuit.SetEnabled(enabled)
	}
	refreshLifecycleMenu()
	quitFlow.SetStateChanged(func() {
		if refreshLifecycleMenu != nil {
			refreshLifecycleMenu()
		}
	})
	menuSettings := menu.Add(initialNative.Settings).OnClick(func(*application.Context) {
		openSettings("settings")
	})
	menuPetVisibility.OnClick(func(ctx *application.Context) {
		requested := ctx.IsChecked()
		if applicationShuttingDown.Load() || !petMenuActionBusy.CompareAndSwap(false, true) {
			return
		}
		menuPetVisibility.SetEnabled(false)
		select {
		case petMenuActionRequests <- requested:
		case <-petMenuRefreshStop:
			petMenuActionBusy.Store(false)
		}
	})
	helpMenu := menu.AddSubmenu(initialNative.Help)
	checkUpdates := helpMenu.Add(initialNative.CheckUpdates).OnClick(func(*application.Context) {
		labels := nativeui.LabelsFor(loadNativeLocale(&activeLocale))
		desktop.Dialog.Info().SetTitle(labels.UpdateTitle).SetMessage(labels.UpdateMessage).Show()
	})
	aboutDshWork := helpMenu.Add(initialNative.About).OnClick(func(*application.Context) {
		openSettings("about")
	})
	desktop.Menu.Set(menu)

	refreshPetMenu = func(ctx context.Context) {
		petMenuUpdateMu.Lock()
		defer petMenuUpdateMu.Unlock()
		if applicationShuttingDown.Load() {
			return
		}
		if petWindow == nil || petOverlayCapabilities.Level != dshworkpet.OverlayFull {
			menuPetVisibility.SetChecked(false).SetEnabled(false)
			return
		}
		panel, err := dshworkapp.GetPetPanelFromHost(ctx, petSettingsService)
		if err != nil {
			menuPetVisibility.SetChecked(false).SetEnabled(false)
			return
		}
		menuPetVisibility.SetChecked(panel.Runtime.EffectiveVisibility == dshworkpet.VisibilityVisible)
		menuPetVisibility.SetEnabled(!petMenuActionBusy.Load() && panel.Runtime.SelectionStatus == dshworkpet.SelectionReady)
	}
	refreshPetMenu(context.Background())
	menuRefreshContext, cancelMenuRefresh := context.WithCancel(context.Background())
	petMenuRefreshWG.Add(1)
	go func() {
		defer petMenuRefreshWG.Done()
		for {
			select {
			case requested := <-petMenuActionRequests:
				_, err := dshworkapp.SetPetVisibilityFromHost(menuRefreshContext, petSettingsService, requested)
				if err != nil && !applicationShuttingDown.Load() {
					log.Printf("dsh-work Pet menu visibility: %v", err)
					labels := nativeui.LabelsFor(loadNativeLocale(&activeLocale))
					desktop.Dialog.Info().SetTitle(labels.PetMenuErrorTitle).SetMessage(labels.PetMenuErrorMessage).Show()
				}
				petMenuActionBusy.Store(false)
				if refreshPetMenu != nil {
					refreshPetMenu(menuRefreshContext)
				}
			case <-petMenuRefreshRequests:
				if refreshPetMenu != nil {
					refreshPetMenu(menuRefreshContext)
				}
			case <-petMenuRefreshStop:
				return
			}
		}
	}()
	stopPetMenuRefresh = func() {
		stopPetMenuRefreshOnce.Do(func() {
			cancelMenuRefresh()
			close(petMenuRefreshStop)
			petMenuRefreshWG.Wait()
		})
	}

	updateNativeLocale := func(locale dshworksettings.Locale) {
		if !locale.Valid() {
			locale = dshworksettings.DefaultLocale
		}
		activeLocale.Store(string(locale))
		labels := nativeui.LabelsFor(locale)
		tray.SetTooltip(labels.TrayTooltip)
		trayStatus.SetLabel(nativeui.TrayStatus(locale, host.Status().State))
		trayOpenWorkspace.SetLabel(labels.OpenWorkspace)
		traySettings.SetLabel(labels.Settings)
		trayRestartDSH.SetLabel(labels.RestartDSH)
		trayQuit.SetLabel(labels.Quit)
		actionsMenu.SetLabel(labels.Actions)
		menuSettings.SetLabel(labels.Settings)
		menuPetVisibility.SetLabel(labels.ShowPet)
		appMenuRestartDSH.SetLabel(labels.RestartDSH)
		appMenuQuit.SetLabel(labels.Quit)
		helpMenu.SetLabel(labels.Help)
		checkUpdates.SetLabel(labels.CheckUpdates)
		aboutDshWork.SetLabel(labels.About)
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
	if petWindow != nil {
		for _, eventType := range []events.WindowEventType{events.Common.WindowDPIChanged, events.Common.WindowFullscreen, events.Common.WindowUnFullscreen} {
			workspaceWindow.OnWindowEvent(eventType, handlePetDisplayChange)
		}
	}
	windowLedger.SetVisible("workspace", true)

	host.SetPublish(func(status lifecycle.Status) {
		dshworkapp.PublishPetHostStatus(petSettingsService, status)
		desktop.Event.Emit("lifecycle", status)
		trayStatus.SetLabel(nativeui.TrayStatus(loadNativeLocale(&activeLocale), status.State))
		if restartActionBusy.Load() && restartSettled(status) {
			restartActionBusy.Store(false)
		}
		if refreshLifecycleMenu != nil {
			refreshLifecycleMenu()
		}
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
			copy := nativeui.FailureNotification(locale)
			notification = dshworknotifications.Event{
				ID:     "dsh-work-host-failure:" + eventID,
				Class:  dshworknotifications.ClassError,
				Title:  copy.Title,
				Body:   copy.Body,
				Target: "workspace",
			}
		default:
			copy := nativeui.LifecycleNotification(locale, status.State)
			if copy.Title == "" || copy.Body == "" {
				return
			}
			notification = dshworknotifications.Event{
				ID:     "dsh-work-host-lifecycle:" + eventID + ":" + string(status.State),
				Class:  dshworknotifications.ClassLifecycle,
				Title:  copy.Title,
				Body:   copy.Body,
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
			func() error {
				petWindowCloseAllowed.Store(true)
				if err := dshworkapp.ShutdownPetService(context.Background(), petSettingsService); err != nil {
					return err
				}
				return host.ShutdownForApp()
			},
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
		if stopPetMenuRefresh != nil {
			stopPetMenuRefresh()
		}
		windowLedger.BeginQuit()
		hideDshWorkWindows()
		petWindowCloseAllowed.Store(true)
		if err := dshworkapp.ShutdownPetService(context.Background(), petSettingsService); err != nil {
			log.Printf("dsh-work Pet shutdown: %v", err)
		}
		if err := host.ShutdownForApp(); err != nil {
			log.Printf("dsh-work host shutdown: %v", err)
		}
	})
	desktop.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if nativeNotificationHost.Unavailable() {
			desktop.Event.Emit("notification-failure", true)
		}
		dshworkapp.StartupPetService(petSettingsService)
		host.Start()
	})

	if err := desktop.Run(); err != nil {
		log.Printf("dsh-work desktop run: %v", err)
	}
	if stopPetMenuRefresh != nil {
		stopPetMenuRefresh()
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
			if section == "runtimes" {
				if selection != nil {
					for _, runtime := range snapshot.Runtimes {
						if runtime.ID == selection.RuntimeID {
							values.Set("version", runtime.Version)
							break
						}
					}
				}
				if values.Get("version") == "" && len(snapshot.Runtimes) > 0 {
					values.Set("version", snapshot.Runtimes[0].Version)
				}
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
