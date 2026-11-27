//go:build windows

package daemon

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"net"
)

func nativePeerContext(ctx context.Context, c net.Conn) context.Context {
	fd, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return ctx
	}
	var pid uint32
	if windows.GetNamedPipeClientProcessId(windows.Handle(fd.Fd()), &pid) != nil {
		return ctx
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ctx
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(handle, &created, &exited, &kernel, &user) != nil {
		return ctx
	}
	return context.WithValue(ctx, processPeerKey{}, ProcessPeer{PID: pid, Started: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)})
}

func verifyServerProcess(connection net.Conn, expected ProcessPeer) error {
	if expected.PID == 0 || expected.Started == 0 {
		return errors.New("owned pipe process instance required")
	}
	descriptor, ok := connection.(interface{ Fd() uintptr })
	if !ok {
		return errors.New("pipe server handle unavailable")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(descriptor.Fd()), &pid); err != nil {
		return err
	}
	if pid != expected.PID {
		return errors.New("pipe server PID does not match owned process")
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(handle, &created, &exited, &kernel, &user); err != nil {
		return err
	}
	if uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != expected.Started {
		return errors.New("pipe server process creation does not match owned instance")
	}
	if state, e := windows.WaitForSingleObject(handle, 0); e != nil || state != uint32(windows.WAIT_TIMEOUT) {
		return errors.New("owned pipe server is not live")
	}
	return nil
}
