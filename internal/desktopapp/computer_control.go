package desktopapp

import (
	"encoding/json"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/wailsapp/wails/v3/pkg/application"
	"io/fs"
)

type computerControlWindow struct {
	app                             *application.App
	control, pointer                *application.WebviewWindow
	revision                        uint64
	quitting                        func() bool
	hotspot                         struct{ X, Y float64 }
	pointerEnabled                  bool
	rendering                       bool
	pending                         uint64
	latest                          automation.Snapshot
	pointerSession, pointerLifetime string
	pointerTarget                   automation.ComputerTarget
	pointKnown                      bool
	pointX, pointY                  float64
}

func newComputerControlWindow(app *application.App, assets fs.FS, quitting func() bool) *computerControlWindow {
	p := &computerControlWindow{app: app, quitting: quitting}
	data, e := fs.ReadFile(assets, "frontend/dist/branding/agent-pointer.json")
	var meta struct{ Hotspot struct{ X, Y float64 } }
	if e == nil {
		e = json.Unmarshal(data, &meta)
	}
	if e != nil {
		logging.Module(logging.ModSettings).Error("computer pointer asset unavailable", "err", e)
		return p
	}
	p.hotspot = meta.Hotspot
	p.pointerEnabled = true
	return p
}
func (p *computerControlWindow) update(s automation.Snapshot) {
	application.InvokeAsync(func() {
		if p.quitting != nil && p.quitting() || s.Revision < p.revision {
			return
		}
		p.revision = s.Revision
		p.rememberLocation(s.ComputerAccess)
		p.latest = s
		p.pending++
		// WebView creation pumps native messages and may re-enter this callback.
		// Keep one creator and render the newest snapshot after creation returns.
		if p.rendering {
			return
		}
		p.rendering = true
		defer func() { p.rendering = false }()
		for {
			pending := p.pending
			p.render(p.latest, pending)
			if pending == p.pending {
				return
			}
		}
	})
}
func (p *computerControlWindow) render(s automation.Snapshot, pending uint64) {
	use := s.ComputerAccess
	if use == nil || use.Use == nil {
		p.pointKnown = false
		if p.control != nil {
			p.control.Hide()
		}
		if p.pointer != nil {
			p.pointer.Hide()
		}
		return
	}
	if p.control == nil {
		p.control = p.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "computer-control", Title: "DSH Work computer controls", URL: "/?surface=computer-control", Width: 460, Height: 72, Frameless: true, AlwaysOnTop: true, Hidden: true, DisableResize: true, Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true}})
	}
	if p.pointerEnabled && p.pointer == nil {
		p.pointer = p.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "computer-pointer", Title: "DSH Work agent pointer", URL: "/?surface=computer-pointer", Width: 56, Height: 56, Frameless: true, AlwaysOnTop: true, Hidden: true, IgnoreMouseEvents: true, DisableResize: true, BackgroundType: application.BackgroundTypeTransparent, BackgroundColour: application.NewRGBA(0, 0, 0, 0), Windows: application.WindowsWindow{HiddenOnTaskbar: true, DisableFramelessWindowDecorations: true}})
	}
	if pending != p.pending {
		return
	}
	nativeui.ShowComputerOverlay(p.control, 24, 24, false)
	u := use.Use
	if !p.pointerEnabled || !p.pointKnown || u.Stopping {
		if p.pointer != nil {
			p.pointer.Hide()
		}
		return
	}
	nativeui.ShowComputerPointer(p.pointer, p.pointX, p.pointY, p.pointerTarget.WindowID, p.hotspot.X, p.hotspot.Y)
}
func (p *computerControlWindow) rememberLocation(s *automation.ComputerSnapshot) {
	if s == nil || s.Use == nil {
		p.pointKnown = false
		return
	}
	u := s.Use
	target := automation.ComputerTarget{}
	if u.Target != nil {
		target = *u.Target
	}
	if p.pointerSession != u.Session || p.pointerLifetime != u.Lifetime || p.pointerTarget != target {
		p.pointKnown = false
		p.pointerSession, p.pointerLifetime, p.pointerTarget = u.Session, u.Lifetime, target
	}
	if u.Stopping {
		p.pointKnown = false
	}
	if !u.Stopping && u.Running && u.Rect != nil && target.WindowID != 0 {
		x, xok := u.Rect["x"].(float64)
		y, yok := u.Rect["y"].(float64)
		if xok && yok {
			p.pointX, p.pointY, p.pointKnown = x, y, true
		}
	}
}
