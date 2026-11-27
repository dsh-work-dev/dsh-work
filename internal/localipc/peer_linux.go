//go:build linux

// Package localipc supplies the current-user Unix transport shared by the
// daemon and Worker bridge. HTTP framing and launch authentication stay with
// their existing owners. x/sys supplies the maintained OS bindings; a socket
// peer pidfd is required because opening a pidfd by numeric PID can race reuse.
package localipc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

func checkSupport() error {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(pair[0])
	defer unix.Close(pair[1])
	fd, err := unix.GetsockoptInt(pair[0], unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	if err != nil {
		return fmt.Errorf("local IPC requires kernel SO_PEERPIDFD: %w", err)
	}
	return unix.Close(fd)
}
func authenticateConn(ctx context.Context, c net.Conn) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, err := Peer(c)
	return c, err
}

type Process struct {
	PID uint32
	// Started is Linux /proc starttime, in clock ticks since boot. Supervisors
	// must capture this same value, not wall time or a client-supplied value.
	Started uint64
}

// Peer authenticates both directions identically. Unsupported SO_PEERPIDFD
// kernels fail closed; support for older kernels remains an explicit gap.
func Peer(c net.Conn) (Process, error) {
	var result Process
	sc, ok := c.(syscall.Conn)
	if !ok {
		return result, errors.New("local IPC socket handle unavailable")
	}
	raw, err := sc.SyscallConn()
	if err != nil {
		return result, err
	}
	var checkErr error
	err = raw.Control(func(fd uintptr) {
		cred, e := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		if e != nil {
			checkErr = e
			return
		}
		if cred.Uid != uint32(os.Geteuid()) || cred.Pid <= 0 {
			checkErr = errors.New("local IPC peer user rejected")
			return
		}
		pidfd, e := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PEERPIDFD)
		if e != nil {
			checkErr = fmt.Errorf("local IPC requires kernel SO_PEERPIDFD: %w", e)
			return
		}
		defer unix.Close(pidfd)
		unix.CloseOnExec(pidfd)
		if e = unix.PidfdSendSignal(pidfd, 0, nil, 0); e != nil {
			checkErr = e
			return
		}
		started, e := ProcessStarted(uint32(cred.Pid))
		if e != nil {
			checkErr = e
			return
		}
		// The socket's original struct pid must still be live after reading /proc;
		// a recycled numeric PID cannot satisfy this check on the original pidfd.
		if e = unix.PidfdSendSignal(pidfd, 0, nil, 0); e != nil {
			checkErr = e
			return
		}
		result = Process{PID: uint32(cred.Pid), Started: started}
	})
	if err != nil {
		return Process{}, err
	}
	return result, checkErr
}

func ProcessStarted(pid uint32) (uint64, error) {
	if pid == 0 {
		return 0, errors.New("local IPC process required")
	}
	f, err := os.Open(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil {
		return 0, err
	}
	if len(data) > 8192 {
		return 0, errors.New("process stat too large")
	}
	return parseStarted(string(data))
}

func parseStarted(stat string) (uint64, error) {
	// comm is parenthesized and may itself contain spaces and ')'.
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, errors.New("invalid process stat")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" || fields[0] == "x" {
		return 0, errors.New("process is not live")
	}
	started, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || started == 0 {
		return 0, errors.New("invalid process creation identity")
	}
	return started, nil
}
