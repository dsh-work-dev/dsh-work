package dshmanager

import (
	"context"
	"github.com/local/dsh-work/internal/lifecycle"
)

type launchProgressKey struct{}

// WithLaunchProgress reports actual local validation boundaries to the Host.
func WithLaunchProgress(ctx context.Context, report func(lifecycle.Phase)) context.Context {
	return context.WithValue(ctx, launchProgressKey{}, report)
}
func reportLaunchPhase(ctx context.Context, phase lifecycle.Phase) {
	if report, ok := ctx.Value(launchProgressKey{}).(func(lifecycle.Phase)); ok {
		report(phase)
	}
}
