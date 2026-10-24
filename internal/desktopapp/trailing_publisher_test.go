package desktopapp

import (
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/daemon"
)

type manualTrailingClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualTrailingTimer
}

type manualTrailingTimer struct {
	clock    *manualTrailingClock
	at       time.Time
	callback func()
	stopped  bool
	fired    bool
}

func newManualTrailingClock() *manualTrailingClock {
	return &manualTrailingClock{now: time.Unix(1, 0)}
}

func (c *manualTrailingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualTrailingClock) AfterFunc(delay time.Duration, callback func()) trailingTimer {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &manualTrailingTimer{clock: c, at: c.now.Add(delay), callback: callback}
	c.timers = append(c.timers, timer)
	return timer
}

func (t *manualTrailingTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.stopped || t.fired {
		return false
	}
	t.stopped = true
	return true
}

func (c *manualTrailingClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	var due []*manualTrailingTimer
	for _, timer := range c.timers {
		if !timer.stopped && !timer.fired && !timer.at.After(c.now) {
			timer.fired = true
			due = append(due, timer)
		}
	}
	c.mu.Unlock()
	for _, timer := range due {
		timer.callback()
	}
}

func TestUpdateProgressFlushesLatestValueAfterQuietInterval(t *testing.T) {
	for _, method := range []string{"setState", "updateState"} {
		t.Run(method, func(t *testing.T) {
			clock := newManualTrailingClock()
			var published []daemon.UpdateSnapshot
			runner := &updateRunner{
				current:      "1.0.0",
				state:        daemon.UpdateSnapshot{Phase: daemon.UpdateAvailable, CurrentVersion: "1.0.0", TargetVersion: "2.0.0", TotalBytes: 100},
				publishClock: clock,
				publish: func(state daemon.UpdateSnapshot) {
					published = append(published, state)
				},
			}

			runner.setState(daemon.UpdateSnapshot{Phase: daemon.UpdateDownloading, CurrentVersion: "1.0.0", TargetVersion: "2.0.0", TotalBytes: 100})
			for _, received := range []int64{12, 38, 79} {
				if method == "setState" {
					runner.setState(daemon.UpdateSnapshot{Phase: daemon.UpdateDownloading, CurrentVersion: "1.0.0", TargetVersion: "2.0.0", ReceivedBytes: received, TotalBytes: 100})
				} else {
					runner.updateState(func(state *daemon.UpdateSnapshot) { state.ReceivedBytes = received })
				}
			}
			if len(published) != 1 {
				t.Fatalf("published %d updates before interval end, want only the leading value", len(published))
			}

			clock.Advance(updateProgressPublishInterval - time.Nanosecond)
			if len(published) != 1 {
				t.Fatalf("published %d updates before the interval ended, want 1", len(published))
			}
			clock.Advance(time.Nanosecond)
			if len(published) != 2 || published[1].ReceivedBytes != 79 {
				t.Fatalf("trailing publication = %+v, want latest ReceivedBytes 79", published)
			}
			clock.Advance(updateProgressPublishInterval)
			if len(published) != 2 {
				t.Fatalf("unexpected duplicate trailing publication: %+v", published)
			}
		})
	}
}

func TestAcquisitionPublisherLimitsProgressAndBatchesOutputLines(t *testing.T) {
	clock := newManualTrailingClock()
	var published []acquisition.OperationStatus
	publisher := newTrailingPublisher(acquisitionPublishInterval, clock, func(status acquisition.OperationStatus) {
		published = append(published, status)
	}, mergeAcquisitionStatus)
	defer publisher.close()

	progress := func(received int64) acquisition.OperationStatus {
		return acquisition.OperationStatus{OperationID: "install-node", State: acquisition.OperationActive, Step: "download", ReceivedBytes: received, TotalBytes: 100, HasTotal: true}
	}
	for _, status := range []acquisition.OperationStatus{
		progress(0),
		progress(10),
		{OperationID: "install-node", State: acquisition.OperationActive, Step: "output", Log: "first line"},
		progress(70),
		{OperationID: "install-node", State: acquisition.OperationActive, Step: "output", Log: "last line"},
	} {
		if publisher.enqueue(status, true) {
			publisher.drain()
		}
	}
	if len(published) != 1 {
		t.Fatalf("published %d statuses inside the interval, want 1", len(published))
	}
	clock.Advance(acquisitionPublishInterval)
	if len(published) != 2 {
		t.Fatalf("published %d statuses after the interval, want 2", len(published))
	}
	got := published[1]
	if got.ReceivedBytes != 70 || got.Log != "first line\nlast line" || got.Step != "download" {
		t.Fatalf("trailing acquisition status = %+v, want latest progress and both output lines", got)
	}
}
