package daemon

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/dshactivity"
)

// DSHPageService accepts callbacks from the framed DSH page. The caller's
// workspace surface comes from the authenticated /control envelope.
type DSHPageService struct {
	Activity *dshactivity.Bridge
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
