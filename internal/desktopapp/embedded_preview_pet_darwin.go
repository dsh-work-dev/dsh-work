//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func configureEmbeddedPreviewPet(config *pet.CatalogConfig, capabilities *pet.OverlayCapabilities, root string) bool {
	if cfg, err := macPreviewConfig(); err == nil && cfg != nil && cfg.PetGeometry {
		config.CodexHome = filepath.Join(root, "probe-pets")
		config.CommunityHome = ""
		config.Env = func(string) string { return "" }
		// Exercise geometry on the real overlay before qualifying Mac drag.
		capabilities.Interaction = true
		capabilities.Drag = true
		return true
	}
	return false
}

type macPreviewPet struct {
	app      *application.App
	settings *settings.Manager
	hitTest  func(float64, float64) bool
	mu       sync.Mutex
}

func (p *macPreviewPet) window(ctx context.Context) (application.Window, error) {
	if _, ok := daemon.ProcessPeerFrom(ctx); !ok {
		return nil, errors.New("native Pet fixture peer required")
	}
	window, ok := p.app.Window.GetByName("pet")
	if !ok || !nativeui.PrepareMacPreviewPetWindow(window) {
		return nil, errors.New("Pet window is not visible")
	}
	return window, nil
}

func (p *macPreviewPet) Input(ctx context.Context) (map[string]bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	window, err := p.window(ctx)
	if err != nil {
		return nil, err
	}
	return nativeui.CheckMacPreviewPetPointer(window, p.hitTest)
}

func (p *macPreviewPet) Click(ctx context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	window, err := p.window(ctx)
	if err != nil {
		return false, err
	}
	return nativeui.CheckMacPreviewPetClick(window)
}

func (p *macPreviewPet) Geometry(ctx context.Context, move bool) (desktopclient.MacPreviewPetFrame, error) {
	window, err := p.window(ctx)
	if err != nil {
		return desktopclient.MacPreviewPetFrame{}, err
	}
	frame, err := desktopclient.MacPreviewObservePetFrame(window)
	if err == nil && move {
		window.SetPosition(frame.AreaX+73, frame.AreaY+91)
	}
	return frame, err
}

func (p *macPreviewPet) DisplayChange(ctx context.Context, change string) error {
	window, err := p.window(ctx)
	if err != nil {
		return err
	}
	var event int
	switch change {
	case "screen":
		event = 0
	case "backing":
		event = 1
	case "topology":
		event = 2
	default:
		return errors.New("unknown fixture display event")
	}
	values, err := p.settings.Snapshot(ctx)
	if err != nil {
		return err
	}
	// Use a stale saved monitor and scale while the real window stays still.
	// Only the notification handler can reconcile the profile to AppKit.
	values.Pet.Position.MonitorID = "fixture-previous-display"
	values.Pet.Position.AnchorX, values.Pet.Position.AnchorY = 0.95, 0.9
	values.Pet.Position.Scale = 1.125
	if _, err := p.settings.SetPetPreference(ctx, values.Pet); err != nil {
		return err
	}
	return desktopclient.MacPreviewPetDisplayChange(window, event)
}
