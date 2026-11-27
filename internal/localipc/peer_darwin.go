//go:build darwin && cgo

package localipc

/*
#include "mach_peer.h"
*/
import "C"

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
)

type Process struct {
	PID     uint32
	Started uint64
}
type authenticatedConn struct {
	net.Conn
	peer C.dsh_ipc_peer
}

func checkSupport() error { return nil }
func ProcessStarted(pid uint32) (uint64, error) {
	started := uint64(C.dsh_ipc_process_started(C.uint32_t(pid)))
	if started == 0 {
		return 0, errors.New("local IPC process is not live")
	}
	return started, nil
}
func socketControl(c net.Conn, call func(int) error) error {
	sc, ok := c.(syscall.Conn)
	if !ok {
		return errors.New("local IPC socket handle unavailable")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return err
	}
	var inner error
	if err = raw.Control(func(fd uintptr) { inner = call(int(fd)) }); err != nil {
		return err
	}
	return inner
}
func authenticateConn(ctx context.Context, c net.Conn) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	defer write.Close()
	stop := context.AfterFunc(ctx, func() { write.Write([]byte{1}) })
	defer stop()
	owned := &authenticatedConn{Conn: c}
	err = socketControl(c, func(fd int) error {
		code := C.dsh_ipc_authenticate(C.int(fd), C.int(read.Fd()), &owned.peer)
		if code != 0 {
			return syscall.Errno(code)
		}
		return nil
	})
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return owned, nil
}
func Peer(c net.Conn) (Process, error) {
	owned, ok := c.(*authenticatedConn)
	if !ok {
		return Process{}, errors.New("local IPC Mach admission required")
	}
	err := socketControl(owned.Conn, func(fd int) error {
		code := C.dsh_ipc_recheck(C.int(fd), &owned.peer)
		if code != 0 {
			return syscall.Errno(code)
		}
		return nil
	})
	if err != nil {
		return Process{}, err
	}
	return Process{PID: uint32(owned.peer.pid), Started: uint64(owned.peer.started)}, nil
}
