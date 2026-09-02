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
	"github.com/local/work/internal/platform"
	"github.com/local/work/internal/workergateway"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	application.RegisterEvent[lifecycle.Status]("lifecycle")

	dependencies := platform.New()
	config := workapp.DefaultConfig(currentWorkspace())
	dsh := dshadapter.New(dependencies.CommandExecutor, config.ExpectedDSHVersion)
	dsh.SetWorkspaceRoot(config.WorkspaceRoot)
	runtimeHint := dsh.RuntimeHint()
	var managerRunner dshmanager.CommandRunner
	if dependencies.CommandExecutor != nil {
		managerRunner = managerCommandRunner{executor: dependencies.CommandExecutor}
	}
	runtimeStore := filepath.Join(filepath.Dir(config.DSHHome), "dsh-work", "runtimes")
	manager, managerErr := dshmanager.New(dshmanager.Config{
		WorkspaceRoot:    config.WorkspaceRoot,
		CommandRunner:    managerRunner,
		PluginCommands:   dshadapter.NewPluginCommands(),
		RuntimeInstaller: platform.NewRuntimeInstaller(runtimeStore),
		RuntimeVerifier:  dsh,
		ProfileCatalog:   dsh,
		Homes: []dshmanager.HomeInfo{{
			ID: "work", Name: "Work DSH home", Path: config.DSHHome, Ownership: dshmanager.HomeOwnershipWork,
		}},
		Runtimes: []dshmanager.RuntimeInfo{{
			ID: "dsh-" + runtimeHint.Version, Version: runtimeHint.Version, Path: runtimeHint.Path,
			Source: dshmanager.RuntimeSourceDevelopmentFixture, Installed: executableExists(runtimeHint.Path),
		}},
		DefaultSelection: dshmanager.LaunchSelection{
			RuntimeID: "dsh-" + runtimeHint.Version,
			Profile:   dshmanager.ProfileRef{HomeID: "work", Name: "web"},
			Workspace: config.WorkspaceRoot,
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
	hostService := workapp.NewHostService(host, workspaceTrusted.Load)
	managerService := workapp.NewManagerService(manager)

	desktop := application.New(application.Options{
		Name:        "Work",
		Description: "A trusted desktop shell for a local DSH workspace.",
		Services: []application.Service{
			application.NewService(hostService),
			application.NewService(managerService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	gateway.SetOpenExternal(desktop.Browser.OpenURL)

	var workspaceWindow application.Window
	var managerWindow application.Window
	var managerWindowMu sync.Mutex
	var openManager func(string)
	menu := desktop.NewMenu()
	if runtime.GOOS == "darwin" {
		menu.AddRole(application.AppMenu)
	}
	workMenu := menu.AddSubmenu("Work")
	workMenu.Add("Open Manager…").SetAccelerator("CmdOrCtrl+,").OnClick(func(*application.Context) {
		openManager("overview")
	})
	workMenu.Add("Manage Plugins…").OnClick(func(*application.Context) {
		openManager("plugins")
	})
	workMenu.Add("Restart DSH").SetAccelerator("CmdOrCtrl+R").OnClick(func(*application.Context) {
		host.Restart()
	})
	workMenu.AddSeparator()
	workMenu.Add("Quit Work").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) {
		host.Quit()
	})
	desktop.Menu.Set(menu)

	openManager = func(section string) {
		managerWindowMu.Lock()
		window := managerWindow
		if window == nil {
			window = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
				Name:               "manager",
				Title:              "Work Manager",
				Width:              980,
				Height:             720,
				MinWidth:           680,
				MinHeight:          480,
				BackgroundColour:   application.NewRGB(13, 18, 27),
				URL:                managerURL(manager, section),
				InitialPosition:    application.WindowCentered,
				UseApplicationMenu: true,
			})
			window.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
				window.Hide()
				event.Cancel()
			})
			managerWindow = window
		}
		managerWindowMu.Unlock()
		window.SetURL(managerURL(manager, section)).Show().Focus()
	}

	workspaceWindow = desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:               "workspace",
		Title:              "Work",
		Width:              1180,
		Height:             760,
		MinWidth:           720,
		MinHeight:          480,
		BackgroundColour:   application.NewRGB(13, 18, 27),
		URL:                "/",
		InitialPosition:    application.WindowCentered,
		UseApplicationMenu: true,
	})

	host.SetPublish(func(status lifecycle.Status) {
		desktop.Event.Emit("lifecycle", status)
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
		desktop.Quit()
	})
	desktop.OnShutdown(func() {
		if err := host.ShutdownForApp(); err != nil {
			log.Printf("Work host shutdown: %v", err)
		}
	})
	desktop.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
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

type managerCommandRunner struct {
	executor dshadapter.CommandExecutor
}

func (r managerCommandRunner) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshmanager.CommandResult, error) {
	result, err := r.executor.Run(ctx, executable, args, env, dir)
	return dshmanager.CommandResult{Stdout: result.Stdout, Stderr: result.Stderr}, err
}

func managerURL(manager *dshmanager.Manager, section string) string {
	values := url.Values{}
	values.Set("surface", "manager")
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
				values.Set("home", selection.Profile.HomeID)
				values.Set("profile", selection.Profile.Name)
			}
		}
	}
	return "/?" + values.Encode()
}

func currentWorkspace() string {
	workspace, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workspace
}
