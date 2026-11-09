package desktopclient

import (
	"context"
	"github.com/local/dsh-work/internal/daemon"
)

type OverviewService struct{ Client *daemon.Client }

func (s *OverviewService) GetSnapshot(ctx context.Context, period, locale string, timezoneOffsetSeconds int) (daemon.OverviewSnapshot, error) {
	var snapshot daemon.OverviewSnapshot
	err := call(ctx, s.Client, "OverviewService", "GetSnapshot", []any{period, locale, timezoneOffsetSeconds}, &snapshot)
	return snapshot, err
}
