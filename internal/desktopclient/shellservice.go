package desktopclient

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/pet"
)

// ShellService serves the trusted workbench shell from the UI process, which
// owns the windows.
type ShellService struct {
	Open func(section string)
	// Pet reads the pet panel, or sets its visibility when visible is not nil.
	// The background grants pet controls to the Settings surface only, so the
	// UI process asks on its behalf, as the native menu did.
	Pet func(ctx context.Context, visible *bool) (app.PetPanel, error)
	// SaveZoomLevel stores the workbench zoom factor in the background settings.
	SaveZoomLevel func(ctx context.Context, zoom float64) error
	// OnBuiltinComponentsChanged reports transient DSH Client handshake state
	// to the window that owns the frame.
	OnBuiltinComponentsChanged func(string, []BuiltinComponentStatus)

	// BuiltinComponentTimeout marks components that never report as failed;
	// zero means defaultBuiltinComponentTimeout.
	BuiltinComponentTimeout time.Duration

	componentsMu    sync.RWMutex
	components      map[string]map[string]string
	componentsRound map[string]uint64
}

// DSH loads its plugins within seconds; a cold start with many profile plugins
// still finishes well inside this budget.
const defaultBuiltinComponentTimeout = 45 * time.Second

// BuiltinComponentStatus is the read-only load state of an application-owned DSH Client.
type BuiltinComponentStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

var builtinComponentIDs = [...]string{"shell", "account", "pet"}

// ShellPet is what the shell menu needs from the pet panel.
type ShellPet struct {
	Ready   bool `json:"ready"`
	Visible bool `json:"visible"`
}

// BeginBuiltinComponents marks each built-in Client as loading for the current DSH frame.
func (s *ShellService) BeginBuiltinComponents(ctx context.Context) error {
	window, err := trustedWindow(ctx)
	if err != nil {
		return err
	}
	surface := window.Name()
	if !workspaceWindowName(surface) {
		return errors.New("workspace window required")
	}
	s.componentsMu.Lock()
	if s.components == nil {
		s.components = make(map[string]map[string]string)
	}
	if s.componentsRound == nil {
		s.componentsRound = make(map[string]uint64)
	}
	s.componentsRound[surface]++
	round := s.componentsRound[surface]
	components := make(map[string]string, len(builtinComponentIDs))
	for _, id := range builtinComponentIDs {
		components[id] = "loading"
	}
	s.components[surface] = components
	s.componentsMu.Unlock()
	s.publishBuiltinComponents(surface)
	timeout := s.BuiltinComponentTimeout
	if timeout <= 0 {
		timeout = defaultBuiltinComponentTimeout
	}
	time.AfterFunc(timeout, func() { s.failUnreported(surface, round) })
	return nil
}

// failUnreported marks the components of one frame load that never reported.
func (s *ShellService) failUnreported(surface string, round uint64) {
	s.componentsMu.Lock()
	changed := false
	components := s.components[surface]
	if round == s.componentsRound[surface] {
		for id, state := range components {
			if state == "loading" {
				components[id] = "failed"
				logging.Module(logging.ModPlugin).Error("host plugin did not load", "plugin", "@dsh-work/"+id, "code", string(lifecycle.ErrorHostPluginLoadFailed))
				changed = true
			}
		}
	}
	s.componentsMu.Unlock()
	if changed {
		s.publishBuiltinComponents(surface)
	}
}

// ResetBuiltinComponents marks the DSH Clients inactive when the frame is removed.
func (s *ShellService) ResetBuiltinComponents(ctx context.Context) error {
	window, err := trustedWindow(ctx)
	if err != nil {
		return err
	}
	surface := window.Name()
	if !workspaceWindowName(surface) {
		return errors.New("workspace window required")
	}
	s.componentsMu.Lock()
	if s.componentsRound == nil {
		s.componentsRound = make(map[string]uint64)
	}
	s.componentsRound[surface]++
	delete(s.components, surface)
	s.componentsMu.Unlock()
	s.publishBuiltinComponents(surface)
	return nil
}

