//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type macPreviewIndicator struct {
	cfg     *macPreviewScenario
	browser *embeddedBrowserService
}

type macPreviewIndicatorRoute struct{ target *embeddedbrowser.OwnedTarget }

func (r macPreviewIndicatorRoute) Select(context.Context, automation.Request) (browserruntime.RouteSelection, error) {
	return browserruntime.RouteSelection{Protocol: "owned-cef-v1", Open: r.target.OpenOwnedRoute}, nil
}

type macPreviewIndicatorSupervisor struct {
	owner  *macowner.MCPAdapter
	worker supervisor.Worker
}

func (s *macPreviewIndicatorSupervisor) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	worker, err := s.owner.Start(ctx, plan, raw)
	s.worker = worker
	return worker, err
}

const macPreviewIndicatorClicks = 3

// Click is registered only for the isolated scenario and uses the real
// workbench owner and locked MCP to click a fixed loopback fixture element.
func (p *macPreviewIndicator) Click(ctx context.Context, parent uint64, windowLease, slot, lease string) (err error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	peer, surface, err := p.browser.peer(ctx)
	if err != nil {
		return err
	}
	target, err := p.browser.Registry.OwnedWindowViewTarget(peer, surface, uintptr(parent), windowLease, slot, lease)
	if err != nil {
		return err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return err
	}
	publication, err := ownedMacPublication()
	if err != nil {
		return err
	}
	root := filepath.Join(filepath.Dir(p.cfg.Workspace), "indicator-runtime")
	owner, err := macowner.NewMCPAdapter(publication, node, p.cfg.IndicatorDependencies, root)
	if err != nil {
		return fmt.Errorf("configure locked Browser MCP: %w", err)
	}
	supervised := &macPreviewIndicatorSupervisor{owner: owner}
	defer func() {
		if err != nil && supervised.worker != nil {
			phase, code, signal, events := macowner.PreviewMCPOutcome(supervised.worker)
			fmt.Fprintf(os.Stderr, "mac-preview-indicator: owner phase=%s exit=%d signal=%d events=%v\n", phase, code, signal, events)
		}
	}()
	factory := browserruntime.NewFactory(browserruntime.Config{
		Routes: macPreviewIndicatorRoute{target}, Supervisor: supervised, Root: root,
		ObserveStage: func(stage string) {
			fmt.Fprintln(os.Stderr, "mac-preview-indicator:", stage)
			if stage == "failure-adapter-read" {
				if worker, ok := supervised.worker.(interface{ PrivateConnectionProcess() (uint32, uint64) }); ok {
					pid, started := worker.PrivateConnectionProcess()
					actual, err := localipc.ProcessStarted(pid)
					fmt.Fprintf(os.Stderr, "mac-preview-indicator: adapter-live-at-read-failure=%t\n", err == nil && started != 0 && actual == started)
				}
			}
		},
		Resolve: func(context.Context) (browserruntime.Runtime, error) {
			return browserruntime.Runtime{
				Node: node, Dependencies: p.cfg.IndicatorDependencies,
				Environment: map[string]string{"DSH_WORK_BROWSER_DIAGNOSTICS": "1"},
			}, nil
		},
	})
	guard := func() (func() error, error) {
		identity, err := target.OwnedIdentity(ctx)
		if err != nil || identity.URL != p.cfg.URL || identity.ViewLease != lease || !identity.ViewVisible || !identity.FrameScopeReady {
			return nil, fmt.Errorf("indicator requires the authenticated visible fixture document: %v", err)
		}
		end, err := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Identity: identity, Token: rand.Text()})
		if err != nil {
			return nil, fmt.Errorf("begin guarded fixture call: %w", err)
		}
		return func() error {
			if err := end(); err != nil {
				return fmt.Errorf("end guarded fixture call: %w", err)
			}
			return nil
		}, nil
	}
	end, err := guard()
	if err != nil {
		return err
	}
	resource, startErr := factory(ctx, automation.Request{Session: "mac-indicator-fixture", Lifetime: "one"})
	if resource != nil {
		defer func() {
			if closeErr := resource.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close locked Browser MCP: %w", closeErr))
			}
		}()
	}
	if startErr != nil {
		startErr = fmt.Errorf("start locked Browser MCP: %w", startErr)
	}
	if err := errors.Join(startErr, end()); err != nil {
		return err
	}
	for click := 1; click <= macPreviewIndicatorClicks; click++ {
		end, err = guard()
		if err != nil {
			return err
		}
		value, callErr := resource.Execute(ctx, "browser_click", map[string]any{"target": "getByRole('button', { name: 'Agent feedback target' })"})
		if err = errors.Join(callErr, end()); err != nil {
			return fmt.Errorf("execute guarded fixture click %d: %w", click, err)
		}
		data, err := json.Marshal(value)
		if err != nil || bytes.Contains(data, []byte(`"isError":true`)) {
			return fmt.Errorf("locked MCP fixture click %d failed: %v", click, err)
		}
	}
	return nil
}

