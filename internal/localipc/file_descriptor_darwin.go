//go:build darwin && cgo

package localipc

/*
#include <stdlib.h>
#include "mach_peer.h"
*/
import "C"

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const fileDescriptorMarker = 'D'

func ReceiveWriteOnlyFile(ctx context.Context, endpoint string, expected Process) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read, write, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer read.Close()
	defer write.Close()
	stop := context.AfterFunc(ctx, func() { _, _ = write.Write([]byte{1}) })
	defer stop()
	path := C.CString(endpoint)
	defer C.free(unsafe.Pointer(path))
	var fd C.int
	status := C.dsh_ipc_receive_writer(path, C.int(read.Fd()), C.uint32_t(expected.PID), C.uint64_t(expected.Started), &fd)
	if ctx.Err() != nil {
		if status == 0 {
			_ = unix.Close(int(fd))
		}
		return nil, ctx.Err()
	}
	if status != 0 {
		return nil, syscall.Errno(status)
	}
	return os.NewFile(uintptr(fd), "download-writer"), nil
}

// SendWriteOnlyFile sends one file descriptor over a dedicated, already
// authenticated connection. Kernel identity and descriptor type are checked
// immediately before send. The caller retains ownership of its local file.
func SendWriteOnlyFile(c net.Conn, expected Process, file *os.File) error {
	peer, err := Peer(c)
	if err != nil || peer != expected || file == nil {
		return errors.New("local IPC writer peer rejected")
	}
	owned := c.(*authenticatedConn)
	socket, ok := owned.Conn.(*net.UnixConn)
	if !ok {
		return errors.New("local IPC writer socket unavailable")
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return err
	}
	// Keeping the raw file callback live keeps Close from recycling this fd
	// while the other socket is sending its capability.
	var sendErr error
	if err = raw.Control(func(fd uintptr) {
		flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
		var info unix.Stat_t
		if err != nil || flags&unix.O_ACCMODE != unix.O_WRONLY || unix.Fstat(int(fd), &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Nlink != 1 {
			sendErr = errors.New("local IPC writer capability rejected")
			return
		}
		rights := unix.UnixRights(int(fd))
		n, oob, err := socket.WriteMsgUnix([]byte{fileDescriptorMarker}, rights, nil)
		if err != nil {
			sendErr = err
		} else if n != 1 || oob != len(rights) {
			sendErr = io.ErrShortWrite
		}
	}); err != nil {
		return err
	}
	if sendErr != nil {
		return sendErr
	}
	var ack [1]byte
	if _, err = io.ReadFull(c, ack[:]); err != nil {
		return err
	}
	if ack[0] != 'A' {
		return errors.New("local IPC writer acknowledgement rejected")
	}
	return nil
}
