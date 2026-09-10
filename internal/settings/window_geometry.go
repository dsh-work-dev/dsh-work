package settings

import (
	"context"
	"errors"
)

// Width and Height are logical normal-window dimensions, independent of maximisation.
type WindowGeometry struct {
	Width     int  `json:"width"`
	Height    int  `json:"height"`
	Maximised bool `json:"maximised"`
}

func (m *Manager) SetWindowGeometry(ctx context.Context, name string, geometry WindowGeometry) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if geometry.Width < 1 || geometry.Height < 1 || geometry.Width > 16384 || geometry.Height > 16384 {
		return errors.New("invalid window dimensions")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.values
	switch name {
	case "workspace":
		next.WorkspaceWindow = geometry
	case "settings":
		next.SettingsWindow = geometry
	default:
		return errors.New("unknown window")
	}
	if next.WorkspaceWindow == m.values.WorkspaceWindow && next.SettingsWindow == m.values.SettingsWindow {
		return nil
	}
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return err
	}
	m.values = next
	return nil
}
