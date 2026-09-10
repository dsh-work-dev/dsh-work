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
	width, height        int
	maximised, minimised bool
	callbacks            map[events.WindowEventType]func(*application.WindowEvent)
}

func (w *geometryWindow) Size() (int, int)   { return w.width, w.height }
func (w *geometryWindow) IsMaximised() bool  { return w.maximised }
func (w *geometryWindow) IsMinimised() bool  { return w.minimised }
func (w *geometryWindow) IsFullscreen() bool { return false }
func (w *geometryWindow) OnWindowEvent(event events.WindowEventType, callback func(*application.WindowEvent)) func() {
	w.callbacks[event] = callback
	return func() {}
}
func (w *geometryWindow) RegisterHook(event events.WindowEventType, callback func(*application.WindowEvent)) func() {
	return w.OnWindowEvent(event, callback)
}

func TestWindowSizeSurvivesCloseAndSettingsReload(t *testing.T) {
	config := settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")}
	manager, err := settings.New(config)
	if err != nil {
		t.Fatal(err)
	}
	options := application.WebviewWindowOptions{Name: "workspace", Width: 1180, Height: 760, MinWidth: 720, MinHeight: 480}
	w := &geometryWindow{width: 1360, height: 920, callbacks: map[events.WindowEventType]func(*application.WindowEvent){}}
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
	options.Name = "settings"
	other := RestoreWindowGeometry(options, reloaded)
	if other.Width != 1180 || other.Height != 760 || other.StartState != application.WindowStateNormal {
		t.Fatal("window sizes leaked between windows")
	}
}
