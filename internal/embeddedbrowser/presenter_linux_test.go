//go:build linux && cgo && e67presenter

package embeddedbrowser

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLinuxPresenterInterruptedPoll(t *testing.T) {
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	// The zero-time liveness check must retry an interrupted syscall too.
	calls := 0
	alive, err := pollPresenterAlive(fd, func(p []unix.PollFd, timeout int) (int, error) {
		calls++
		if calls == 1 {
			return -1, unix.EINTR
		}
		return unix.Poll(p, timeout)
	})
	if err != nil || !alive || calls != 2 {
		t.Fatal("interrupted live probe retired own process", alive, err, calls)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	tid := unix.Gettid()
	stop := make(chan struct{})
	cancelSignal := make(chan struct{})
	defer close(cancelSignal)
	var sent atomic.Bool
	signalResult := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			select {
			case <-cancelSignal:
				return
			default:
			}
			data, e := os.ReadFile(fmt.Sprintf("/proc/self/task/%d/syscall", tid))
			fields := strings.Fields(string(data))
			if e == nil && len(fields) > 0 {
				nr, _ := strconv.Atoi(fields[0])
				if nr == unix.SYS_PPOLL {
					sent.Store(true)
					signalResult <- unix.Tgkill(os.Getpid(), tid, unix.SIGURG)
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
		signalResult <- fmt.Errorf("own thread did not enter kernel poll")
	}()
	interrupted := false
	var stopOnce sync.Once
	deadline := time.Now().Add(4 * time.Second)
	released, revents, err := waitPresenterProcess(fd, stop, func(p []unix.PollFd, timeout int) (int, error) {
		n, e := unix.Poll(p, timeout)
		if e == unix.EINTR && sent.Load() {
			interrupted = true
			stopOnce.Do(func() { close(stop) })
		}
		// A bounded failure still releases the waiter without treating timeout
		// as a successful interruption. No timing delay makes this test pass.
		if time.Now().After(deadline) {
			stopOnce.Do(func() { close(stop) })
		}
		return n, e
	})
	if !released || revents != 0 || err != nil || !interrupted {
		t.Fatal("actual interrupted poll invalidated owner", released, revents, err, interrupted)
	}
	if err := <-signalResult; err != nil {
		t.Fatal(err)
	}
	if live, e := pollPresenterAlive(fd, unix.Poll); e != nil || !live {
		t.Fatal("own page process no longer live", live, e)
	}
	t.Log("own held pidfd remained live across actual thread-directed signal/EINTR; cancellation then released the owner")
}
