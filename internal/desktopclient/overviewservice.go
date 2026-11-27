package desktopclient

import (
	"context"
	"errors"

	"github.com/local/dsh-work/internal/daemon"
)

type OverviewService struct{ Client *daemon.Client }

func (s *OverviewService) GetSnapshot(ctx context.Context, period, locale string, timezoneOffsetSeconds int) (daemon.OverviewSnapshot, error) {
	var snapshot daemon.OverviewSnapshot
	err := call(ctx, s.Client, "OverviewService", "GetSnapshot", []any{period, locale, timezoneOffsetSeconds}, &snapshot)
	if errors.Is(err, context.Canceled) {
		// The browser already rejected the cancelled Wails promise. Resolve
		// native cleanup quietly instead of reporting a second, unhandled error.
		return daemon.OverviewSnapshot{}, nil
	}
	return snapshot, err
}
