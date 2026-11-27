package embeddedbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/supervisor"
)

// Failure injection here checks cleanup error contracts, not engine behavior.
type cleanupWorker struct {
	exited                       chan struct{}
	emptyErr, forceErr, closeErr error
	closed, forced               atomic.Int32
}

func (w *cleanupWorker) Events() <-chan supervisor.OutputEvent { return nil }
func (w *cleanupWorker) Exited() <-chan struct{}               { return w.exited }
func (w *cleanupWorker) ExitResult() supervisor.ExitResult {
	return supervisor.ExitResult{Started: true}
}
func (w *cleanupWorker) RequestStop(context.Context) error   { return nil }
func (w *cleanupWorker) ForceStop(context.Context) error     { w.forced.Add(1); return w.forceErr }
func (w *cleanupWorker) WaitEmpty(context.Context) error     { return w.emptyErr }
func (w *cleanupWorker) Diagnostics() supervisor.Diagnostics { return supervisor.Diagnostics{} }
func (w *cleanupWorker) Close() error                        { w.closed.Add(1); return w.closeErr }

func TestCleanupRetainsProcessTreeAndSupervisorErrors(t *testing.T) {
	exited := make(chan struct{})
	close(exited)
	emptyErr, forceErr, closeErr := errors.New("tree query failed"), errors.New("force failed"), errors.New("join failed")
	w := &cleanupWorker{exited: exited, emptyErr: emptyErr, forceErr: forceErr, closeErr: closeErr}
	b := &Broker{worker: w, routes: map[string]*route{}, done: make(chan struct{}), closed: make(chan struct{})}
	err := b.Close()
	for _, want := range []error{emptyErr, forceErr} {
		if !errors.Is(err, want) {
			t.Fatalf("cleanup lost %v: %v", want, err)
		}
	}
	if w.closed.Load() != 0 || w.forced.Load() != 1 || !b.CleanupPending() {
		t.Fatal("unproven tree join disposed its owner")
	}
	w.emptyErr = nil
	if retry := b.Close(); !errors.Is(retry, closeErr) || w.closed.Load() != 1 || !b.CleanupPending() {
		t.Fatal("worker join failure lost owner/history")
	}
	w.closeErr = nil
	if retry := b.Close(); !errors.Is(retry, closeErr) || w.closed.Load() != 2 || b.CleanupPending() {
		t.Fatal("retained supervisor join cannot be retried or lost its failure history")
	}

}

func TestDamagedProtocolTerminatesOwnedBroker(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	exited := make(chan struct{})
	close(exited)
	w := &cleanupWorker{exited: exited}
	b := &Broker{conn: host, worker: w, generation: "current", routes: map[string]*route{}, done: make(chan struct{}), closed: make(chan struct{})}
	go b.receive()
	if err := browsernative.WriteWireMessage(peer, map[string]any{"kind": "ready", "generation": "stale", "protocol": 1}); err != nil {
		t.Fatal(err)
	}
	var command struct{ Kind string }
	if err := browsernative.ReadWireMessage(peer, &command); err != nil || command.Kind != "shutdown" {
		t.Fatalf("terminal protocol cleanup: %s %v", command.Kind, err)
	}
	select {
	case <-b.Closed():
	case <-time.After(time.Second):
		t.Fatal("protocol failure retained live owner")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if w.closed.Load() != 1 {
		t.Fatal("protocol failure did not join worker")
	}
}

func TestRendererPauseRetainsOwnerAndAllowsControlRelease(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	exited := make(chan struct{})
	close(exited)
	w := &cleanupWorker{exited: exited}
	b := &Broker{conn: host, worker: w, generation: "current", routes: map[string]*route{}, done: make(chan struct{}), closed: make(chan struct{})}
	r := &route{broker: b, id: "owned", messages: make(chan json.RawMessage, 1), done: make(chan struct{})}
	b.routes[r.id] = r
	go b.receive()
	if err := browsernative.WriteWireMessage(peer, map[string]any{"kind": "fault", "generation": "current", "scope": "broker"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("pause did not invalidate control")
	}
	if w.closed.Load() != 0 || w.forced.Load() != 0 {
		t.Fatal("renderer uncertainty destroyed broker")
	}
	b.mu.Lock()
	retained := b.routes[r.id] == r
	b.mu.Unlock()
	if !retained {
		t.Fatal("pause dropped resource owner")
	}
	wires := make(chan string, 2)
	go func() {
		for {
			var m struct{ Kind string }
			if browsernative.ReadWireMessage(peer, &m) != nil {
				return
			}
			wires <- m.Kind
		}
	}()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-wires; got != "release" {
		t.Fatalf("pause disposal: %s", got)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if got := <-wires; got != "shutdown" {
		t.Fatalf("owner shutdown: %s", got)
	}
}
