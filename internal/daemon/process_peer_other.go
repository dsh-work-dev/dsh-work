//go:build !windows && !linux

package daemon

import (
	"context"
	"errors"
	"net"
)

func nativePeerContext(ctx context.Context, c net.Conn) context.Context { return ctx }

func verifyServerProcess(net.Conn, ProcessPeer) error {
	return errors.New("owned process pipe verification requires Windows")
}
