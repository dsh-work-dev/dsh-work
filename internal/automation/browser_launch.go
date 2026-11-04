package automation

import (
	"context"
	"errors"
	"time"

	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/settings"
)

func (s *Service) cancelBrowserOpeningLocked() {
	if s.profiles.launchCancel != nil {
		s.profiles.launchCancel()
	}
}
func (s *Service) ConfigureProfileLauncher(launcher browserhost.ProfileLauncher) {
	s.mu.Lock()
	s.profiles.launcher = launcher
	s.mu.Unlock()
}

// ConfigureBrowserLaunch associates a user-chosen folder with an already bound,
// selected, live instance. It is a launch hint, never a replacement identity.
func (s *Service) ConfigureBrowserLaunch(ctx context.Context, id, directory string) error {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.root.Err() != nil || !s.state.CanChange || s.profiles.prefs.Selected != id {
		s.mu.Unlock()
		return ErrRevoked
	}
	prefs := s.profiles.prefs
	prefs.Bindings = append([]settings.BrowserBinding(nil), prefs.Bindings...)
	index := -1
	for i, b := range prefs.Bindings {
		if b.ID == id {
			index = i
		}
	}
	if index < 0 {
		s.mu.Unlock()
		return ErrBrowserBinding
	}
	launcher := s.profiles.launcher
	var executable string
	if directory != "" {
		connection, err := s.selectedConnectionLocked()
		if err != nil {
			s.mu.Unlock()
			return err
		}
		executable = s.profiles.connections[connection].executable
		if executable == "" || launcher == nil {
			s.mu.Unlock()
			return errors.New("BROWSER_PROFILE_LAUNCH_UNAVAILABLE")
		}
	}
	s.mu.Unlock()
	if directory == "" {
		prefs.Bindings[index].Launch = nil
	} else {
		hint, err := launcher.Resolve(prefs.Bindings[index].Browser, executable, directory)
		if err != nil {
			return err
		}
		prefs.Bindings[index].Launch = &hint
	}
	if err := s.saveProfiles(ctx, prefs); err != nil {
		return err
	}
	s.mu.Lock()
	s.profiles.prefs = prefs
	s.cancelBrowserOpeningLocked()
	s.publishLocked()
	s.mu.Unlock()
	return nil
}

// OpenBrowserProfile is connection preparation for a trusted settings action or
// a future authorized gateway. It does not execute tools or create MCP/routes.
// The Factory never calls it while normal product execution remains closed.
func (s *Service) OpenBrowserProfile(ctx context.Context, id string) error {
	s.op.Lock()
	s.mu.Lock()
	if s.root.Err() != nil || !s.state.CanChange || !s.slots[Browser].state.Enabled || !s.state.DefaultBrowserEnabled || s.profiles.prefs.Selected != id {
		s.mu.Unlock()
		s.op.Unlock()
		return ErrRevoked
	}
	if _, err := s.selectedConnectionLocked(); err == nil {
		s.mu.Unlock()
		s.op.Unlock()
		return nil
	} else if !errors.Is(err, ErrBrowserOffline) {
		s.mu.Unlock()
		s.op.Unlock()
		return err
	}
	var binding settings.BrowserBinding
	for _, b := range s.profiles.prefs.Bindings {
		if b.ID == id {
			binding = b
		}
	}
	if binding.Launch == nil || s.profiles.launcher == nil {
		s.mu.Unlock()
		s.op.Unlock()
		return errors.New("BROWSER_PROFILE_LAUNCH_NOT_CONFIGURED")
	}
	if s.profiles.opening != "" {
		s.mu.Unlock()
		s.op.Unlock()
		return errors.New("BROWSER_PROFILE_LAUNCH_IN_PROGRESS")
	}
	launcher, hint := s.profiles.launcher, *binding.Launch
	run, cancel := context.WithCancel(ctx)
	stopRoot := context.AfterFunc(s.root, cancel)
	s.profiles.launchCancel = cancel
	s.profiles.opening = id
	s.publishLocked()
	s.mu.Unlock()
	s.op.Unlock()
	defer func() {
		cancel()
		stopRoot()
		s.mu.Lock()
		s.profiles.launchCancel = nil
		s.profiles.opening = ""
		s.publishLocked()
		s.mu.Unlock()
	}()
	if err := launcher.Open(run, binding.Browser, hint); err != nil {
		return err
	}
	deadline := time.NewTimer(12 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if err := run.Err(); err != nil {
			return err
		}
		s.mu.Lock()
		selected := s.profiles.prefs.Selected
		_, err := s.selectedConnectionLocked()
		s.mu.Unlock()
		if selected != id {
			return ErrRevoked
		}
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrBrowserOffline) {
			return err
		}
		select {
		case <-run.Done():
			return run.Err()
		case <-s.root.Done():
			return ErrRevoked
		case <-deadline.C:
			return errors.New("BROWSER_SELECTED_PROFILE_NOT_CONNECTED")
		case <-tick.C:
		}
	}
}
