package desktopapp

import "sync"

const focusReportQueueSize = 16

type focusReport struct {
	surface string
	focused bool
}

// focusReporter preserves the order of native focus events while keeping
// local IPC calls off the window event callback.
type focusReporter struct {
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
	queue     chan focusReport
	done      chan struct{}
	send      func(string, bool)
}

func newFocusReporter(send func(string, bool)) *focusReporter {
	reporter := &focusReporter{
		queue: make(chan focusReport, focusReportQueueSize),
		done:  make(chan struct{}),
		send:  send,
	}
	go reporter.run()
	return reporter
}

func (r *focusReporter) enqueue(surface string, focused bool) {
	if r == nil || surface == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.queue <- focusReport{surface: surface, focused: focused}
}

func (r *focusReporter) close() {
	if r != nil {
		r.closeOnce.Do(func() {
			r.mu.Lock()
			r.closed = true
			close(r.queue)
			r.mu.Unlock()
		})
		<-r.done
	}
}

func (r *focusReporter) run() {
	defer close(r.done)
	for update := range r.queue {
		if r.send != nil {
			r.send(update.surface, update.focused)
		}
	}
}
