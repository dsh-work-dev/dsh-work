package dshmanager

import "context"

// ResolveStartupLaunch keeps the persisted environment intact. An installed
// executable alone does not prove compatibility with another profile's plugins.
func (m *Manager) ResolveStartupLaunch(ctx context.Context, request LaunchRequest) (ResolvedLaunch, error) {
	return m.ResolveLaunch(ctx, request)
}
