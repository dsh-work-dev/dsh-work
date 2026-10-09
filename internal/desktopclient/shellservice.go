package desktopclient

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/local/dsh-work/internal/app"
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
	// OnBuiltinComponentsChanged broadcasts transient DSH Client handshake state to every window.
	OnBuiltinComponentsChanged func([]BuiltinComponentStatus)

	componentsMu sync.RWMutex
	components   map[string]string
}

// BuiltinComponentStatus is the read-only load state of an application-owned DSH Client.
type BuiltinComponentStatus struct {
	ID    string `json:"id"`
	State string `json:"state"`
}

var builtinComponentIDs = [...]string{"shell", "account", "activity"}

// ShellPet is what the shell menu needs from the pet panel.
type ShellPet struct {
	Ready   bool `json:"ready"`
	Visible bool `json:"visible"`
}

func workspaceWindow(ctx context.Context) error {
	window, err := trustedWindow(ctx)
	if err != nil {
		return err
	}
	if window.Name() != "workspace" {
		return errors.New("workspace window required")
	}
	return nil
}

// BeginBuiltinComponents marks each built-in Client as loading for the current DSH frame.
func (s *ShellService) BeginBuiltinComponents(ctx context.Context) error {
	if err := workspaceWindow(ctx); err != nil {
		return err
	}
	s.componentsMu.Lock()
	s.components = make(map[string]string, len(builtinComponentIDs))
	for _, id := range builtinComponentIDs {
		s.components[id] = "loading"
	}
	s.componentsMu.Unlock()
	s.publishBuiltinComponents()
	return nil
}

// ResetBuiltinComponents marks the DSH Clients inactive when the frame is removed.
func (s *ShellService) ResetBuiltinComponents(ctx context.Context) error {
	if err := workspaceWindow(ctx); err != nil {
		return err
	}
	s.componentsMu.Lock()
	s.components = nil
	s.componentsMu.Unlock()
	s.publishBuiltinComponents()
	return nil
}

// ReportBuiltinComponent accepts a readiness handshake from the framed DSH Client.
func (s *ShellService) ReportBuiltinComponent(ctx context.Context, id string) error {
	if err := workspaceWindow(ctx); err != nil {
		return err
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
	if s.components[id] != "loading" {
		s.componentsMu.Unlock()
		return nil
	}
	s.components[id] = "loaded"
	s.componentsMu.Unlock()
	s.publishBuiltinComponents()
	return nil
}

// GetBuiltinComponents returns a detached status snapshot to the workspace or Settings window.
func (s *ShellService) GetBuiltinComponents(ctx context.Context) ([]BuiltinComponentStatus, error) {
	if _, err := trustedWindow(ctx); err != nil {
		return nil, err
	}
	return s.builtinComponentSnapshot(), nil
}

func (s *ShellService) builtinComponentSnapshot() []BuiltinComponentStatus {
	s.componentsMu.RLock()
	defer s.componentsMu.RUnlock()
	result := make([]BuiltinComponentStatus, 0, len(builtinComponentIDs))
	for _, id := range builtinComponentIDs {
		state := s.components[id]
		if state == "" {
			state = "inactive"
		}
		result = append(result, BuiltinComponentStatus{ID: id, State: state})
	}
	return result
}

func (s *ShellService) publishBuiltinComponents() {
	if s.OnBuiltinComponentsChanged != nil {
		s.OnBuiltinComponentsChanged(s.builtinComponentSnapshot())
	}
}

var shellSettingsSections = map[string]bool{
	"overview": true, "settings": true, "notifications": true, "pets": true,
	"runtimes": true, "profiles": true, "plugins": true, "data-directories": true, "about": true,
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
