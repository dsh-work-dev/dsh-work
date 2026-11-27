package browserruntime

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation/browsernative"
)

// The adapter queues its last reply and closes before Execute starts waiting.
type replyBeforeCloseConn struct {
	net.Conn
	done <-chan struct{}
}

func (c replyBeforeCloseConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	if err == nil {
		<-c.done
	}
	return n, err
}

func TestBrowserActionReturnsAdapterErrorBeforeEOF(t *testing.T) {
	host, adapter := net.Pipe()
	t.Cleanup(func() { _ = adapter.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := &resource{ctx: ctx, cancel: cancel, done: make(chan struct{}), pending: make(map[uint64]chan envelope)}
	r.conn = replyBeforeCloseConn{Conn: host, done: r.done}
	t.Cleanup(func() { _ = r.Close() })
	if err := adapter.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	const actionError = "BROWSER_FIXTURE_ACTION_FAILED"
	written := make(chan error, 1)
	go func() {
		defer adapter.Close()
		var call envelope
		if err := browsernative.ReadWireMessage(adapter, &call); err != nil {
			written <- err
			return
		}
		written <- browsernative.WriteWireMessage(adapter, envelope{
			Kind: "result", ID: call.ID, Error: actionError, Fatal: true,
		})
	}()
	go r.receive()

	_, actionErr := r.Execute(context.Background(), "browser_click", map[string]any{})
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if actionErr == nil || actionErr.Error() != actionError || !r.AutomationTerminal() {
		t.Fatalf("action error lost on disconnect: %v", actionErr)
	}
}
