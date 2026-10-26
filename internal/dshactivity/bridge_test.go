package dshactivity

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestActivityPriorityAndDistinctOutcomes(t *testing.T) {
	s := Snapshot{Sessions: []Activity{
		{SessionID: "running", Seq: 100, Running: true},
		{SessionID: "completed", Seq: 5, Outcome: "completed", UpdatedAt: 8},
		{SessionID: "failed", Seq: 9, Outcome: "error"},
		{SessionID: "question", Seq: 2, Running: true, Interaction: &Interaction{Key: "q1", Kind: "question", Text: "Choose a format"}},
		{SessionID: "cancelled", Seq: 4, Outcome: "aborted", UpdatedAt: 4},
	}}
	if !normalize(&s) {
		t.Fatal("valid activities rejected")
	}
	for i, id := range []string{"question", "failed", "running", "completed", "cancelled"} {
		if s.Sessions[i].SessionID != id {
			t.Fatalf("priority: %+v", s.Sessions)
		}
	}
	s.Connected = true
	kind, key := AnimationIntent(s)
	if kind != "task.waiting" || key == "" {
		t.Fatalf("question intent: %s %s", kind, key)
	}
	for _, outcome := range []string{"blocked", "max-tokens", "interrupted"} {
		if got := activityState(Activity{Outcome: outcome}); got != outcome {
			t.Fatalf("%s => %s", outcome, got)
		}
	}
	if got := activityState(Activity{Outcome: "completed"}); got != "idle" {
		t.Fatal("completed activity should be idle")
	}
	if got := activityState(Activity{Running: true, Goal: &Goal{Phase: "blocked"}}); got != "blocked" {
		t.Fatal("blocked goal lost")
	}
	for outcome, intent := range map[string]string{"completed": "task.completed", "aborted": "task.cancelled", "error": "task.failed", "blocked": "task.blocked", "max-tokens": "task.limited", "interrupted": "task.interrupted"} {
		a := Activity{SessionID: "terminal", Outcome: outcome, LiveCompletion: outcome == "completed"}
		a.State = activityState(a)
		got, _ := AnimationIntent(Snapshot{Connected: true, Sessions: []Activity{a}})
		if got != intent {
			t.Fatalf("%s intent = %s", outcome, got)
		}
	}
}

func TestAnimationIntentCelebratesEachCompletedTurnOnce(t *testing.T) {
	completed := Activity{SessionID: "session-1", Turn: 3, Outcome: "completed", LiveCompletion: true}
	completed.State = activityState(completed)
	snapshot := Snapshot{Connected: true, Sessions: []Activity{completed}}
	kind, key := AnimationIntent(snapshot)
	if kind != "task.completed" || key == "" {
		t.Fatalf("completed intent = %q %q", kind, key)
	}
	if repeatedKind, repeatedKey := AnimationIntent(snapshot); repeatedKind != kind || repeatedKey != key {
		t.Fatalf("same turn changed completion intent: %q %q", repeatedKind, repeatedKey)
	}
	nextTurn := completed
	nextTurn.Turn++
	nextTurn.State = activityState(nextTurn)
	nextKind, nextKey := AnimationIntent(Snapshot{Connected: true, Sessions: []Activity{nextTurn}})
	if nextKind != "task.completed" || nextKey == key {
		t.Fatalf("next turn intent = %q %q, previous key %q", nextKind, nextKey, key)
	}
	running := Activity{SessionID: "session-1", Turn: nextTurn.Turn, Running: true, State: "running", WorkPhase: "working"}
	if runningKind, _ := AnimationIntent(Snapshot{Connected: true, Sessions: []Activity{running}}); runningKind != "task.working" {
		t.Fatalf("running intent = %q", runningKind)
	}
}

func TestAnimationIntentDoesNotCelebrateReplayedCompletion(t *testing.T) {
	completed := Activity{SessionID: "session-1", Turn: 3, Outcome: "completed", State: "idle"}
	if kind, _ := AnimationIntent(Snapshot{Connected: true, Sessions: []Activity{completed}}); kind == "task.completed" {
		t.Fatal("replayed completion triggered a completion animation")
	}
}

