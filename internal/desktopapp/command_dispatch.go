package desktopapp

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/acquisition"
	"github.com/local/dsh-work/internal/hostevents"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const petCommandQueueLimit = 16

type commandWindow interface {
	EmitEvent(string, ...any) bool
}

type wailsCommandWindow struct{ window application.Window }

func (w wailsCommandWindow) EmitEvent(name string, data ...any) bool {
	return w.window.EmitEvent(name, data...)
}

type queuedCommand struct {
	name      string
	expiresAt int64
	data      any
}

type commandDispatcher struct {
	mu       sync.Mutex
	now      func() time.Time
	windows  map[string]commandWindow
	petReady map[string]bool
	queued   map[string][]queuedCommand
	emitAll  func(string, any)
	onError  func(string, error)
	onDrop   func(window, command string)
}

func newCommandDispatcher(emitAll func(string, any), onError func(string, error), onDrop func(window, command string)) *commandDispatcher {
	return &commandDispatcher{
		now:      time.Now,
		windows:  make(map[string]commandWindow),
		petReady: make(map[string]bool),
		queued:   make(map[string][]queuedCommand),
		emitAll:  emitAll,
		onError:  onError,
		onDrop:   onDrop,
	}
}

func (d *commandDispatcher) addWindow(name string, window commandWindow) {
	d.mu.Lock()
	d.windows[name] = window
	d.mu.Unlock()
}

func (d *commandDispatcher) removeWindow(name string) {
	d.mu.Lock()
	delete(d.windows, name)
	delete(d.petReady, name)
	delete(d.queued, name)
	d.mu.Unlock()
}

func (d *commandDispatcher) setPetReady(name string, ready bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.windows[name]; !exists {
		return
	}
	d.petReady[name] = ready
	if !ready {
		return
	}
	window := d.windows[name]
	commands := d.queued[name]
	delete(d.queued, name)
	for _, command := range commands {
		if d.expired(command.expiresAt) {
			continue
		}
		d.emitWindowLocked(window, command)
	}
}

func (d *commandDispatcher) dispatch(message hostevents.Message, data any) {
	if d.expired(message.ExpiresAt) {
		return
	}
	command := queuedCommand{name: message.Name, expiresAt: message.ExpiresAt, data: data}
	if message.Window == "" {
		if d.emitAll != nil {
			d.emitAll(command.name, command.data)
		}
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	window, exists := d.windows[message.Window]
	if !exists {
		return
	}
	if !d.petReady[message.Window] {
		pending := d.pruneExpiredLocked(d.queued[message.Window])
		if len(pending) == petCommandQueueLimit {
			dropped := pending[0]
			copy(pending, pending[1:])
			pending = pending[:len(pending)-1]
			if d.onDrop != nil {
				d.onDrop(message.Window, dropped.name)
			}
		}
		d.queued[message.Window] = append(pending, command)
		return
	}
	d.emitWindowLocked(window, command)
}

func (d *commandDispatcher) pruneExpiredLocked(commands []queuedCommand) []queuedCommand {
	kept := commands[:0]
	for _, command := range commands {
		if !d.expired(command.expiresAt) {
			kept = append(kept, command)
		}
	}
	return kept
}

func (d *commandDispatcher) expired(expiresAt int64) bool {
	return expiresAt <= d.now().UnixMilli()
}

func (d *commandDispatcher) emitWindowLocked(window commandWindow, command queuedCommand) {
	if window.EmitEvent(command.name, command.data) && d.onError != nil {
		d.onError(command.name, errors.New("Wails event was cancelled"))
	}
}

func commandData(name string, raw json.RawMessage) (any, bool, error) {
	switch name {
	case "notification-failure", "background-stopping":
		var data bool
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, true, err
		}
		return data, true, nil
	case "acquisition-log":
		var data acquisition.OperationStatus
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, true, err
		}
		return data, true, nil
	default:
		return nil, false, nil
	}
}
