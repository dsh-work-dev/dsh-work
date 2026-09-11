//go:build windows

package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os/exec"
	"syscall"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func endpoint(identity string) (string, string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", "", err
	}
	sid := user.User.Sid.String()
	return fmt.Sprintf(`\\.\pipe\dsh-work-daemon-%x`, sha256.Sum256([]byte(sid+identity))), sid, nil
}

func Listen(identity string) (net.Listener, error) {
	path, sid, err := endpoint(identity)
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "D:P(A;;GA;;;" + sid + ")", InputBufferSize: 64 << 10, OutputBufferSize: 64 << 10})
}

func dial(ctx context.Context, identity string) (net.Conn, error) {
	path, _, err := endpoint(identity)
	if err != nil {
		return nil, err
	}
	return winio.DialPipeContext(ctx, path)
}

func Detach(cmd *exec.Cmd) {
	// Suppress a console without STARTF_USESHOWWINDOW/SW_HIDE, which would
	// override the first native ShowWindow request in the desktop client.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
}
