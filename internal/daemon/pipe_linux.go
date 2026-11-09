//go:build linux

package daemon

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/local/dsh-work/internal/localipc"
)

func endpoint(identity string) (string, string, error) {
	dir, err := localipc.PrivateDirectory(os.TempDir(), fmt.Sprintf("dsh-work-%d", os.Geteuid()))
	if err != nil {
		return "", "", err
	}
	return filepath.Join(dir, fmt.Sprintf("daemon-%x.sock", sha256.Sum256([]byte(identity)))), dir, nil
}
func Listen(identity string) (net.Listener, error) {
	path, _, err := endpoint(identity)
	if err != nil {
		return nil, err
	}
	return localipc.Listen(path, false)
}
func dial(ctx context.Context, identity string) (net.Conn, error) {
	path, _, err := endpoint(identity)
	if err != nil {
		return nil, err
	}
	return localipc.Dial(ctx, path)
}
func Detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
