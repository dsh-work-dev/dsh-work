//go:build !windows

package daemon

import (
	"context"
	"errors"
	"net"
	"os/exec"
)

func Listen(string) (net.Listener, error) {
	return nil, errors.New("daemon native transport requires Windows")
}
func dial(context.Context, string) (net.Conn, error) {
	return nil, errors.New("daemon native transport requires Windows")
}
func Detach(*exec.Cmd) {}
