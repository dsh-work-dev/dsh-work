//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type macPreviewConsumer struct {
	ctx            context.Context
	app            *application.App
	window         application.Window
	service        *desktopclient.EmbeddedBrowserService
	visibleWindows func() int
	cfg            *macPreviewScenario
	requested      macPreviewReport
	before         embeddedbrowser.ViewState
	afterInput     macPreviewPageState
	backingBytes   float64
	result         map[string]any
	await          func(string) (macPreviewReport, error)
	httpClient     *http.Client
}

type macPreviewStageError struct {
	stage string
	err   error
}

func (e *macPreviewStageError) Error() string { return fmt.Sprint(e.err) }
func (e *macPreviewStageError) Unwrap() error { return e.err }

func macPreviewFailure(stage string, err error) error {
	return &macPreviewStageError{stage: stage, err: err}
}

func installEmbeddedPreviewConsumer(app *application.App, window application.Window, service *desktopclient.EmbeddedBrowserService, visibleWindows func() int) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return
	}
	go func() {
		timeout := 100 * time.Second
		if cfg.ManualFiles != "" || cfg.ManualDownload || cfg.ManualNotification {
			timeout = 10 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		ctx = context.WithValue(ctx, application.WindowKey, window)
		if err := os.MkdirAll(cfg.Evidence, 0700); err != nil {
			return
		}
		p := &macPreviewConsumer{
			ctx: ctx, app: app, window: window, service: service, visibleWindows: visibleWindows, cfg: cfg,
			result:     map[string]any{"qualification": "actual-Wails/DSH-native-view-normal-consumer", "screenshotSource": "AppKit-offscreen-actual-owned-NSView-tree-plus-CEF-CALayer", "globalScreenCapture": "not-requested", "passed": false, "closeInstanceReturned": false},
			httpClient: &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		}
		defer p.httpClient.CloseIdleConnections()
		stop := p.startShell()
		defer stop()
		if err := p.run(); err != nil {
			var failure *macPreviewStageError
			if errors.As(err, &failure) {
				p.result["failureStage"] = failure.stage
			}
			p.result["failureCode"] = fmt.Sprint(err)
			_ = p.save("mac-preview-consumer", p.result)
			app.Quit()
		}
	}()
}

func (p *macPreviewConsumer) run() error {
	if err := p.openBrowser(); err != nil {
		return err
	}
	native, err := p.waitForNativeView()
	if err != nil {
		return err
	}
	if p.cfg.FailNavigation {
		return p.finishNavigationRetry()
	}
	if err := p.verifyNativeInput(native); err != nil {
		return err
	}
	switch {
	case p.cfg.DockReopen:
		if err := p.verifyWorkspaceReopen(); err != nil {
			return err
		}
		return p.finish(p.ctx, "fixture-instance-close", "evidence-write")
	case p.cfg.IndicatorDependencies != "":
		if err := p.verifyIndicator(); err != nil {
			return err
		}
		return p.finish(p.ctx, "fixture-instance-close", "evidence-write")
	case p.cfg.Download:
		return p.runDownloads()
	default:
		if err := p.verifyBrowser(); err != nil {
			return err
		}
		return p.finish(context.WithoutCancel(p.ctx), "normal-instance-close", "evidence-write")
	}
}

func (p *macPreviewConsumer) startShell() func() {
	cfg, ctx, window := p.cfg, p.ctx, p.window
	script := previewScript(macPreviewShellJS, cfg)
	inject := time.NewTicker(250 * time.Millisecond)
	window.ExecJS(script)
	p.await = func(stage string) (macPreviewReport, error) {
		for {
			select {
			case value := <-macPreviewReports:
				if value.Stage == "shell-ready" {
					inject.Stop()
					// The initial Show can precede AppKit key-window eligibility.
					// Focus the realized fixture window before DSH opens its sidebar.
					if !window.IsFocused() {
						window.Focus()
					}
					if err := p.save("mac-preview-shell-boot", map[string]string{"shellOrigin": value.ShellOrigin, "entry": "actual-Wails-window"}); err != nil {
						return value, err
					}
					if value.ShellOrigin == "" || value.ShellOrigin == "null" {
						return value, errors.New("MAC_PREVIEW_SHELL_ORIGIN_OPAQUE")
					}
				}
				if value.Stage == "failure" {
					if value.BrowserSidebar != nil {
						_ = p.save("mac-preview-browser-sidebar", value.BrowserSidebar)
					}
					return value, fmt.Errorf("MAC_PREVIEW_DOM_FAILED_%s", value.Step)
				}
				if value.Stage == stage {
					return value, nil
				}
			case <-inject.C:
				window.ExecJS(script)
			case <-ctx.Done():
				return macPreviewReport{}, ctx.Err()
			}
		}
	}

	return inject.Stop
}

func (p *macPreviewConsumer) save(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(p.cfg.Evidence, name+".json"), append(data, '\n'), 0600)
}

func (p *macPreviewConsumer) finish(ctx context.Context, closeStage, evidenceStage string) error {
	if err := p.closeInstance(ctx); err != nil {
		return macPreviewFailure(closeStage, err)
	}
	p.result["closeInstanceReturned"] = true
	p.result["passed"] = true
	if err := p.save("mac-preview-consumer", p.result); err != nil {
		return macPreviewFailure(evidenceStage, err)
	}
	p.app.Quit()
	return nil
}

// CloseInstance completes while Cocoa still pumps, before UI exit.
func (p *macPreviewConsumer) closeInstance(ctx context.Context) error {
	cleanup, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	return p.service.Client.Call(cleanup, "EmbeddedBrowserService", "CloseInstance", p.window.Name(), nil, nil)
}

func (p *macPreviewConsumer) command(value string) {
	data, _ := json.Marshal(map[string]string{"nonce": p.cfg.Nonce, "command": value})
	p.window.ExecJS("globalThis.__MAC_PREVIEW_COMMAND__?.(" + string(data) + ")")
}

func (p *macPreviewConsumer) wait(check func() bool) error {
	return waitMacPreview(p.ctx, check)
}

func waitMacPreview(ctx context.Context, check func() bool) error {
	for {
		if check() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
