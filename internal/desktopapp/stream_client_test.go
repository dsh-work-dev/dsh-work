package desktopapp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/hostevents"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
)

func TestStreamProcessorMapsTopicsAndEmitsOnlyPreferenceChanges(t *testing.T) {
	var current daemon.Snapshot
	var mu sync.Mutex
	type emitted struct {
		name string
		data any
	}
	var got []emitted
	processor := &streamProcessor{current: &current, currentMu: &mu, emitEvent: func(name string, data any) { got = append(got, emitted{name, data}) }}
	push := func(topic string, value any) {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		processor.handle(hostevents.Message{Type: "state", Topic: topic, Value: raw})
	}

	status := lifecycle.Status{State: lifecycle.StateReady, WorkspaceURL: "http://127.0.0.1:1234/"}
	update := daemon.UpdateSnapshot{Phase: daemon.UpdateAvailable, TargetVersion: "2.0.0"}
	acquired := acquisition.OperationStatus{OperationID: "op", State: acquisition.OperationActive}
	push("lifecycle", status)
	push("worker-url", status.WorkspaceURL)
	push("update", update)
	push("acquisition", acquired)
	if current.Status != status || current.URL != status.WorkspaceURL || current.Update != update {
		t.Fatalf("stream did not update current snapshot: %+v", current)
	}
	if len(got) != 4 || got[0].name != "lifecycle" || got[1].name != "worker-url" || got[1].data != shellWorkerURL(status.WorkspaceURL) || got[2].name != "update-state" || got[3].name != "acquisition" {
		t.Fatalf("topic events = %+v", got)
	}

	preferences := settings.DefaultValues()
	push("preferences", preferences) // Initial preferences seed local state only.
	if len(got) != 4 {
		t.Fatalf("initial preferences emitted events: %+v", got)
	}
	preferences.Locale = settings.LocaleEnglish
	push("preferences", preferences)
	if len(got) != 5 || got[4].name != "locale" || got[4].data != settings.LocaleEnglish {
		t.Fatalf("locale preference event = %+v", got[4:])
	}
	preferences.Appearance.Mode = settings.AppearanceDark
	push("preferences", preferences)
	if len(got) != 6 || got[5].name != "appearance" || got[5].data != preferences.Appearance {
		t.Fatalf("appearance preference event = %+v", got[5:])
	}
	push("preferences", preferences)
	if len(got) != 6 {
		t.Fatalf("unchanged preferences emitted events: %+v", got[6:])
	}
	if current.Preferences != preferences {
		t.Fatalf("current preferences = %+v, want %+v", current.Preferences, preferences)
	}
}

type fakeClientTimer struct {
	mu         sync.Mutex
	channel    chan time.Time
	duration   time.Duration
	resetCount int
}

func (t *fakeClientTimer) Chan() <-chan time.Time { return t.channel }
func (t *fakeClientTimer) Stop() bool             { return true }
func (t *fakeClientTimer) Reset(duration time.Duration) bool {
	t.mu.Lock()
	t.duration = duration
	t.resetCount++
	t.mu.Unlock()
	select {
	case <-t.channel:
	default:
	}
	return true
}
func (t *fakeClientTimer) fire() {
	t.channel <- time.Unix(1, 0)
}

type fakeClientClock struct{ timer *fakeClientTimer }

func newFakeClientClock() *fakeClientClock {
	return &fakeClientClock{timer: &fakeClientTimer{channel: make(chan time.Time, 1)}}
}
func (c *fakeClientClock) NewTimer(duration time.Duration) clientStreamTimer {
	c.timer.duration = duration
	return c.timer
}

func TestDaemonStreamQuietForThirtySecondsQuitsUsingInjectedClock(t *testing.T) {
	reader, writer := io.Pipe()
	clock := newFakeClientClock()
	finished := make(chan error, 1)
	rows := make(chan hostevents.Message, 1)
	go func() {
		finished <- consumeDaemonStream(context.Background(), reader, clock, func(message hostevents.Message) { rows <- message })
	}()
	_, _ = io.WriteString(writer, `{"type":"ping"}`+"\n")
	select {
	case message := <-rows:
		if message.Type != "ping" {
			t.Fatalf("row = %+v, want ping", message)
		}
	case <-time.After(time.Second):
		t.Fatal("stream row was not consumed")
	}
	clock.timer.mu.Lock()
	resets := clock.timer.resetCount
	duration := clock.timer.duration
	clock.timer.mu.Unlock()
	if resets != 1 || duration != 30*time.Second {
		t.Fatalf("activity timer resets=%d duration=%s", resets, duration)
	}
	clock.timer.fire()
	select {
	case err := <-finished:
		if !errors.Is(err, errDaemonStreamIdle) {
			t.Fatalf("stream end = %v, want idle timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not exit on the injected 30 second timeout")
	}
	_ = writer.Close()
}
