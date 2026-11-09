package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"errors"
	"sync"
)

type WindowAdmission struct {
	Lease   string `json:"lease"`
	Surface string `json:"surface"`
}
type artifactAdmission struct {
	cancel context.CancelFunc
	done   chan struct{}
}

type presenterWindow struct {
	artifactPick *artifactAdmission
	serial       sync.Mutex
	peer         PresenterPeer
	parent       uintptr
	owner        presenterOwner
	lease        string
	closed       bool
	finished     bool
	done         chan struct{}
}

func (r *PresenterRegistry) RegisterWindow(ctx context.Context, peer PresenterPeer, surface string, parent uintptr) (WindowAdmission, error) {
	if _, err := presenterBounds(peer, parent, SlotBounds{}, 1); err != nil {
		return WindowAdmission{}, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return WindowAdmission{}, ErrClosed
	}
	if r.windows == nil {
		r.windows = map[string]*presenterWindow{}
	}
	window := r.windows[surface]
	if window == nil {
		window = &presenterWindow{}
		r.windows[surface] = window
	}
	r.mu.Unlock()
	window.serial.Lock()
	defer window.serial.Unlock()
	if window.owner != nil {
		if !window.closed && window.peer == peer && window.parent == parent {
			return WindowAdmission{Lease: window.lease, Surface: surface}, nil
		}
		if presenterAlive(window.peer) {
			return WindowAdmission{}, errors.New("CEF_WINDOW_SURFACE_ALREADY_OWNED")
		}
		if err := r.closeWindowViews(ctx, window.peer, surface); err != nil {
			return WindowAdmission{}, err
		}
		if !window.finished {
			window.closed = true
			window.retireArtifactPick()
			window.finished = true
			close(window.done)
		}
		if r.WindowRetired != nil {
			r.WindowRetired(window.peer, surface)
		}
		if err := window.owner.Close(); err != nil {
			return WindowAdmission{}, err
		}
	}
	owner, err := admitPresenter(peer, parent)
	if err != nil {
		return WindowAdmission{}, err
	}
	window.peer = peer
	window.parent = parent
	window.owner = owner
	window.lease = rand.Text()
	window.closed = false
	window.finished = false
	window.done = make(chan struct{})
	lease := window.lease
	go func() {
		<-owner.Done()
		window.serial.Lock()
		if window.lease != lease || window.finished {
			window.serial.Unlock()
			return
		}
		wasClosed := window.closed
		window.closed = true
		window.retireArtifactPick()
		window.finished = true
		close(window.done)
		if !wasClosed && r.WindowRetired != nil {
			r.WindowRetired(peer, surface)
		}
		window.serial.Unlock()
		_ = r.closeWindowViews(context.Background(), peer, surface)
	}()

	return WindowAdmission{Lease: lease, Surface: surface}, nil
}

// lockWindow is held through target creation/presentation and document retirement.
func (r *PresenterRegistry) lockWindow(peer PresenterPeer, surface string, parent uintptr, lease string) (*presenterWindow, error) {
	r.mu.Lock()
	window := r.windows[surface]
	closed := r.closed
	r.mu.Unlock()
	if closed || window == nil {
		return nil, errors.New("CEF_WINDOW_ADMISSION_REQUIRED")
	}
	window.serial.Lock()
	if window.closed || window.peer != peer || window.parent != parent || lease == "" || window.lease != lease {
		window.serial.Unlock()
		return nil, errors.New("CEF_WINDOW_LEASE_REJECTED")
	}
	return window, nil
}
func (r *PresenterRegistry) RetireDocument(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, lease string) error {
	w, err := r.lockWindow(peer, surface, parent, lease)
	if err != nil {
		return err
	}
	defer w.serial.Unlock()
	w.retireArtifactPick()
	return r.closeWindowViews(ctx, peer, surface)
}
func (r *PresenterRegistry) WindowStreamScope(peer PresenterPeer, lease string) (string, <-chan struct{}, error) {
	r.mu.Lock()
	windows := make(map[string]*presenterWindow, len(r.windows))
	for surface, w := range r.windows {
		windows[surface] = w
	}
	r.mu.Unlock()
	for surface, w := range windows {
		w.serial.Lock()
		if w.lease == lease && w.peer == peer && !w.closed {
			parent, done := w.parent, w.done
			w.serial.Unlock()
			if _, err := presenterBounds(peer, parent, SlotBounds{}, 1); err != nil {
				return "", nil, err
			}
			return surface, done, nil
		}
		w.serial.Unlock()
	}
	return "", nil, errors.New("CEF_STREAM_WINDOW_LEASE_REJECTED")
}

// WithWindowLease serializes retirement against a replacement window admission.
// The callback must not call back into the registry.
func (r *PresenterRegistry) WithWindowLease(peer PresenterPeer, surface, lease string, fn func()) bool {
	r.mu.Lock()
	w := r.windows[surface]
	r.mu.Unlock()
	if w == nil {
		return false
	}
	w.serial.Lock()
	defer w.serial.Unlock()
	if w.peer != peer || w.lease != lease || w.closed {
		return false
	}
	fn()
	return true
}
func (r *PresenterRegistry) CloseWindow(ctx context.Context, peer PresenterPeer, surface string) error {
	r.mu.Lock()
	w := r.windows[surface]
	r.mu.Unlock()
	if w == nil {
		return nil
	}
	w.serial.Lock()
	if w.peer != peer {
		w.serial.Unlock()
		return errors.New("CEF_WINDOW_LEASE_REJECTED")
	}
	lease := w.lease
	if !w.closed {
		w.closed = true
		w.retireArtifactPick()
		if r.WindowRetired != nil {
			r.WindowRetired(peer, surface)
		}
	}
	w.serial.Unlock()
	if err := r.closeWindowViews(ctx, peer, surface); err != nil {
		return err
	}
	w.serial.Lock()
	defer w.serial.Unlock()
	if w.lease != lease {
		return errors.New("CEF_WINDOW_LEASE_REJECTED")
	}
	return r.finishWindowLocked(w)
}

func (r *PresenterRegistry) invalidateWindow(w *presenterWindow, surface string) {
	w.serial.Lock()
	defer w.serial.Unlock()
	if !w.closed {
		w.closed = true
		w.retireArtifactPick()
		if r.WindowRetired != nil {
			r.WindowRetired(w.peer, surface)
		}
	}
}
func (r *PresenterRegistry) finishWindow(w *presenterWindow) error {
	w.serial.Lock()
	defer w.serial.Unlock()
	return r.finishWindowLocked(w)
}
func (r *PresenterRegistry) finishWindowLocked(w *presenterWindow) error {
	if w.owner != nil {
		if err := w.owner.Close(); err != nil {
			return err
		}
		w.owner = nil
	}
	if !w.finished {
		w.finished = true
		close(w.done)
	}
	return nil
}

// Called with the window serial lock. The native picker closes its done barrier
// immediately after its actual STA drains, before reacquiring registry locks.
func (w *presenterWindow) retireArtifactPick() {
	if p := w.artifactPick; p != nil {
		p.cancel()
		<-p.done
		w.artifactPick = nil
	}
}
