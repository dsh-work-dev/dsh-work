package automation

import (
	"context"
	"errors"
	"testing"
)

type retainedBrowserCleanup struct {
	calls   int
	failure error
}

func (r *retainedBrowserCleanup) Execute(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}
func (r *retainedBrowserCleanup) OwnedBrowserBinding() OwnedBrowserIdentity {
	return OwnedBrowserIdentity{BrokerGeneration: "isolated-owner"}
}
func (r *retainedBrowserCleanup) Close() error         { r.calls++; return r.failure }
func (r *retainedBrowserCleanup) CleanupPending() bool { return r.calls < 2 }

func TestStopOwnedRetainsScopedCleanupUntilJoin(t *testing.T) {
	s := New(nil, nil)
	s.EnableBrowserGateway(BrowserGatewayConfig{})
	defer s.Close()
	q := Request{Capability: Browser, Session: "a", Lifetime: "old"}
	resource := &retainedBrowserCleanup{failure: errors.New("actual join unfinished")}
	ready := make(chan struct{})
	close(ready)
	e := &entry{ready: ready, closed: make(chan struct{}), cancel: func() {}, resource: resource}
	s.mu.Lock()
	s.gate.sessions["a"] = &browserConversation{life: "old", browser: true}
	s.gate.sessions["b"] = &browserConversation{life: "other", browser: true}
	s.entries[key(q)] = e
	s.mu.Unlock()
	if err := s.StopBrowserUse("a", "old"); !errors.Is(err, resource.failure) || resource.calls != 1 || len(s.browserCleanup) != 1 {
		t.Fatal("unfinished owner lost", err, resource.calls, len(s.browserCleanup))
	}
	if !s.gate.sessions["a"].stopped || s.gate.sessions["b"].stopped {
		t.Fatal("stop escaped original scope")
	}
	// A new lifetime must not become the old cleanup retry's stop target.
	s.mu.Lock()
	s.gate.sessions["a"] = &browserConversation{life: "new", browser: true}
	s.mu.Unlock()
	if err := s.StopBrowserUse("a", "old"); err != nil || resource.calls != 2 || len(s.browserCleanup) != 0 {
		t.Fatal("completed historical failure blocked retry", err, resource.calls, len(s.browserCleanup))
	}
	if s.gate.sessions["a"].stopped || s.gate.sessions["b"].stopped {
		t.Fatal("cleanup retry stopped a new/other lifetime")
	}
	if !errors.Is(e.closeErr, resource.failure) {
		t.Fatal("initial failure diagnosis lost")
	}
}
