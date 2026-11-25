package dshactivity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInspectQuitRequiresCurrentConnection(t *testing.T) {
	closed := make(chan struct{})
	close(closed)
	for _, b := range []*Bridge{New(), {generation: "current", client: &http.Client{}, done: closed}, {generation: "old", client: &http.Client{}, done: make(chan struct{})}} {
		if _, err := b.InspectQuit(context.Background(), "current"); err == nil {
			t.Fatal("unavailable inspection did not return an error")
		}
	}
}

func TestInspectQuitAuthenticatesAndReportsFailures(t *testing.T) {
	for _, mode := range []string{"ok", "status", "timeout", "missing", "invalid", "changed"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/__dshwork/quit-inspection" || r.Header.Get("X-DSH-Work-Token") != "generation-token" || r.Header.Get("Origin") == "" {
					t.Error("missing authenticated inspection request")
				}
				switch mode {
				case "status":
					w.WriteHeader(http.StatusServiceUnavailable)
				case "timeout":
					<-r.Context().Done()
				case "missing":
					_, _ = w.Write([]byte(`{}`))
				case "invalid":
					_, _ = w.Write([]byte(`{"activeTasks":false,"scheduledTasks":"unknown"}`))
				default:
					_, _ = w.Write([]byte(`{"activeTasks":true,"scheduledTasks":true}`))
				}
			}))
			defer server.Close()
			b := &Bridge{generation: "current", client: server.Client(), origin: server.URL, token: "generation-token", done: make(chan struct{})}
			if mode == "changed" {
				b.client.Transport = quitRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					resp, err := http.DefaultTransport.RoundTrip(req)
					b.mu.Lock()
					b.generation = "replacement"
					b.mu.Unlock()
					return resp, err
				})
			}
			start := time.Now()
			got, err := b.InspectQuit(context.Background(), "current")
			if mode == "ok" {
				if err != nil || !got.ActiveTasks || !got.ScheduledTasks {
					t.Fatalf("inspection=%+v err=%v", got, err)
				}
			} else if err == nil {
				t.Fatal("failed inspection did not return an error")
			}
			if mode == "timeout" && (!errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second) {
				t.Fatalf("timeout err=%v elapsed=%s", err, time.Since(start))
			}
		})
	}
}

type quitRoundTripFunc func(*http.Request) (*http.Response, error)

func (f quitRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
