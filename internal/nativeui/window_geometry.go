package nativeui

import (
	"context"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type GeometryStore interface {
	Snapshot(context.Context) (settings.Values, error)
	SetWindowGeometry(context.Context, string, settings.WindowGeometry) error
}

func RestoreWindowGeometry(options application.WebviewWindowOptions, manager GeometryStore, screens func() []*application.Screen) application.WebviewWindowOptions {
	if manager == nil {
		return options
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		return options
	}
	g := values.WorkspaceWindow
	if g.Width >= options.MinWidth && g.Width <= 16384 && g.Height >= options.MinHeight && g.Height <= 16384 {
		options.Width, options.Height = g.Width, g.Height
		if g.Maximised {
			options.StartState = application.WindowStateMaximised
		}
	}
	if g.Positioned {
		if screens != nil && intersectsVisibleTitlebar(g.X, g.Y, options.Width, screens()) {
			options.InitialPosition = application.WindowXY
			options.X, options.Y = g.X, g.Y
		} else {
			options.InitialPosition = application.WindowCentered
			options.X, options.Y = 0, 0
		}
	}
	return options
}

func intersectsVisibleTitlebar(x, y, width int, screens []*application.Screen) bool {
	titlebar := application.Rect{X: x, Y: y, Width: width, Height: 40}
	for _, screen := range screens {
		if screen == nil {
			continue
		}
		intersection := titlebar.Intersect(screen.WorkArea)
		if intersection.Width >= 100 && intersection.Height >= 40 {
			return true
		}
	}
	return false
}

// Capture position and dimensions on the native event thread; debounce only the settings write.
// Closing and application shutdown flush pending changes before the window dies.
func RememberWindowGeometry(window application.Window, options application.WebviewWindowOptions, manager GeometryStore) func() {
	if manager == nil {
		return func() {}
	}
	var mu sync.Mutex
	var timer *time.Timer
	g := settings.WindowGeometry{
		X: options.X, Y: options.Y, Positioned: options.InitialPosition == application.WindowXY,
		Width: options.Width, Height: options.Height, Maximised: options.StartState == application.WindowStateMaximised,
	}
	save := func() {
		if err := manager.SetWindowGeometry(context.Background(), options.Name, g); err != nil {
			attrs := append([]any{"window", options.Name}, logging.ErrAttrs(err, "")...)
			logging.Module(logging.ModSettings).Warn("window geometry save failed", attrs...)
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
		x, y := window.Position()
		mu.Lock()
		defer mu.Unlock()
		g.Maximised = maximised
		g.X, g.Y, g.Positioned = x, y, true
		if !maximised && width >= options.MinWidth && height >= options.MinHeight {
			g.Width, g.Height = width, height
		}
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(300*time.Millisecond, flush)
	}
	for _, event := range []events.WindowEventType{events.Common.WindowDidMove, events.Common.WindowDidResize, events.Common.WindowMaximise, events.Common.WindowUnMaximise} {
		window.OnWindowEvent(event, record)
	}
	window.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { flush() })
	return flush
}