func TestBridgeSSEAcceptsSnapshotsAndDisconnectsOnGenerationMismatch(t *testing.T) {
	clock := newManualBridgeClock()
	b := newWithClock(clock)
	if _, err := b.Begin("g1"); err != nil {
		t.Fatal(err)
	}
	token := b.token
	frames := make(chan Snapshot, 2)
	started := make(chan struct{}, 2)
	var streams atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "dsh-test", Value: "authenticated", Path: "/"})
			return
		}
		cookie, err := r.Cookie("dsh-test")
		if err != nil || cookie.Value != "authenticated" || r.Header.Get("X-DSH-Work-Token") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "text/event-stream" || r.Header.Get("Origin") != "http://"+r.Host {
			http.Error(w, "invalid stream request", http.StatusBadRequest)
			return
		}
		if r.URL.Path != "/__dshwork/pet-activity" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		streams.Add(1)
		started <- struct{}{}
		for {
			select {
			case state := <-frames:
				data, _ := json.Marshal(state)
				_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer b.Close()
	b.Start(ctx, server.URL, http.DefaultTransport)
	waitSignal(t, started)
	frames <- Snapshot{SchemaVersion: 1, Generation: "g1", Sessions: []Activity{{SessionID: "parent", Seq: 1, Running: true}, {SessionID: "child", ParentSessionID: "parent", Seq: 2}}}
	waitFor(t, func() bool { return b.Snapshot().Connected })
	if got := b.Snapshot(); len(got.Sessions) != 2 || got.Sessions[0].SessionID != "parent" {
		t.Fatalf("SSE snapshot not accepted: %+v", got)
	}

	frames <- Snapshot{SchemaVersion: 1, Generation: "old-generation", Sessions: []Activity{{SessionID: "stale"}}}
	waitFor(t, func() bool { return !b.Snapshot().Connected })
	waitCreatedTimer(t, clock, activityRetryMinimum)
	clock.Advance(activityRetryMinimum - time.Millisecond)
	if streams.Load() != 1 {
		t.Fatalf("reconnected before 500 ms: %d streams", streams.Load())
	}
	clock.Advance(time.Millisecond)
	waitSignal(t, started)
	waitFor(t, func() bool { return streams.Load() == 2 })
}

func TestBridgeDisconnectsAndReconnectsWhenActivityStreamEnds(t *testing.T) {
	clock := newManualBridgeClock()
	b := newWithClock(clock)
	if _, err := b.Begin("g1"); err != nil {
		t.Fatal(err)
	}
	var streams atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			return
		}
		if r.URL.Path != "/__dshwork/pet-activity" {
			http.NotFound(w, r)
			return
		}
		streams.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		state := Snapshot{SchemaVersion: 1, Generation: "g1", Sessions: []Activity{{SessionID: "one", Seq: 1, Running: true}}}
		data, _ := json.Marshal(state)
		_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer b.Close()
	b.Start(ctx, server.URL, http.DefaultTransport)
	waitFor(t, func() bool {
		state := b.Snapshot()
		return streams.Load() == 1 && !state.Connected && len(state.Sessions) == 1 && state.Sessions[0].SessionID == "one"
	})
	waitCreatedTimer(t, clock, activityRetryMinimum)
	clock.Advance(activityRetryMinimum - time.Millisecond)
	if streams.Load() != 1 {
		t.Fatalf("reconnected before 500 ms: %d streams", streams.Load())
	}
	clock.Advance(time.Millisecond)
	waitFor(t, func() bool { return streams.Load() == 2 })
}

