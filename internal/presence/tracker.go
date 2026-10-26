// Package presence tracks focus by surface without depending on a desktop or
// IPC implementation.
package presence

import "sync"

type Tracker struct {
	mu          sync.RWMutex
	focused     map[string]bool
	lastFocused string
}

func New() *Tracker { return &Tracker{focused: make(map[string]bool)} }

func (t *Tracker) SetFocused(surface string, focused bool) {
	if t == nil || surface == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if focused {
		t.focused[surface] = true
		t.lastFocused = surface
	} else {
		delete(t.focused, surface)
	}
}

func (t *Tracker) Clear(surface string) {
	if t == nil || surface == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.focused, surface)
	if t.lastFocused == surface {
		t.lastFocused = ""
	}
}

func (t *Tracker) ClearAll() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.focused = make(map[string]bool)
	t.lastFocused = ""
	t.mu.Unlock()
}

func (t *Tracker) AnyFocused() bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.focused) > 0
}

func (t *Tracker) Focused(surface string) bool {
	if t == nil {
		return false
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.focused[surface]
}

func (t *Tracker) LastFocused() string {
	if t == nil {
		return "workspace"
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.lastFocused == "" {
		return "workspace"
	}
	return t.lastFocused
}
