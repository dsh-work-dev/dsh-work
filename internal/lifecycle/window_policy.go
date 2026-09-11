package lifecycle

import "sync"

// WindowLedger tracks visibility independently from native window handles.
// It keeps the shared contract testable and prevents a platform hook from
// treating WindowClosing as an implicit application quit.
type WindowLedger struct {
	mu       sync.Mutex
	visible  map[string]bool
	quitting bool
}

func NewWindowLedger(windowIDs ...string) *WindowLedger {
	visible := make(map[string]bool, len(windowIDs))
	for _, id := range windowIDs {
		visible[id] = false
	}
	return &WindowLedger{visible: visible}
}

func (l *WindowLedger) SetVisible(id string, visible bool) {
	l.mu.Lock()
	if !visible || !l.quitting {
		l.visible[id] = visible
	}
	l.mu.Unlock()
}

// TryShow records a window as visible only when the application is not in a
// managed quit. Keeping the guard and the ledger update atomic prevents a
// tray action racing with quit from bringing a window back on screen.
func (l *WindowLedger) TryShow(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quitting {
		return false
	}
	l.visible[id] = true
	return true
}

func (l *WindowLedger) BeginQuit() {
	l.mu.Lock()
	l.quitting = true
	for id := range l.visible {
		l.visible[id] = false
	}
	l.mu.Unlock()
}

// CancelQuit reopens the lifecycle boundary after a pre-exit cleanup failure.
// Windows remain logically hidden until a caller explicitly shows one again.
func (l *WindowLedger) CancelQuit() {
	l.mu.Lock()
	l.quitting = false
	l.mu.Unlock()
}

func (l *WindowLedger) IsQuitting() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.quitting
}

func (l *WindowLedger) VisibleCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.visibleCountLocked()
}

func (l *WindowLedger) visibleCountLocked() int {
	count := 0
	for _, visible := range l.visible {
		if visible {
			count++
		}
	}
	return count
}
