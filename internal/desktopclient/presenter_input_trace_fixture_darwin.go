//go:build darwin && cgo && e67previewfixture && !server

package desktopclient

import (
	"encoding/json"
	"sync"
	"sync/atomic"
)

type macViewInputTraceEvent struct {
	Stage string `json:"stage"`
	Edge  string `json:"edge,omitempty"`
	Epoch string `json:"epoch,omitempty"`
}

type macViewInputTrace struct {
	enabled                          atomic.Bool
	mu                               sync.Mutex
	events                           []macViewInputTraceEvent
	previousLayoutEpoch, layoutEpoch string
}

func (t *macViewInputTrace) recordLayout(previous, next string) {
	t.mu.Lock()
	t.previousLayoutEpoch, t.layoutEpoch = previous, next
	t.mu.Unlock()
}

func (t *macViewInputTrace) layoutSnapshot() (string, string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.previousLayoutEpoch, t.layoutEpoch
}

func (t *macViewInputTrace) record(stage string, data []byte, epoch string) {
	if !t.enabled.Load() {
		return
	}
	event := macViewInputTraceEvent{Stage: stage, Epoch: epoch}
	if data != nil {
		var input struct {
			Op, Kind, Epoch string
			Button          int
		}
		if json.Unmarshal(data, &input) != nil || input.Op != "mouse" || input.Button != 0 ||
			(input.Kind != "down" && input.Kind != "up") {
			return
		}
		event.Edge = input.Kind
		if event.Epoch == "" {
			event.Epoch = input.Epoch
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Keep the pointer edges even if geometry acknowledgements continue until
	// the caller's failure timeout. The last consecutive ack gives the epoch.
	if n := len(t.events); n > 0 && data == nil && t.events[n-1].Stage == stage {
		t.events[n-1] = event
		return
	}
	const limit = 24
	if len(t.events) == limit {
		copy(t.events, t.events[1:])
		t.events = t.events[:limit-1]
	}
	t.events = append(t.events, event)
}

func (t *macViewInputTrace) snapshot() []macViewInputTraceEvent {
	t.enabled.Store(true)
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]macViewInputTraceEvent{}, t.events...)
}
