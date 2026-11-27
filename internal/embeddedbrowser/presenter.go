package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type PresenterPeer struct {
	PID     uint32
	Started uint64
}
type SlotBounds struct{ X, Y, Width, Height float64 }
type PixelBounds struct{ X, Y, Width, Height int }
type ViewState struct {
	Instance         string `json:"instance,omitempty"`
	BrokerGeneration string `json:"brokerGeneration,omitempty"`
	Title            string `json:"title"`
	CanGoBack        bool   `json:"canGoBack"`
	CanGoForward     bool   `json:"canGoForward"`
	Lease            string `json:"lease"`
	State            string `json:"state"`
	URL              string `json:"url"`
	Visible          bool   `json:"visible"`
	Target           string `json:"target"`
	Document         string `json:"document"`
	// Read-only CEF identity evidence; never accepts authority from a caller.
	Frame   string      `json:"frame,omitempty"`
	Epoch   string      `json:"epoch"`
	Bounds  PixelBounds `json:"bounds"`
	Failure string      `json:"failure,omitempty"`
}
type presenterOwner interface {
	Done() <-chan struct{}
	Close() error
}
type presentation struct {
	serial  sync.Mutex
	peer    PresenterPeer
	surface string
	parent  uintptr
	owner   presenterOwner
	target  *OwnedTarget
	view    ViewState
	epoch   uint64
	cleaned bool
}

// PresenterRegistry owns admitted UI instances, native windows and view leases.
// A slot only proposes geometry; process evidence and HWND ownership admit it.
type PresenterRegistry struct {
	mu            sync.Mutex
	broker        *Broker
	views         map[string]*presentation
	windows       map[string]*presenterWindow
	WindowRetired func(PresenterPeer, string)
	closed        bool
}

func NewPresenterRegistry(b *Broker) *PresenterRegistry {
	return &PresenterRegistry{broker: b, views: map[string]*presentation{}}
}
func presentationKey(surface, slot string) string { return surface + "/" + slot }
func (r *PresenterRegistry) get(surface, slot string) *presentation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.views[presentationKey(surface, slot)]
}
func (r *PresenterRegistry) watch(p *presentation, owner presenterOwner) {
	go func() {
		<-owner.Done()
		p.serial.Lock()
		defer p.serial.Unlock()
		if p.owner != owner {
			return
		}
		_ = r.loseLocked(p)
	}()
}
func (r *PresenterRegistry) Update(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, windowLease, slot, address string, bounds SlotBounds, zoom float64, visible bool) (ViewState, error) {
	w, err := r.lockWindow(peer, surface, parent, windowLease)
	if err != nil {
		return ViewState{}, err
	}
	defer w.serial.Unlock()
	return r.updateLocked(ctx, peer, surface, parent, slot, address, bounds, zoom, visible)
}
func (r *PresenterRegistry) updateLocked(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, slot, address string, bounds SlotBounds, zoom float64, visible bool) (ViewState, error) {
	if len(slot) == 0 || len(slot) > 1024 {
		return ViewState{}, errors.New("CEF_SLOT_REJECTED")
	}
	pixels, err := presenterBounds(peer, parent, bounds, zoom)
	if err != nil {
		return ViewState{}, err
	}
	key := presentationKey(surface, slot)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ViewState{}, ErrClosed
	}
	p := r.views[key]
	if r.broker.ControlPaused() {
		r.mu.Unlock()
		if p == nil {
			return ViewState{State: "controlPaused", Failure: ErrControlPaused.Error()}, ErrControlPaused
		}
		p.serial.Lock()
		defer p.serial.Unlock()
		if p.peer != peer || p.parent != parent {
			return ViewState{}, errors.New("CEF_WINDOW_LEASE_REJECTED")
		}
		view, err := r.pausePresentationLocked(ctx, p, pixels, visible)
		if err == nil && address != "" {
			err = ErrControlPaused
		}
		return view, err
	}
	if p == nil && address == "" {
		r.mu.Unlock()
		return ViewState{State: "empty"}, nil
	}
	if p == nil {
		p = &presentation{surface: surface, peer: peer, parent: parent, view: ViewState{Lease: rand.Text(), State: "empty", URL: address}}
		r.views[key] = p
	}
	r.mu.Unlock()
	p.serial.Lock()
	defer p.serial.Unlock()
	if p.peer != peer || p.parent != parent {
		if p.view.State != "NeedsReload" || !p.cleaned || presenterAlive(p.peer) {
			return ViewState{}, errors.New("CEF_WINDOW_LEASE_REJECTED")
		}
		// A dead UI may recover only the descriptor, never the old native lease.
		verified, err := admitPresenter(peer, parent)
		if err != nil {
			return ViewState{}, err
		}
		if err = verified.Close(); err != nil {
			return ViewState{}, err
		}
		p.owner = nil
		p.peer = peer
		p.parent = parent
		p.view.Lease = rand.Text()
		p.view.Visible = false
	}
	if p.view.State == "NeedsReload" {
		return p.view, nil
	}
	if p.owner == nil {
		owner, e := admitPresenter(peer, parent)
		if e != nil {
			return ViewState{}, e
		}
		p.owner = owner
		r.watch(p, owner)
	}
	if p.target == nil {
		if address == "" {
			return ViewState{}, errors.New("CEF_TARGET_REQUIRED")
		}
		target, e := r.broker.openManagedWebTarget(ctx, address)
		if e != nil {
			return p.view, e
		}
		p.target = target
	}
	if address != "" && address != p.view.URL {
		if e := p.target.Navigate(ctx, address); e != nil {
			return p.view, e
		}
	}
	if address == "" && p.view.Bounds == pixels && p.view.Visible == visible && p.view.State != "empty" && p.view.State != "presentationFailed" {
		if err := r.refreshLocked(ctx, p); err != nil {
			return p.view, err
		}
		return p.view, nil
	}
	p.epoch++
	if pixels.Width <= 0 || pixels.Height <= 0 {
		visible = false
	}
	parameters, err := presenterParameters(peer, parent)
	if err != nil {
		return p.view, err
	}
	if parameters == nil {
		parameters = map[string]any{}
	}
	parameters["parent"] = fmt.Sprint(parent)
	parameters["pid"] = int(peer.PID)
	parameters["started"] = fmt.Sprint(peer.Started)
	parameters["view"] = p.view.Lease
	parameters["epoch"] = fmt.Sprint(p.epoch)
	parameters["bounds"] = pixels
	parameters["visible"] = visible
	_, err = p.target.broker.controlCall(ctx, "target.present", p.target.owner, parameters)
	if err != nil {
		p.view.State = "presentationFailed"
		p.view.Visible = false
		p.view.Failure = err.Error()
		return p.view, err
	}
	if err := r.refreshLocked(ctx, p); err != nil {
		p.view.State = "loading"
		p.view.Failure = err.Error()
	}
	p.view.Failure = ""
	p.view.Visible = visible
	p.view.Epoch = fmt.Sprint(p.epoch)
	p.view.Bounds = pixels
	return p.view, nil
}

