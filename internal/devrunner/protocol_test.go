package devrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

type blockedBackend struct {
	entered chan struct{}
	release chan struct{}
	starts  atomic.Int32
}

func (b *blockedBackend) Execute(ctx context.Context, action string, instance int) error {
	if action == "build" {
		close(b.entered)
		select {
		case <-b.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.starts.Add(1)
	return nil
}
func (b *blockedBackend) Status(context.Context) Status { return Status{Build: "building"} }
func perform(s *Service, r Request, extras map[string]any) *httptest.ResponseRecorder {
	data, _ := json.Marshal(r)
	if extras != nil {
		var m map[string]any
		json.Unmarshal(data, &m)
		for k, v := range extras {
			m[k] = v
		}
		data, _ = json.Marshal(m)
	}
	request := httptest.NewRequest("POST", "/command", bytes.NewReader(data))
	recorder := httptest.NewRecorder()
	s.ServeHTTP(recorder, request)
	return recorder
}
func TestFixedCommandsRejectUnknownFieldsGenerationAndExpiredQueue(t *testing.T) {
	b := &blockedBackend{entered: make(chan struct{}), release: make(chan struct{})}
	s := NewService(b)
	request := Request{Generation: s.Generation, Action: "start", Instance: 1, Deadline: time.Now().Add(time.Second).UnixMilli()}
	for _, bad := range []map[string]any{{"command": "powershell"}, {"env": map[string]string{"PATH": "wrong"}}, {"workspace": "elsewhere"}} {
		if r := perform(s, request, bad); r.Code != http.StatusBadRequest {
			t.Fatal("unbounded command field accepted", r.Code)
		}
	}
	stale := request
	stale.Generation = "old"
	if r := perform(s, stale, nil); r.Code != 409 {
		t.Fatal("stale runner generation accepted")
	}
	go perform(s, Request{Generation: s.Generation, Action: "build", Deadline: time.Now().Add(time.Second).UnixMilli()}, nil)
	<-b.entered
	status := perform(s, Request{Generation: s.Generation, Action: "status", Deadline: time.Now().Add(time.Second).UnixMilli()}, nil)
	if status.Code != 200 {
		t.Fatal("status blocked behind build")
	}
	request.Deadline = time.Now().Add(30 * time.Millisecond).UnixMilli()
	expired := perform(s, request, nil)
	if expired.Code != 408 {
		t.Fatal("expired queued request was not rejected", expired.Code)
	}
	close(b.release)
	time.Sleep(10 * time.Millisecond)
	if b.starts.Load() != 0 {
		t.Fatal("timed-out start executed later")
	}
}
func TestLiveExistingWebViewReadinessOnly(t *testing.T) {
	port := os.Getenv("E67_RUNNER_LIVE_PROBE")
	if port == "" {
		t.Skip("explicit existing candidate CDP port required; does not launch UI")
	}
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if !webviewReady(ctx, port) {
		t.Fatal("actual shell/DSH readiness was not confirmed")
	}
	t.Log("existing real WebView shell iframe and DSH provider/version readiness confirmed without launching or changing app state")
}
