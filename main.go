package main

import (
	"embed"
	"log"
	"os"

	workapp "github.com/local/work/internal/app"
	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/platform"
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
	host := workapp.NewHost(workapp.Dependencies{
		DSH:           dsh,
		Supervisor:    dependencies.Supervisor,
		PlatformError: dependencies.Err,
	}, config)
	service := workapp.NewHostService(host)

	desktop := application.New(application.Options{
		Name:        "Work",
		Description: "A trusted desktop shell for a local DSH workspace.",
		Services: []application.Service{
			application.NewService(service),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})

	window := desktop.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "host",
		Title:            "Work",
		Width:            1180,
		Height:           760,
		MinWidth:         720,
		MinHeight:        480,
		BackgroundColour: application.NewRGB(13, 18, 27),
		URL:              "/",
		InitialPosition:  application.WindowCentered,
	})

	host.SetPublish(func(status lifecycle.Status) {
		desktop.Event.Emit("lifecycle", status)
	})
	host.SetReadyHandler(func(workspaceURL string) {
		window.SetURL(workspaceURL)
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

func currentWorkspace() string {
	workspace, err := os.Getwd()
	if err != nil {
		return "."
	}
	return workspace
}
