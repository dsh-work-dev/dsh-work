//go:build linux || (darwin && cgo)

package daemon

import (
	"context"
	"errors"
	"net"

	"github.com/local/dsh-work/internal/localipc"
)

func nativePeerContext(ctx context.Context, c net.Conn) context.Context {
	p, err := localipc.Peer(c)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, processPeerKey{}, ProcessPeer{PID: p.PID, Started: p.Started})
}
func verifyServerProcess(c net.Conn, expected ProcessPeer) error {
	if expected.PID == 0 || expected.Started == 0 {
		return errors.New("owned socket process instance required")
	}
	p, err := localipc.Peer(c)
	if err != nil {
		return err
	}
	if p.PID != expected.PID || p.Started != expected.Started {
		return errors.New("socket server does not match owned process instance")
	}
	return nil
}
