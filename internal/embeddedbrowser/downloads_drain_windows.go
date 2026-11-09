//go:build windows

package embeddedbrowser

import (
	"context"
	"errors"
	"sync"
	"time"
)

func (c *windowsDownloads) quiesce() (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.quiescing || (c.closed && c.process != 0) {
		return nil, errors.New("DOWNLOAD_SHUTDOWN_BUSY")
	}
	c.quiescing = true
	var once sync.Once
	return func() { once.Do(func() { c.mu.Lock(); c.quiescing = false; c.mu.Unlock() }) }, nil
}

func downloadUnfinished(state string) bool {
	switch state {
	case "complete", "failed", "canceled", "revoked", "interrupted":
		return false
	}
	return true
}
func (c *windowsDownloads) busy() bool {
	c.mu.Lock()
	prompts := len(c.prompts)
	c.mu.Unlock()
	if prompts != 0 {
		return true
	}
	for _, s := range c.manager.Snapshots() {
		if downloadUnfinished(s.State) {
			return true
		}
	}
	return false
}

// Prompts own the whole approval/start handshake. Join them before canceling
// grants, so a late Save/confirmation answer cannot race a final native GET.
// Terminal manager state is set only after native writer drain and file cleanup.
func (c *windowsDownloads) drain(ctx context.Context, cancel bool) error {
	if cancel {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, 15*time.Second)
		defer stop()
	}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	canceled := false
	for {
		c.mu.Lock()
		if cancel {
			for _, p := range c.prompts {
				p.cancel()
			}
		}
		prompts := len(c.prompts)
		closed := c.closed
		joined := c.process == 0
		c.mu.Unlock()
		if closed {
			if joined {
				return c.manager.ReadyForShutdown()
			}
			return ErrClosed
		}
		if prompts == 0 && cancel && !canceled {
			for _, s := range c.manager.Snapshots() {
				if downloadUnfinished(s.State) {
					if err := c.cancel(ctx, s.ID, false); err != nil {
						return err
					}
				}
			}
			canceled = true
		}
		if prompts == 0 && !c.busy() {
			return c.manager.ReadyForShutdown()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}
