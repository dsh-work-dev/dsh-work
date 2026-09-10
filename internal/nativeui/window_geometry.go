package nativeui

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

func RestoreWindowGeometry(options application.WebviewWindowOptions, manager *settings.Manager) application.WebviewWindowOptions {
	if manager == nil {
		return options
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		return options
	}
	g := values.WorkspaceWindow
	if options.Name == "settings" {
		g = values.SettingsWindow
	}
	if g.Width >= options.MinWidth && g.Width <= 16384 && g.Height >= options.MinHeight && g.Height <= 16384 {
		options.Width, options.Height = g.Width, g.Height
		if g.Maximised {
			options.StartState = application.WindowStateMaximised
		}
	}
	return options
}

// Capture dimensions on the native event thread; debounce only the settings write.
// Closing and application shutdown flush pending changes before the window dies.
func RememberWindowGeometry(window application.Window, options application.WebviewWindowOptions, manager *settings.Manager) func() {
	if manager == nil {
		return func() {}
	}
	var mu sync.Mutex
	var timer *time.Timer
	g := settings.WindowGeometry{Width: options.Width, Height: options.Height, Maximised: options.StartState == application.WindowStateMaximised}
	save := func() {
		if err := manager.SetWindowGeometry(context.Background(), options.Name, g); err != nil {
			log.Printf("save %s window size: %v", options.Name, err)
		}
	}
	flush := func() {
		mu.Lock()
		defer mu.Unlock()
		if timer != nil {
			timer.Stop()
		}
		save()
	}
	record := func(*application.WindowEvent) {
		if window.IsMinimised() || window.IsFullscreen() {
			return
		}
		maximised := window.IsMaximised()
		width, height := window.Size()
		mu.Lock()
		defer mu.Unlock()
		g.Maximised = maximised
		if !maximised && width >= options.MinWidth && height >= options.MinHeight {
			g.Width, g.Height = width, height
		}
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(300*time.Millisecond, flush)
	}
	for _, event := range []events.WindowEventType{events.Common.WindowDidResize, events.Common.WindowMaximise, events.Common.WindowUnMaximise} {
		window.OnWindowEvent(event, record)
	}
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { flush() })
	return flush
}
