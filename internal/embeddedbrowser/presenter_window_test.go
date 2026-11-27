package embeddedbrowser

import (
	"context"
	"testing"
	"time"
)

// Registry ordering qualification; actual HWND/CEF evidence remains a separate gate.
func TestWindowRetirementSerializesTargetCreation(t *testing.T) {
	peer := PresenterPeer{PID: 7, Started: 9}
	w := &presenterWindow{peer: peer, parent: 11, lease: "window-lease", done: make(chan struct{})}
	r := &PresenterRegistry{windows: map[string]*presenterWindow{"workspace": w}, views: map[string]*presentation{}}
	locked, e := r.lockWindow(peer, "workspace", 11, "window-lease")
	if e != nil {
		t.Fatal(e)
	}
	started, finished := make(chan struct{}), make(chan error, 1)
	go func() { close(started); finished <- r.CloseWindow(context.Background(), peer, "workspace") }()
	<-started
	select {
	case e := <-finished:
		t.Fatalf("retired while creation owned serial: %v", e)
	default:
	}
	p := &presentation{peer: peer, surface: "workspace", view: ViewState{State: "ready"}}
	r.mu.Lock()
	r.views["workspace/slot"] = p
	r.mu.Unlock()
	locked.serial.Unlock()
	select {
	case e := <-finished:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("retirement blocked")
	}
	if p.view.State != "NeedsReload" || !p.cleaned {
		t.Fatal("retirement missed newly created view")
	}
	if _, e := r.Update(context.Background(), peer, "workspace", 11, "window-lease", "late", "https://example.test", SlotBounds{}, 1, true); e == nil {
		t.Fatal("retired window accepted late Update")
	}
	if r.get("workspace", "late") != nil {
		t.Fatal("late Update created target owner")
	}
	w.closed = false
	w.lease = "new-lease"
	if _, e := r.lockWindow(peer, "workspace", 11, "window-lease"); e == nil {
		t.Fatal("old actual window lease accepted")
	}
}

// Controlled registry-state negatives; actual OS/native acceptance remains the
// separately recorded live presenter gate.
func TestOwnedWindowViewSelectionRejectsForeignAndRetiredLease(t *testing.T) {
	peer := PresenterPeer{PID: 7, Started: 9}
	broker := &Broker{}
	target := &OwnedTarget{broker: broker}
	w := &presenterWindow{peer: peer, parent: 11, lease: "window"}
	p := &presentation{peer: peer, view: ViewState{Lease: "view", Visible: true, State: "ready"}, target: target}
	views := &PresenterRegistry{windows: map[string]*presenterWindow{"workspace": w}, views: map[string]*presentation{"workspace/slot": p}}
	i := &instancePresenter{peer: peer, views: views, broker: broker}
	r := &InstancePresenterRegistry{surfaces: map[string]*instancePresenter{"workspace": i}}
	if got, err := r.OwnedWindowViewTarget(peer, "workspace", 11, "window", "slot", "view"); err != nil || got != target {
		t.Fatal(got, err)
	}
	for _, test := range []struct {
		name         string
		peer         PresenterPeer
		surface      string
		parent       uintptr
		window, view string
	}{
		{"other UI", PresenterPeer{PID: 8, Started: 9}, "workspace", 11, "window", "view"},
		{"PID reused", PresenterPeer{PID: 7, Started: 10}, "workspace", 11, "window", "view"},
		{"foreign surface", peer, "workspace-other", 11, "window", "view"},
		{"old window", peer, "workspace", 11, "retired-window", "view"},
		{"wrong HWND", peer, "workspace", 12, "window", "view"},
		{"old view", peer, "workspace", 11, "window", "retired-view"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := r.OwnedWindowViewTarget(test.peer, test.surface, test.parent, test.window, "slot", test.view); err == nil {
				t.Fatal("selection admitted")
			}
		})
	}
	p.view.Visible = false
	if _, err := r.OwnedWindowViewTarget(peer, "workspace", 11, "window", "slot", "view"); err == nil {
		t.Fatal("hidden target admitted")
	}
	p.view.Visible = true
	w.closed = true
	if _, err := r.OwnedWindowViewTarget(peer, "workspace", 11, "window", "slot", "view"); err == nil {
		t.Fatal("retired native window admitted")
	}
}
