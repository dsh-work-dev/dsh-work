//go:build darwin && cgo

package localipc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinMutualPeerAndCancellation(t *testing.T) {
	dir, err := os.MkdirTemp(SocketTempRoot(), "dsh-peer-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "ipc.sock")
	l, err := Listen(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := make(chan net.Conn, 1)
	failure := make(chan error, 1)
	go func() {
		c, e := Dial(ctx, path)
		if e != nil {
			failure <- e
		} else {
			client <- c
		}
	}()
	server, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	var c net.Conn
	select {
	case c = <-client:
		defer c.Close()
	case err = <-failure:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	started, err := ProcessStarted(uint32(os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	for _, socket := range []net.Conn{server, c} {
		p, e := Peer(socket)
		if e != nil || p.PID != uint32(os.Getpid()) || p.Started != started {
			t.Fatalf("peer %+v: %v", p, e)
		}
	}
	// One stalled raw peer must neither block a second authenticated peer nor
	// prevent listener cancellation from retiring its native handshake.
	stalled, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer stalled.Close()
	go func() {
		c, e := Dial(ctx, path)
		if e != nil {
			failure <- e
		} else {
			client <- c
		}
	}()
	other, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	select {
	case c = <-client:
		defer c.Close()
	case err = <-failure:
		t.Fatal(err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	begin := time.Now()
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if time.Since(begin) > time.Second {
		t.Fatal("stalled handshake delayed listener close")
	}
}
