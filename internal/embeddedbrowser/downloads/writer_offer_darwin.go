//go:build darwin && cgo

package downloads

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

// WriterOffer carries a one-use descriptor transfer for a single armed grant.
// The socket is separate from the framed broker stream, so a partial send or
// a canceled offer cannot inject an ancillary message into a control frame.
type WriterOffer struct {
	Endpoint string
	cancel   context.CancelFunc
	done     chan struct{}
	err      error
}

func (m *Manager) OfferWriter(ctx context.Context, id string, expected localipc.Process) (*WriterOffer, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if expected.PID == 0 || expected.Started == 0 {
		return nil, errors.New("download writer process required")
	}
	directory, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-dl-")
	if err != nil {
		return nil, err
	}
	endpoint := filepath.Join(directory, "w.sock")
	listener, err := localipc.Listen(endpoint, true)
	if err != nil {
		_ = os.Remove(directory)
		return nil, err
	}
	file, err := m.OpenWriter(id)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	lifetime, cancel := context.WithTimeout(ctx, 5*time.Second)
	offer := &WriterOffer{Endpoint: endpoint, cancel: cancel, done: make(chan struct{})}
	go offer.serve(lifetime, listener, file, expected)
	return offer, nil
}

func (o *WriterOffer) Wait(ctx context.Context) error {
	select {
	case <-o.done:
		return o.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Close joins the local offer. If it already delivered a writer, the broker
// must still drain that writer before the Host aborts or publishes the stage.
func (o *WriterOffer) Close() error {
	o.cancel()
	<-o.done
	return o.err
}

func (o *WriterOffer) serve(ctx context.Context, listener net.Listener, file *os.File, expected localipc.Process) {
	defer close(o.done)
	defer o.cancel()
	stopListener := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer func() {
		stopListener()
		o.err = errors.Join(o.err, listener.Close(), file.Close())
	}()
	for {
		connection, err := listener.Accept()
		if err != nil {
			o.err = errors.Join(ctx.Err(), err)
			return
		}
		peer, err := localipc.Peer(connection)
		if err != nil || peer != expected {
			_ = connection.Close()
			continue
		}
		if deadline, ok := ctx.Deadline(); ok {
			_ = connection.SetDeadline(deadline)
		}
		stopConnection := context.AfterFunc(ctx, func() { _ = connection.Close() })
		if err = ctx.Err(); err == nil {
			err = localipc.SendWriteOnlyFile(connection, expected, file)
		}
		stopConnection()
		_ = connection.Close()
		o.err = err
		return
	}
}
