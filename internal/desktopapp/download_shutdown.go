package desktopapp

import (
	"context"
	"sync"

	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// downloadShutdownLease reuses the same admission barrier when a confirmed
// exit hands off to the updater and then the normal QuitFlow.
type downloadShutdownLease struct {
	mu                  sync.Mutex
	resume              func()
	begin               func(context.Context) (func(), error)
	cancelQuitAdmission func()
	preparing           chan struct{}
	prepareCancel       context.CancelFunc
	epoch               uint64
}

func (g *downloadShutdownLease) prepare(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		g.mu.Lock()
		if g.resume != nil || g.begin == nil {
			g.mu.Unlock()
			return nil
		}
		if pending := g.preparing; pending != nil {
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		operation, cancel := context.WithCancel(ctx)
		epoch := g.epoch
		g.preparing, g.prepareCancel = pending, cancel
		g.mu.Unlock()
		resume, err := g.begin(operation)
		cancel()
		g.mu.Lock()
		if g.epoch != epoch {
			if resume != nil {
				resume()
			}
			if err == nil {
				err = context.Canceled
			}
		} else if err == nil {
			g.resume = resume
		}
		g.preparing, g.prepareCancel = nil, nil
		close(pending)
		g.mu.Unlock()
		return err
	}
}
func (g *downloadShutdownLease) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.epoch++
	if g.prepareCancel != nil {
		g.prepareCancel()
	}
	if g.resume != nil {
		g.resume()
		g.resume = nil
	}
}

// withdraw is the explicit exit-abort boundary. Ordinary lease release does
// not cancel a successful runner quit's separate admission fence.
func (g *downloadShutdownLease) withdraw() {
	g.reset()
	if g.cancelQuitAdmission != nil {
		g.cancelQuitAdmission()
	}
}

func askDownloadShutdown(ctx context.Context, app *application.App, locale settings.Locale) (bool, error) {
	return askDownloadShutdownPlatform(ctx, app, locale)
}
