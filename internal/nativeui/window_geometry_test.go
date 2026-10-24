package nativeui

import (
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type geometryWindow struct {
	application.Window
	x, y                 int
	width, height        int
	maximised, minimised bool
	callbacks            map[events.WindowEventType]func(*application.WindowEvent)
}

func (w *geometryWindow) Size() (int, int)     { return w.width, w.height }
func (w *geometryWindow) Position() (int, int) { return w.x, w.y }
func (w *geometryWindow) IsMaximised() bool    { return w.maximised }
func (w *geometryWindow) IsMinimised() bool    { return w.minimised }
func (w *geometryWindow) IsFullscreen() bool   { return false }
func (w *geometryWindow) OnWindowEvent(event events.WindowEventType, callback func(*application.WindowEvent)) func() {
	w.callbacks[event] = callback
	return func() {}
}
func (w *geometryWindow) RegisterHook(event events.WindowEventType, callback func(*application.WindowEvent)) func() {
	return w.OnWindowEvent(event, callback)
}

func TestWindowSizeSurvivesCloseAndReload(t *testing.T) {
	config := settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")}
	manager, err := settings.New(config)
	if err != nil {
		t.Fatal(err)
	}
	options := application.WebviewWindowOptions{Name: "workspace", Width: 1180, Height: 760, MinWidth: 720, MinHeight: 480}
	w := &geometryWindow{x: -1280, y: 45, width: 1360, height: 920, callbacks: map[events.WindowEventType]func(*application.WindowEvent){}}
	flush := RememberWindowGeometry(w, options, manager)
	defer flush()
	w.callbacks[events.Common.WindowDidResize](nil)
	// Maximising and minimising must preserve the last ordinary dimensions.
	w.width, w.height, w.maximised = 1920, 1080, true
	w.callbacks[events.Common.WindowMaximise](nil)
	w.width, w.height, w.minimised = 0, 0, true
	w.callbacks[events.Common.WindowDidResize](nil)
	w.callbacks[events.Common.WindowClosing](nil)
	reloaded, err := settings.New(config)
	if err != nil {
		t.Fatal(err)
	}
	restored := RestoreWindowGeometry(options, reloaded)
	if restored.Width != 1360 || restored.Height != 920 || restored.StartState != application.WindowStateMaximised {
		t.Fatalf("restored window: %+v", restored)
	}
	if restored.InitialPosition != application.WindowXY || restored.X != -1280 || restored.Y != 45 {
		t.Fatalf("restored position: initial=%v x=%d y=%d", restored.InitialPosition, restored.X, restored.Y)
	}
}

func TestWindowPositionAtOriginSurvivesMoveAndReload(t *testing.T) {
	config := settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")}
	manager, err := settings.New(config)
	if err != nil {
		t.Fatal(err)
	}
	options := application.WebviewWindowOptions{Name: "workspace", Width: 1180, Height: 760, MinWidth: 720, MinHeight: 480}
	w := &geometryWindow{x: 240, y: 80, width: 1180, height: 760, callbacks: map[events.WindowEventType]func(*application.WindowEvent){}}
	flush := RememberWindowGeometry(w, options, manager)
	defer flush()
	w.x, w.y = 0, 0
	w.callbacks[events.Common.WindowDidMove](nil)
	w.callbacks[events.Common.WindowClosing](nil)
	reloaded, err := settings.New(config)
	if err != nil {
		t.Fatal(err)
	}
	values, err := reloaded.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got := values.WorkspaceWindow; got.X != 0 || got.Y != 0 || !got.Positioned {
		t.Fatalf("saved position = %+v, want positioned at (0, 0)", got)
	}
	restored := RestoreWindowGeometry(options, reloaded)
	if restored.InitialPosition != application.WindowXY || restored.X != 0 || restored.Y != 0 {
		t.Fatalf("restored origin: initial=%v x=%d y=%d", restored.InitialPosition, restored.X, restored.Y)
	}
}

func TestLegacyWindowGeometryKeepsCenteredStartup(t *testing.T) {
	config := settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")}
	manager, err := settings.New(config)
	if err != nil {
		t.Fatal(err)
	}
	options := application.WebviewWindowOptions{Name: "workspace", Width: 1180, Height: 760, InitialPosition: application.WindowCentered}
	restored := RestoreWindowGeometry(options, manager)
	if restored.InitialPosition != application.WindowCentered {
		t.Fatalf("legacy settings changed initial position to %v", restored.InitialPosition)
	}
}
