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

// WebView2 accepts zoom factors up to 5; Wails keeps them at or above actual size.
func validZoom(zoom float64) bool { return zoom >= 1 && zoom <= 5 }

// SetWorkspaceZoom remembers the workbench zoom factor for the next start.
func (m *Manager) SetWorkspaceZoom(ctx context.Context, zoom float64) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if !validZoom(zoom) {
		return errors.New("invalid zoom")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.values.WorkspaceZoom == zoom {
		return nil
	}
	next := m.values
	next.WorkspaceZoom = zoom
	if err := m.store.Save(ctx, m.path, next); err != nil {
		return err
	}
	m.values = next
	return nil
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
