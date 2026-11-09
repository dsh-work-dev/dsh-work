//go:build !windows

package workeripc

import (
	"errors"
	"net"
)

func peerProcessID(net.Conn) (uint32, error) {
	return 0, errors.New("Worker IPC process admission is currently supported on Windows")
}

func listenLocal(string) (net.Listener, string, error) {
	return nil, "", errors.New("Worker IPC execution is currently supported on Windows")
}

func peerProcessStarted(net.Conn) (uint64, error) {
	return 0, errors.New("process instance admission is currently supported on Windows")
}
