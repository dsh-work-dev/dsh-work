package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/local/dsh-work/internal/hostevents"
)

const streamPingInterval = 10 * time.Second

type streamTicker interface {
	Chan() <-chan time.Time
	Stop()
}

type streamClock interface {
	NewTicker(time.Duration) streamTicker
}

type realStreamClock struct{}

func (realStreamClock) NewTicker(interval time.Duration) streamTicker {
	return realStreamTicker{Ticker: time.NewTicker(interval)}
}

type realStreamTicker struct{ *time.Ticker }

func (t realStreamTicker) Chan() <-chan time.Time { return t.C }

func (s *Server) serveStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	if s.Hub == nil {
		http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	s.streamStartMu.Lock()
	if ctx.Err() != nil {
		s.streamStartMu.Unlock()
		cancel()
		return
	}
	s.streamMu.Lock()
	previous := s.streamCancel
	previousSub := s.streamSub
	if previous != nil || previousSub != nil {
		if previous != nil {
			previous()
		}
		if previousSub != nil {
			previousSub.Close()
		}
		if s.Presence != nil {
			s.Presence.ClearAll()
		}
	}
	s.streamMu.Unlock()

	// Let a current write finish or be interrupted before assigning the new
	// stream as the active writer. This keeps any in-flight old line ahead of
	// the replacement stream and prevents the old handler from writing again.
	s.streamWriteMu.Lock()
	if ctx.Err() != nil {
		s.streamWriteMu.Unlock()
		s.streamStartMu.Unlock()
		cancel()
		return
	}
	s.streamMu.Lock()
	s.streamID++
	streamID := s.streamID
	s.streamCancel = cancel
	subscription, initial := s.Hub.Subscribe()
	s.streamSub = &subscription
	s.streamMu.Unlock()
	s.streamWriteMu.Unlock()
	s.streamStartMu.Unlock()

	defer func() {
		cancel()
		s.streamMu.Lock()
		if s.streamID == streamID {
			s.streamCancel = nil
			s.streamSub = nil
			if s.Presence != nil {
				s.Presence.ClearAll()
			}
		}
		s.streamMu.Unlock()
	}()

	defer subscription.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	flusher.Flush()
	controller := http.NewResponseController(w)
	writerDone := make(chan struct{})
	defer close(writerDone)
	// Interrupt a blocked pipe write if the bounded queue overflows or this
	// connection is replaced. The HTTP handler remains the sole writer.
	go func() {
		select {
		case <-ctx.Done():
			_ = controller.SetWriteDeadline(time.Now())
		case <-subscription.Done:
			_ = controller.SetWriteDeadline(time.Now())
		case <-writerDone:
		}
	}()

	write := func(message hostevents.Message) error {
		s.streamWriteMu.Lock()
		defer s.streamWriteMu.Unlock()
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := json.NewEncoder(w).Encode(message); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	for _, message := range initial {
		if err := write(message); err != nil {
			return
		}
	}
	clock := s.streamClock
	if clock == nil {
		clock = realStreamClock{}
	}
	ticker := clock.NewTicker(streamPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-subscription.Done:
			return
		case message := <-subscription.Messages:
			if err := write(message); err != nil {
				return
			}
		case <-ticker.Chan():
			if err := write(hostevents.Message{Type: "ping"}); err != nil {
				return
			}
		}
	}
}
