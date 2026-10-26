package desktopapp

import (
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/hostevents"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type recordedCommandWindow struct {
	mu     sync.Mutex
	events []*application.CustomEvent
}

func (w *recordedCommandWindow) EmitEvent(name string, data ...any) bool {
	w.mu.Lock()
	var value any
	if len(data) != 0 {
		value = data[0]
	}
	w.events = append(w.events, &application.CustomEvent{Name: name, Data: value})
	w.mu.Unlock()
	return true
}

func (w *recordedCommandWindow) names() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	names := make([]string, 0, len(w.events))
	for _, event := range w.events {
		names = append(names, event.Name)
	}
	return names
}

func TestCommandsRouteByWindowAndWaitForPetReadiness(t *testing.T) {
	now := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	first, second := &recordedCommandWindow{}, &recordedCommandWindow{}
	var broadcast []string
	dispatcher := newCommandDispatcher(func(name string, _ any) { broadcast = append(broadcast, name) }, nil, nil)
	dispatcher.now = func() time.Time { return now }
	dispatcher.addWindow("workspace", first)
	dispatcher.addWindow("workspace-2", second)

	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "missing", ExpiresAt: now.Add(time.Minute).UnixMilli()}, "lost")
	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace", ExpiresAt: now.Add(time.Minute).UnixMilli()}, "first-1")
	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace", ExpiresAt: now.Add(time.Minute).UnixMilli()}, "first-2")
	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace-2", ExpiresAt: now.Add(time.Minute).UnixMilli()}, "second")
	dispatcher.dispatch(hostevents.Message{Name: "notification-failure", Window: "", ExpiresAt: now.Add(time.Minute).UnixMilli()}, true)
	if len(first.events) != 0 || len(second.events) != 0 || len(broadcast) != 1 || broadcast[0] != "notification-failure" {
		t.Fatalf("pre-ready route first=%v second=%v broadcast=%v", first.names(), second.names(), broadcast)
	}
	dispatcher.setPetReady("workspace", true)
	dispatcher.setPetReady("workspace-2", true)
	if got := first.names(); len(got) != 2 || got[0] != "test-window-command" || got[1] != "test-window-command" {
		t.Fatalf("workspace queued commands = %v", got)
	}
	if got := second.names(); len(got) != 1 || got[0] != "test-window-command" {
		t.Fatalf("workspace-2 commands = %v", got)
	}
	if first.events[0].Data.(string) != "first-1" || first.events[1].Data.(string) != "first-2" {
		t.Fatalf("workspace queue order = %+v", first.events)
	}

	dispatcher.setPetReady("workspace", false)
	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace", ExpiresAt: now.Add(time.Minute).UnixMilli()}, "after-reset")
	if len(first.events) != 2 {
		t.Fatal("command was delivered while pet was reset")
	}
	dispatcher.setPetReady("workspace", true)
	if got := first.names(); len(got) != 3 || first.events[2].Data.(string) != "after-reset" {
		t.Fatalf("command was not re-queued after reset: %v", got)
	}
}

func TestCommandDispatcherDropsExpiredCommandsAndBoundsPendingQueue(t *testing.T) {
	now := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	window := &recordedCommandWindow{}
	var droppedWindow, droppedCommand string
	dispatcher := newCommandDispatcher(nil, nil, func(window, command string) {
		droppedWindow, droppedCommand = window, command
	})
	dispatcher.now = func() time.Time { return now }
	dispatcher.addWindow("workspace", window)
	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace", ExpiresAt: now.UnixMilli()}, "expired-now")
	dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace", ExpiresAt: now.Add(time.Second).UnixMilli()}, "expired-queued")
	now = now.Add(2 * time.Second)
	dispatcher.dispatch(hostevents.Message{Name: "oldest-command", Window: "workspace", ExpiresAt: now.Add(time.Hour).UnixMilli()}, "oldest")
	for i := 0; i < petCommandQueueLimit; i++ {
		dispatcher.dispatch(hostevents.Message{Name: "test-window-command", Window: "workspace", ExpiresAt: now.Add(time.Hour).UnixMilli()}, string(rune('a'+i)))
	}
	dispatcher.setPetReady("workspace", true)
	if len(window.events) != petCommandQueueLimit {
		t.Fatalf("delivered commands = %d, want %d", len(window.events), petCommandQueueLimit)
	}
	if window.events[0].Data.(string) != "a" || window.events[1].Data.(string) != "b" {
		t.Fatalf("bounded command queue did not retain the newest FIFO items: %+v", window.events[:2])
	}
	if droppedWindow != "workspace" || droppedCommand != "oldest-command" {
		t.Fatalf("queue overflow warning window=%q command=%q, want workspace and oldest-command", droppedWindow, droppedCommand)
	}
}
