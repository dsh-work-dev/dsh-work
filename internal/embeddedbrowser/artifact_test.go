package embeddedbrowser

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation/browsernative"
)

// Deterministic protocol scheduling, not engine acceptance: the native bound
// and revoke ACK arrive only after the Open caller has canceled its context.
func TestCanceledArtifactInitializationConsumesLateACKAndClosesRoot(t *testing.T) {
	host, peer := net.Pipe()
	defer peer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := &Broker{conn: host, generation: "launch", routes: map[string]*route{}, artifacts: map[string]*Artifact{}, done: make(chan struct{}), closed: make(chan struct{}), observe: func(direction, method string) {
		if direction == "artifact" && method == "open-dispatched" {
			cancel()
		}
	}}
	go b.receive()
	retained := make(chan *Artifact, 1)
	peerErr := make(chan error, 1)
	go func() {
		var opened struct{ Kind, Lease, Partition string }
		if err := browsernative.ReadWireMessage(peer, &opened); err != nil {
			peerErr <- err
			return
		}
		b.mu.Lock()
		a := b.artifacts[opened.Lease]
		b.mu.Unlock()
		retained <- a
		var revoked struct{ Kind string }
		if err := browsernative.ReadWireMessage(peer, &revoked); err != nil {
			peerErr <- err
			return
		}
		if revoked.Kind != "artifact.revoke" {
			peerErr <- errors.New("cleanup did not dispatch revoke")
			return
		}
		for _, message := range []map[string]any{
			{"kind": "artifact.bound", "browser": 7},
			{"kind": "artifact.revoked", "closedBrowsers": 1, "closedFrames": 1, "liveFrames": 0, "partitionRetired": true},
		} {
			message["generation"] = "launch"
			message["lease"] = opened.Lease
			message["partition"] = opened.Partition
			if err := browsernative.WriteWireMessage(peer, message); err != nil {
				peerErr <- err
				return
			}
		}
		var stop struct{ Kind string }
		err := browsernative.ReadWireMessage(peer, &stop)
		if err == nil && stop.Kind != "shutdown" {
			err = errors.New("missing owner shutdown")
		}
		peerErr <- err
	}()
	if _, err := b.OpenArtifact(ctx, t.TempDir(), "index.html"); !errors.Is(err, context.Canceled) {
		t.Fatal("lost caller cancellation", err)
	}
	if b.ArtifactCount() != 0 {
		t.Fatal("failed initializer retained artifact registry")
	}
	a := <-retained
	if a == nil {
		t.Fatal("missing real root owner")
	}
	if _, err := a.root.Open("index.html"); !errors.Is(err, os.ErrClosed) {
		t.Fatal("root remained open after late ACK", err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-peerErr:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("protocol fixture retained owner")
	}
}
