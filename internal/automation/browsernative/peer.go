package browsernative

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/automation"
)

// Chromium's native-message receive limit is 1 MiB, including the complete
// UTF-8 JSON envelope. Inbound data is bounded below Chromium's 64 MiB limit.
const MaxToExtensionBytes = 1 << 20
const MaxFromExtensionBytes = 8 << 20

var errRouteClosed = errors.New("BROWSER_ROUTE_CLOSED")

type nativePeer struct {
	service *automation.Service
	id      string
	conn    net.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	routes  map[string]*nativeRoute
	closed  bool
}
type nativeRoute struct {
	peer     *nativePeer
	id       string
	lease    automation.BrowserLease
	messages chan json.RawMessage
	done     chan struct{}
	mu       sync.Mutex
	err      error
	once     sync.Once
}

func (p *nativePeer) write(v any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	defer p.conn.SetWriteDeadline(time.Time{})
	return writeMessageLimit(p.conn, v, MaxToExtensionBytes)
}
func (p *nativePeer) Open(ctx context.Context, id, title string) (automation.BrowserRoute, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lease, err := p.service.SelectedBrowserConnection()
	if err != nil {
		return nil, err
	}
	if lease.Connection != p.id {
		return nil, automation.ErrRevoked
	}
	r := &nativeRoute{peer: p, id: id, lease: lease, messages: make(chan json.RawMessage, 64), done: make(chan struct{})}
	p.mu.Lock()
	if p.closed || len(p.routes) >= 9 || p.routes[id] != nil {
		p.mu.Unlock()
		return nil, errors.New("BROWSER_ROUTE_UNAVAILABLE")
	}
	p.routes[id] = r
	p.mu.Unlock()
	if err := p.write(map[string]any{"kind": "open", "route": id, "wireProtocol": 2, "title": title}); err != nil {
		r.finish(err, false)
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			r.Close()
		case <-r.done:
		}
	}()
	return r, nil
}
func (p *nativePeer) receive(kind, id string, payload json.RawMessage, message string) {
	p.mu.Lock()
	r := p.routes[id]
	p.mu.Unlock()
	if r == nil {
		return
	} // Late response cannot create/revive a route.
	if kind != "wire" {
		r.finish(errors.New("BROWSER_ROUTE_FAILED: "+message), kind != "closed")
		return
	}
	if err := p.service.ValidateBrowserLease(r.lease); err != nil {
		r.finish(err, true)
		return
	}
	select {
	case <-r.done:
		return
	default:
	}
	select {
	case r.messages <- append(json.RawMessage(nil), payload...):
	default:
		r.finish(errors.New("BROWSER_ROUTE_BACKPRESSURE"), true)
	}
}
func (p *nativePeer) disconnect(cause error) {
	p.mu.Lock()
	p.closed = true
	routes := make([]*nativeRoute, 0, len(p.routes))
	for _, r := range p.routes {
		routes = append(routes, r)
	}
	p.mu.Unlock()
	for _, r := range routes {
		r.finish(cause, false)
	}
}
func (r *nativeRoute) Send(ctx context.Context, payload json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-r.done:
		return r.Err()
	default:
	}
	if err := r.peer.service.ValidateBrowserLease(r.lease); err != nil {
		r.finish(err, true)
		return err
	}
	err := r.peer.write(map[string]any{"kind": "wire", "route": r.id, "payload": payload})
	if err != nil {
		r.finish(err, true)
	}
	return err
}
func (r *nativeRoute) Messages() <-chan json.RawMessage { return r.messages }
func (r *nativeRoute) Done() <-chan struct{}            { return r.done }
func (r *nativeRoute) Err() error                       { r.mu.Lock(); defer r.mu.Unlock(); return r.err }
func (r *nativeRoute) finish(err error, notify bool) {
	r.once.Do(func() {
		r.mu.Lock()
		r.err = err
		r.mu.Unlock()
		close(r.done)
		r.peer.mu.Lock()
		delete(r.peer.routes, r.id)
		r.peer.mu.Unlock()
		if notify {
			_ = r.peer.write(map[string]any{"kind": "close", "route": r.id})
		}
	})
}
func (r *nativeRoute) Close() error { r.finish(errRouteClosed, true); return nil }
