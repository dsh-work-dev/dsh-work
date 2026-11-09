//go:build windows

package workeripc

import (
	"errors"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"net"
)

func peerProcessID(c net.Conn) (uint32, error) {
	h, ok := c.(interface{ Fd() uintptr })
	if !ok {
		return 0, errors.New("pipe handle unavailable")
	}
	var pid uint32
	err := windows.GetNamedPipeClientProcessId(windows.Handle(h.Fd()), &pid)
	return pid, err
}

func listenLocal(name string) (net.Listener, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, "", err
	}
	path := `\\.\pipe\dsh-work-` + name
	l, err := winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;" + user.User.Sid.String() + ")",
		InputBufferSize:    64 << 10, OutputBufferSize: 64 << 10,
	})
	return l, path, err
}

func peerProcessStarted(c net.Conn) (uint64, error) {
	pid, err := peerProcessID(c)
	if err != nil {
		return 0, err
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)
	var created, exited, kernel, user windows.Filetime
	if err = windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return 0, err
	}
	if status, e := windows.WaitForSingleObject(h, 0); e != nil || status != uint32(windows.WAIT_TIMEOUT) {
		return 0, errors.New("pipe peer process is not live")
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), nil
}
