package dshmanager

import (
	"context"
)

// ApplyPlugin runs one plugin command for a profile no Worker is using: inside
// the Host's stopped-Worker transaction, or after the profile failed to start.
// The context value grants this one command its already-resolved launch.
type pluginLaunchKey struct{}

func (m *Manager) ApplyPlugin(ctx context.Context, launch ResolvedLaunch, packageSpec, operation string) (PluginResult, error) {
	if err := m.requireNoCurrentRunContext(); err != nil {
		return PluginResult{}, err
	}
	return m.runPluginCommand(context.WithValue(ctx, pluginLaunchKey{}, launch), PluginTarget{Profile: launch.Target.Profile}, packageSpec, operation)
}
