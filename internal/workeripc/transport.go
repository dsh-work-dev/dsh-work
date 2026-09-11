// Package workeripc carries HTTP bytes on authenticated, current-user OS pipes.
// The Worker connects to the Host; neither side opens a TCP listener.
package workeripc

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"
)

//go:embed host.mjs remote.mjs
var hostSource embed.FS

const Origin = "http://127.0.0.1:1" // Routing identity only; never dialled as TCP.

type Transport struct {
	listener    net.Listener
	path, token string
	ready       chan net.Conn
	done        chan struct{}
	once        sync.Once
	mu          sync.Mutex
	connections map[net.Conn]struct{}
	HTTP        *http.Transport
}

func New() (*Transport, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	l, path, err := listenLocal(token[:24])
	if err != nil {
		return nil, err
	}
	t := &Transport{listener: l, path: path, token: token, ready: make(chan net.Conn, 16), done: make(chan struct{}), connections: make(map[net.Conn]struct{})}
	t.HTTP = &http.Transport{Proxy: nil, DialContext: t.dial, MaxConnsPerHost: 16, MaxIdleConnsPerHost: 16, IdleConnTimeout: 30 * time.Second, DisableCompression: true}
	go t.accept()
	return t, nil
}

func (t *Transport) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if address != "127.0.0.1:1" {
		return nil, errors.New("Worker IPC cannot dial another authority")
	}
	select {
	case <-t.done:
		return nil, net.ErrClosed
	default:
	}
	select {
	case c := <-t.ready:
		return c, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, net.ErrClosed
	}
}

func (t *Transport) accept() {
	// Track and cap pending handshakes as well as admitted connections. A stalled
	// client must not block authentication of other Worker connections.
	for {
		c, err := t.listener.Accept()
		if err != nil {
			return
		}
		t.mu.Lock()
		select {
		case <-t.done:
			t.mu.Unlock()
			c.Close()
			return
		default:
		}
		if len(t.connections) >= 32 {
			t.mu.Unlock()
			c.Close()
			continue
		}
		t.connections[c] = struct{}{}
		t.mu.Unlock()
		go t.admit(c)
	}
}

func (t *Transport) admit(c net.Conn) {
	wrapped := &trackedConn{Conn: c, owner: t}
	if err := t.authenticate(c); err != nil {
		wrapped.Close()
		return
	}
	select {
	case t.ready <- wrapped:
	case <-t.done:
		wrapped.Close()
	}
}

func (t *Transport) authenticate(c net.Conn) error {
	c.SetDeadline(time.Now().Add(3 * time.Second))
	defer c.SetDeadline(time.Time{})
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if _, err := c.Write(nonce); err != nil {
		return err
	}
	mac := func(role string) []byte {
		h := hmac.New(sha256.New, []byte(t.token))
		h.Write([]byte(role))
		h.Write(nonce)
		return h.Sum(nil)
	}
	answer := make([]byte, 32)
	if _, err := io.ReadFull(c, answer); err != nil {
		return err
	}
	if !hmac.Equal(answer, mac("worker")) {
		return errors.New("Worker IPC authentication failed")
	}
	_, err := c.Write(mac("host"))
	return err
}

type trackedConn struct {
	net.Conn
	owner *Transport
	once  sync.Once
}

func (c *trackedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.mu.Lock(); delete(c.owner.connections, c.Conn); c.owner.mu.Unlock() })
	return err
}

func (t *Transport) Close() error {
	t.once.Do(func() {
		close(t.done)
		t.listener.Close()
		t.HTTP.CloseIdleConnections()
		t.mu.Lock()
		for c := range t.connections {
			c.Close()
		}
		t.connections = make(map[net.Conn]struct{})
		t.mu.Unlock()
	})
	return nil
}

// Prepare adds a launch-only carrier override. It never changes the profile.
// profileRoot is the selected DSH profile npm project.
func (t *Transport) Prepare(root, profileRoot, previousPatch string) (string, error) {
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	source, err := hostSource.ReadFile("host.mjs")
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, "host.mjs"))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, source, 0600); err != nil {
		return "", err
	}
	remotePath, err := writeRemote(root)
	if err != nil {
		return "", err
	}
	var rows []any
	if previousPatch != "" {
		b, err := os.ReadFile(previousPatch)
		if err != nil {
			return "", err
		}
		if err := json.Unmarshal(b, &rows); err != nil {
			return "", err
		}
	}
	u := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}).String()
	rows = append(rows, remoteRow(remotePath), map[string]any{"id": "webserver", "disabled": true}, map[string]any{"insert": []any{map[string]any{"id": "dsh-work-pipe", "name": u, "config": map[string]any{"host": "127.0.0.1", "port": 1, "compression": "none"}}}})
	// Module imports happen before schema processing. Per-launch sidecar owns
	// credentials and module resolution; only its path enters the patch graph.
	config, _ := json.Marshal(map[string]string{"pipe": t.path, "token": t.token, "profileRoot": profileRoot})
	if err := os.WriteFile(filepath.Join(root, "connection.json"), config, 0600); err != nil {
		return "", err
	}
	data, err := json.Marshal(rows)
	if err != nil {
		return "", err
	}
	patch := filepath.Join(root, "launch.patch.json")
	return patch, os.WriteFile(patch, data, 0600)
}

func writeRemote(root string) (string, error) {
	path, err := filepath.Abs(filepath.Join(root, "remote.mjs"))
	if err != nil {
		return "", err
	}
	data, err := hostSource.ReadFile("remote.mjs")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, data, 0600)
}
func remoteRow(path string) any {
	u := (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path)}).String()
	return map[string]any{"insert": []any{map[string]any{"id": "dsh-work-remote-stream", "name": u}}}
}
