package dshmanager

import "context"

// Startup also creates the app-owned profile when it is missing.
func (m *Manager) ResolveStartupLaunch(ctx context.Context, request LaunchRequest) (ResolvedLaunch, error) {
	resolved, err := m.ResolveLaunch(ctx, request)
	if err != nil {
		return ResolvedLaunch{}, err
	}
	m.mu.RLock()
	config := m.configSnapshotLocked()
	m.mu.RUnlock()
	if err := ensureAppProfile(config, resolved.DataDirectory, resolved.Target.Profile.Name); err != nil {
		return ResolvedLaunch{}, err
	}
	return resolved, nil
}