// ReportBuiltinComponent accepts a readiness handshake from the framed DSH Client.
func (s *ShellService) ReportBuiltinComponent(ctx context.Context, id string) error {
	window, err := trustedWindow(ctx)
	if err != nil {
		return err
	}
	surface := window.Name()
	if !workspaceWindowName(surface) {
		return errors.New("workspace window required")
	}
	known := false
	for _, candidate := range builtinComponentIDs {
		if id == candidate {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("unknown built-in component %q", id)
	}
	s.componentsMu.Lock()
	components := s.components[surface]
	if components[id] != "loading" {
		s.componentsMu.Unlock()
		return nil
	}
	components[id] = "loaded"
	s.componentsMu.Unlock()
	s.publishBuiltinComponents(surface)
	return nil
}

// GetBuiltinComponents returns a detached status snapshot to the workbench shell.
func (s *ShellService) GetBuiltinComponents(ctx context.Context) ([]BuiltinComponentStatus, error) {
	window, err := trustedWindow(ctx)
	if err != nil {
		return nil, err
	}
	if !workspaceWindowName(window.Name()) {
		return nil, errors.New("workspace window required")
	}
	return s.builtinComponentSnapshot(window.Name()), nil
}

func (s *ShellService) builtinComponentSnapshot(surface string) []BuiltinComponentStatus {
	s.componentsMu.RLock()
	defer s.componentsMu.RUnlock()
	result := make([]BuiltinComponentStatus, 0, len(builtinComponentIDs))
	for _, id := range builtinComponentIDs {
		state := s.components[surface][id]
		if state == "" {
			state = "inactive"
		}
		result = append(result, BuiltinComponentStatus{ID: id, State: state})
	}
	return result
}

func (s *ShellService) publishBuiltinComponents(surface string) {
	if s.OnBuiltinComponentsChanged != nil {
		s.OnBuiltinComponentsChanged(surface, s.builtinComponentSnapshot(surface))
	}
}

var shellSettingsSections = map[string]bool{
	"overview": true, "settings": true, "notifications": true, "pets": true,
	"runtimes": true, "profiles": true, "plugins": true, "data-directories": true, "diagnostics": true, "about": true,
}

// OpenSettings shows dsh-work Settings at a section the shell menu offers.
func (s *ShellService) OpenSettings(ctx context.Context, section string) error {
	if _, err := trustedWindow(ctx); err != nil {
		return err
	}
	if !shellSettingsSections[section] {
		return fmt.Errorf("unknown Settings section %q", section)
	}
	if s.Open == nil {
		return errors.New("Settings is unavailable")
	}
	go s.Open(section)
	return nil
}

// SaveZoom remembers the workbench zoom factor for the next start.
func (s *ShellService) SaveZoom(ctx context.Context, zoom float64) error {
	if _, err := trustedWindow(ctx); err != nil {
		return err
	}
	if s.SaveZoomLevel == nil {
		return errors.New("zoom cannot be saved")
	}
	return s.SaveZoomLevel(ctx, zoom)
}

// GetPet reports whether a pet is selected and visible.
func (s *ShellService) GetPet(ctx context.Context) (ShellPet, error) {
	return s.pet(ctx, nil)
}

// SetPetVisible shows or hides the selected pet.
func (s *ShellService) SetPetVisible(ctx context.Context, visible bool) (ShellPet, error) {
	return s.pet(ctx, &visible)
}

func (s *ShellService) pet(ctx context.Context, visible *bool) (ShellPet, error) {
	if _, err := trustedWindow(ctx); err != nil {
		return ShellPet{}, err
	}
	if s.Pet == nil {
		return ShellPet{}, errors.New("pet controls are unavailable")
	}
	panel, err := s.Pet(ctx, visible)
	if err != nil {
		return ShellPet{}, err
	}
	return ShellPet{Ready: panel.Runtime.SelectionStatus == pet.SelectionReady, Visible: panel.Runtime.EffectiveVisibility == pet.VisibilityVisible}, nil
}
