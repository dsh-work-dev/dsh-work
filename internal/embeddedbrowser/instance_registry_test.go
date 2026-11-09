package embeddedbrowser

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// These are owner-state failure injections, not native/browser acceptance.
func TestInstancePausedBrokerCannotRestartFromNewTabOrRecovery(t *testing.T) {
	w := &cleanupWorker{exited: make(chan struct{})}
	b := &Broker{worker: w, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	b.invalidate(errors.New("CEF_RENDERER_SCOPE_UNCERTAIN"))
	starts := 0
	i := &instancePresenter{peer: PresenterPeer{PID: 7, Started: 11}, broker: b, views: NewPresenterRegistry(b)}
	owner := &drainOwner{done: make(chan struct{})}
	target := &OwnedTarget{broker: b}
	p := &presentation{peer: i.peer, owner: owner, target: target, view: ViewState{State: "ready", Lease: "retained", Target: "2", Document: "document-before-pause", Frame: "5-CEF-FRAME", Visible: true}}
	i.views.views["workspace/old-slot"] = p
	r := &InstancePresenterRegistry{surfaces: map[string]*instancePresenter{"workspace": i}, config: InstanceRegistryConfig{StartBroker: func(context.Context, InstanceConfig) (*Broker, error) {
		starts++
		return nil, errors.New("unexpected restart")
	}}}
	for _, call := range []func() error{
		func() error {
			_, e := r.Update(context.Background(), i.peer, "workspace", 1, "lease", "new-slot", "https://example.test", SlotBounds{}, 1, true)
			return e
		},
		func() error {
			_, e := r.Recover(context.Background(), i.peer, "workspace", 1, "lease", "old-slot", "")
			return e
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "CEF_BROKER_CONTROL_PAUSED") {
			t.Fatal("paused entry changed owner", err)
		}
	}
	if starts != 0 || w.closed.Load() != 0 || w.forced.Load() != 0 {
		t.Fatal("paused browser was destroyed or replaced")
	}
	r.markPausedLocked(i)
	view, err := i.views.State(context.Background(), i.peer, "workspace", "old-slot")
	if err != nil || view.State != "controlPaused" || view.Document != "document-before-pause" || view.Frame != "5-CEF-FRAME" || view.Target != "2" || view.Lease != "retained" || p.target != target || p.owner != owner || owner.closes.Load() != 0 {
		t.Fatal("pause retired or misreported healthy page", view, err)
	}
	r.markLostLocked(i)
	if p.view.Frame != "" {
		t.Fatal("terminal descriptor retained old frame identity")
	}
	select {
	case <-b.Closed():
		t.Fatal("pause became terminal cleanup")
	default:
	}
}

type retainedProfile struct{ closes int }

func (p *retainedProfile) Close() error { p.closes++; return nil }
func TestInstanceRetainsProfileUntilExplicitTreeJoin(t *testing.T) {
	exited := make(chan struct{})
	close(exited)
	w := &cleanupWorker{exited: exited, emptyErr: errors.New("tree still active")}
	b := &Broker{worker: w, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	profile := &retainedProfile{}
	i := &instancePresenter{broker: b, profile: profile}
	r := &InstancePresenterRegistry{instances: map[string]*instancePresenter{"current": i}}
	if e := r.retireLocked(i, true); e == nil || !b.CleanupPending() || i.broker != b || i.profile != io.Closer(profile) || profile.closes != 0 || w.closed.Load() != 0 {
		t.Fatal("pending tree lost retry/profile owner", e)
	}
	w.emptyErr = nil
	// A historical failure remains reportable even when the retained retry joins.
	if e := r.retireLocked(i, true); e == nil || b.CleanupPending() || i.broker != nil || i.profile != nil || profile.closes != 1 || w.closed.Load() != 1 {
		t.Fatal("profile did not release only after successful join", e)
	}
}

func TestCompletedInstanceCleanupKeepsAbnormalResult(t *testing.T) {
	exited := make(chan struct{})
	close(exited)
	failure := errors.New("supervisor join failed")
	w := &cleanupWorker{exited: exited, closeErr: failure}
	b := &Broker{worker: w, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	i := &instancePresenter{broker: b, profile: &retainedProfile{}}
	r := &InstancePresenterRegistry{instances: map[string]*instancePresenter{"current": i}}
	if err := r.retireLocked(i, true); !errors.Is(err, failure) || r.CleanupComplete() {
		t.Fatal("incomplete close lost owner/failure", err)
	}
	w.closeErr = nil
	if err := r.retireLocked(i, true); !errors.Is(err, failure) || !r.CleanupComplete() {
		t.Fatal("successful join lost abnormal result or remained unfinishable", err)
	}
	if err := r.Close(); !errors.Is(err, failure) || !strings.Contains(err.Error(), "CEF_CLEANUP_COMPLETED_ABNORMALLY") || !r.CleanupComplete() {
		t.Fatal("later close falsely reported normal cleanup", err)
	}
}
func TestLongTitleAndFailedDescriptorWriteRemainRecoverable(t *testing.T) {
	store, err := OpenInstanceStore(t.TempDir(), instanceStoreLock)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	c, err := store.Resolve("current")
	if err != nil {
		t.Fatal(err)
	}
	// A real transient filesystem failure, not an injected success fallback.
	if err = os.WriteFile(c.directory, []byte("occupied"), 0600); err != nil {
		t.Fatal(err)
	}
	p := &presentation{surface: "workspace", view: ViewState{URL: "https://example.test/path", Title: strings.Repeat("粤", 500) + "\x00"}}
	i := &instancePresenter{config: c, profile: &retainedProfile{}, views: &PresenterRegistry{views: map[string]*presentation{"workspace/slot": p}}}
	r := &InstancePresenterRegistry{config: InstanceRegistryConfig{Store: store}, instances: map[string]*instancePresenter{"current": i}}
	if err = r.retireLocked(i, false); err == nil || i.retired || i.profile == nil {
		t.Fatal("failed descriptor write irreversibly retired live instance", err)
	}
	if err = os.Remove(c.directory); err != nil {
		t.Fatal(err)
	}
	if err = r.retireLocked(i, false); err != nil || !i.retired || !r.CleanupComplete() {
		t.Fatal("descriptor persistence/close could not retry", err)
	}
	rows, err := store.Descriptors(c)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	if !utf8.ValidString(rows[0].Title) || len(rows[0].Title) > 1024 || strings.ContainsRune(rows[0].Title, 0) || len(rows[0].Title) < 1021 {
		t.Fatalf("unsafe/invalid normalized title: bytes=%d", len(rows[0].Title))
	}
}
