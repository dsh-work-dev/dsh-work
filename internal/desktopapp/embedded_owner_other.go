//go:build !darwin || !cgo

package desktopapp

import (
	"context"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/supervisor"
)

func embeddedBrowserSupervisor(base supervisor.Adapter, _ string) supervisor.Adapter { return base }
func startEmbeddedBroker(ctx context.Context, c embeddedbrowser.Config) (*embeddedbrowser.Broker, error) {
	return embeddedbrowser.Start(ctx, c)
}
