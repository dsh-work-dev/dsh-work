//go:build !windows

package workeripc

import (
	"errors"
	"net"
)

func listenLocal(string) (net.Listener, string, error) {
	return nil, "", errors.New("Worker IPC execution is currently supported on Windows")
}
