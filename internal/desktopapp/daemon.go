package desktopapp

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/local/dsh-work/internal/acquisition"
	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/desktopprobe"
	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/nativeui"
	dshworknotifications "github.com/local/dsh-work/internal/notifications"
	dshworkpet "github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/platform"
	dshworksettings "github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/storagepaths"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

func runDaemon(identity string, resources Resources) {
	dependencies := platform.New()
	config := dshworkapp.DefaultConfig(currentDiscoveryRoot())
	config.RequireWebBoot = true
	defaultRoot := filepath.Dir(config.SettingsPath)
	if root := os.Getenv("DSH_WORK_DESKTOP_ROOT"); root != "" && os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		defaultRoot = root
	}

	locatorPath := filepath.Join(filepath.Dir(defaultRoot), "dsh-work-location", "locations.json")
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		locatorPath = filepath.Join(defaultRoot, "location", "locations.json")
	}
	locationLock, err := dshworkapp.AcquireManagerProcessLock(locatorPath)
	if err != nil {
		log.Fatalf("lock storage locations: %v", err)
	}
	defer locationLock.Close()
	locations, err := storagepaths.Open(locatorPath, defaultRoot)
	if err != nil {
		log.Fatalf("read storage locations: %v", err)
	}
	// Keep the existing manager lock boundary while preparing a relocation.
	previousRoot := locations.Snapshot().Current.Root
	previousLock, err := dshworkapp.AcquireManagerProcessLock(filepath.Join(previousRoot, "settings.json"))
	if err != nil {
		log.Fatalf("lock current storage: %v", err)
	}
	if err := locations.ApplyPending(); err != nil {
		log.Printf("storage migration failed; using original location: %v", err)
	}
	_ = previousLock.Close()
	if previousRoot != locations.Snapshot().Current.Root {
		if entries, e := os.ReadDir(previousRoot); e == nil && len(entries) == 1 && entries[0].Name() == "manager.lock" {
			_ = os.Remove(filepath.Join(previousRoot, "manager.lock"))
			_ = os.Remove(previousRoot)
		}
	}
	storage := locations.Snapshot().Current
	config.SettingsPath = filepath.Join(storage.Root, "settings.json")
	config.DSHDataDirectory = filepath.Join(storage.Root, "environment")
	config.BootstrapDirectory = filepath.Join(storage.Root, "bootstrap")
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
	dsh.SetUserDataDirectory(storage.UserDataPath())
	petActivity := dshactivity.New(filepath.Join(filepath.Dir(config.SettingsPath), "pet-activity-bridge"))
	defer petActivity.Close()
	dsh.SetLaunchPatch(petActivity.Prepare)
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		dsh.SetLaunchPatch(func(generation string) (string, error) {
			patch, err := petActivity.Prepare(generation)
			if err != nil {
				return "", err
			}
			return desktopprobe.Patch(filepath.Join(storage.Root, "probe"), config.DiscoveryRoot)(patch)
		})
	}
	var managerRunner dshmanager.CommandRunner
	if dependencies.CommandExecutor != nil {
		managerRunner = managerCommandRunner{executor: dependencies.CommandExecutor}
	}
	runtimeStore := filepath.Join(filepath.Dir(config.DSHDataDirectory), "runtimes")
	managerConfig := dshmanager.Config{
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
			ID: "dsh-work", Name: "DSH Work", Path: config.DSHDataDirectory, Ownership: dshmanager.DataDirectoryOwnershipDSHWork,
		}},
	}
	desktopprobe.Configure(&managerConfig, dsh, storage.Root)
	manager, managerErr := dshmanager.New(managerConfig)
	if managerErr != nil {
		log.Printf("dsh-work manager state unavailable: %v", managerErr)
		manager = nil
	}
	channel := workerchannel.New()
	channel.SetWorkerObserver(petActivity.Start)
	var automaticRuntimeRollback atomic.Bool
	automaticRuntimeRollback.Store(true)
	host := dshworkapp.NewHost(dshworkapp.Dependencies{
		DSH:                      dsh,
		Manager:                  manager,
		ManagerError:             managerErr,
		Supervisor:               dependencies.Supervisor,
		Channel:                  channel,
		PlatformError:            dependencies.Err,
		AutomaticRuntimeRollback: automaticRuntimeRollback.Load,
	}, config)
	settingsManager, settingsErr := dshworksettings.New(dshworksettings.Config{
		Path:     config.SettingsPath,
		Replacer: dependencies.FileReplacer,
	})
	localePreference := dshworksettings.DefaultLocale
	notificationPreference := dshworknotifications.DefaultPreferences()
	if settingsErr != nil {
		log.Printf("dsh-work settings unavailable: %v", settingsErr)
	} else if values, err := settingsManager.Snapshot(context.Background()); err != nil {
		log.Printf("dsh-work settings could not be loaded: %v", err)
	} else {
		localePreference = values.Locale
		notificationPreference = values.Notifications
		automaticRuntimeRollback.Store(values.AutomaticRuntimeRollback)
	}
	var petCatalog dshworkpet.PetCatalog
	petCatalogRoot := filepath.Join(filepath.Dir(config.SettingsPath), "pet-cache")
	petCatalogConfig := dshworkpet.CatalogConfig{CacheRoot: petCatalogRoot, CommunityHome: dshworkpet.CommunityHome()}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		petCatalogConfig.CodexHome = filepath.Join(storage.Root, "probe-pets")
		petCatalogConfig.Env = func(string) string { return "" }
		petCatalogConfig.CommunityHome = ""
	}
	if catalog, err := dshworkpet.NewPetCatalog(petCatalogConfig); err != nil {
		log.Printf("dsh-work Pet catalog unavailable: %v", err)
	} else {
		petCatalog = catalog
	}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		host.SetDebug(func(message string) { log.Print(message) })
	}
	var activeLocale atomic.Value
	activeLocale.Store(string(localePreference))
	windowLedger := lifecycle.NewWindowLedger("workspace", "settings")
	var petWindow application.Window
	var petWindowMu sync.Mutex
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
	var updateMenuApply func(daemon.UpdateSnapshot)
	var updateMenuStateMu sync.RWMutex
	var updateMenuState daemon.UpdateSnapshot
	var petWindowCloseAllowed atomic.Bool
	var petWindowRepositioning atomic.Bool
	var petWindowPositionRestored atomic.Bool
	nativeNotification := wailsnotifications.New()
	nativeNotificationHost := nativeui.NewNotificationService(nativeNotification)
	backgroundFocused := func() bool { return false }
	notificationRouter := dshworknotifications.NewRouter(
		nativeui.NewNotificationDelivery(nativeNotification, nativeNotificationHost),
		func() bool {
			return backgroundFocused()
		},
	)
	notificationRouter.SetPreferences(notificationPreference)
	hostService := dshworkapp.NewHostService(host, func() dshworksettings.Locale {
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
	var publishRemote = func(string, any) {}
	managerService := dshworkapp.NewManagerServiceWithRuntimeProgress(manager, host, func(status acquisition.OperationStatus) {
		if desktop != nil {
			desktop.Event.Emit("acquisition", status)
			publishRemote("acquisition", status)
		}
	})
	dshworkapp.SetBackupFileActions(managerService,
		func() (string, error) { return nativeui.ChooseProfileBackup(desktop) },
		func(path string) error { return desktop.Env.OpenFileManager(path, false) })
	dshworkapp.SetProfileExportAction(managerService, func(name string) (string, error) { return nativeui.SaveProfileArchive(desktop, name) })
	var publishLocale func(dshworksettings.Locale)
	settingsService := dshworkapp.NewSettingsService(
		settingsManager,
		func(locale dshworksettings.Locale) {
			if publishLocale != nil {
				publishLocale(locale)
			}
		},
		notificationRouter.SetPreferences,
		automaticRuntimeRollback.Store,
	)
	petSettingsService := dshworkapp.NewPetSettingsService(settingsManager, petCatalog)
	dshworkapp.SetPetHostMaintenance(petSettingsService, maintenance.InstallerInProgress)
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
	storageService := dshworkapp.NewStorageService(locations,
		func(path string) error { return desktop.Env.OpenFileManager(path, false) },
		func(currentPath string) (string, error) { return nativeui.ChooseDirectory(desktop, currentPath) })
	server := &daemon.Server{Host: host, Channel: channel, Settings: settingsManager, Root: storage.Root, PID: os.Getpid(), Services: map[string]any{
		"HostService": hostService, "ManagerService": managerService, "SettingsService": settingsService, "StorageService": storageService, "PetSettingsService": petSettingsService,
	}, Maintenance: maintenance.InstallerInProgress}
	publishRemote = server.Publish
	backgroundFocused = server.UIFocused
	launcher := newUILauncher(identity)
	server.OpenUI = launcher.Open
	server.Media = dshworkapp.PetMediaHandler(petSettingsService, http.NotFoundHandler())
	listener, err := daemon.Listen(identity)
	if err != nil {
		log.Fatalf("listen background: %v", err)
	}
	ipcServer := daemon.HTTPServer(server)
	defer ipcServer.Close()
	defer listener.Close()
	desktop = application.New(application.Options{
		Name:        "dsh-work",
		Description: "A local desktop shell for DSH workspaces.",
		Icon:        resources.AppIcon,
		Windows: application.WindowsOptions{
			WebviewUserDataPath:           filepath.Join(filepath.Dir(config.SettingsPath), "webview-background"),
			DisableQuitOnLastWindowClosed: true,
		},
		Services: []application.Service{
			application.NewService(&desktopclient.PetSettingsService{Local: petSettingsService, Maintenance: maintenance.InstallerInProgress}),
			application.NewService(nativeNotificationHost),
		},
		Assets: application.AssetOptions{Handler: dshworkapp.PetMediaHandler(petSettingsService, application.AssetFileServerFS(resources.Assets))},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	updates := newUpdateRunner(desktop, launcher.Stop, func(state daemon.UpdateSnapshot) {
		server.Publish("update-state", state)
		updateMenuStateMu.Lock()
		updateMenuState = state
		apply := updateMenuApply
		updateMenuStateMu.Unlock()
		if apply != nil {
			apply(state)
		}
		if state.Phase == daemon.UpdateReady && state.TargetVersion != "" {
			locale := loadNativeLocale(&activeLocale)
			copy := nativeui.UpdateReadyNotification(locale, state.TargetVersion)
			if err := notificationRouter.Publish(context.Background(), dshworknotifications.Event{
				ID:     "dsh-work-update-ready:" + state.TargetVersion,
				Class:  dshworknotifications.ClassActionRequired,
				Title:  copy.Title,
				Body:   copy.Body,
				Target: "about",
			}); err != nil {
				log.Printf("update notification delivery: %v", err)
			}
		}
	})
	server.UpdateState = updates.Snapshot
	server.UpdateCommand = updates.Action
	var repositionPetWindow func()
	var persistPetWindowPosition func()
	if petOverlayCapabilities.Level != dshworkpet.OverlayFallback {
		petWindow = desktop.Window.NewWithOptions(nativeui.PetWindowOptions())
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
		windowWidth, windowHeight := nativeui.PetActivityWindowSize(position.Width, position.Height)
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
			windowWidth, windowHeight := nativeui.PetActivityWindowSize(width, height)
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
	notificationRouter.SetDeliveryFailureHandler(func(dshworknotifications.Event) {
		desktop.Event.Emit("notification-failure", true)
		server.Publish("notification-failure", true)
	})

	hideBackgroundSurfaces := func() {
		petWindowMu.Lock()
		defer petWindowMu.Unlock()
		if petWindow != nil {
			petWindow.Hide()
		}
	}
	showWorkspace = func() {
		if maintenance.InstallerInProgress() {
			maintenance.ShowInstallerBusy()
			return
		}
		if err := launcher.Open(""); err != nil {
			log.Printf("open UI: %v", err)
		}
	}
	nativeNotification.OnNotificationResponse(func(result wailsnotifications.NotificationResult) {
		if result.Error != nil {
			log.Printf("desktop notification response: %v", result.Error)
			return
		}
		if result.Response.ID == "" {
			return
		}
		if target, ok := result.Response.UserInfo["target"].(string); ok && target == "about" && openSettings != nil {
			openSettings("about")
			return
		}
		if showWorkspace != nil {
			showWorkspace()
		}
	})

	tray := desktop.SystemTray.New()
	if runtime.GOOS == "darwin" {
		tray.SetTemplateIcon(resources.TrayTemplateIcon)
	} else {
		tray.SetIcon(resources.TrayIcon).SetDarkModeIcon(resources.TrayDarkIcon)
	}
	initialNative := nativeui.LabelsFor(localePreference)
	tray.SetTooltip(initialNative.TrayTooltip)
	restartSettled := func(status lifecycle.Status) bool {
		return status.State == lifecycle.StateReady || status.State == lifecycle.StateFailed || status.State == lifecycle.StateStopped
	}
	restartDSH := func() {
		if maintenance.InstallerInProgress() || applicationShuttingDown.Load() || quitFlow.InProgress() || windowLedger.IsQuitting() || !restartActionBusy.CompareAndSwap(false, true) {
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
		server.BeginDrain()
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
	trayCheckUpdates := trayMenu.Add(initialNative.CheckUpdates).OnClick(func(*application.Context) {
		openSettings("about")
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
		trayCheckUpdates.SetEnabled(enabled)
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
		if maintenance.InstallerInProgress() {
			maintenance.ShowInstallerBusy()
			return
		}
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
		openSettings("about")
	})
	updateMenuApply = func(state daemon.UpdateSnapshot) {
		labels := nativeui.LabelsFor(loadNativeLocale(&activeLocale))
		label := updateMenuLabel(labels, state)
		trayCheckUpdates.SetLabel(label)
		checkUpdates.SetLabel(label)
	}
	updateMenuStateMu.RLock()
	initialUpdateState := updateMenuState
	updateMenuStateMu.RUnlock()
	updateMenuApply(initialUpdateState)
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
		aboutDshWork.SetLabel(labels.About)
		updateMenuStateMu.RLock()
		state := updateMenuState
		updateMenuStateMu.RUnlock()
		if updateMenuApply != nil {
			updateMenuApply(state)
		} else {
			checkUpdates.SetLabel(labels.CheckUpdates)
		}
	}
	publishLocale = func(locale dshworksettings.Locale) {
		updateNativeLocale(locale)
		desktop.Event.Emit("locale", locale)
		server.Publish("locale", locale)
	}

	openSettings = func(section string) {
		if maintenance.InstallerInProgress() {
			maintenance.ShowInstallerBusy()
			return
		}
		if err := launcher.Open(section); err != nil {
			log.Printf("open Settings: %v", err)
		}
	}
	host.SetPublish(func(status lifecycle.Status) {
		if status.State == lifecycle.StateStarting && status.Phase == lifecycle.PhaseCheckpoint {
			go func() {
				if err := launcher.Open("__boot"); err != nil {
					log.Printf("open boot WebView: %v", err)
				}
			}()
		}
		if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" && status.Error != nil {
			log.Printf("Host failure: %+v diagnostics=%+v", status.Error, host.Diagnostics())
		}
		dshworkapp.PublishPetHostStatus(petSettingsService, status)
		desktop.Event.Emit("lifecycle", status)
		server.Publish("lifecycle", status)
		trayStatus.SetLabel(nativeui.TrayStatus(loadNativeLocale(&activeLocale), status.State))
		if restartActionBusy.Load() && restartSettled(status) {
			restartActionBusy.Store(false)
		}
		if refreshLifecycleMenu != nil {
			refreshLifecycleMenu()
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
	host.SetQuitHandler(func() {
		windowLedger.BeginQuit()
		quitFlow.Begin(
			hideBackgroundSurfaces,
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
				server.ResetDrain()
				windowLedger.CancelQuit()
				if openSettings != nil {
					openSettings("overview")
				}
			},
		)
	})
	petInputContext, stopPetInput := context.WithCancel(context.Background())
	desktop.OnShutdown(func() {
		server.BeginDrain()
		stopPetInput()
		applicationShuttingDown.Store(true)
		updates.Stop()
		server.Publish("background-stopping", true)
		_ = launcher.Stop()
		if stopPetMenuRefresh != nil {
			stopPetMenuRefresh()
		}
		windowLedger.BeginQuit()
		hideBackgroundSurfaces()
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
			server.Publish("notification-failure", true)
		}
		if petWindow != nil {
			nativeui.StartPetPointer(petInputContext, petWindow, func(x, y float64) bool {
				return dshworkapp.PetPointerHit(petSettingsService, x, y)
			})
		}
		dshworkapp.StartupPetService(petSettingsService)
		if !maintenance.InstallerInProgress() {
			host.Start()
			updates.Start()
		}
		go func() {
			if err := ipcServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
				log.Printf("background IPC: %v", err)
				host.Quit()
			}
		}()
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

type managerSnapshotReader interface {
	Snapshot(context.Context) (dshmanager.Snapshot, error)
}

func dshWindowTheme(manager managerSnapshotReader) application.Theme {
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

func settingsURL(manager managerSnapshotReader, section string) string {
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
