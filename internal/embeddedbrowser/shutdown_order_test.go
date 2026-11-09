package embeddedbrowser

import (
	"context"
	"encoding/json"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/lifecycle"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type drainOwner struct {
	closes atomic.Int32
	done   chan struct{}
}

func (o *drainOwner) Done() <-chan struct{} { return o.done }
func (o *drainOwner) Close() error          { o.closes.Add(1); return nil }

// Injected wire sequencing checks owners/signals only, not CEF lifecycle.
func TestPresenterDrainFailureKeepsStreamAndRetryOwner(t *testing.T) {
	for _, windowOnly := range []bool{false, true} {
		name := "registry"
		if windowOnly {
			name = "window"
		}
		t.Run(name, func(t *testing.T) { testPresenterDrainFailure(t, windowOnly) })
	}
}
func testPresenterDrainFailure(t *testing.T, windowOnly bool) {
	host, peer := net.Pipe()
	exited := make(chan struct{})
	close(exited)
	b := &Broker{conn: host, generation: "current", worker: &cleanupWorker{exited: exited}, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	target := &OwnedTarget{broker: b, owner: "target"}
	target.register()
	go b.receive()
	t.Cleanup(func() { peer.Close(); b.Close() })
	nativeOwner, windowOwner := &drainOwner{}, &drainOwner{}
	identity := PresenterPeer{PID: 7, Started: 11}
	window := &presenterWindow{peer: identity, parent: 1, lease: "window", owner: windowOwner, done: make(chan struct{})}
	p := &presentation{peer: identity, surface: "workspace", parent: 1, owner: nativeOwner, target: target, view: ViewState{State: "ready", Visible: true}}
	r := &PresenterRegistry{broker: b, windows: map[string]*presenterWindow{"workspace": window}, views: map[string]*presentation{"workspace/slot": p}}
	closePresenter := r.Close
	if windowOnly {
		closePresenter = func() error { return r.CloseWindow(context.Background(), identity, "workspace") }
	}
	finished := make(chan error, 1)
	go func() { finished <- closePresenter() }()
	var request struct{ Kind, Request string }
	if e := browsernative.ReadWireMessage(peer, &request); e != nil || request.Kind != "target.close" {
		t.Fatal(request, e)
	}
	window.serial.Lock()
	closed := window.closed
	window.serial.Unlock()
	if !closed {
		t.Fatal("new admission not retired")
	}
	select {
	case <-window.done:
		t.Fatal("UI stream ended before native drain")
	default:
	}
	if nativeOwner.closes.Load() != 0 || windowOwner.closes.Load() != 0 {
		t.Fatal("live retry owner discarded")
	}
	if e := browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "current", "request": request.Request, "error": "injected-native-drain"}); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-finished:
		if e == nil {
			t.Fatal("failure swallowed")
		}
	case <-time.After(time.Second):
		t.Fatal("failed close did not return")
	}
	select {
	case <-window.done:
		t.Fatal("failed close ended UI stream")
	default:
	}
	if p.target != target || p.cleaned || nativeOwner.closes.Load() != 0 || windowOwner.closes.Load() != 0 {
		t.Fatal("failed native close lost retry owner")
	}
	go func() { finished <- closePresenter() }()
	if e := browsernative.ReadWireMessage(peer, &request); e != nil {
		t.Fatal(e)
	}
	for _, m := range []map[string]any{{"kind": "target.closed", "generation": "current", "owner": "target"}, {"kind": "target.result", "generation": "current", "request": request.Request, "result": json.RawMessage(`{}`)}} {
		if e := browsernative.WriteWireMessage(peer, m); e != nil {
			t.Fatal(e)
		}
	}
	if e := <-finished; e != nil {
		t.Fatal(e)
	}
	select {
	case <-window.done:
	default:
		t.Fatal("successful retry did not complete UI stream")
	}
	if !p.cleaned || nativeOwner.closes.Load() != 1 || windowOwner.closes.Load() != 1 {
		t.Fatal("successful drain did not close once")
	}
	if e := closePresenter(); e != nil {
		t.Fatal(e)
	}
	if windowOwner.closes.Load() != 1 || nativeOwner.closes.Load() != 1 {
		t.Fatal("completion repeated owner close")
	}
}

func TestInstanceNativeDrainFailureDoesNotQuitOrStopBroker(t *testing.T) {
	// A failing native close must return before broker shutdown and must leave
	// the stream signal open. The caller's existing QuitFlow decides retries.
	host, peer := net.Pipe()
	exited := make(chan struct{})
	close(exited)
	worker := &cleanupWorker{exited: exited}
	b := &Broker{conn: host, generation: "current", worker: worker, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	target := &OwnedTarget{broker: b, owner: "target"}
	target.register()
	go b.receive()
	t.Cleanup(func() { peer.Close(); b.Close() })
	window := &presenterWindow{peer: PresenterPeer{PID: 7}, done: make(chan struct{})}
	views := &PresenterRegistry{broker: b, windows: map[string]*presenterWindow{"workspace": window}, views: map[string]*presentation{"workspace/slot": {surface: "workspace", target: target, view: ViewState{URL: "https://example.test", State: "ready"}}}}
	// No durable store is needed for this signal test; keep the descriptor empty.
	views.views["workspace/slot"].view.URL = ""
	i := &instancePresenter{broker: b, views: views, profile: &retainedProfile{}}
	r := &InstancePresenterRegistry{}
	go func() {
		var m struct{ Kind, Request string }
		if e := browsernative.ReadWireMessage(peer, &m); e != nil {
			return
		}
		_ = browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "current", "request": m.Request, "error": "injected-native-drain"})
	}()
	if e := r.retireLocked(i, false); e == nil {
		t.Fatal("failed drain accepted")
	}
	if worker.closed.Load() != 0 || worker.forced.Load() != 0 || i.broker != b || i.profile == nil {
		t.Fatal("normal drain failure forced terminal broker/profile cleanup")
	}
	select {
	case <-window.done:
		t.Fatal("failed drain allowed UI stream termination")
	default:
	}
	select {
	case <-b.done:
		t.Fatal("failed drain invalidated broker lifetime")
	default:
	}

}

