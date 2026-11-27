//go:build darwin && cgo && e67nativefixture

package localipc

import (
	"context"
	"errors"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
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
	// Unlike kill(pid, 0), Darwin's audit-token API sends real nonzero
	// signals. SIGCONT is harmless for this running process; observing its
	// delivery proves the kernel accepted the authenticated instance target.
	received := make(chan os.Signal, 2)
	signal.Notify(received, syscall.SIGCONT)
	defer signal.Stop(received)
	deliver := func() {
		t.Helper()
		if err := SignalTarget(target, syscall.SIGCONT); err != nil {
			t.Fatal("authenticated SIGCONT:", err)
		}
		select {
		case got := <-received:
			if got != syscall.SIGCONT {
				t.Fatal("unexpected signal", got)
			}
		case <-ctx.Done():
			t.Fatal("authenticated signal was not delivered", ctx.Err())
		}
	}
	deliver()
	changed := target
	changed.Started++
	if !errors.Is(SignalTarget(changed, syscall.SIGCONT), ErrTargetStale) {
		t.Fatal("changed process instance accepted")
	}
	changed = target
	changed.AuditToken[7]++
	if changed.AuditToken[7] == 0 {
		changed.AuditToken[7]++
	}
	if !errors.Is(SignalTarget(changed, syscall.SIGCONT), os.ErrProcessDone) {
		t.Fatal("stale kernel pidversion was not rejected")
	}
	deliver() // Rejecting stale evidence must leave the real instance usable.
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err = TargetFromConn(a); err == nil {
		t.Fatal("unauthenticated transport acquired a target")
	}
}
