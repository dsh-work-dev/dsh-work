//go:build darwin && cgo

package localipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestDarwinNativeWriterReceiverRejectsMultipleOrReadableDescriptors(t *testing.T) {
	started, err := ProcessStarted(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	self := Process{PID: uint32(os.Getpid()), Started: started}
	for _, scenario := range []struct {
		name  string
		flags int
		count int
	}{
		{name: "extra writer", flags: os.O_WRONLY, count: 2},
		{name: "readable writer", flags: os.O_RDWR, count: 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			directory, err := os.MkdirTemp(SocketTempRoot(), "dsh-writer-wire-")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "w.sock")
			listener, err := Listen(path, true)
			if err != nil {
				_ = os.Remove(directory)
				t.Fatal(err)
			}
			defer listener.Close()
			file, err := os.OpenFile(filepath.Join(t.TempDir(), "fixture"), os.O_CREATE|scenario.flags, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			fds := make([]int, scenario.count)
			for i := range fds {
				fds[i] = int(file.Fd())
			}
			result := make(chan error, 1)
			release := make(chan struct{})
			defer close(release)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					result <- err
					return
				}
				defer conn.Close()
				socket := conn.(*authenticatedConn).Conn.(*net.UnixConn)
				_ = socket.SetWriteDeadline(time.Now().Add(time.Second))
				_, _, err = socket.WriteMsgUnix([]byte{fileDescriptorMarker}, unix.UnixRights(fds...), nil)
				result <- err
				if err == nil {
					// Keep the peer connected while the native receiver
					// authenticates it and inspects the invalid offer.
					<-release
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			received, err := ReceiveWriteOnlyFile(ctx, path, self)
			if received != nil {
				_ = received.Close()
				t.Fatal("native receiver admitted a malformed writer offer")
			}
			if !errors.Is(err, unix.EACCES) {
				t.Fatalf("native rejection = %v, want EACCES", err)
			}
			if err = <-result; err != nil {
				t.Fatal(err)
			}
		})
	}
}
