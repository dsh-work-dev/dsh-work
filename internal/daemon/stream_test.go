package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/hostevents"
	"github.com/local/dsh-work/internal/presence"
)

type controlledStreamTicker struct{ ticks chan time.Time }

func (t *controlledStreamTicker) Chan() <-chan time.Time { return t.ticks }
func (t *controlledStreamTicker) Stop()                  {}

type controlledStreamClock struct {
	created   chan *controlledStreamTicker
	intervals chan time.Duration
}

func newControlledStreamClock() *controlledStreamClock {
	return &controlledStreamClock{created: make(chan *controlledStreamTicker, 4), intervals: make(chan time.Duration, 4)}
}
func (c *controlledStreamClock) NewTicker(interval time.Duration) streamTicker {
	ticker := &controlledStreamTicker{ticks: make(chan time.Time, 1)}
	c.intervals <- interval
	c.created <- ticker
	return ticker
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
	var ticker *controlledStreamTicker
	select {
	case ticker = <-clock.created:
	case <-time.After(time.Second):
		t.Fatal("stream did not install its ping ticker")
	}
	if interval := <-clock.intervals; interval != streamPingInterval {
		t.Fatalf("ping interval = %s, want %s", interval, streamPingInterval)
	}
	ticker.ticks <- time.Unix(10, 0)
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
	writes  atomic.Int32
	cutoff  atomic.Bool
}

