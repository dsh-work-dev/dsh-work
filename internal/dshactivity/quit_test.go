package dshactivity

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestInspectQuitWithoutWorker(t *testing.T) {
	got, err := New().InspectQuit(context.Background())
	if err != nil || got != (QuitInspection{}) {
		t.Fatalf("inspection=%+v err=%v", got, err)
	}
}

func TestInspectQuitAuthenticatesAndReportsFailures(t *testing.T) {
	for _, mode := range []string{"ok", "status", "timeout"} {
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
				default:
					_, _ = w.Write([]byte(`{"activeTasks":true,"scheduledTasks":true}`))
				}
			}))
			defer server.Close()
			b := &Bridge{client: server.Client(), origin: server.URL, token: "generation-token", done: make(chan struct{})}
			start := time.Now()
			got, err := b.InspectQuit(context.Background())
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
