package embeddedbrowser

import (
	"errors"
	"sync"
)

type downloadEvent struct {
	received, total   int64
	finished, success bool
	failure           string
}

// One mailbox and one consumer per issued grant: at most one replaceable
// progress snapshot plus one reliable terminal, including before start ACK.
// The pipe receiver publishes in wire order; callbacks never compete as tasks.
type downloadMailbox struct {
	mu                 sync.Mutex
	wake               chan struct{}
	progress, terminal *downloadEvent
	last               int64
	ended              bool
}

func newDownloadMailbox() *downloadMailbox { return &downloadMailbox{wake: make(chan struct{}, 1)} }
func (q *downloadMailbox) push(e downloadEvent) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ended || e.received < q.last || e.total < 0 {
		return errors.New("DOWNLOAD_PROGRESS_REJECTED")
	}
	q.last = e.received
	if e.finished {
		q.ended = true
		q.terminal = &e
	} else {
		q.progress = &e
	}
	select {
	case q.wake <- struct{}{}:
	default:
	}
	return nil
}
func (q *downloadMailbox) run(done, ready <-chan struct{}, apply func(downloadEvent)) {
	select {
	case <-done:
		return
	case <-ready:
	}
	for {
		select {
		case <-done:
			return
		case <-q.wake:
		}
		q.mu.Lock()
		progress, terminal := q.progress, q.terminal
		q.progress = nil
		q.terminal = nil
		q.mu.Unlock()
		if progress != nil {
			apply(*progress)
		}
		if terminal != nil {
			apply(*terminal)
			return
		}
	}
}
