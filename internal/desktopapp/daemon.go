package desktopapp

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/accountcallback"
	"github.com/local/dsh-work/internal/acquisition"
	dshworkapp "github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/automation/computerruntime"
	"github.com/local/dsh-work/internal/automation/computeruia"
	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/desktopprobe"
	"github.com/local/dsh-work/internal/diagnostics"
	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshinteraction"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
	"github.com/local/dsh-work/internal/hostevents"
	"github.com/local/dsh-work/internal/hostplugins"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/nativeui"
	dshworknotifications "github.com/local/dsh-work/internal/notifications"
	dshworkpet "github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/platform"
	"github.com/local/dsh-work/internal/presence"
	dshworksettings "github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/storagepaths"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/version"
	"github.com/local/dsh-work/internal/workerchannel"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

func runDaemon(identity string, resources Resources) {
	dependencies := platform.New()
	paths, pathErr := storagepaths.DefaultDirectories()
	if pathErr != nil {
		logging.Fatal(logging.Module(logging.ModStorage), "storage directories unavailable", logging.ErrAttrs(pathErr, "")...)
	}
	config := dshworkapp.DefaultConfig(currentDiscoveryRoot())
	config.RequireWebBoot = true
	defaultRoot := paths.Root
	defaultUserData := paths.UserDataRoot
	if root := os.Getenv("DSH_WORK_DESKTOP_ROOT"); root != "" && os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		defaultRoot = filepath.Join(root, "app")
		defaultUserData = filepath.Join(root, "data")
		config.CacheDirectory = filepath.Join(root, "cache")
		paths.ConfigFile = filepath.Join(root, "config.toml")
	}
	if root := embeddedDevelopmentRoot(); root != "" {
		defaultRoot = filepath.Join(root, "app")
		defaultUserData = filepath.Join(root, "data")
		config.CacheDirectory = filepath.Join(root, "cache")
		paths.ConfigFile = filepath.Join(root, "config.toml")
	}
	locations, err := storagepaths.Open(paths.ConfigFile, defaultRoot, defaultUserData)
	if err != nil {
		logging.Fatal(logging.Module(logging.ModStorage), "storage locations unreadable", logging.ErrAttrs(err, "")...)
	}
	// Keep the existing manager lock boundary while preparing a relocation.
	previousRoot := locations.Snapshot().Current.Root
	previousLock, err := dshworkapp.AcquireManagerProcessLock(filepath.Join(previousRoot, "settings.json"))
	if err != nil {
		logging.Fatal(logging.Module(logging.ModStorage), "storage lock failed", logging.ErrAttrs(err, "")...)
	}
	var nextRootLock *dshworkapp.ProcessLock
	if err := locations.ApplyPendingWith(func(next storagepaths.Locations) error {
		if filepath.Clean(next.Root) == filepath.Clean(previousRoot) {
			return nil
		}
		nextRootLock, err = dshworkapp.AcquireManagerProcessLock(filepath.Join(next.Root, "settings.json"))
		return err
	}); err != nil {
		logging.Module(logging.ModStorage).Warn("storage migration failed", logging.ErrAttrs(err, "")...)
		if nextRootLock != nil {
			_ = nextRootLock.Close()
			nextRootLock = nil
		}
	}
	storage := locations.Snapshot().Current
	var managerLock *dshworkapp.ProcessLock
	if previousRoot != storage.Root {
		// The destination lock was acquired before the location record commit,
		// so readers cannot start a CLI manager in the handoff window.
		managerLock = nextRootLock
		if managerLock == nil {
			_ = previousLock.Close()
			logging.Fatal(logging.Module(logging.ModStorage), "storage lock missing after relocation")
		}
		_ = previousLock.Close()
		if entries, e := os.ReadDir(previousRoot); e == nil && len(entries) == 1 && entries[0].Name() == "state.lock" {
			_ = os.Remove(filepath.Join(previousRoot, "state.lock"))
			_ = os.Remove(previousRoot)
		}
	} else {
		if nextRootLock != nil {
			_ = nextRootLock.Close()
		}
		managerLock = previousLock
	}
	config.SettingsPath = filepath.Join(storage.Root, "settings.json")
	dshHome, runtimeStore, statePath := storagepaths.Layout(storage)
	config.DSHDataDirectory = dshHome
	config.BootstrapDirectory = filepath.Join(storage.Root, "bootstrap")
	if managerLock == nil {
		logging.Fatal(logging.Module(logging.ModStorage), "manager lock unavailable")
	}
	if err := logging.Attach(); err != nil {
		logging.Module(logging.ModApp).Warn("log files unavailable", logging.ErrAttrs(err, "")...)
	}
	defer func() {
		if err := managerLock.Close(); err != nil {
			logging.Module(logging.ModStorage).Warn("manager lock release failed", logging.ErrAttrs(err, "")...)
		}
	}()
	dsh := dshadapter.New(dependencies.CommandExecutor)
	dsh.SetDiscoveryRoot(config.DiscoveryRoot)
	petActivity := dshactivity.New()
	defer petActivity.Close()
	hostPlugins := hostplugins.NewOverlay(filepath.Join(filepath.Dir(config.SettingsPath), "host-plugins"), version.String())
	automationBridge := automation.NewBridge()
	defer automationBridge.Stop()
	coreOverlay := func(generation string) (string, error) {
		petConfig, err := petActivity.Begin(generation)
		if err != nil {
			return "", err
		}
		automationConfig, err := automationBridge.Begin(generation)
		if err != nil {
			return "", err
		}
		return hostPlugins.Prepare(generation, map[string]map[string]string{
			"automation": automationConfig,
			"shell":      {"generation": generation},
			"pet":        petConfig,
		})
	}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		probePatch := desktopprobe.Patch(filepath.Join(storage.Root, "probe"), config.DiscoveryRoot)
		withProbe := coreOverlay
		coreOverlay = func(generation string) (string, error) {
			patch, err := withProbe(generation)
			if err != nil {
				return "", err
			}
			return probePatch(patch)
		}
	}
	dsh.SetCoreOverlayPatch(coreOverlay)
	var managerRunner dshmanager.CommandRunner
	if dependencies.CommandExecutor != nil {
		managerRunner = managerCommandRunner{executor: dependencies.CommandExecutor}
	}
	managerConfig := dshmanager.Config{
		StatePath:          statePath,
		AutomationVersions: automation.CurrentVersionInputs,
		MinimumDSHVersion:  dshadapter.MinimumVersion,
		CommandRunner:      managerRunner,
		PluginCommands:     dshadapter.NewPluginCommands(),
		RuntimeInstaller:   platform.NewRuntimeInstaller(runtimeStore),
		DSHCatalog:         platform.NewDSHReleaseCatalog(runtimeStore),
		NodeCatalog:        platform.NewNodeReleaseCatalog(runtimeStore),
		NodeInstaller:      platform.NewNodeInstaller(runtimeStore),
		NodeResolver:       platform.NewNodeResolver(runtimeStore),
		RuntimeVerifier:    dsh,
		ProfileCatalog:     dsh,
		AppProfile:         dsh,
		DataDirectories: []dshmanager.DataDirectoryInfo{{
			ID: "dsh-work", Name: "DSH Work", Path: config.DSHDataDirectory, Ownership: dshmanager.DataDirectoryOwnershipDSHWork,
		}},
	}
	desktopprobe.Configure(&managerConfig, dsh, storage.Root)
	if err := embeddedConfigureManager(&managerConfig, dsh); err != nil {
		logging.Fatal(logging.Module(logging.ModRuntime), "embedded runtime unavailable", "error", err)
	}
	manager, managerErr := dshmanager.New(managerConfig)
	if managerErr != nil {
		logging.Module(logging.ModRuntime).Error("manager state unavailable", logging.ErrAttrs(managerErr, lifecycle.ErrorManagerStateInvalid)...)
		manager = nil
	}
	channel := workerchannel.New()
	channel.SetWorkerObserver(func(ctx context.Context, auth string, transport http.RoundTripper) {
		petActivity.Start(ctx, auth, transport)
		automationBridge.Start(ctx, auth, transport)
	})
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
		WorkerOutput: func(generation string, event supervisor.OutputEvent) {
			logging.WorkerOutput(generation, string(event.Stream), event.Text)
		},
	}, config)
	settingsManager, settingsErr := dshworksettings.New(dshworksettings.Config{
		Path:     config.SettingsPath,
		Replacer: dependencies.FileReplacer,
	})
	localePreference := dshworksettings.DefaultLocale
	notificationPreference := dshworknotifications.DefaultPreferences()
	if settingsErr != nil {
		logging.Module(logging.ModSettings).Error("settings unavailable", logging.ErrAttrs(settingsErr, lifecycle.ErrorSettingsUnavailable)...)
	} else if values, err := settingsManager.Snapshot(context.Background()); err != nil {
		logging.Module(logging.ModSettings).Error("settings load failed", logging.ErrAttrs(err, lifecycle.ErrorSettingsStateInvalid)...)
	} else {
		localePreference = values.Locale
		notificationPreference = values.Notifications
		automaticRuntimeRollback.Store(values.AutomaticRuntimeRollback)
		logging.SetVerbose(values.VerboseLogging)
		// Settings written before dsh-work owned its appearance followed the
		// DSH theme; adopt that mode once so the look does not change on upgrade.
		if manager != nil {
			if theme, err := manager.Theme(context.Background()); err == nil {
				if err := settingsManager.ImportAppearanceMode(context.Background(), dshworksettings.AppearanceMode(theme)); err != nil {
					logging.Module(logging.ModSettings).Warn("appearance import failed", logging.ErrAttrs(err, "")...)
				}
			}
		}
	}
	var petCatalog dshworkpet.PetCatalog
	petCatalogRoot := filepath.Join(config.CacheDirectory, "pets")
	petCatalogConfig := dshworkpet.CatalogConfig{CacheRoot: petCatalogRoot, CommunityHome: dshworkpet.CommunityHome()}
	if os.Getenv("DSH_WORK_DESKTOP_REPORT") != "" {
		petCatalogConfig.CodexHome = filepath.Join(storage.Root, "probe-pets")
		petCatalogConfig.Env = func(string) string { return "" }
		petCatalogConfig.CommunityHome = ""
	}
	if catalog, err := dshworkpet.NewPetCatalog(petCatalogConfig); err != nil {
		logging.Module(logging.ModPet).Warn("pet catalog unavailable", logging.ErrAttrs(err, "")...)
	} else {
		petCatalog = catalog
	}
	var activeLocale atomic.Value
	activeLocale.Store(string(localePreference))
	windowLedger := lifecycle.NewWindowLedger("workspace")
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
	dshworkapp.SetHostAppearanceProvider(hostService, func() dshworksettings.Appearance {
		if settingsManager == nil {
			return dshworksettings.DefaultAppearance()
		}
		values, err := settingsManager.Snapshot(context.Background())
		if err != nil {
			return dshworksettings.DefaultAppearance()
		}
		return values.Appearance
	})
	var desktop *application.App
	var acquisitionStreamPublisher *trailingPublisher[acquisition.OperationStatus]
	managerService := dshworkapp.NewManagerServiceWithRuntimeProgress(manager, host, func(status acquisition.OperationStatus) {
		if acquisitionStreamPublisher != nil && acquisitionStreamPublisher.enqueue(status, true) {
			acquisitionStreamPublisher.drain()
		}
	})
	dshworkapp.SetBackupFileActions(managerService,
		func() (string, error) { return nativeui.ChooseProfileBackup(desktop) },
		func(path string) error { return desktop.Env.OpenFileManager(path, false) })
	dshworkapp.SetProfileExportAction(managerService, func(name string) (string, error) { return nativeui.SaveProfileArchive(desktop, name) })
	var publishLocale func(dshworksettings.Locale)
	automationPreparer := platform.NewAutomationPreparer(config.CacheDirectory)
	if configurable, ok := automationPreparer.(interface {
		ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error))
	}); ok {
		configurable.ConfigureToolchain(func(ctx context.Context) (dshmanager.ResolvedNode, error) {
			if manager == nil {
				return dshmanager.ResolvedNode{}, automation.ErrUnavailable
			}
			snapshot, err := manager.Snapshot(ctx)
			if err != nil {
				return dshmanager.ResolvedNode{}, err
			}
			selection := snapshot.Current
			if selection == nil {
				return dshmanager.ResolvedNode{}, automation.ErrUnavailable
			}
			launch, err := manager.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: selection.RuntimeID, Node: selection.Node, Profile: selection.Profile})
			return launch.Node, err
		})
	}
	automationState := automation.New(settingsManager, automationPreparer)
	if inspector, ok := automationPreparer.(interface{ Inventory() []automation.Component }); ok {
		automationState.ConfigureComponents(inspector.Inventory)
	}
	defer automationState.Close()
	automationState.ConfigureProfileLauncher(browserhost.NewProfileLauncher())
	automationState.EnableBrowserGateway(automation.BrowserGatewayConfig{
		NativePrompts:   true,
		ExtensionSource: automation.CurrentVersionInputs().ExtensionSource,
		ActivityPath:    filepath.Join(filepath.Dir(config.SettingsPath), "automation", "activity.jsonl"), Replacer: dependencies.FileReplacer,
	})
	automationState.BrowserDiscovery(browserhost.Discover())
	agentDiagnostic := newEmbeddedAgentDiagnostic()
	automationState.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{
		ObserveStage: agentDiagnostic.stage, Service: automationState, Supervisor: embeddedBrowserSupervisor(dependencies.Supervisor, config.CacheDirectory), Root: filepath.Join(config.CacheDirectory, "automation", "browser-sessions"),
		Resolve: func(ctx context.Context) (browserruntime.Runtime, error) {
			if manager == nil {
				return browserruntime.Runtime{}, automation.ErrUnavailable
			}
			snapshot, err := manager.Snapshot(ctx)
			if err != nil {
				return browserruntime.Runtime{}, err
			}
			selection := snapshot.Current
			if selection == nil || selection.Profile.DataDirectoryID != "dsh-work" || selection.Profile.Name != dshmanager.AppProfileName {
				return browserruntime.Runtime{}, automation.ErrRevoked
			}
			resolved, err := manager.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: selection.RuntimeID, Node: selection.Node, Profile: selection.Profile})
			if err != nil {
				return browserruntime.Runtime{}, err
			}
			directory, err := automation.PublishedPackage(config.CacheDirectory, automation.Browser)
			if err != nil {
				return browserruntime.Runtime{}, err
			}
			return browserruntime.Runtime{Node: resolved.Node.NodePath, Environment: agentDiagnostic.environment(resolved.Node.ChildEnvironment), Dependencies: directory}, nil
		},
	}), browserruntime.ToolSchemas())
	computerHelperExecutable, _ := os.Executable()
	automationState.ConfigureComputerGateway(automation.ComputerGatewayConfig{Inspect: automation.InspectComputerTarget, Acquire: automation.AcquireComputerDesktopLease, Probe: computeruia.NewProbe(dependencies.Supervisor, computerHelperExecutable), Factory: computerruntime.NewFactory(computerruntime.Config{
		Supervisor: dependencies.Supervisor, Root: filepath.Join(config.CacheDirectory, "automation", "computer-sessions"),
		Resolve: func(ctx context.Context, engine string) (computerruntime.Runtime, error) {
			if manager == nil || engine != "cua-driver" && engine != "auv" {
				return computerruntime.Runtime{}, automation.ErrUnavailable
			}
			capability := automation.Computer
			if engine == "auv" {
				capability = automation.AUV
			}
			checker, ok := automationPreparer.(interface {
				Ready(automation.Capability) bool
			})
			if !ok || !checker.Ready(capability) {
				return computerruntime.Runtime{}, automation.ErrUnavailable
			}
			snapshot, err := manager.Snapshot(ctx)
			if err != nil {
				return computerruntime.Runtime{}, err
			}
			selection := snapshot.Current
			if selection == nil || selection.Profile.DataDirectoryID != "dsh-work" || selection.Profile.Name != dshmanager.AppProfileName {
				return computerruntime.Runtime{}, automation.ErrRevoked
			}
			resolved, err := manager.ResolveLaunch(ctx, dshmanager.LaunchRequest{RuntimeID: selection.RuntimeID, Node: selection.Node, Profile: selection.Profile})
			if err != nil {
				return computerruntime.Runtime{}, err
			}
			directory, err := automation.PublishedPackage(config.CacheDirectory, capability)
			if err != nil {
				return computerruntime.Runtime{}, err
			}
			driver := filepath.Join(config.CacheDirectory, "automation", "cua-driver", automation.DriverVersion, "windows-amd64", "cua-driver.exe")
			if engine == "auv" {
				driver = filepath.Join(directory, "node_modules", "@auv-js", "cli-win32-x64-msvc", "bin", "auv.exe")
			}
			return computerruntime.Runtime{Node: resolved.Node.NodePath, Environment: resolved.Node.ChildEnvironment, Dependencies: directory, Driver: driver}, nil
		},
	})})
	automationBridge.Attach(automationState)
	automationService := dshworkapp.NewAutomationService(automationState)
	dshworkapp.SetBrowserPermissionLocale(automationService, func() dshworksettings.Locale { return loadNativeLocale(&activeLocale) })
	dshworkapp.SetBrowserPermissionAppearance(automationService, func() dshworksettings.Appearance {
		v, _ := settingsManager.Snapshot(context.Background())
		return v.Appearance
	})
	var permissionUI atomic.Pointer[browserPermissionWindow]
	var computerUI atomic.Pointer[computerControlWindow]
	dshworkapp.SetBrowserProfileDirectoryPicker(automationService, func(current string) (string, error) { return nativeui.ChooseDirectory(desktop, current) })
	nativeContext, stopNative := context.WithCancel(context.Background())
	defer stopNative()
	selfExecutable, selfExecutableErr := os.Executable()
	if selfExecutableErr == nil {
		nativeConfig := browserhost.Config{Directory: filepath.Join(storage.Root, "browser-native"), Executable: selfExecutable, Identity: identity, Replacer: dependencies.FileReplacer}
		if inspector, ok := automationPreparer.(interface{ Inventory() []automation.Component }); ok {
			automationState.ConfigureComponents(func() []automation.Component {
				return append(inspector.Inventory(), automation.ExtensionComponent(browserhost.DevelopmentDirectory(nativeConfig)))
			})
		}
		dshworkapp.SetBrowserHostRegistrar(automationService, &developmentBrowserRegistrar{config: nativeConfig, maintenance: automationState.BeginBrowserMaintenance, Registrar: browserhost.New(nativeConfig)})
		dshworkapp.SetBrowserExtensionFolder(automationService, func() error {
			return desktop.Env.OpenFileManager(browserhost.DevelopmentDirectory(nativeConfig), false)
		})
	}
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
	dshworkapp.SetVerboseLoggingChanged(settingsService, logging.SetVerbose)
	petSettingsService := dshworkapp.NewPetSettingsService(settingsManager, petCatalog)
	dshworkapp.SetPetHostMaintenance(petSettingsService, maintenance.InstallerInProgress)
	dshworkapp.SetPetRendererFactory(petSettingsService, func() dshworkpet.PetRenderer {
		return dshworkpet.NewRasterRenderer()
	})
	petOverlayCapabilities := dshworkpet.CurrentOverlayCapabilities()
	dshworkapp.SetPetOverlayCapabilities(petSettingsService, petOverlayCapabilities)
	storageService := dshworkapp.NewStorageService(locations,
		func(path string) error { return desktop.Env.OpenFileManager(path, false) },
		func(currentPath string) (string, error) { return nativeui.ChooseDirectory(desktop, currentPath) })
	diag := diagnostics.New(diagnostics.Config{Dir: logging.Dir(), Environment: func(ctx context.Context) diagnostics.Environment {
		environment := diagnostics.Environment{
			AppVersion: version.String(),
			AppChannel: version.Channel(),
			State:      string(host.Status().State),
		}
		if manager == nil {
			return environment
		}
		snapshot, err := manager.Snapshot(ctx)
		if err != nil || snapshot.Current == nil {
			return environment
		}
		current := snapshot.Current
		environment.Profile = current.Profile.Name
		for _, runtime := range snapshot.Runtimes {
			if runtime.ID == current.RuntimeID {
				environment.DSHVersion = runtime.Version
				break
			}
		}
		if current.Node.Kind == dshmanager.NodeSelectionSystem {
			if snapshot.SystemNode != nil {
				environment.NodeVersion = snapshot.SystemNode.Version
			}
		} else {
			for _, node := range snapshot.Nodes {
				if node.ID == current.Node.InstallationID {
					environment.NodeVersion = node.Version
					break
				}
			}
		}
		for _, profile := range snapshot.Profiles {
			if profile.Ref == current.Profile {
				environment.PluginCount = len(profile.Plugins)
				break
			}
		}
		return environment
	}})
	diagnosticsService := dshworkapp.NewDiagnosticsService(diag,
		func(path string) error { return desktop.Env.OpenFileManager(path, false) },
		func(name string) (string, error) { return nativeui.SaveDiagnosticsBundle(desktop, name) })
	hub := hostevents.New()
	var updateBrowserTray atomic.Value
	var browserAttention atomic.Bool
	var browserTrayBase atomic.Value
	automationState.OnChange(func(state automation.Snapshot) {
		if controls := computerUI.Load(); controls != nil {
			controls.update(state)
		}
		if popup := permissionUI.Load(); popup != nil {
			popup.update(state)
		}
		if update := updateBrowserTray.Load(); update != nil {
			update.(func(automation.Snapshot))(state)
		}
		hub.SetTopic("automation", state)
		automationBridge.Publish()
	})
	hub.SetTopic("automation", automationState.Snapshot())
	tracker := presence.New()
	interactions := dshinteraction.New()
	embeddedAvailable := embeddedPlatformAvailable()
	embeddedService := &embeddedBrowserService{Automation: automationState, Compatible: func(context.Context) bool {
		status := host.Status()
		selection := status.LaunchSelection
		return embeddedAvailable && embeddedRuntimeReady() && selection != nil && selection.RuntimeVersion == desktopbridge.EmbeddedBrowserRuntime && selection.ProfileName == dshmanager.AppProfileName && filepath.Clean(selection.DataDirectoryPath) == filepath.Clean(config.DSHDataDirectory)
	}}
	if embeddedRuntimeReady() {
		acquire := func(directory string) (io.Closer, error) {
			return dshworkapp.AcquireManagerProcessLock(filepath.Join(directory, "instance.json"))
		}
		store, err := embeddedbrowser.OpenInstanceStore(filepath.Join(storage.Root, "embedded-browser", "instances"), acquire)
		if err != nil {
			logging.Fatal(logging.Module(logging.ModRuntime), "embedded instance store unavailable", "error", err)
		}
		defer store.Close()
		registry, err := embeddedbrowser.NewInstancePresenterRegistry(embeddedbrowser.InstanceRegistryConfig{
			Store: store, AcquireProfile: acquire,
			ArtifactLocale: func() dshworksettings.Locale { return loadNativeLocale(&activeLocale) },
			DownloadDecision: func(ctx context.Context) (bool, error) {
				return askDownloadShutdown(ctx, desktop, loadNativeLocale(&activeLocale))
			},
			StartBroker: func(ctx context.Context, instance embeddedbrowser.InstanceConfig) (*embeddedbrowser.Broker, error) {
				if embeddedDevelopmentRoot() == "" {
					if err := distribution.AdmitProfile(instance.Profile); err != nil {
						return nil, err
					}
				}
				return startEmbeddedBroker(ctx, embeddedbrowser.Config{Executable: embeddedExecutable(), Profile: instance.Profile, Supervisor: dependencies.Supervisor, ObserveCommand: agentDiagnostic.commands()})
			},
			OnFailure: func(instance string, err error) {
				logging.Module(logging.ModRuntime).Warn("embedded instance cleanup failed", "instance", instance, "error", err)
			},
		})
		if err != nil {
			logging.Fatal(logging.Module(logging.ModRuntime), "embedded instance registry unavailable", "error", err)
		}
		embeddedService.Registry = registry
		defer func() {
			if err := registry.Close(); err != nil {
				label := "embedded instance final cleanup incomplete"
				if registry.CleanupComplete() {
					label = "embedded cleanup completed-abnormally"
				}
				logging.Module(logging.ModRuntime).Error(label, "error", err)
			}
		}()
	}
	downloadShutdown := &downloadShutdownLease{}
	if embeddedService.Registry != nil {
		downloadShutdown.begin = embeddedService.Registry.PrepareDownloadShutdown
		downloadShutdown.cancelQuitAdmission = embeddedService.Registry.CancelQuitAdmission
	}
	daemonHostService := &daemon.HostService{HostService: hostService}
	server := &daemon.Server{Host: host, Channel: channel, Settings: settingsManager, Root: storage.Root, PID: os.Getpid(), Hub: hub, Presence: tracker, Interactions: interactions, Services: map[string]any{
		"HostService": daemonHostService, "WindowService": &daemon.WindowService{Settings: settingsManager, Presence: tracker, Interactions: interactions},
		"EmbeddedBrowserService": embeddedService,
		"DSHPageService":         &daemon.DSHPageService{Activity: petActivity, Interactions: interactions},
		"AutomationService":      automationService, "ManagerService": managerService, "SettingsService": settingsService, "StorageService": storageService, "PetSettingsService": petSettingsService, "DiagnosticsService": diagnosticsService,
	}, Maintenance: maintenance.InstallerInProgress}
	server.PrepareQuit = downloadShutdown.prepare
	if registry := embeddedService.Registry; registry != nil {
		clearWindow := func(surface string) { tracker.Clear(surface); interactions.ClearWindow(surface) }
		registry.WindowRetired = func(_ embeddedbrowser.PresenterPeer, surface string) { clearWindow(surface) }
		server.UIStreamScope = func(ctx context.Context, lease string) (daemon.UIStreamAdmission, error) {
			native, ok := daemon.ProcessPeerFrom(ctx)
			if !ok {
				return daemon.UIStreamAdmission{}, errors.New("CEF_STREAM_NATIVE_PEER_REQUIRED")
			}
			peer := embeddedbrowser.PresenterPeer{PID: native.PID, Started: native.Started}
			surface, done, err := registry.WindowStreamScope(peer, lease)
			if err != nil {
				return daemon.UIStreamAdmission{}, err
			}
			return daemon.UIStreamAdmission{Key: lease, Windows: []string{surface}, Done: done, Retire: func() {
				if registry.WithWindowLease(peer, surface, lease, func() { clearWindow(surface) }) {
					ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
					defer cancel()
					if err := registry.Hide(ctx, peer, surface); err != nil {
						logging.Module(logging.ModIPC).Warn("embedded stream hide failed", "error", err)
					}
				}
			}}, nil
		}
	}
	server.BrowserNative = &browsernative.Handler{Service: automationState, Context: nativeContext}
	accountCallback, callbackErr := accountcallback.NewAccountCallbackServer(func(w http.ResponseWriter, r *http.Request, generation string) {
		server.ServeAccountCallback(w, r, generation)
	})
	if callbackErr != nil {
		logging.Module(logging.ModAccount).Warn("account callback listener unavailable", logging.ErrAttrs(callbackErr, "")...)
	} else {
		server.AccountCallback = accountCallback
		defer accountCallback.Close()
	}
	acquisitionStreamPublisher = newTrailingPublisher(acquisitionPublishInterval, nil, func(status acquisition.OperationStatus) {
		publishAcquisitionStatus(hub, status, func(status acquisition.OperationStatus) {
			if desktop != nil {
				desktop.Event.Emit("acquisition", status)
			}
		})
	}, mergeAcquisitionStatus)
	backgroundFocused = server.UIFocused
	launcher := newUILauncher(identity)
	server.OpenUI = func(section string) error {
		_, err := launcher.Open(section, "workspace")
		return err
	}
	petActivity.SetNavigation(launcher.Open, hub.SendCommand)
	dshworkapp.SetBrowserConversationOpener(automationService, func(ctx context.Context, id string) error { return petActivity.Open(ctx, id, "workspace") })
	interactionNotifications := &interactionNotifier{
		page:        interactions.Page,
		focused:     tracker.Focused,
		preferences: notificationRouter.Preferences,
		publish:     notificationRouter.Publish,
		remove:      nativeNotification.RemoveDeliveredNotification,
		locale:      func() dshworksettings.Locale { return loadNativeLocale(&activeLocale) },
		owner:       interactions.Owner,
		sendCommand: hub.SendCommand,
		open:        petActivity.Open,
		showWorkspace: func() {
			if showWorkspace != nil {
				showWorkspace()
			}
		},
	}
	interactions.OnAdded(interactionNotifications.added)
	interactions.OnRemoved(interactionNotifications.removed)
	registerDSHApprovalCategory := func(locale dshworksettings.Locale) {
		if err := nativeNotification.RegisterNotificationCategory(dshApprovalNotificationCategory(locale)); err != nil {
			logging.Module(logging.ModNotify).Warn("approval notification category registration failed", logging.ErrAttrs(err, "")...)
		}
	}
	dshworkapp.SetPetActivity(petSettingsService, petActivity, tracker.LastFocused)
	server.Media = dshworkapp.PetMediaHandler(petSettingsService, http.NotFoundHandler())
	listener, err := daemon.Listen(identity)
	if err != nil {
		logging.Fatal(logging.Module(logging.ModIPC), "background listen failed", logging.ErrAttrs(err, "")...)
	}
	ipcServer := daemon.HTTPServer(server)
	defer ipcServer.Close()
	defer listener.Close()
	if err := nativeui.EnsureToastActivator(appName, toastDisplayName, toastActivatorCLSID); err != nil {
		logging.Module(logging.ModNotify).Error("notification activation registration failed", logging.ErrAttrs(err, "")...)
	}
	desktop = application.New(application.Options{
		Name:         appName,
		Logger:       logging.WailsLogger(),
		ErrorHandler: logging.WailsErrorHandler(),
		Description:  "A local desktop shell for DSH workspaces.",
		Icon:         resources.AppIcon,
		Windows: application.WindowsOptions{
			WebviewUserDataPath:           filepath.Join(config.CacheDirectory, "webview-background"),
			AdditionalBrowserArgs:         backgroundWebviewDebugArgs(),
			DisableQuitOnLastWindowClosed: true,
		},
		Services: []application.Service{
			application.NewService(&desktopclient.PetSettingsService{Local: petSettingsService, Maintenance: maintenance.InstallerInProgress}),
			application.NewService(nativeNotificationHost),
			application.NewService(&desktopclient.AutomationService{Local: automationService}),
		},
		Assets: application.AssetOptions{Handler: dshworkapp.PetMediaHandler(petSettingsService, application.AssetFileServerFS(resources.Assets))},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})
	computerUI.Store(newComputerControlWindow(desktop, resources.Assets, applicationShuttingDown.Load))
	permissionPresenter := &browserPermissionWindow{app: desktop, service: automationState, quitting: applicationShuttingDown.Load}
	permissionUI.Store(permissionPresenter)
	dshworkapp.SetBrowserPermissionRendered(automationService, permissionPresenter.rendered)
	updateChannel := dshworksettings.DefaultUpdateChannel
	if settingsManager != nil {
		if values, err := settingsManager.Snapshot(context.Background()); err == nil {
			updateChannel = values.UpdateChannel
		}
	}
	updates := newUpdateRunner(filepath.Join(storage.Root, "update"), updateChannel, launcher.Stop, func(state daemon.UpdateSnapshot) {
		hub.SetTopic("update", state)
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
				logging.Module(logging.ModNotify).Warn("update notification failed", logging.ErrAttrs(err, "")...)
			}
		}
	})
	server.UpdateState = updates.Snapshot
	server.UpdateCommand = updates.Action
	daemonHostService.UpdateState = updates.Snapshot
	daemonHostService.UpdateCommand = updates.Action
	var initialPreferences dshworksettings.Values
	if settingsManager != nil {
		initialPreferences, _ = settingsManager.Snapshot(context.Background())
	} else {
		initialPreferences = dshworksettings.DefaultValues()
	}
	initialLifecycle := host.Status()
	initialWorkerURL := ""
	if initialLifecycle.State == lifecycle.StateReady {
		initialWorkerURL = initialLifecycle.WorkspaceURL
	} else if initialLifecycle.State == lifecycle.StateStarting {
		initialWorkerURL = host.WebBootURL()
	}
	hub.SetTopic("lifecycle", initialLifecycle)
	hub.SetTopic("worker-url", initialWorkerURL)
	hub.SetTopic("update", updates.Snapshot())
	hub.SetTopic("preferences", initialPreferences)
	hub.SetTopic("acquisition", acquisition.OperationStatus{})
	lifecyclePublisher := &serializedCurrentPublisher[lifecycleTopicValue]{
		readCurrent: func() (lifecycleTopicValue, error) {
			status := host.Status()
			workerURL := ""
			if status.State == lifecycle.StateReady {
				workerURL = status.WorkspaceURL
			} else if status.State == lifecycle.StateStarting {
				workerURL = host.WebBootURL()
			}
			return lifecycleTopicValue{status: status, workerURL: workerURL}, nil
		},
		publish: func(value lifecycleTopicValue) {
			hub.SetTopic("lifecycle", value.status)
			hub.SetTopic("worker-url", value.workerURL)
		},
	}
	if settingsManager != nil {
		preferencesPublisher := &serializedCurrentPublisher[dshworksettings.Values]{
			readCurrent: func() (dshworksettings.Values, error) { return settingsManager.Snapshot(context.Background()) },
			publish:     func(values dshworksettings.Values) { hub.SetTopic("preferences", values) },
		}
		settingsManager.OnChange(func(dshworksettings.Values) {
			if err := preferencesPublisher.publishCurrent(); err != nil {
				logging.Module(logging.ModSettings).Warn("preferences publication failed", logging.ErrAttrs(err, "")...)
			}
		})
	}
	// The running updater switches before the choice is saved, so a channel the
	// updater refuses is never persisted.
	dshworkapp.SetUpdateChannelChanged(settingsService, func(ctx context.Context, channel dshworksettings.UpdateChannel) error {
		return updates.SetChannel(ctx, channel)
	})
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
			logging.Module(logging.ModPet).Warn("pet position save failed", logging.ErrAttrs(err, "")...)
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
		hub.SendCommand("", "notification-failure", 10*time.Second, true)
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
		if _, err := launcher.Open("", "workspace"); err != nil {
			logging.Module(logging.ModApp).Error("open window failed", logging.ErrAttrs(err, "")...)
		}
	}
	nativeNotification.OnNotificationResponse(func(result wailsnotifications.NotificationResult) {
		if interactionNotifications.respond(result) {
			return
		}
		if result.Response.UserInfo != nil && result.Response.UserInfo["kind"] == "browser-consent" {
			if session, ok := result.Response.UserInfo["sessionId"].(string); ok {
				_ = petActivity.Open(context.Background(), session, "workspace")
			}
			if showWorkspace != nil {
				showWorkspace()
			}
			return
		}
		if result.Error != nil {
			logging.Module(logging.ModNotify).Warn("notification response failed", logging.ErrAttrs(result.Error, "")...)
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
	var downloadQuitBusy atomic.Bool
	reportDownloadQuitFailure := func(err error) {
		logging.Module(logging.ModLifecycle).Warn("download shutdown deferred", "error", err)
		if errors.Is(err, embeddedbrowser.ErrDownloadShutdownDeclined) {
			return
		}
		message := "Could not finish exiting. Try again."
		switch loadNativeLocale(&activeLocale) {
		case dshworksettings.LocaleChinese:
			message = "暂时无法退出，请重试。"
		case dshworksettings.LocaleJapanese:
			message = "終了できませんでした。もう一度お試しください。"
		}
		go desktop.Dialog.Error().SetTitle("DSH Work").SetMessage(message).Show()
	}
	quitDSHWork := func() {
		if applicationShuttingDown.Load() || quitFlow.InProgress() || windowLedger.IsQuitting() {
			return
		}
		if !downloadQuitBusy.CompareAndSwap(false, true) {
			return
		}
		go func() {
			defer downloadQuitBusy.Store(false)
			if err := downloadShutdown.prepare(context.Background()); err != nil {
				downloadShutdown.withdraw()
				reportDownloadQuitFailure(err)
				return
			}
			if updates.InstallOnExit(func() { host.Quit() }) {
				return
			}
			server.BeginDrain()
			host.Quit()
			if refreshLifecycleMenu != nil {
				refreshLifecycleMenu()
			}
		}()
	}
	embeddedService.Quit = quitDSHWork
	quitConfirmation := &quitConfirmation{
		inspect: petActivity.InspectQuit,
		ask: func(inspection dshactivity.QuitInspection, complete func(bool)) {
			message := nativeui.QuitConfirmationMessage(loadNativeLocale(&activeLocale), inspection.ActiveTasks, inspection.ScheduledTasks)
			showQuitQuestion(desktop.Dialog.Question().SetTitle("DSH Work").SetMessage(message), complete)
		},
	}
	confirmQuit := func(proceed func()) { quitConfirmation.confirm(proceed) }
	hostService.ConfirmQuit = func(func()) { confirmQuit(quitDSHWork) }
	openUpdates := func(*application.Context) {
		if maintenance.InstallerInProgress() || applicationShuttingDown.Load() || quitFlow.InProgress() || windowLedger.IsQuitting() {
			return
		}
		phase := updates.Snapshot().Phase
		if phase == daemon.UpdateIdle || phase == daemon.UpdateUpToDate || phase == daemon.UpdateError {
			if err := updates.Action(context.Background(), daemon.UpdateActionCheck); err != nil {
				logging.Module(logging.ModUpdate).Warn("menu update check failed", logging.ErrAttrs(err, "")...)
			}
		}
		openSettings("about")
	}
	trayMenu := desktop.NewMenu()
	browserTrayBase.Store(nativeui.TrayStatus(localePreference, lifecycle.StateStarting))
	trayStatus := trayMenu.Add(browserTrayBase.Load().(string)).SetEnabled(false)
	trayOpenWorkspace := trayMenu.Add(initialNative.OpenWorkspace).OnClick(func(*application.Context) {
		showWorkspace()
	})
	trayMenu.AddSeparator()
	traySettings := trayMenu.Add(initialNative.Settings).OnClick(func(*application.Context) {
		openSettings("settings")
	})
	trayBrowser := trayMenu.Add(initialNative.Browser).OnClick(func(*application.Context) { openSettings("browser") })
	trayStopBrowser := trayMenu.Add(browserStopLabel(localePreference)).OnClick(func(*application.Context) { _ = automationState.StopBrowserUse("", "") })
	trayStopBrowser.SetHidden(true)
	updateBrowserTray.Store(func(state automation.Snapshot) {
		trayStopBrowser.SetHidden(state.BrowserAccess == nil || len(state.BrowserAccess.Sessions) == 0)
		attention := state.BrowserAccess != nil && (len(state.BrowserAccess.Requests) > 0 || state.BrowserAccess.PendingActions > 0)
		browserAttention.Store(attention)
		if attention {
			trayStatus.SetLabel(browserAttentionLabel(loadNativeLocale(&activeLocale)))
		} else {
			trayStatus.SetLabel(browserTrayBase.Load().(string))
		}
	})
	updateBrowserTray.Load().(func(automation.Snapshot))(automationState.Snapshot())
	trayComputer := trayMenu.Add(initialNative.Computer).OnClick(func(*application.Context) { openSettings("computer") })
	trayMenu.AddSeparator()
	trayCheckUpdates := trayMenu.Add(initialNative.CheckUpdates).OnClick(openUpdates)
	trayMenu.AddSeparator()
	trayRestartDSH := trayMenu.Add(initialNative.RestartDSH).OnClick(func(*application.Context) {
		restartDSH()
	})
	trayQuit := trayMenu.Add(initialNative.Quit).OnClick(func(*application.Context) {
		confirmQuit(quitDSHWork)
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
		confirmQuit(quitDSHWork)
	})
	refreshLifecycleMenu = func() {
		enabled := !applicationShuttingDown.Load() && !quitFlow.InProgress() && !windowLedger.IsQuitting() && !restartActionBusy.Load()
		trayCheckUpdates.SetEnabled(enabled)
		state := host.Status().State
		restartable := enabled && state != lifecycle.StateStarting && state != lifecycle.StateStopping
		quittable := enabled && state != lifecycle.StateStopping
		trayRestartDSH.SetEnabled(restartable)
		trayQuit.SetEnabled(quittable)
		appMenuRestartDSH.SetEnabled(restartable)
		appMenuQuit.SetEnabled(quittable)
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
	checkUpdates := helpMenu.Add(initialNative.CheckUpdates).OnClick(openUpdates)
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
					logging.Module(logging.ModPet).Warn("pet menu visibility failed", logging.ErrAttrs(err, "")...)
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
		base := nativeui.TrayStatus(locale, host.Status().State)
		browserTrayBase.Store(base)
		if browserAttention.Load() {
			trayStatus.SetLabel(browserAttentionLabel(locale))
		} else {
			trayStatus.SetLabel(base)
		}
		trayOpenWorkspace.SetLabel(labels.OpenWorkspace)
		traySettings.SetLabel(labels.Settings)
		trayBrowser.SetLabel(labels.Browser)
		trayStopBrowser.SetLabel(browserStopLabel(locale))
		trayComputer.SetLabel(labels.Computer)
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
		registerDSHApprovalCategory(locale)
	}

	openSettings = func(section string) {
		if maintenance.InstallerInProgress() {
			maintenance.ShowInstallerBusy()
			return
		}
		if _, err := launcher.Open(section, "workspace"); err != nil {
			attrs := append([]any{"section", section}, logging.ErrAttrs(err, "")...)
			logging.Module(logging.ModApp).Error("open settings failed", attrs...)
		}
	}
	var lifecycleLogMu sync.Mutex
	var lifecycleLogKey, lifecycleLogFailure string
	host.SetPublish(func(status lifecycle.Status) {
		if status.State == lifecycle.StateStarting && status.Phase == lifecycle.PhaseCheckpoint {
			go func() {
				if _, err := launcher.Open("__boot", "workspace"); err != nil {
					logging.Module(logging.ModApp).Error("open boot window failed", logging.ErrAttrs(err, "")...)
				}
			}()
		}
		if err := lifecyclePublisher.publishCurrent(); err != nil {
			logging.Module(logging.ModLifecycle).Warn("lifecycle publication failed", logging.ErrAttrs(err, "")...)
		}
		key := string(status.State) + "|" + string(status.Phase) + "|" + status.GenerationID
		failureKey := ""
		if status.Error != nil {
			failureKey = string(status.Error.Code) + "|" + status.Error.CorrelationID
		}
		lifecycleLogMu.Lock()
		changed := key != lifecycleLogKey
		failed := failureKey != "" && failureKey != lifecycleLogFailure
		lifecycleLogKey, lifecycleLogFailure = key, failureKey
		lifecycleLogMu.Unlock()
		lifecycleLogger := logging.Module(logging.ModLifecycle)
		if changed {
			lifecycleLogger.Info("status changed", "state", string(status.State), "phase", string(status.Phase), "gen", status.GenerationID, "op", status.CorrelationID)
		}
		if failed {
			lifecycleLogger.Error("lifecycle failed", "code", string(status.Error.Code), "op", status.Error.CorrelationID, "gen", status.GenerationID, "retryable", status.Error.Retryable, "summary", status.Error.Summary, "err", status.Error.Detail)
		}
		profile := false
		if manager != nil {
			if snapshot, err := manager.Snapshot(context.Background()); err == nil {
				selection := snapshot.Current
				if selection == nil {
					selection = snapshot.Configured
				}
				profile = selection != nil && selection.Profile.DataDirectoryID == "dsh-work" && selection.Profile.Name == dshmanager.AppProfileName
			}
		}
		automationState.Worker(status.GenerationID, status.State == lifecycle.StateReady, profile)
		dshworkapp.PublishPetHostStatus(petSettingsService, status)
		desktop.Event.Emit("lifecycle", status)
		base := nativeui.TrayStatus(loadNativeLocale(&activeLocale), status.State)
		browserTrayBase.Store(base)
		if !browserAttention.Load() {
			trayStatus.SetLabel(base)
		}
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
			logging.Module(logging.ModNotify).Warn("notification delivery failed", logging.ErrAttrs(err, "")...)
		}
	})
	closeEmbeddedInstances := func() error {
		if embeddedService.Registry != nil {
			// Executors must revoke/detach while their native brokers still
			// accept lifecycle cleanup. Keep the broker/GUI owner on failure.
			if err := automationState.StopBrowserUse("", ""); err != nil {
				return err
			}
			if err := embeddedService.Registry.Close(); err != nil {
				if !embeddedService.Registry.CleanupComplete() {
					return err
				}
				logging.Module(logging.ModRuntime).Error("embedded cleanup completed-abnormally", "error", err)
			}
		}
		return nil
	}
	updates.beforeInstall = func(ctx context.Context) error {
		if err := downloadShutdown.prepare(ctx); err != nil {
			return err
		}
		if err := closeEmbeddedInstances(); err != nil {
			downloadShutdown.withdraw()
			return err
		}
		return nil
	}
	updates.installFailed = func() error {
		if embeddedService.Registry != nil {
			if err := embeddedService.Registry.ReopenAfterMaintenance(); err != nil {
				return err
			}
		}
		downloadShutdown.reset()
		server.ResetDrain()
		message := "Update failed. Reopen the workbench from the tray to try again."
		switch loadNativeLocale(&activeLocale) {
		case dshworksettings.LocaleChinese:
			message = "更新失败。请从托盘重新打开工作台后重试。"
		case dshworksettings.LocaleJapanese:
			message = "更新に失敗しました。トレイからワークベンチを開き直して再試行してください。"
		}
		go desktop.Dialog.Error().SetTitle("DSH Work").SetMessage(message).Show()
		return nil
	}
	host.SetQuitHandler(func() {
		// Direct/internal host exits also retain the UI owner while downloads
		// finish. Interactive exits acquired this same lease before host.Quit.
		if err := downloadShutdown.prepare(context.Background()); err != nil {
			reportDownloadQuitFailure(err)
			return
		}
		windowLedger.BeginQuit()
		quitFlow.Begin(
			hideBackgroundSurfaces,
			func() error {
				// Keep every UI's stream and native message pump alive until all CEF
				// documents and supervised process trees have joined. QuitFlow retains
				// the normal retry/recovery route if this barrier fails.
				if err := closeEmbeddedInstances(); err != nil {
					return err
				}
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
				logging.Module(logging.ModLifecycle).Error("host shutdown failed", logging.ErrAttrs(err, "")...)
				if applicationShuttingDown.Load() {
					return
				}
				server.ResetDrain()
				windowLedger.CancelQuit()
				downloadShutdown.withdraw()
				if openSettings != nil {
					openSettings("overview")
				}
			},
		)
	})
	petInputContext, stopPetInput := context.WithCancel(context.Background())
	desktop.OnShutdown(func() {
		server.BeginDrain()
		embeddedCloseErr := closeEmbeddedInstances()
		if embeddedCloseErr != nil {
			label := "embedded instance shutdown incomplete"
			if embeddedService.Registry != nil && embeddedService.Registry.CleanupComplete() {
				label = "embedded cleanup completed-abnormally"
			}
			logging.Module(logging.ModRuntime).Error(label, "error", embeddedCloseErr)
		}
		stopPetInput()
		applicationShuttingDown.Store(true)
		updates.Stop()
		updates.closeStatePublisher()
		acquisitionStreamPublisher.close()
		if embeddedCloseErr == nil || (embeddedService.Registry != nil && embeddedService.Registry.CleanupComplete()) {
			hub.SendCommand("", "background-stopping", 10*time.Second, true)
		}
		_ = launcher.Stop()
		if stopPetMenuRefresh != nil {
			stopPetMenuRefresh()
		}
		windowLedger.BeginQuit()
		hideBackgroundSurfaces()
		petWindowCloseAllowed.Store(true)
		if err := dshworkapp.ShutdownPetService(context.Background(), petSettingsService); err != nil {
			logging.Module(logging.ModPet).Warn("pet shutdown failed", logging.ErrAttrs(err, "")...)
		}
		if err := host.ShutdownForApp(); err != nil {
			logging.Module(logging.ModLifecycle).Error("host shutdown failed", logging.ErrAttrs(err, "")...)
		}
	})
	desktop.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		if popup := permissionUI.Load(); popup != nil {
			popup.update(automationState.Snapshot())
		}
		registerDSHApprovalCategory(loadNativeLocale(&activeLocale))
		if nativeNotificationHost.Unavailable() {
			desktop.Event.Emit("notification-failure", true)
			hub.SendCommand("", "notification-failure", 10*time.Second, true)
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
				logging.Module(logging.ModIPC).Error("background ipc stopped", logging.ErrAttrs(err, "")...)
				host.Quit()
			}
		}()
	})

	if err := desktop.Run(); err != nil {
		logging.Module(logging.ModApp).Error("wails run failed", logging.ErrAttrs(err, "")...)
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

// updateMenuLabel names the tray update item for the current update phase.
func updateMenuLabel(labels nativeui.Labels, state daemon.UpdateSnapshot) string {
	switch state.Phase {
	case daemon.UpdateAvailable:
		return labels.UpdateDownloadMenu
	case daemon.UpdateReady:
		return labels.UpdateAvailableMenu
	case daemon.UpdateChecking, daemon.UpdateDownloading, daemon.UpdateVerifying, daemon.UpdateInstalling:
		return labels.UpdateInProgressMenu
	default:
		return labels.CheckUpdates
	}
}

// hostWindowTheme maps dsh-work's own appearance to the native frame of the
// workbench window.
func hostWindowTheme(mode dshworksettings.AppearanceMode) application.Theme {
	switch mode {
	case dshworksettings.AppearanceLight:
		return application.Light
	case dshworksettings.AppearanceDark:
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

func workspaceURL(manager managerSnapshotReader, section string) string {
	values := url.Values{}
	values.Set("shell", "1")
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