// A paused document may stay alive, but never at obsolete visible geometry.
// Existing hide/unmount is still an acknowledged native operation; a failed
// hide keeps the owner and the last known visible state for an explicit retry.
func (r *PresenterRegistry) pausePresentationLocked(ctx context.Context, p *presentation, bounds PixelBounds, visible bool) (ViewState, error) {
	p.view.State = "controlPaused"
	p.view.Failure = ErrControlPaused.Error()
	if p.target != nil && (!visible || p.view.Bounds != bounds) {
		p.epoch++
		_, err := p.target.broker.controlCall(ctx, "target.hide", p.target.owner, map[string]any{"epoch": fmt.Sprint(p.epoch)})
		if err != nil {
			p.view.Failure = err.Error()
			return p.view, err
		}
		p.view.Visible = false
		p.view.Epoch = fmt.Sprint(p.epoch)
	}
	return p.view, nil
}
func (r *PresenterRegistry) State(ctx context.Context, peer PresenterPeer, surface, slot string) (ViewState, error) {
	p := r.get(surface, slot)
	if p == nil {
		return ViewState{}, ErrClosed
	}
	p.serial.Lock()
	defer p.serial.Unlock()
	if p.peer != peer {
		return ViewState{}, errors.New("CEF_WINDOW_LEASE_REJECTED")
	}
	if r.broker.ControlPaused() {
		p.view.State = "controlPaused"
		p.view.Failure = ErrControlPaused.Error()
		return p.view, nil
	}
	if p.view.State == "NeedsReload" || p.view.State == "presentationFailed" {
		return p.view, nil
	}
	if p.target == nil {
		return p.view, nil
	}
	if err := r.refreshLocked(ctx, p); err != nil {
		_ = r.loseLocked(p)
	}
	return p.view, nil
}
func (r *PresenterRegistry) Hide(ctx context.Context, peer PresenterPeer, surface string) error {
	r.mu.Lock()
	views := make([]*presentation, 0, len(r.views))
	for _, p := range r.views {
		if p.surface == surface {
			views = append(views, p)
		}
	}
	r.mu.Unlock()
	var result error
	for _, p := range views {
		p.serial.Lock()
		if p.peer == peer && p.target != nil && p.view.State != "NeedsReload" {
			p.epoch++
			_, err := p.target.broker.controlCall(ctx, "target.hide", p.target.owner, map[string]any{"epoch": fmt.Sprint(p.epoch)})
			result = errors.Join(result, err)
			p.view.Visible = false
			p.view.Epoch = fmt.Sprint(p.epoch)
		}
		p.serial.Unlock()
	}
	return result
}

