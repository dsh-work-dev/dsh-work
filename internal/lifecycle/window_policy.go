package lifecycle

import "sync"

// WindowCloseAction is the platform-neutral result of a user closing one
// Work window. The Wails/platform edge decides how to realize the action.
type WindowCloseAction string

const (
	WindowCloseToTray WindowCloseAction = "tray"
	WindowCloseQuit   WindowCloseAction = "quit"
)

type WindowCloseDecision struct {
	Action      WindowCloseAction
	LastVisible bool
}

// WindowLedger tracks visibility independently from native window handles.
// It keeps the shared contract testable and prevents a platform hook from
// treating WindowClosing as an implicit application quit.
type WindowLedger struct {
	mu          sync.Mutex
	visible     map[string]bool
	closeToTray bool
	quitting    bool
}

func NewWindowLedger(closeToTray bool, windowIDs ...string) *WindowLedger {
	visible := make(map[string]bool, len(windowIDs))
	for _, id := range windowIDs {
		visible[id] = false
	}
	return &WindowLedger{visible: visible, closeToTray: closeToTray}
}

func (l *WindowLedger) SetCloseToTray(enabled bool) {
	l.mu.Lock()
	l.closeToTray = enabled
	l.mu.Unlock()
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

func (l *WindowLedger) RequestClose(id string) WindowCloseDecision {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.quitting {
		return WindowCloseDecision{Action: WindowCloseQuit, LastVisible: true}
	}
	l.visible[id] = false
	lastVisible := l.visibleCountLocked() == 0
	if lastVisible && !l.closeToTray {
		l.quitting = true
		return WindowCloseDecision{Action: WindowCloseQuit, LastVisible: true}
	}
	return WindowCloseDecision{Action: WindowCloseToTray, LastVisible: lastVisible}
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
