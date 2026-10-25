package daemon

import (
	"context"
	"errors"
	"unicode/utf8"

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
	if err := validateInteractionReport(report); err != nil {
		return err
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
	if err := validateInteractionInstance(instance); err != nil {
		return err
	}
	if s == nil || s.Interactions == nil {
		return errors.New("DSH interaction tracking is unavailable")
	}
	s.Interactions.Reset(surface, instance)
	return nil
}

func validateInteractionReport(report dshinteraction.Report) error {
	if report.Instance == "" {
		return errors.New("interaction instance is required")
	}
	if utf8.RuneCountInString(report.Instance) > 256 {
		return errors.New("interaction instance is too long")
	}
	if utf8.RuneCountInString(report.SessionID) > 256 {
		return errors.New("interaction session ID is too long")
	}
	switch report.Kind {
	case "", "approval", "question", "plan-review":
	default:
		return errors.New("interaction kind is invalid")
	}
	if report.Kind != "" && report.Key == "" {
		return errors.New("interaction key is required")
	}
	if utf8.RuneCountInString(report.Key) > 256 {
		return errors.New("interaction key is too long")
	}
	if utf8.RuneCountInString(report.ToolName) > 120 {
		return errors.New("interaction tool name is too long")
	}
	if utf8.RuneCountInString(report.Current) > 256 {
		return errors.New("current session ID is too long")
	}
	return nil
}

func validateInteractionInstance(instance string) error {
	if instance == "" {
		return errors.New("interaction instance is required")
	}
	if utf8.RuneCountInString(instance) > 256 {
		return errors.New("interaction instance is too long")
	}
	return nil
}
