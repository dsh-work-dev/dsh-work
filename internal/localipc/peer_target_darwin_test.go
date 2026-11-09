//go:build darwin && cgo && e67nativefixture

package localipc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDarwinAuthenticatedTarget(t *testing.T) {
	dir, err := os.MkdirTemp(SocketTempRoot(), "dsh-target-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	l, err := Listen(filepath.Join(dir, "peer.sock"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := make(chan net.Conn, 1)
	failure := make(chan error, 1)
	go func() {
		c, e := Dial(ctx, filepath.Join(dir, "peer.sock"))
		if e != nil {
			failure <- e
		} else {
			client <- c
		}
	}()
	s, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case c := <-client:
		defer c.Close()
	case e := <-failure:
		t.Fatal(e)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	target, err := TargetFromConn(s)
	if err != nil {
		t.Fatal(err)
	}
	if target.PID != uint32(os.Getpid()) || target.AuditToken[5] != target.PID || target.Started == 0 {
		t.Fatal("kernel target mismatch")
	}
	if err = SignalTarget(target, 0); err != nil {
		t.Fatal(err)
	}
	changed := target
	changed.Started++
	if !errors.Is(SignalTarget(changed, 0), ErrTargetStale) {
		t.Fatal("changed process instance accepted")
	}
	changed = target
	changed.AuditToken[7]++
	if changed.AuditToken[7] == 0 {
		changed.AuditToken[7]++
	}
	if !errors.Is(SignalTarget(changed, 0), os.ErrProcessDone) {
		t.Fatal("stale kernel pidversion was not rejected")
	}
	if err = SignalTarget(target, 0); err != nil {
		t.Fatal("stale signal damaged the live instance:", err)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err = TargetFromConn(a); err == nil {
		t.Fatal("unauthenticated transport acquired a target")
	}
}