func (p *macPreviewConsumer) verifyIndicator() error {
	ctx, cfg, window := p.ctx, p.cfg, p.window
	service, requested, before := p.service, p.requested, p.before
	result := p.result
	var err error
	if err = verifyMacPreviewIndicator(ctx, cfg, service, window, requested, before.Lease); err != nil {
		return macPreviewFailure("agent-indicator-native-paint", err)
	} else if err = p.wait(func() bool {
		value, e := p.readPage()
		return e == nil && value.Nonce == cfg.Nonce && slices.Equal(value.IndicatorEvents,
			slices.Repeat([]string{"mousedown:true", "mouseup:true", "click:true"}, macPreviewIndicatorClicks))
	}); err != nil {
		return macPreviewFailure("agent-indicator-input-order", err)
	} else {
		result["agentIndicatorPassed"] = true
		result["agentIndicatorClicks"] = macPreviewIndicatorClicks
	}
	return nil
}

func verifyMacPreviewIndicator(ctx context.Context, cfg *macPreviewScenario, service *desktopclient.EmbeddedBrowserService, window application.Window, requested macPreviewReport, lease string) error {
	ctx, cancel := context.WithTimeout(ctx, 48*time.Second)
	defer cancel()
	admission, err := service.RegisterWindow(ctx)
	if err != nil {
		return err
	}
	parent, err := desktopclient.MacPreviewParent(window)
	if err != nil {
		return err
	}
	baseline, err := macPreviewIndicatorPixels(filepath.Join(cfg.Evidence, "mac-preview-visible.png"))
	if err != nil {
		return err
	}
	click := make(chan error, 1)
	go func() {
		click <- service.Client.Call(ctx, "MacPreviewIndicator", "Click", window.Name(), []any{parent, admission.Lease, requested.Slot, lease}, nil)
	}()
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	sample := filepath.Join(cfg.Evidence, "mac-preview-indicator.png")
	painted := false
	for {
		select {
		case err := <-click:
			if err != nil {
				return err
			}
			if !painted {
				return errors.New("indicator pixels were not observed in the owned AppKit view")
			}
			return nil
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if !painted {
				if err := desktopclient.MacPreviewRender(window, lease, sample); err != nil {
					return err
				}
				pixels, err := macPreviewIndicatorPixels(sample)
				if err != nil {
					return err
				}
				painted = pixels >= baseline+12
			}
		}
	}
}

func macPreviewIndicatorPixels(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	pixels, err := png.Decode(file)
	if err != nil {
		return 0, err
	}
	count := 0
	bounds := pixels.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X + bounds.Dx()/2; x < bounds.Max.X; x++ {
			r, g, b, _ := pixels.At(x, y).RGBA()
			if r < 110*257 && g > 140*257 && g > r+55*257 && b > g+28*257 {
				count++
			}
		}
	}
	return count, nil
}