func TestDaemonQuitDrainFailureKeepsEveryUIUntilRetry(t *testing.T) {
	host, peer := net.Pipe()
	exited := make(chan struct{})
	close(exited)
	worker := &cleanupWorker{exited: exited}
	b := &Broker{conn: host, generation: "current", worker: worker, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	target := &OwnedTarget{broker: b, owner: "target"}
	target.register()
	go b.receive()
	t.Cleanup(func() { peer.Close(); b.Close() })
	aWindow, bWindow := &presenterWindow{done: make(chan struct{})}, &presenterWindow{done: make(chan struct{})}
	a := &instancePresenter{broker: b, profile: &retainedProfile{}, views: &PresenterRegistry{broker: b, windows: map[string]*presenterWindow{"a": aWindow}, views: map[string]*presentation{"a/slot": {surface: "a", target: target, view: ViewState{State: "ready"}}}}}
	other := &instancePresenter{views: &PresenterRegistry{windows: map[string]*presenterWindow{"b": bWindow}}}
	r := &InstancePresenterRegistry{instances: map[string]*instancePresenter{"a": a, "b": other}}
	go func() {
		for attempt := 0; ; {
			var m struct{ Kind, Request string }
			if browsernative.ReadWireMessage(peer, &m) != nil {
				return
			}
			if m.Kind == "shutdown" {
				return
			}
			if m.Kind != "target.close" {
				continue
			}
			attempt++
			if attempt == 1 {
				_ = browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "current", "request": m.Request, "error": "injected-native-drain"})
				continue
			}
			_ = browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.closed", "generation": "current", "owner": "target"})
			_ = browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "current", "request": m.Request, "result": json.RawMessage(`{}`)})
		}
	}()
	var flow lifecycle.QuitFlow
	var quits, stopping atomic.Int32
	failed := make(chan error, 1)
	completed := make(chan struct{})
	begin := func() bool {
		return flow.Begin(nil, r.Close, func() { stopping.Add(1); quits.Add(1); close(completed) }, func(e error) { failed <- e })
	}
	if !begin() {
		t.Fatal("first quit rejected")
	}
	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("quit failure did not return")
	}
	for _, w := range []*presenterWindow{aWindow, bWindow} {
		select {
		case <-w.done:
			t.Fatal("failed global drain ended a UI stream")
		default:
		}
	}
	if stopping.Load() != 0 || quits.Load() != 0 || worker.closed.Load() != 0 || worker.forced.Load() != 0 {
		t.Fatal("failed quit stopped UI or native broker")
	}
	deadline := time.Now().Add(time.Second)
	for flow.InProgress() {
		if time.Now().After(deadline) {
			t.Fatal("quit flow retained in-progress flag")
		}
		time.Sleep(time.Millisecond)
	}
	if !begin() {
		t.Fatal("retry rejected")
	}
	select {
	case <-completed:
	case <-time.After(time.Second):
		t.Fatal("successful retry did not quit")
	}
	for _, w := range []*presenterWindow{aWindow, bWindow} {
		select {
		case <-w.done:
		default:
			t.Fatal("successful global drain retained UI completion")
		}
	}
	if stopping.Load() != 1 || quits.Load() != 1 || worker.closed.Load() != 1 {
		t.Fatal("successful quit did not complete exactly once")
	}
}

func TestRetireDocumentKeepsWindowAdmissionAndStream(t *testing.T) {
	host, peer := net.Pipe()
	exited := make(chan struct{})
	close(exited)
	b := &Broker{conn: host, generation: "current", worker: &cleanupWorker{exited: exited}, done: make(chan struct{}), closed: make(chan struct{}), routes: map[string]*route{}}
	target := &OwnedTarget{broker: b, owner: "target"}
	target.register()
	go b.receive()
	t.Cleanup(func() { peer.Close(); b.Close() })
	peerID := PresenterPeer{PID: 7, Started: 11}
	owner := &drainOwner{}
	w := &presenterWindow{peer: peerID, parent: 1, lease: "window", owner: owner, done: make(chan struct{})}
	r := &PresenterRegistry{windows: map[string]*presenterWindow{"workspace": w}, views: map[string]*presentation{"workspace/slot": {peer: peerID, surface: "workspace", target: target, view: ViewState{State: "ready"}}}}
	go func() {
		var m struct{ Request string }
		if browsernative.ReadWireMessage(peer, &m) != nil {
			return
		}
		_ = browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.closed", "generation": "current", "owner": "target"})
		_ = browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "current", "request": m.Request, "result": json.RawMessage(`{}`)})
	}()
	if e := r.RetireDocument(context.Background(), peerID, "workspace", 1, "window"); e != nil {
		t.Fatal(e)
	}
	if w.closed || w.finished || owner.closes.Load() != 0 {
		t.Fatal("document retirement ended the native window lifetime")
	}
	select {
	case <-w.done:
		t.Fatal("document retirement ended the UI stream")
	default:
	}
	locked, e := r.lockWindow(peerID, "workspace", 1, "window")
	if e != nil {
		t.Fatal("document retirement lost reusable window lease", e)
	}
	locked.serial.Unlock()
}
