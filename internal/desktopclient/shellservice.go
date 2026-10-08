package desktopclient

import (
	"context"
	"errors"
	"fmt"

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
}

// ShellPet is what the shell menu needs from the pet panel.
type ShellPet struct {
	Ready   bool `json:"ready"`
	Visible bool `json:"visible"`
}

var shellSettingsSections = map[string]bool{"settings": true, "about": true}

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
