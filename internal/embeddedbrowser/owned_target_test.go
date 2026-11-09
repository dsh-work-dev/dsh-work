package embeddedbrowser

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"net"
	"strings"
	"testing"
	"time"
)

// These injected wire failures establish cleanup ownership, not CEF behavior.
// The actual CEF cancellation/dispatch boundary is checked by the Windows gate.
func TestUnknownBeginOutcomeHasIndependentCleanupOwner(t *testing.T) {
	for _, confirm := range []bool{true, false} {
		t.Run(map[bool]string{true: "late-begin-ACK-lost-revoke-confirmed", false: "revoke-unconfirmed-terminal-owner"}[confirm], func(t *testing.T) {
			host, peer := net.Pipe()
			defer peer.Close()
			exited := make(chan struct{})
			close(exited)
			worker := &cleanupWorker{exited: exited}
			b := &Broker{conn: host, worker: worker, generation: "current", routes: map[string]*route{}, done: make(chan struct{}), closed: make(chan struct{})}
			defer b.Close()
			target := &OwnedTarget{broker: b, owner: "target"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			revoked := make(chan struct{})
			protocolErrors := make(chan error, 1)
			go b.receive()
			go func() {
				for {
					var m struct{ Kind, Request string }
					if err := browsernative.ReadWireMessage(peer, &m); err != nil {
						return
					}
					switch m.Kind {
					case "target.begin":
						cancel() // Native may have applied it, ACK is lost.
					case "target.revoke":
						close(revoked)
						if confirm {
							if err := browsernative.WriteWireMessage(peer, map[string]any{"kind": "target.result", "generation": "current", "request": m.Request, "result": map[string]any{}}); err != nil {
								protocolErrors <- err
								return
							}
						}
					case "shutdown":
						return
					}
				}
			}()
			_, err := target.BeginOwnedCall(ctx, automation.OwnedBrowserCall{Token: "unknown"})
			if !errors.Is(err, context.Canceled) {
				t.Fatal("caller cancellation lost", err)
			}
			select {
			case <-revoked:
			default:
				t.Fatal("no independent cleanup request")
			}
			if confirm {
				select {
				case <-b.done:
					t.Fatal("confirmed revoke destroyed retained document owner")
				default:
				}
			} else {
				if !strings.Contains(err.Error(), "CEF_GUARD_CLEANUP_UNCONFIRMED") {
					t.Fatal("unconfirmed cleanup reported success", err)
				}
				select {
				case <-b.Closed():
				case <-time.After(time.Second):
					t.Fatal("terminal cleanup owner did not close")
				}
				if worker.closed.Load() != 1 {
					t.Fatal("supervisor owner not joined")
				}
			}
			select {
			case err := <-protocolErrors:
				t.Fatal(err)
			default:
			}
		})
	}
}
