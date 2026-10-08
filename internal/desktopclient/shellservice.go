package desktopclient

import (
	"context"
	"errors"
	"fmt"
)

// ShellService serves the trusted workbench shell from the UI process, which
// owns the windows; it never reaches the background.
type ShellService struct{ Open func(section string) }

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
