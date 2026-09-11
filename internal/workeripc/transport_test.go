package workeripc

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

type queuedListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *queuedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *queuedListener) Close() error { l.once.Do(func() { close(l.done) }); return nil }
func (*queuedListener) Addr() net.Addr { return &net.UnixAddr{Name: "fixture", Net: "unix"} }

func TestStalledHandshakeDoesNotBlockWorker(t *testing.T) {
	l := &queuedListener{connections: make(chan net.Conn, 2), done: make(chan struct{})}
	transport := &Transport{listener: l, token: "fixture-secret", ready: make(chan net.Conn, 16), done: make(chan struct{}), connections: make(map[net.Conn]struct{}), HTTP: &http.Transport{}}
	defer transport.Close()
	slowServer, slowClient := net.Pipe()
	defer slowClient.Close()
	workerServer, workerClient := net.Pipe()
	defer workerClient.Close()
	l.connections <- slowServer
	l.connections <- workerServer
	go transport.accept()
	// The first client never even reads its challenge. The second completes
	// both halves of authentication before the first client's 3s deadline.
	workerClient.SetDeadline(time.Now().Add(time.Second))
	nonce := make([]byte, 32)
	if _, err := io.ReadFull(workerClient, nonce); err != nil {
		t.Fatalf("Worker challenge blocked by idle client: %v", err)
	}
	mac := func(role string) []byte {
		h := hmac.New(sha256.New, []byte(transport.token))
		h.Write([]byte(role))
		h.Write(nonce)
		return h.Sum(nil)
	}
	if _, err := workerClient.Write(mac("worker")); err != nil {
		t.Fatal(err)
	}
	answer := make([]byte, 32)
	if _, err := io.ReadFull(workerClient, answer); err != nil {
		t.Fatal(err)
	}
	if !hmac.Equal(answer, mac("host")) {
		t.Fatal("invalid Host authentication")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	c, err := transport.dial(ctx, "tcp", "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}
