package automation

import (
	"context"
	"encoding/json"
)

// BrowserPeer is an authenticated native connection. It owns transport I/O,
// not resource lifetimes or authorization. No method is exposed to the UI.
type BrowserPeer interface {
	Open(context.Context, string, string) (BrowserRoute, error)
}

type BrowserRoute interface {
	Send(context.Context, json.RawMessage) error
	Messages() <-chan json.RawMessage
	Done() <-chan struct{}
	Err() error
	Close() error
}

func (s *Service) AttachBrowserPeer(connection string, peer BrowserPeer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.profiles.connections[connection]
	if !ok || c.peer != nil || c.hello.WireProtocol != 2 || peer == nil {
		return ErrBrowserProtocol
	}
	c.peer = peer
	s.profiles.connections[connection] = c
	return nil
}

// OpenBrowserRoute is an in-process backend dependency, not an execution grant.
// The Factory is reached only after Service's execution gate accepts a Request.
func (s *Service) OpenBrowserRoute(ctx context.Context, lease BrowserLease, title string) (BrowserRoute, error) {
	if err := s.ValidateBrowserLease(lease); err != nil {
		return nil, err
	}
	s.mu.Lock()
	peer := s.profiles.connections[lease.Connection].peer
	s.mu.Unlock()
	if peer == nil {
		return nil, ErrBrowserProtocol
	}
	id, err := browserID()
	if err != nil {
		return nil, err
	}
	route, err := peer.Open(ctx, "s-"+id, title)
	if err != nil {
		return nil, err
	}
	if err := s.ValidateBrowserLease(lease); err != nil {
		route.Close()
		return nil, err
	}
	return route, nil
}
