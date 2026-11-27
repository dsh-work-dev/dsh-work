//go:build darwin && cgo

package macowner

import (
	"errors"
	"syscall"
	"testing"
)

func TestGroupCountPreservesMeasurementErrors(t *testing.T) {
	for _, code := range []syscall.Errno{syscall.EPERM, syscall.EIO, syscall.EINTR} {
		calls := 0
		_, err := groupCount(func() (int, syscall.Errno) { calls++; return 0, code }, 4096)
		if !errors.Is(err, code) {
			t.Fatalf("zero+%v accepted: %v", code, err)
		}
		want := 1
		if code == syscall.EINTR {
			want = 3
		}
		if calls != want {
			t.Fatalf("retry count %d, want %d", calls, want)
		}
	}
	for _, code := range []syscall.Errno{0, syscall.ESRCH} {
		if n, err := groupCount(func() (int, syscall.Errno) { return 0, code }, 4096); n != 0 || err != nil {
			t.Fatalf("legal empty: %d %v", n, err)
		}
	}
	calls := 0
	if n, err := groupCount(func() (int, syscall.Errno) {
		calls++
		if calls == 1 {
			return 0, syscall.EINTR
		}
		return 2, 0
	}, 4096); n != 2 || err != nil || calls != 2 {
		t.Fatalf("interrupted query: %d %v calls=%d", n, err, calls)
	}
	if _, err := groupCount(func() (int, syscall.Errno) { return 4096, 0 }, 4096); err == nil {
		t.Fatal("truncated query accepted")
	}
}
