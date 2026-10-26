package desktopapp

import (
	"sync"
	"time"
)

type trailingTimer interface {
	Stop() bool
}

type trailingClock interface {
	Now() time.Time
	AfterFunc(time.Duration, func()) trailingTimer
}

type realTrailingClock struct{}

func (realTrailingClock) Now() time.Time { return time.Now() }

func (realTrailingClock) AfterFunc(delay time.Duration, callback func()) trailingTimer {
	return time.AfterFunc(delay, callback)
}

// trailingPublisher limits a high-volume value stream while retaining the
// latest value at each interval boundary. merge keeps useful event payloads
// that can be combined without changing the latest state projection.
type trailingPublisher[T any] struct {
	mu       sync.Mutex
	interval time.Duration
	clock    trailingClock
	publish  func(T)
	merge    func(T, T) T

	lastPublished time.Time
	hasPublished  bool
	pending       *T
	timer         trailingTimer
	timerID       uint64

	outbox   []T
	draining bool
	closed   bool
}

func newTrailingPublisher[T any](interval time.Duration, clock trailingClock, publish func(T), merge func(T, T) T) *trailingPublisher[T] {
	if clock == nil {
		clock = realTrailingClock{}
	}
	return &trailingPublisher[T]{interval: interval, clock: clock, publish: publish, merge: merge}
}

// enqueue queues a publication and reports whether the caller owns draining
// the ordered output queue. Callers may invoke it under their state lock, then
// drain after releasing that lock.
func (p *trailingPublisher[T]) enqueue(value T, throttle bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || p.publish == nil {
		return false
	}

	now := p.clock.Now()
	if throttle && p.hasPublished && now.Sub(p.lastPublished) < p.interval {
		p.setPendingLocked(value)
		if p.timer == nil {
			remaining := p.interval - now.Sub(p.lastPublished)
			p.timerID++
			id := p.timerID
			p.timer = p.clock.AfterFunc(remaining, func() { p.flush(id) })
		}
		return false
	}

	if throttle && p.pending != nil && p.merge != nil {
		value = p.merge(*p.pending, value)
	}
	p.clearPendingLocked()
	p.lastPublished = now
	p.hasPublished = true
	return p.enqueueLocked(value)
}

func (p *trailingPublisher[T]) setPendingLocked(value T) {
	if p.pending == nil {
		copy := value
		p.pending = &copy
		return
	}
	if p.merge != nil {
		*p.pending = p.merge(*p.pending, value)
		return
	}
	*p.pending = value
}

func (p *trailingPublisher[T]) clearPendingLocked() {
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
		p.timerID++
	}
	p.pending = nil
}

func (p *trailingPublisher[T]) enqueueLocked(value T) bool {
	p.outbox = append(p.outbox, value)
	if p.draining {
		return false
	}
	p.draining = true
	return true
}

func (p *trailingPublisher[T]) flush(id uint64) {
	p.mu.Lock()
	if p.closed || p.timerID != id {
		p.mu.Unlock()
		return
	}
	p.timer = nil
	if p.pending == nil {
		p.mu.Unlock()
		return
	}
	value := *p.pending
	p.pending = nil
	p.lastPublished = p.clock.Now()
	p.hasPublished = true
	drain := p.enqueueLocked(value)
	p.mu.Unlock()
	if drain {
		p.drain()
	}
}

func (p *trailingPublisher[T]) drain() {
	for {
		p.mu.Lock()
		if p.closed || len(p.outbox) == 0 {
			p.outbox = nil
			p.draining = false
			p.mu.Unlock()
			return
		}
		value := p.outbox[0]
		var zero T
		p.outbox[0] = zero
		p.outbox = p.outbox[1:]
		p.mu.Unlock()
		p.publish(value)
	}
}

func (p *trailingPublisher[T]) close() {
	p.mu.Lock()
	p.closed = true
	p.clearPendingLocked()
	p.outbox = nil
	p.mu.Unlock()
}
