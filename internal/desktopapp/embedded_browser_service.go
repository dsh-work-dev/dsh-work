package desktopapp

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
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
	state, err := s.Availability(ctx)
	return state.State == distribution.Ready, err
}
func (s *embeddedBrowserService) Availability(ctx context.Context) (distribution.Availability, error) {
	if embeddedDevelopmentRoot() == "" {
		publication := embeddedPublication()
		if publication.State != distribution.Ready {
			return distribution.Availability{State: publication.State, Code: availabilityCode(publication.State)}, nil
		}
	} else if embeddedExecutable() == "" {
		return distribution.Availability{State: distribution.Missing, Code: availabilityCode(distribution.Missing)}, nil
	}
	if s == nil || s.Registry == nil || s.Compatible == nil || !s.Compatible(ctx) {
		return distribution.Availability{State: distribution.UnsupportedRuntime, Code: availabilityCode(distribution.UnsupportedRuntime)}, nil
	}
	return distribution.Availability{State: distribution.Ready, Code: availabilityCode(distribution.Ready)}, nil
}

func availabilityCode(state distribution.State) string {
	switch state {
	case distribution.Missing:
		return "CEF_INSTALL_MISSING"
	case distribution.Corrupt:
		return "CEF_INSTALL_CORRUPT"
	case distribution.Incompatible:
		return "CEF_INSTALL_INCOMPATIBLE"
	case distribution.UnsupportedRuntime:
		return "CEF_DSH_RUNTIME_UNSUPPORTED"
	case distribution.UnsupportedPlatform:
		return "CEF_PLATFORM_UNSUPPORTED"
	default:
		return "CEF_READY"
	}
}
func (s *embeddedBrowserService) nativePeer(ctx context.Context) (embeddedbrowser.PresenterPeer, string, error) {
	enabled := s != nil && s.Registry != nil && embeddedRuntimeReady()
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
func (s *embeddedBrowserService) Cover(ctx context.Context) ([]embeddedbrowser.Preview, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return nil, err
	}
	return s.Registry.Cover(ctx, peer, surface)
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

func (s *embeddedBrowserService) PrepareCloseInstance(ctx context.Context) error {
	peer, surface, err := s.nativePeer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.PrepareCloseInstance(ctx, peer, surface)
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

// ConfigureMacPresenter receives only the native registrar's private channel.
// It is not exposed by the frontend-bound EmbeddedBrowserService wrapper.
func (s *embeddedBrowserService) ConfigureMacPresenter(ctx context.Context, parent uint64, endpoint, token string) error {
	peer, _, err := s.nativePeer(ctx)
	if err != nil {
		return err
	}
	return embeddedbrowser.ConfigureMacPresenter(peer, uintptr(parent), endpoint, token)
}

func (s *embeddedBrowserService) AnswerDialog(ctx context.Context, slot string, dialog embeddedbrowser.WebDialog, accept bool, text string) error {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.AnswerDialog(ctx, peer, surface, slot, dialog, accept, text)
}

func (s *embeddedBrowserService) PresentDialog(ctx context.Context, slot string, dialog embeddedbrowser.WebDialog, ready bool) error {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return err
	}
	return s.Registry.PresentDialog(ctx, peer, surface, slot, dialog, ready)
}

func (s *embeddedBrowserService) CancelDownload(ctx context.Context, slot, id string) error {
	peer, surface, e := s.peer(ctx)
	if e != nil {
		return e
	}
	return s.Registry.CancelDownload(ctx, peer, surface, slot, id)
}

func (s *embeddedBrowserService) Popup(ctx context.Context, slot string, popup embeddedbrowser.WebPopup, action string) (string, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return "", err
	}
	return s.Registry.Popup(ctx, peer, surface, slot, popup, action)
}

func (s *embeddedBrowserService) OpenArtifact(ctx context.Context, parent uint64, windowLease, slot string) (embeddedbrowser.ViewState, error) {
	peer, surface, err := s.peer(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	return s.Registry.OpenArtifact(ctx, peer, surface, uintptr(parent), windowLease, slot)
}
