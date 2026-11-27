package splash

import (
	"context"
	"errors"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// SplashService is the splash API for the workbench window. It runs in the UI
// process; the daemon has no part in the splash.
type SplashService struct {
	Store *Store
	// ChooseFile opens the native picker; "" means the user cancelled.
	ChooseFile func() (string, error)
}

func workspace(ctx context.Context) error {
	window, ok := ctx.Value(application.WindowKey).(application.Window)
	if !ok || window == nil || window.Name() != "workspace" {
		return errors.New("trusted desktop window required")
	}
	return nil
}

func (s *SplashService) List(ctx context.Context) ([]Splash, error) {
	if err := workspace(ctx); err != nil {
		return nil, err
	}
	return s.Store.List()
}

func (s *SplashService) Current(ctx context.Context) (Splash, error) {
	if err := workspace(ctx); err != nil {
		return Splash{}, err
	}
	return s.Store.Current(), nil
}

func (s *SplashService) Select(ctx context.Context, id string) (Splash, error) {
	if err := workspace(ctx); err != nil {
		return Splash{}, err
	}
	return s.Store.Select(id)
}

// Import returns nil when the user cancels the picker.
func (s *SplashService) Import(ctx context.Context) (*Splash, error) {
	if err := workspace(ctx); err != nil {
		return nil, err
	}
	path, err := s.ChooseFile()
	if err != nil || path == "" {
		return nil, err
	}
	imported, err := s.Store.Import(path)
	if err != nil {
		return nil, err
	}
	return &imported, nil
}

func (s *SplashService) Remove(ctx context.Context, id string) error {
	if err := workspace(ctx); err != nil {
		return err
	}
	return s.Store.Remove(id)
}
