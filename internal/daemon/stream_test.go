package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/hostevents"
	"github.com/local/dsh-work/internal/presence"
)

type controlledStreamTicker struct{ ticks chan time.Time }

func (t *controlledStreamTicker) Chan() <-chan time.Time { return t.ticks }
func (t *controlledStreamTicker) Stop()                  {}

type controlledStreamClock struct {
	ticker   *controlledStreamTicker
	created  chan struct{}
	interval time.Duration
}

func newControlledStreamClock() *controlledStreamClock {
	return &controlledStreamClock{ticker: &controlledStreamTicker{ticks: make(chan time.Time, 1)}, created: make(chan struct{})}
}
func (c *controlledStreamClock) NewTicker(interval time.Duration) streamTicker {
	c.interval = interval
	close(c.created)
	return c.ticker
}

func TestStreamWritesTenSecondPingWithInjectedClock(t *testing.T) {
	clock := newControlledStreamClock()
	server := &Server{Hub: hostevents.New(), streamClock: clock}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	response, err := httpServer.Client().Get(httpServer.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != "application/x-ndjson" {
		t.Fatalf("content type = %q", response.Header.Get("Content-Type"))
	}
	select {
	case <-clock.created:
	case <-time.After(time.Second):
		t.Fatal("stream did not install its ping ticker")
	}
	if clock.interval != streamPingInterval {
		t.Fatalf("ping interval = %s, want %s", clock.interval, streamPingInterval)
	}
	clock.ticker.ticks <- time.Unix(10, 0)
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "{\"type\":\"ping\"}\n" {
		t.Fatalf("ping line = %q error=%v", line, err)
	}
}

func TestStreamWritesOrderedTopicsThenStagedCommands(t *testing.T) {
	hub := hostevents.New()
	for _, topic := range []string{"lifecycle", "worker-url", "update", "preferences", "acquisition"} {
		hub.SetTopic(topic, topic)
	}
	hub.SendCommand("", "notification-failure", time.Minute, true)
	server := httptest.NewServer(&Server{Hub: hub})
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	for index, topic := range []string{"lifecycle", "worker-url", "update", "preferences", "acquisition"} {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read topic %s: %v", topic, err)
		}
		var message hostevents.Message
		if err := json.Unmarshal(line, &message); err != nil {
			t.Fatal(err)
		}
		if message.Type != "state" || message.Topic != topic {
			t.Fatalf("initial[%d] = %+v, want state %s", index, message, topic)
		}
	}
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var command hostevents.Message
	if err := json.Unmarshal(line, &command); err != nil {
		t.Fatal(err)
	}
	if command.Type != "command" || command.Name != "notification-failure" {
		t.Fatalf("staged message = %+v, want command after state snapshot", command)
	}
}

type blockingStreamWriter struct {
	header  http.Header
	started chan struct{}
	release chan struct{}
	once    sync.Once
	close   sync.Once
}

func (w *blockingStreamWriter) Header() http.Header { return w.header }
func (w *blockingStreamWriter) WriteHeader(int)     {}
func (w *blockingStreamWriter) Flush()              {}
func (w *blockingStreamWriter) Write(value []byte) (int, error) {
	w.once.Do(func() {
		close(w.started)
		<-w.release
	})
	return len(value), nil
}

func TestFullStreamQueueClosesConnection(t *testing.T) {
	hub := hostevents.New()
	hub.SetTopic("initial", "ready")
	server := &Server{Hub: hub}
	writer := &blockingStreamWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	finished := make(chan struct{})
	go func() {
		server.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/stream", nil))
		close(finished)
	}()
	defer writer.close.Do(func() { close(writer.release) })
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start writing its initial state")
	}
	for index := 0; index <= 256; index++ {
		hub.SetTopic("overflow", fmt.Sprintf("%d", index))
	}
	writer.close.Do(func() { close(writer.release) })
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("stream handler did not close after its output queue filled")
	}
}

func TestNewStreamReplacesOldAndClearsPresenceOnDisconnect(t *testing.T) {
	hub := hostevents.New()
	hub.SetTopic("lifecycle", "ready")
	tracker := presence.New()
	server := &Server{Hub: hub, Presence: tracker}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := httpServer.Client()

	first, err := client.Get(httpServer.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(first.Body).ReadString('\n'); err != nil || !strings.Contains(line, `"topic":"lifecycle"`) || strings.Contains(line, `"window"`) {
		t.Fatalf("initial state line = %q error=%v", line, err)
	}
	tracker.SetFocused("workspace", true)
	second, err := client.Get(httpServer.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(second.Body).ReadString('\n'); err != nil || !strings.Contains(line, `"topic":"lifecycle"`) {
		t.Fatalf("replacement initial state line = %q error=%v", line, err)
	}
	if tracker.AnyFocused() {
		t.Fatal("replacing the stream did not clear old presence")
	}
	readEnded := make(chan error, 1)
	go func() {
		_, err := bufio.NewReader(first.Body).ReadString('\n')
		readEnded <- err
	}()
	select {
	case err := <-readEnded:
		if err == nil {
			t.Fatal("replaced stream stayed open")
		}
	case <-time.After(time.Second):
		t.Fatal("new connection did not close the previous stream")
	}

	tracker.SetFocused("workspace-2", true)
	_ = second.Body.Close()
	wait := make(chan bool, 1)
	go func() {
		deadline := time.Now().Add(time.Second)
		for tracker.AnyFocused() && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		wait <- tracker.AnyFocused()
	}()
	if stillFocused := <-wait; stillFocused {
		t.Fatal("disconnect did not clear all presence")
	}
	_ = first.Body.Close()
}

func TestStreamRejectsOriginAndNonGetRequests(t *testing.T) {
	server := &Server{Hub: hostevents.New()}
	for _, test := range []struct {
		method string
		origin string
		status int
	}{{http.MethodGet, "https://example.test", http.StatusForbidden}, {http.MethodPost, "", http.StatusMethodNotAllowed}} {
		request := httptest.NewRequest(test.method, "/stream", strings.NewReader(""))
		if test.origin != "" {
			request.Header.Set("Origin", test.origin)
		}
		recorder := httptest.NewRecorder()
		server.ServeHTTP(recorder, request)
		if recorder.Code != test.status {
			t.Errorf("%s stream origin=%q status=%d, want %d", test.method, test.origin, recorder.Code, test.status)
		}
	}
}
