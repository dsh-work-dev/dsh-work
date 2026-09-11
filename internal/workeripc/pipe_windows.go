//go:build windows

package workeripc

import (
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"net"
)

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