// loss invalidates the descriptor before awaiting native frame-close evidence.
// Failure retains both target and process owners so Detach/Close can retry.
func (r *PresenterRegistry) loseLocked(p *presentation) error {
	p.view.State = "NeedsReload"
	p.view.Frame = ""
	p.view.Visible = false
	p.epoch++
	if p.cleaned {
		return nil
	}
	ctx, stop := context.WithTimeout(context.Background(), 8*time.Second)
	defer stop()
	if p.target != nil {
		if err := p.target.CloseNative(ctx); err != nil {
			p.view.Failure = err.Error()
			return err
		}
	}
	if p.owner != nil {
		if err := p.owner.Close(); err != nil {
			p.view.Failure = err.Error()
			return err
		}
	}
	p.owner = nil
	p.cleaned = true
	p.target = nil
	p.view.Failure = ""
	return nil
}
func (r *PresenterRegistry) Detach(ctx context.Context, peer PresenterPeer, surface, slot string) error {
	p := r.get(surface, slot)
	if p == nil {
		return nil
	}
	p.serial.Lock()
	defer p.serial.Unlock()
	if p.peer != peer {
		return errors.New("CEF_WINDOW_LEASE_REJECTED")
	}
	return r.loseLocked(p)
}
func (r *PresenterRegistry) Recover(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, windowLease, slot, address string) (ViewState, error) {
	w, err := r.lockWindow(peer, surface, parent, windowLease)
	if err != nil {
		return ViewState{}, err
	}
	defer w.serial.Unlock()
	p := r.get(surface, slot)
	if p == nil {
		return ViewState{}, ErrClosed
	}
	p.serial.Lock()
	if p.peer != peer || p.parent != parent || p.view.State != "NeedsReload" {
		p.serial.Unlock()
		return ViewState{}, errors.New("CEF_RELOAD_NOT_AVAILABLE")
	}
	if !p.cleaned {
		if err := r.loseLocked(p); err != nil {
			view := p.view
			p.serial.Unlock()
			return view, err
		}
	}
	p.owner = nil
	p.view.Lease = rand.Text()
	p.view.State = "empty"
	p.cleaned = false
	if address == "" {
		address = p.view.URL
	}
	p.view.URL = ""
	p.serial.Unlock()
	return r.updateLocked(ctx, peer, surface, parent, slot, address, SlotBounds{}, 1, false)
}

// drain invalidates admission before waiting for native execution-context
// destruction. It deliberately leaves stream completion/process observers alive.
func (r *PresenterRegistry) drain() error {
	r.mu.Lock()
	r.closed = true
	views := make([]*presentation, 0, len(r.views))
	for _, p := range r.views {
		views = append(views, p)
	}
	windows := make(map[string]*presenterWindow, len(r.windows))
	for s, w := range r.windows {
		windows[s] = w
	}
	r.mu.Unlock()
	for surface, w := range windows {
		r.invalidateWindow(w, surface)
	}
	var result error
	for _, p := range views {
		p.serial.Lock()
		result = errors.Join(result, r.loseLocked(p))
		p.serial.Unlock()
	}
	return result
}
func (r *PresenterRegistry) finish() error {
	r.mu.Lock()
	windows := make([]*presenterWindow, 0, len(r.windows))
	for _, w := range r.windows {
		windows = append(windows, w)
	}
	r.mu.Unlock()
	var result error
	for _, w := range windows {
		result = errors.Join(result, r.finishWindow(w))
	}
	return result
}
func (r *PresenterRegistry) Close() error {
	if err := r.drain(); err != nil {
		return err
	}
	return r.finish()
}

func (r *PresenterRegistry) closeWindowViews(ctx context.Context, peer PresenterPeer, surface string) error {
	r.mu.Lock()
	views := make([]*presentation, 0)
	for _, p := range r.views {
		if p.surface == surface {
			views = append(views, p)
		}
	}
	r.mu.Unlock()
	var err error
	for _, p := range views {
		p.serial.Lock()
		if p.peer == peer {
			err = errors.Join(err, r.loseLocked(p))
		}
		p.serial.Unlock()
	}
	return err
}

type navigationState struct {
	URL, Title                       string
	Loading, CanGoBack, CanGoForward bool
}

func (r *PresenterRegistry) refreshLocked(ctx context.Context, p *presentation) error {
	raw, err := p.target.broker.controlCall(ctx, "target.navigation", p.target.owner, nil)
	if err != nil {
		return err
	}
	var state navigationState
	if json.Unmarshal(raw, &state) != nil {
		return errors.New("CEF_NAVIGATION_STATE_REJECTED")
	}
	p.view.URL = state.URL
	p.view.Title = state.Title
	p.view.CanGoBack = state.CanGoBack
	p.view.CanGoForward = state.CanGoForward
	p.view.State = "ready"
	if state.Loading {
		p.view.State = "loading"
		p.view.Frame = ""
	}
	if !state.Loading {
		if identity, e := p.target.OwnedIdentity(ctx); e == nil {
			p.view.Target = identity.Target
			p.view.Document = identity.Document
			p.view.Frame = identity.Frame
		} else {
			p.view.State = "loading"
			p.view.Frame = ""
		}
	}
	return nil
}
func (r *PresenterRegistry) Action(ctx context.Context, peer PresenterPeer, surface, slot, action string) error {
	p := r.get(surface, slot)
	if p == nil {
		return ErrClosed
	}
	p.serial.Lock()
	defer p.serial.Unlock()
	if p.peer != peer || p.target == nil || p.view.State == "NeedsReload" {
		return ErrClosed
	}
	_, err := p.target.broker.controlCall(ctx, "target.action", p.target.owner, map[string]any{"action": action})
	return err
}
