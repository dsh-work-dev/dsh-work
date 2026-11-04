package automation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBridgeReconnectsAndStopEndsRetries(t *testing.T) {
	s := New(managerForTest(t), nil)
	defer s.Close()
	s.Worker("worker", true, true)
	bridge := NewBridge()
	bridge.Attach(s)
	if _, e := bridge.Begin("worker"); e != nil {
		t.Fatal(e)
	}
	var connections atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth" && r.Header.Get("X-DSH-Work-Token") == "" {
			t.Error("missing host credential")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/__dshwork/automation/events" {
			n := connections.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			if n == 1 {
				return
			}
			<-r.Context().Done()
			return
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	bridge.Start(context.Background(), server.URL+"/auth", http.DefaultTransport)
	defer bridge.Stop()
	awaitState(t, s, func(v Snapshot) bool { return connections.Load() >= 2 && v.GatewayConnected })
	bridge.Stop()
	n := connections.Load()
	time.Sleep(350 * time.Millisecond)
	if connections.Load() != n || s.Snapshot().GatewayConnected {
		t.Fatal("stopped bridge recovered unexpectedly")
	}
	if len(s.Snapshot().BrowserTools) != 0 {
		t.Fatal("transport recovery authorized browser")
	}
}