func TestBridgeSSEResetsThe45SecondSilenceDeadlineOnPing(t *testing.T) {
	clock := newManualBridgeClock()
	b := newWithClock(clock)
	if _, err := b.Begin("g1"); err != nil {
		t.Fatal(err)
	}
	frames := make(chan Snapshot, 2)
	pings := make(chan struct{}, 1)
	pingSent := make(chan struct{}, 1)
	started := make(chan int, 3)
	var streams atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "dsh-test", Value: "ok", Path: "/"})
			return
		}
		if r.URL.Path != "/__dshwork/pet-activity" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		n := int(streams.Add(1))
		started <- n
		if n == 1 {
			state := <-frames
			data, _ := json.Marshal(state)
			_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
			w.(http.Flusher).Flush()
			for {
				select {
				case <-pings:
					_, _ = io.WriteString(w, ": ping\n\n")
					w.(http.Flusher).Flush()
					pingSent <- struct{}{}
				case <-r.Context().Done():
					return
				}
			}
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer b.Close()
	b.Start(ctx, server.URL, http.DefaultTransport)
	waitSignal(t, started)
	frames <- Snapshot{SchemaVersion: 1, Generation: "g1", Sessions: []Activity{{SessionID: "one", Seq: 7, Running: true}}}
	waitFor(t, func() bool { return b.Snapshot().Connected })
	waitBridgeReset(t, clock, activitySilenceTimeout)
	clock.Advance(44 * time.Second)
	if !b.Snapshot().Connected {
		t.Fatal("bridge disconnected before the 45 second idle limit")
	}
	pings <- struct{}{}
	waitSignal(t, pingSent)
	waitBridgeReset(t, clock, activitySilenceTimeout)
	clock.Advance(44 * time.Second)
	if !b.Snapshot().Connected {
		t.Fatal("ping did not extend the silence deadline")
	}
	clock.Advance(time.Second)
	waitFor(t, func() bool { return !b.Snapshot().Connected })
	waitCreatedTimer(t, clock, activityRetryMinimum)
	clock.Advance(activityRetryMinimum - time.Millisecond)
	if streams.Load() != 1 {
		t.Fatalf("reconnected before the initial 500 ms backoff: %d", streams.Load())
	}
	clock.Advance(time.Millisecond)
	waitSignal(t, started)
	waitFor(t, func() bool { return streams.Load() == 2 })
}

func TestBridgeReconnectBackoffDoublesUntilFiveSeconds(t *testing.T) {
	clock := newManualBridgeClock()
	b := newWithClock(clock)
	if _, err := b.Begin("g1"); err != nil {
		t.Fatal(err)
	}
	started := make(chan int, 8)
	var streams atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "dsh-test", Value: "ok", Path: "/"})
			return
		}
		if r.URL.Path != "/__dshwork/pet-activity" {
			http.NotFound(w, r)
			return
		}
		n := int(streams.Add(1))
		started <- n
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		data, _ := json.Marshal(Snapshot{SchemaVersion: 1, Generation: "not-g1", Sessions: []Activity{}})
		_, _ = fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", data)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer b.Close()
	b.Start(ctx, server.URL, http.DefaultTransport)
	delays := []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second}
	waitSignal(t, started)
	for index, delay := range delays {
		waitCreatedTimer(t, clock, delay)
		clock.Advance(delay - time.Millisecond)
		if got := streams.Load(); got != int32(index+1) {
			t.Fatalf("stream count before %s delay completed = %d", delay, got)
		}
		clock.Advance(time.Millisecond)
		if index+1 < len(delays) {
			waitSignal(t, started)
			waitFor(t, func() bool { return streams.Load() == int32(index+2) })
		}
	}
}

