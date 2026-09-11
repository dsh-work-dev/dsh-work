package dshmanager

import (
	"context"
	"errors"
)

// ApplyPlugin is available only inside the Host's stopped-worker transaction.
// The context value grants this one command its already-resolved launch.
type pluginLaunchKey struct{}

func (m *Manager) ApplyPlugin(ctx context.Context, launch ResolvedLaunch, packageSpec, operation string) (PluginResult, error) {
	m.mu.RLock()
	allowed := m.switching && m.current == nil
	m.mu.RUnlock()
	if !allowed {
		return PluginResult{}, errors.New("plugin apply requires a stopped Run context transaction")
	}
	return m.runPluginCommand(context.WithValue(ctx, pluginLaunchKey{}, launch), PluginTarget{Profile: launch.Target.Profile}, packageSpec, operation)
}
