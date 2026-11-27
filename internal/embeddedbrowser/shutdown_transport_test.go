package embeddedbrowser

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation/browsernative"
)

type shutdownDrainWorker struct {
	*cleanupWorker
	waiting chan struct{}
	once    sync.Once
}

func (w *shutdownDrainWorker) Exited() <-chan struct{} {
	w.once.Do(func() { close(w.waiting) })
	return w.cleanupWorker.Exited()
}

// Exact protocol scheduling, not an engine substitute. The final native frame
// starts only after Close reaches its process-exit wait: old immediate pipe
// closure deterministically refuses that frame. EOF re-enters Close through the
// normal reader abort path while the first Close still owns closeMu.
func TestShutdownKeepsReaderUntilNativeExit(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	worker := &shutdownDrainWorker{cleanupWorker: &cleanupWorker{exited: make(chan struct{})}, waiting: make(chan struct{})}
	b := &Broker{conn: host, worker: worker, generation: "shutdown", routes: map[string]*route{}, done: make(chan struct{}), closed: make(chan struct{})}
	target := &OwnedTarget{broker: b, owner: "last-native-target"}
	target.register()
	go b.receive()
	native := make(chan error, 1)
	go func() {
		defer close(worker.exited)
		var command struct{ Kind string }
		if err := browsernative.ReadWireMessage(peer, &command); err != nil {
			native <- err
			return
		}
		if command.Kind != "shutdown" {
			native <- errors.New("shutdown was not dispatched")
			return
		}
		select {
		case <-b.done:
		default:
			native <- errors.New("shutdown preceded Agent invalidation")
			return
		}
		<-worker.waiting
		err := browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.closed", "generation": "shutdown", "owner": target.owner})
		native <- err
		peer.Close() // actual native exit closes its pipe; reader abort must not deadlock.
	}()
	finished := make(chan error, 1)
	go func() { finished <- b.Close() }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown/receive EOF re-entry deadlocked")
	}
	if err := <-native; err != nil {
		t.Fatal("native final frame could not drain before process exit", err)
	}
	select {
	case <-target.nativeClosed:
	case <-time.After(time.Second):
		t.Fatal("last authenticated native frame was not received")
	}
	if worker.forced.Load() != 0 || worker.closed.Load() != 1 || b.CleanupPending() {
		t.Fatal("normal drain forced or retained process owner")
	}
	// Completed Close remains independently usable after the EOF abort goroutine.
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
}