func TestBridgeOpenTargetsWindowAndRecordsResultOrTimeout(t *testing.T) {
	clock := newManualBridgeClock()
	b := newWithClock(clock)
	if _, err := b.Begin("g1"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.client = &http.Client{}
	b.state = Snapshot{SchemaVersion: 1, Generation: "g1", Connected: true, Sessions: []Activity{
		{SessionID: "parent", Seq: 1}, {SessionID: "child", ParentSessionID: "parent", Seq: 2},
	}}
	b.mu.Unlock()
	var calls []string
	var commandWindow, commandName string
	var commandTTL time.Duration
	var command map[string]string
	b.SetNavigation(func(section, window string) (string, error) {
		calls = append(calls, "open:"+section+":"+window)
		return "workspace", nil // UI falls back when the requested window is absent.
	}, func(window, name string, ttl time.Duration, data any) {
		calls = append(calls, "command")
		commandWindow, commandName, commandTTL = window, name, ttl
		command = data.(map[string]string)
	})
	if err := b.Open(context.Background(), "child", "workspace-2"); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(strings.Join(calls, ","), "open::workspace-2,command") {
		t.Fatalf("navigation order/target = %v", calls)
	}
	if commandWindow != "workspace" || commandName != "dsh-open-session" || commandTTL != 15*time.Second || command["sessionId"] != "child" || command["parentSessionId"] != "parent" {
		t.Fatalf("open command target/data = %q %q %s %#v", commandWindow, commandName, commandTTL, command)
	}
	if err := b.Open(context.Background(), "unknown", "workspace"); err == nil {
		t.Fatal("unknown session was allowed")
	}
	b.ReportSessionOpened(command["id"], "")
	waitCreatedTimer(t, clock, openSessionTimeout)
	clock.Advance(openSessionTimeout)
	if got := b.Snapshot().NavigationError; got != "" {
		t.Fatalf("successful navigation timed out: %q", got)
	}
	if err := b.Open(context.Background(), "child", "workspace"); err != nil {
		t.Fatal(err)
	}
	failedID := command["id"]
	b.ReportSessionOpened(failedID, "Conversation is unavailable")
	waitCreatedTimer(t, clock, openSessionTimeout)
	if got := b.Snapshot().NavigationError; got != "Conversation is unavailable" {
		t.Fatalf("reported navigation error = %q", got)
	}

	if err := b.Open(context.Background(), "parent", "workspace"); err != nil {
		t.Fatal(err)
	}
	waitCreatedTimer(t, clock, openSessionTimeout)
	clock.Advance(openSessionTimeout)
	waitFor(t, func() bool { return b.Snapshot().NavigationError == "Conversation is unavailable" })
	b.accept("g1", Snapshot{SchemaVersion: 1, Generation: "g1", Sessions: []Activity{{SessionID: "parent"}}})
	if got := b.Snapshot().NavigationError; got != "Conversation is unavailable" {
		t.Fatalf("activity frame erased navigation result: %q", got)
	}
}

type manualBridgeClock struct {
	mu      sync.Mutex
	now     time.Time
	timers  map[*manualBridgeTimer]bool
	created chan time.Duration
	reset   chan time.Duration
}

type manualBridgeTimer struct {
	clock  *manualBridgeClock
	c      chan time.Time
	due    time.Time
	active bool
}

func newManualBridgeClock() *manualBridgeClock {
	return &manualBridgeClock{now: time.Unix(0, 0), timers: make(map[*manualBridgeTimer]bool), created: make(chan time.Duration, 128), reset: make(chan time.Duration, 128)}
}

func (c *manualBridgeClock) NewTimer(delay time.Duration) bridgeTimer {
	c.mu.Lock()
	timer := &manualBridgeTimer{clock: c, c: make(chan time.Time, 1), due: c.now.Add(delay), active: true}
	c.timers[timer] = true
	c.mu.Unlock()
	select {
	case c.created <- delay:
	default:
	}
	return timer
}

func (t *manualBridgeTimer) Chan() <-chan time.Time { return t.c }
func (t *manualBridgeTimer) Stop() {
	t.clock.mu.Lock()
	t.active = false
	t.clock.mu.Unlock()
}
func (t *manualBridgeTimer) Reset(delay time.Duration) {
	t.clock.mu.Lock()
	select {
	case <-t.c:
	default:
	}
	t.due, t.active = t.clock.now.Add(delay), true
	t.clock.mu.Unlock()
	select {
	case t.clock.reset <- delay:
	default:
	}
}

func (c *manualBridgeClock) Advance(delay time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delay)
	now := c.now
	for timer := range c.timers {
		if timer.active && !timer.due.After(now) {
			timer.active = false
			select {
			case timer.c <- now:
			default:
			}
		}
	}
	c.mu.Unlock()
}

func waitSignal[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for event")
		var zero T
		return zero
	}
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func waitCreatedTimer(t *testing.T, clock *manualBridgeClock, delay time.Duration) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case created := <-clock.created:
			if created == delay {
				return
			}
		case <-deadline:
			t.Fatalf("timer %s was not created", delay)
		}
	}
}

func waitBridgeReset(t *testing.T, clock *manualBridgeClock, delay time.Duration) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case reset := <-clock.reset:
			if reset == delay {
				return
			}
		case <-deadline:
			t.Fatalf("timer %s was not reset", delay)
		}
	}
}
