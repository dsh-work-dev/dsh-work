//go:build windows

package embeddedbrowser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	win "golang.org/x/sys/windows"
)

// Test-only: stop the real receiver at its existing map mutex, then fill the
// actual authenticated outbound pipe with non-coalescible control responses.
// The remote write handle is checked through DuplicateHandle, independently
// of EOF/events; no production hook, fabricated drain, or alternate transport.
func NativeBackpressureCancelForTest(ctx context.Context, b *Broker, id string) error {
	c, ok := b.downloads.(*windowsDownloads)
	if !ok {
		return errors.New("no Windows downloads")
	}
	c.mu.Lock()
	live := c.active[id]
	if live == nil {
		c.mu.Unlock()
		return errors.New("no issued transfer")
	}
	writer := live.writer
	c.mu.Unlock()
	b.mu.Lock()
	unlocked := false
	defer func() {
		if !unlocked {
			b.mu.Unlock()
		}
	}()
	// Each response echoes this Host request token. Three MiB exceeds the real
	// pipe buffer while staying below the sender's bounded queue budget.
	for n := 0; n < 3; n++ {
		if e := b.write(map[string]any{"kind": "target.state", "route": live.owner, "request": strings.Repeat("q", 1<<20)}); e != nil {
			return e
		}
	}
	time.Sleep(150 * time.Millisecond)
	cancelDone := make(chan error, 1)
	go func() { cancelDone <- c.manager.Cancel(ctx, id, false) }()
	if e := b.write(map[string]any{"kind": "download.cancel", "request": "owned-backpressure-cancel", "arguments": map[string]any{"id": id, "writer": fmt.Sprint(writer)}}); e != nil {
		return e
	}
	var closed bool
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var duplicate win.Handle
		e := win.DuplicateHandle(c.process, win.Handle(writer), win.CurrentProcess(), &duplicate, 0, false, win.DUPLICATE_SAME_ACCESS)
		if errors.Is(e, win.ERROR_INVALID_HANDLE) {
			closed = true
			break
		}
		if e != nil {
			return e
		}
		win.CloseHandle(duplicate)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	b.mu.Unlock()
	unlocked = true
	if !closed {
		return errors.New("native writer did not close under real Host-read backpressure")
	}
	select {
	case e := <-cancelDone:
		return e
	case <-ctx.Done():
		return ctx.Err()
	}
}
