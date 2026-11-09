package desktopapp

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/embeddedbrowser"
)

// embeddedBrowserService is a presenter-only bridge. No Agent identity or
// native handle supplied by the DSH frame can establish this kernel peer.
type embeddedBrowserService struct {
	Registry   *embeddedbrowser.InstancePresenterRegistry
	Compatible func(context.Context) bool
	Quit       func()
	Automation *automation.Service
}

func (s *embeddedBrowserService) Capability(ctx context.Context) (bool, error) {
	return s != nil && s.Registry != nil && s.Compatible != nil && s.Compatible(ctx), nil
}
func (s *embeddedBrowserService) nativePeer(ctx context.Context) (embeddedbrowser.PresenterPeer, string, error) {
	enabled := s != nil && s.Registry != nil && embeddedDevelopmentRoot() != ""
	p, ok := daemon.ProcessPeerFrom(ctx)
	surface := daemon.SurfaceFrom(ctx)
	if !enabled || !ok || !embeddedWorkspaceSurface(surface) {
		return embeddedbrowser.PresenterPeer{}, "", errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	return embeddedbrowser.PresenterPeer{PID: p.PID, Started: p.Started}, surface, nil
}
func (s *embeddedBrowserService) Update(ctx context.Context, parent uint64, windowLease, slot, address string, bounds embeddedbrowser.SlotBounds, zoom float64, visible bool) (embeddedbrowser.ViewState, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	return s.Registry.Update(ctx, peer, surface, uintptr(parent), windowLease, slot, address, bounds, zoom, visible)
}
func (s *embeddedBrowserService) State(ctx context.Context, slot string) (embeddedbrowser.ViewState, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	return s.Registry.State(ctx, peer, surface, slot)
}
func (s *embeddedBrowserService) Hide(ctx context.Context) error {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.Hide(ctx, peer, surface)
}
func (s *embeddedBrowserService) Detach(ctx context.Context, slot string) error {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.Detach(ctx, peer, surface, slot)
}
func (s *embeddedBrowserService) Recover(ctx context.Context, parent uint64, windowLease, slot, address string) (embeddedbrowser.ViewState, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	return s.Registry.Recover(ctx, peer, surface, uintptr(parent), windowLease, slot, address)
}
func (s *embeddedBrowserService) Closed(ctx context.Context) error {
	peer, surface, err := s.nativePeer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.CloseWindow(ctx, peer, surface)
}

func embeddedWorkspaceSurface(surface string) bool {
	return daemon.IsWorkspaceSurface(surface)
}

func (s *embeddedBrowserService) Action(ctx context.Context, slot, action string) error {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.Action(ctx, peer, surface, slot, action)
}

func (s *embeddedBrowserService) peer(ctx context.Context) (embeddedbrowser.PresenterPeer, string, error) {
	enabled, _ := s.Capability(ctx)
	if !enabled {
		return embeddedbrowser.PresenterPeer{}, "", errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	return s.nativePeer(ctx)
}
func (s *embeddedBrowserService) RegisterWindow(ctx context.Context, parent uint64, instanceKey string) (embeddedbrowser.WindowAdmission, error) {
	peer, surface, err := s.nativePeer(ctx)
	if err != nil {
		return embeddedbrowser.WindowAdmission{}, err
	}
	return s.Registry.RegisterInstanceWindow(ctx, peer, instanceKey, surface, uintptr(parent))
}

func (s *embeddedBrowserService) RetireDocument(ctx context.Context, parent uint64, windowLease string) error {
	peer, surface, err := s.nativePeer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.RetireDocument(ctx, peer, surface, uintptr(parent), windowLease)
}

func (s *embeddedBrowserService) CloseInstance(ctx context.Context) error {
	peer, surface, err := s.nativePeer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.CloseInstance(ctx, peer, surface)
}

func (s *embeddedBrowserService) QuitOwned(ctx context.Context, owners []embeddedbrowser.ExpectedUIOwner, expires int64) error {
	if s == nil || s.Registry == nil || s.Quit == nil || len(owners) > 2 {
		return errors.New("CEF_RUNNER_QUIT_UNAVAILABLE")
	}
	peer, ok := daemon.ProcessPeerFrom(ctx)
	if !ok || !embeddedRunnerPeer(peer) || !embeddedWorkspaceSurface(daemon.SurfaceFrom(ctx)) {
		return errors.New("CEF_RUNNER_PEER_REJECTED")
	}
	return s.Registry.QuitOwned(ctx, owners, expires, s.Quit)
}

// SelectControl receives selection intent from an admitted UI, never a tool.
// Real Worker generation/lifetime and all permission decisions remain in Service.
func (s *embeddedBrowserService) SelectControl(ctx context.Context, parent uint64, windowLease, document, generation, session, slot, viewLease string) (automation.OwnedSelectionState, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return automation.OwnedSelectionState{}, err
	}
	if s.Automation == nil || len(document) < 16 || len(document) > 128 {
		return automation.OwnedSelectionState{}, automation.ErrUnavailable
	}
	actual := s.Automation.Snapshot().Generation
	if actual == "" || generation != actual {
		return automation.OwnedSelectionState{}, automation.ErrRevoked
	}
	resolve := func(context.Context) (automation.OwnedBrowserTarget, error) {
		return s.Registry.OwnedWindowViewTarget(peer, surface, uintptr(parent), windowLease, slot, viewLease)
	}
	return s.Automation.SelectOwnedBrowser(ctx, automation.OwnedSelectionIntent{Generation: actual, Session: session, Source: windowLease + "/" + document + "/" + viewLease, Resolve: resolve})
}
