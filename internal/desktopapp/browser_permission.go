package desktopapp

import (
	"context"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/logging"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"strconv"
	"strings"
	"time"
)

// Own native top-level window, not a DSH iframe or an in-workbench dialog.
// Only Go snapshots select requests; closing it denies the displayed ID.
type browserPermissionWindow struct {
	app        *application.App
	service    *automation.Service
	window     *application.WebviewWindow
	id         string
	renderedID string
	shown      bool
	computer   bool
	revision   uint64
	quitting   func() bool
}

func (p *browserPermissionWindow) setRequestID(id string) bool {
	changed := id != p.id
	if changed || id == "" {
		p.renderedID = ""
	}
	p.id = id
	return changed
}
func (p *browserPermissionWindow) markRendered(id string) bool {
	if !p.shown || id == "" || p.id != id || p.renderedID == id {
		return false
	}
	p.renderedID = id
	return true
}
func (p *browserPermissionWindow) rendered(id string) {
	application.InvokeAsync(func() {
		if p.window == nil || p.quitting != nil && p.quitting() {
			return
		}
		r, computer := automation.FirstAutomationPermission(p.service.Snapshot())
		if computer != nil || r == nil || r.ID != id || r.Phase != "waiting" || !time.Now().Before(r.Expires) || !p.markRendered(id) {
			return
		}
		// Typed presentation metadata only: no site, message or page contents.
		logging.Module(logging.ModRuntime).Info("native permission content ready", "request", id, "window", "browser-permission")
	})
}

func (p *browserPermissionWindow) update(state automation.Snapshot) {
	application.InvokeAsync(func() {
		if p.quitting != nil && p.quitting() {
			return
		}
		if state.Revision < p.revision {
			return
		}
		p.revision = state.Revision
		id := ""
		browser, request := automation.FirstAutomationPermission(state)
		computer := request != nil
		if request != nil {
			id = request.ID
		} else if browser != nil {
			id = browser.ID
		}
		p.computer = computer
		// Do not create a second WebView while the Pet environment is still
		// starting. The permission window is needed only for a real request.
		if id == "" {
			p.setRequestID("")
			p.shown = false
			if p.window != nil {
				p.window.Hide()
			}
			return
		}
		if p.window == nil {
			p.window = p.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "browser-permission", Title: "DSH Work", URL: "/?surface=browser-permission", Width: 540, Height: 410, DisableResize: true, AlwaysOnTop: true, Hidden: true})
			p.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				p.renderedID = ""
				p.shown = false
				if p.quitting != nil && p.quitting() {
					return
				}
				e.Cancel()
				id := p.id
				p.window.Hide()
				if id != "" {
					computer := p.computer
					go func() {
						if computer {
							_, _ = p.service.AnswerComputerConsent(context.Background(), id, "deny")
						} else {
							_, _ = p.service.AnswerBrowserConsent(context.Background(), id, "deny")
						}
					}()
				}
			})
			p.window.RegisterHook(events.Common.WindowRuntimeReady, func(*application.WindowEvent) {
				p.renderedID = ""
				logging.Module(logging.ModRuntime).Info("native permission runtime ready", "window", "browser-permission")
			})
		}
		changed := p.setRequestID(id)
		if computer || browser == nil || browser.Phase != "waiting" {
			p.renderedID = ""
		}

		if changed {
			p.shown = true
			p.window.Show()
			p.window.Focus()
		}
	})
}

// The independent daemon WebView uses the next port in development only.
func backgroundWebviewDebugArgs() []string {
	a := webviewDebugArgs()
	if len(a) != 1 {
		return nil
	}
	n, e := strconv.Atoi(strings.TrimPrefix(a[0], "--remote-debugging-port="))
	if e != nil || n < 1 || n >= 65535 {
		return nil
	}
	return []string{"--remote-debugging-port=" + strconv.Itoa(n+1)}
}