func (w *blockingStreamWriter) Header() http.Header { return w.header }
func (w *blockingStreamWriter) WriteHeader(int)     {}
func (w *blockingStreamWriter) Flush()              {}
func (w *blockingStreamWriter) Write(value []byte) (int, error) {
	w.writes.Add(1)
	w.once.Do(func() {
		close(w.started)
		<-w.release
	})
	if w.cutoff.Load() {
		return 0, http.ErrHandlerTimeout
	}
	return len(value), nil
}
func (w *blockingStreamWriter) SetWriteDeadline(deadline time.Time) error {
	if !deadline.IsZero() && !deadline.After(time.Now()) {
		w.cutoff.Store(true)
		w.close.Do(func() { close(w.release) })
	}
	return nil
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

func TestNewStreamInterruptsBlockedOldWriteBeforeStarting(t *testing.T) {
	hub := hostevents.New()
	tracker := presence.New()
	clock := newControlledStreamClock()
	server := &Server{Hub: hub, Presence: tracker, streamClock: clock}
	oldWriter := &blockingStreamWriter{header: make(http.Header), started: make(chan struct{}), release: make(chan struct{})}
	defer oldWriter.close.Do(func() { close(oldWriter.release) })
	oldFinished := make(chan struct{})
	go func() {
		server.ServeHTTP(oldWriter, httptest.NewRequest(http.MethodGet, "/stream", nil))
		close(oldFinished)
	}()
	oldTicker := receiveControlledTicker(t, clock)
	server.streamMu.Lock()
	oldSubscription := server.streamSub
	server.streamMu.Unlock()
	if oldSubscription == nil {
		t.Fatal("old stream has no active subscription")
	}
	tracker.SetFocused("workspace", true)
	hub.SetTopic("lifecycle", "ready")
	select {
	case <-oldWriter.started:
	case <-time.After(time.Second):
		t.Fatal("old stream did not begin its blocked state write")
	}

	secondContext, cancelSecond := context.WithCancel(context.Background())
	defer cancelSecond()
	secondRequest := httptest.NewRequest(http.MethodGet, "/stream", nil).WithContext(secondContext)
	secondWriter := httptest.NewRecorder()
	secondFinished := make(chan struct{})
	go func() {
		server.ServeHTTP(secondWriter, secondRequest)
		close(secondFinished)
	}()
	select {
	case <-oldSubscription.Done:
	case <-time.After(time.Second):
		t.Fatal("new connection did not close the old subscription")
	}
	if tracker.AnyFocused() {
		t.Fatal("new connection did not clear presence")
	}
	newTicker := receiveControlledTicker(t, clock)
	if newTicker == nil {
		t.Fatal("replacement stream has no ping ticker")
	}
	select {
	case <-oldFinished:
	case <-time.After(time.Second):
		t.Fatal("old blocked writer was not interrupted")
	}
	if oldWriter.writes.Load() != 1 {
		t.Fatalf("old stream wrote %d rows, want only the blocked initial write", oldWriter.writes.Load())
	}
	oldTicker.ticks <- time.Unix(10, 0)
	if oldWriter.writes.Load() != 1 {
		t.Fatal("old stream wrote a ping after replacement")
	}
	if body := secondWriter.Body.String(); !strings.Contains(body, `"topic":"lifecycle"`) {
		t.Fatalf("replacement stream did not write its initial state: %q", body)
	}

	server.streamMu.Lock()
	newSubscription := server.streamSub
	server.streamMu.Unlock()
	if newSubscription == nil {
		t.Fatal("replacement stream has no active subscription")
	}
	tracker.SetFocused("workspace-2", true)
	cancelSecond()
	select {
	case <-newSubscription.Done:
	case <-time.After(time.Second):
		t.Fatal("closing replacement request did not close its subscription")
	}
	deadline := time.Now().Add(time.Second)
	for tracker.AnyFocused() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if tracker.AnyFocused() {
		t.Fatal("replacement disconnect did not clear presence")
	}
	select {
	case <-secondFinished:
	case <-time.After(time.Second):
		t.Fatal("replacement stream did not stop after cancellation")
	}
}

func TestNewStreamClosesOldSubscriptionAndStopsMessages(t *testing.T) {
	hub := hostevents.New()
	hub.SetTopic("lifecycle", "ready")
	tracker := presence.New()
	clock := newControlledStreamClock()
	server := &Server{Hub: hub, Presence: tracker, streamClock: clock}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	client := httpServer.Client()

	first, err := client.Get(httpServer.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	firstReader := bufio.NewReader(first.Body)
	if line, err := firstReader.ReadString('\n'); err != nil || !strings.Contains(line, `"topic":"lifecycle"`) || strings.Contains(line, `"window"`) {
		t.Fatalf("initial state line = %q error=%v", line, err)
	}
	firstTicker := receiveControlledTicker(t, clock)
	server.streamMu.Lock()
	oldSubscription := server.streamSub
	server.streamMu.Unlock()
	if oldSubscription == nil {
		t.Fatal("first stream has no active subscription")
	}
	tracker.SetFocused("workspace", true)
	second, err := client.Get(httpServer.URL + "/stream")
	if err != nil {
		t.Fatal(err)
	}
	secondReader := bufio.NewReader(second.Body)
	if line, err := secondReader.ReadString('\n'); err != nil || !strings.Contains(line, `"topic":"lifecycle"`) {
		t.Fatalf("replacement initial state line = %q error=%v", line, err)
	}
	secondTicker := receiveControlledTicker(t, clock)
	select {
	case <-oldSubscription.Done:
	case <-time.After(time.Second):
		t.Fatal("replacement did not close the old Hub subscription")
	}
	if tracker.AnyFocused() {
		t.Fatal("replacing the stream did not clear old presence")
	}
	oldLine := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, err := firstReader.ReadString('\n')
		oldLine <- struct {
			line string
			err  error
		}{line, err}
	}()
	firstTicker.ticks <- time.Unix(10, 0)
	hub.SetTopic("lifecycle", "restarting")
	line, err := secondReader.ReadString('\n')
	if err != nil || !strings.Contains(line, `"value":"restarting"`) {
		t.Fatalf("replacement stream state = %q error=%v", line, err)
	}
	hub.SendCommand("", "background-stopping", time.Minute, true)
	line, err = secondReader.ReadString('\n')
	if err != nil || !strings.Contains(line, `"name":"background-stopping"`) {
		t.Fatalf("replacement stream command = %q error=%v", line, err)
	}
	secondTicker.ticks <- time.Unix(20, 0)
	line, err = secondReader.ReadString('\n')
	if err != nil || line != "{\"type\":\"ping\"}\n" {
		t.Fatalf("replacement stream ping = %q error=%v", line, err)
	}
	select {
	case result := <-oldLine:
		if result.err == nil || result.line != "" {
			t.Fatalf("old stream wrote after replacement: line=%q error=%v", result.line, result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("old stream did not end after replacement")
	}
	server.streamMu.Lock()
	newSubscription := server.streamSub
	server.streamMu.Unlock()
	if newSubscription == nil {
		t.Fatal("replacement stream has no active subscription")
	}
	tracker.SetFocused("workspace-2", true)
	_ = second.Body.Close()
	select {
	case <-newSubscription.Done:
	case <-time.After(time.Second):
		t.Fatal("closing the replacement stream did not close its subscription")
	}
	deadline := time.Now().Add(time.Second)
	for tracker.AnyFocused() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if tracker.AnyFocused() {
		t.Fatal("disconnect did not clear all presence")
	}
	_ = first.Body.Close()
	_ = second.Body.Close()
}

func receiveControlledTicker(t *testing.T, clock *controlledStreamClock) *controlledStreamTicker {
	t.Helper()
	select {
	case ticker := <-clock.created:
		if interval := <-clock.intervals; interval != streamPingInterval {
			t.Fatalf("ping interval = %s, want %s", interval, streamPingInterval)
		}
		return ticker
	case <-time.After(time.Second):
		t.Fatal("stream did not install its ping ticker")
		return nil
	}
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
