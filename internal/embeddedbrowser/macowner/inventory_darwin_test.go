//go:build darwin && cgo

package macowner

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"golang.org/x/sys/unix"
)

func TestRendererKeventRetriesOnlyInterruptions(t *testing.T) {
	for _, tc := range []struct {
		name      string
		attempts  []error
		wantCalls int
		wantError error
	}{
		{name: "success after interrupted observation", attempts: []error{unix.EINTR, nil}, wantCalls: 2},
		{name: "exited after interrupted registration", attempts: []error{unix.EINTR, unix.ESRCH}, wantCalls: 2, wantError: unix.ESRCH},
		{name: "permission failure", attempts: []error{unix.EPERM}, wantCalls: 1, wantError: unix.EPERM},
		{name: "bounded interruptions", attempts: []error{unix.EINTR, unix.EINTR, unix.EINTR}, wantCalls: 3, wantError: unix.EINTR},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			var event [1]unix.Kevent_t
			n, err := rendererKevent(func(fd int, changes, events []unix.Kevent_t, timeout *unix.Timespec) (int, error) {
				if fd != 42 || len(changes) != 0 || len(events) != 1 || timeout == nil || *timeout != (unix.Timespec{}) {
					t.Fatal("retry changed the liveness observation")
				}
				if calls == len(tc.attempts) {
					t.Fatal("unexpected retry")
				}
				attemptErr := tc.attempts[calls]
				calls++
				if attemptErr != nil {
					return -1, attemptErr
				}
				events[0].Fflags = unix.NOTE_EXIT
				return 1, nil
			}, 42, nil, event[:], &unix.Timespec{})
			if calls != tc.wantCalls || !errors.Is(err, tc.wantError) {
				t.Fatalf("got %d calls and %v, want %d calls and %v", calls, err, tc.wantCalls, tc.wantError)
			}
			if err == nil && (n != 1 || event[0].Fflags != unix.NOTE_EXIT) {
				t.Fatal("retry lost the exit observation")
			}
		})
	}
}

func TestRendererCensusRechecksInconsistentImage(t *testing.T) {
	member := Process{PID: 41, Started: 52, PPID: 30, State: "live"}
	helpers := []string{"/signed/renderer"}
	for _, tc := range []struct {
		name, state, path string
		parent            uint32
		wantError         bool
	}{
		{name: "exited during sampling", state: "gone"},
		{name: "reused PID", state: "reused"},
		{name: "signed image now available", state: "live", path: "/signed/renderer", parent: 30},
		{name: "persistent missing image", state: "live", parent: 30, wantError: true},
		{name: "foreign executable", state: "live", path: "/other", parent: 30, wantError: true},
		{name: "foreign parent", state: "live", path: "/signed/renderer", parent: 31, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkRendererCensusMember(member, 30, helpers, func(sampled Process) (Process, error) {
				if sampled.PID != member.PID || sampled.Started != member.Started {
					t.Fatal("recheck must use the original process instance")
				}
				sampled.State, sampled.Path, sampled.PPID = tc.state, tc.path, tc.parent
				return sampled, nil
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("want error=%t, got %v", tc.wantError, err)
			}
		})
	}
}

func TestMacRendererLeasePinsImageAndObservesExit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dir, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-renderer-lease-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "fixture.sock")
	listener, err := localipc.Listen(socket, false)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { _ = listener.Close() })
	defer stop()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, exe, "-test.run=^TestMacRendererLeaseFixtureChild$")
	child.Env = append(os.Environ(), "DSH_TEST_RENDERER_SOCKET="+socket)
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait(); close(done) }()
	defer func() { cancel(); <-done }()

	conn, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	target, err := localipc.TargetFromConn(conn)
	if err != nil || target.PID != uint32(child.Process.Pid) {
		t.Fatalf("expected the fixture child target, got PID %d: %v", target.PID, err)
	}
	forged := target
	forged.AuditToken[7] ^= 1
	if _, err = openRendererLease(forged, 7, image); err == nil {
		t.Fatal("accepted a changed kernel audit token")
	}
	unrelated, err := os.Stat("/usr/bin/true")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = openRendererLease(target, 7, unrelated); !errors.Is(err, errRendererInventory) {
		t.Fatalf("expected the unrelated image to be rejected: %v", err)
	}
	lease, err := openRendererLease(target, 7, image)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if alive, err := lease.Alive(); err != nil || !alive {
		t.Fatalf("expected a live lease: %t, %v", alive, err)
	}
	if _, err = conn.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if alive, err := lease.Alive(); err != nil || alive {
		t.Fatalf("expected the retained kernel lease to observe exit: %t, %v", alive, err)
	}
}

func TestMacRendererLeaseFixtureChild(t *testing.T) {
	socket := os.Getenv("DSH_TEST_RENDERER_SOCKET")
	if socket == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := localipc.Dial(ctx, socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err = io.ReadFull(conn, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
}
