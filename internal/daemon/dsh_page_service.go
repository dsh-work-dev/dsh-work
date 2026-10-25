package daemon

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/dshinteraction"
)

// DSHPageService accepts callbacks from the framed DSH page. The caller's
// workspace surface comes from the authenticated /control envelope.
type DSHPageService struct {
	Activity     *dshactivity.Bridge
	Interactions *dshinteraction.Store
}

func (s *DSHPageService) ReportSessionOpened(ctx context.Context, id, reportedError string) error {
	if !isWorkspaceSurface(SurfaceFrom(ctx)) {
		return errors.New("workspace surface required")
	}
	if s == nil || s.Activity == nil {
		return errors.New("DSH activity is unavailable")
	}
	s.Activity.ReportSessionOpened(id, reportedError)
	return nil
}

// ReportInteraction records pending interaction state from the trusted DSH page.
func (s *DSHPageService) ReportInteraction(ctx context.Context, report dshinteraction.Report) error {
	surface := SurfaceFrom(ctx)
	if !isWorkspaceSurface(surface) {
		return errors.New("workspace surface required")
	}
	if s == nil || s.Interactions == nil {
		return errors.New("DSH interaction tracking is unavailable")
	}
	return s.Interactions.Report(surface, report)
}

// ResetInteractions removes all state reported by one DSH page instance.
func (s *DSHPageService) ResetInteractions(ctx context.Context, instance string) error {
	surface := SurfaceFrom(ctx)
	if !isWorkspaceSurface(surface) {
		return errors.New("workspace surface required")
	}
	if s == nil || s.Interactions == nil {
		return errors.New("DSH interaction tracking is unavailable")
	}
	s.Interactions.Reset(surface, instance)
	return nil
}
