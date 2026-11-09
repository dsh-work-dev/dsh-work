//go:build linux

package workeripc

import (
	"net"
	"os"
	"path/filepath"

	"github.com/local/dsh-work/internal/localipc"
)

func peerProcessID(c net.Conn) (uint32, error) {
	p, err := localipc.Peer(c)
	return p.PID, err
}
func peerProcessStarted(c net.Conn) (uint64, error) {
	p, err := localipc.Peer(c)
	return p.Started, err
}
func listenLocal(string) (net.Listener, string, error) {
	dir, err := os.MkdirTemp("", "dsh-worker-")
	if err != nil {
		return nil, "", err
	}
	path := filepath.Join(dir, "bridge.sock")
	l, err := localipc.Listen(path, true)
	if err != nil {
		os.Remove(path + ".lock")
		os.Remove(dir)
	}
	return l, path, err
}
