package desktopclient

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"sync"
	"time"
)

// Native presenter fields are read from the trusted Wails context, never from
// JavaScript arguments. The daemon independently checks the pipe peer/HWND.
type embeddedGate struct {
	sync.Mutex
	blocked  bool
	document string
}
type EmbeddedBrowserService struct {
	Client      *daemon.Client
	Enabled     bool
	InstanceKey string
	QuitUI      func()
	gatesMu     sync.Mutex
	gates       map[string]*embeddedGate
}

func (s *EmbeddedBrowserService) Capability(ctx context.Context) (bool, error) {
	if !s.Enabled {
		return false, nil
	}
	var enabled bool
	err := call(ctx, s.Client, "EmbeddedBrowserService", "Capability", nil, &enabled)
	return enabled, err
}
func (s *EmbeddedBrowserService) Update(ctx context.Context, document, slot, address string, bounds embeddedbrowser.SlotBounds, visible bool) (embeddedbrowser.ViewState, error) {
	if !s.Enabled {
		return embeddedbrowser.ViewState{}, errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	window, err := trustedWindow(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	admission, err := s.RegisterWindow(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	gate := s.gate(window.Name())
	gate.Lock()
	defer gate.Unlock()
	if document == "" || gate.document != document {
		return embeddedbrowser.ViewState{}, errors.New("CEF_PRESENTER_DOCUMENT_RETIRED")
	}
	visible = visible && !gate.blocked
	var state embeddedbrowser.ViewState
	err = call(ctx, s.Client, "EmbeddedBrowserService", "Update", []any{uint64(uintptr(window.NativeWindow())), admission.Lease, slot, address, bounds, window.GetZoom(), visible}, &state)
	return state, err
}
func (s *EmbeddedBrowserService) State(ctx context.Context, document, slot string) (embeddedbrowser.ViewState, error) {
	gate, gateErr := s.lockDocument(ctx, document)
	if gateErr != nil {
		return embeddedbrowser.ViewState{}, gateErr
	}
	defer gate.Unlock()
	var state embeddedbrowser.ViewState
	if !s.Enabled {
		return state, errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	err := call(ctx, s.Client, "EmbeddedBrowserService", "State", []any{slot}, &state)
	return state, err
}
func (s *EmbeddedBrowserService) Hide(ctx context.Context) error {
	if !s.Enabled {
		return nil
	}
	return call(ctx, s.Client, "EmbeddedBrowserService", "Hide", nil, nil)
}
func (s *EmbeddedBrowserService) Detach(ctx context.Context, document, slot string) error {
	gate, gateErr := s.lockDocument(ctx, document)
	if gateErr != nil {
		return gateErr
	}
	defer gate.Unlock()
	if !s.Enabled {
		return nil
	}
	return call(ctx, s.Client, "EmbeddedBrowserService", "Detach", []any{slot}, nil)
}
func (s *EmbeddedBrowserService) Recover(ctx context.Context, document, slot, address string) (embeddedbrowser.ViewState, error) {
	window, err := trustedWindow(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	gate := s.gate(window.Name())
	gate.Lock()
	defer gate.Unlock()
	if document == "" || gate.document != document {
		return embeddedbrowser.ViewState{}, errors.New("CEF_PRESENTER_DOCUMENT_RETIRED")
	}
	admission, err := s.RegisterWindow(ctx)
	if err != nil {
		return embeddedbrowser.ViewState{}, err
	}
	var state embeddedbrowser.ViewState
	if !s.Enabled {
		return state, errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	err = call(ctx, s.Client, "EmbeddedBrowserService", "Recover", []any{uint64(uintptr(window.NativeWindow())), admission.Lease, slot, address}, &state)
	return state, err
}

// ExitUI is present only as a disabled-by-default development lifetime action;
// it quits this real UI process, not the daemon or another process.
func (s *EmbeddedBrowserService) ExitUI(ctx context.Context) error {
	if !s.Enabled || s.QuitUI == nil {
		return errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	if _, err := trustedWindow(ctx); err != nil {
		return err
	}
	cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer stop()
	// The binding runs off the GUI thread. Keep the message pump alive through
	// the native frame-close barrier before asking Wails to exit.
	if err := call(cleanup, s.Client, "EmbeddedBrowserService", "CloseInstance", nil, nil); err != nil {
		return err
	}
	go s.QuitUI()
	return nil
}
func (s *EmbeddedBrowserService) gate(surface string) *embeddedGate {
	s.gatesMu.Lock()
	defer s.gatesMu.Unlock()
	if s.gates == nil {
		s.gates = map[string]*embeddedGate{}
	}
	if s.gates[surface] == nil {
		s.gates[surface] = &embeddedGate{}
	}
	return s.gates[surface]
}
func (s *EmbeddedBrowserService) Overlay(ctx context.Context, blocked bool) error {
	if !s.Enabled {
		return nil
	}
	window, err := trustedWindow(ctx)
	if err != nil {
		return err
	}
	gate := s.gate(window.Name())
	gate.Lock()
	defer gate.Unlock()
	gate.blocked = blocked
	if blocked {
		return call(ctx, s.Client, "EmbeddedBrowserService", "Hide", nil, nil)
	}
	return nil
}
func (s *EmbeddedBrowserService) Action(ctx context.Context, document, slot, action string) error {
	gate, gateErr := s.lockDocument(ctx, document)
	if gateErr != nil {
		return gateErr
	}
	defer gate.Unlock()
	if !s.Enabled {
		return errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	return call(ctx, s.Client, "EmbeddedBrowserService", "Action", []any{slot, action}, nil)
}

func (s *EmbeddedBrowserService) RegisterWindow(ctx context.Context) (embeddedbrowser.WindowAdmission, error) {
	if !s.Enabled {
		return embeddedbrowser.WindowAdmission{}, errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	window, err := trustedWindow(ctx)
	if err != nil {
		return embeddedbrowser.WindowAdmission{}, err
	}
	var admission embeddedbrowser.WindowAdmission
	err = call(ctx, s.Client, "EmbeddedBrowserService", "RegisterWindow", []any{uint64(uintptr(window.NativeWindow())), s.InstanceKey}, &admission)
	return admission, err
}

// BeginDocument is called by the trusted shell before replacing its iframe.
// The gate retires old calls before the daemon's native frame-close barrier.
func (s *EmbeddedBrowserService) BeginDocument(ctx context.Context, document string) (bool, error) {
	if !s.Enabled {
		return false, nil
	}
	if len(document) < 16 || len(document) > 128 {
		return true, errors.New("CEF_DOCUMENT_REVISION_REQUIRED")
	}
	window, err := trustedWindow(ctx)
	if err != nil {
		return true, err
	}
	gate := s.gate(window.Name())
	gate.Lock()
	defer gate.Unlock()
	gate.document = ""
	admission, err := s.RegisterWindow(ctx)
	if err != nil {
		return true, err
	}
	err = call(ctx, s.Client, "EmbeddedBrowserService", "RetireDocument", []any{uint64(uintptr(window.NativeWindow())), admission.Lease}, nil)
	if err == nil {
		gate.document = document
	}
	return true, err
}

func (s *EmbeddedBrowserService) lockDocument(ctx context.Context, document string) (*embeddedGate, error) {
	if !s.Enabled {
		return nil, errors.New("CEF_PRESENTER_UNAVAILABLE")
	}
	w, err := trustedWindow(ctx)
	if err != nil {
		return nil, err
	}
	g := s.gate(w.Name())
	g.Lock()
	if document == "" || g.document != document {
		g.Unlock()
		return nil, errors.New("CEF_PRESENTER_DOCUMENT_RETIRED")
	}
	return g, nil
}

func (s *EmbeddedBrowserService) SelectControl(ctx context.Context, document, generation, session, slot, viewLease string) (automation.OwnedSelectionState, error) {
	gate, err := s.lockDocument(ctx, document)
	if err != nil {
		return automation.OwnedSelectionState{}, err
	}
	defer gate.Unlock()
	if gate.blocked {
		return automation.OwnedSelectionState{}, errors.New("BROWSER_VIEW_HIDDEN")
	}
	window, err := trustedWindow(ctx)
	if err != nil {
		return automation.OwnedSelectionState{}, err
	}
	admission, err := s.RegisterWindow(ctx)
	if err != nil {
		return automation.OwnedSelectionState{}, err
	}
	var result automation.OwnedSelectionState
	err = call(ctx, s.Client, "EmbeddedBrowserService", "SelectControl", []any{uint64(uintptr(window.NativeWindow())), admission.Lease, document, generation, session, slot, viewLease}, &result)
	return result, err
}
