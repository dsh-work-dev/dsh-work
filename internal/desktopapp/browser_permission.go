package desktopapp

import (
	"context"
	"github.com/local/dsh-work/internal/automation"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"strconv"
	"strings"
)

// Own native top-level window, not a DSH iframe or an in-workbench dialog.
// Only Go snapshots select requests; closing it denies the displayed ID.
type browserPermissionWindow struct {
	app      *application.App
	service  *automation.Service
	window   *application.WebviewWindow
	id       string
	revision uint64
	quitting func() bool
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
		if state.BrowserAccess != nil && len(state.BrowserAccess.Requests) > 0 {
			id = state.BrowserAccess.Requests[0].ID
		}
		// Do not create a second WebView while the Pet environment is still
		// starting. The permission window is needed only for a real request.
		if id == "" {
			p.id = ""
			if p.window != nil {
				p.window.Hide()
			}
			return
		}
		if p.window == nil {
			p.window = p.app.Window.NewWithOptions(application.WebviewWindowOptions{Name: "browser-permission", Title: "DSH Work", URL: "/?surface=browser-permission", Width: 540, Height: 410, DisableResize: true, AlwaysOnTop: true, Hidden: true})
			p.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
				if p.quitting != nil && p.quitting() {
					return
				}
				e.Cancel()
				id := p.id
				p.window.Hide()
				if id != "" {
					go func() { _, _ = p.service.AnswerBrowserConsent(context.Background(), id, "deny") }()
				}
			})
		}
		changed := id != p.id
		p.id = id

		if changed {
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
