package automation

import (
	"context"
	"encoding/json"
	"github.com/local/dsh-work/internal/settings"
	"testing"
	"time"
)

func TestOwnedOriginAndLifetimeBinding(t *testing.T) {
	for address, want := range map[string]string{"https://WWW.Example.com:443/path": "https://www.example.com", "https://example.com:8443/": "https://example.com:8443", "http://[::1]:80/": "http://[::1]"} {
		got, err := BrowserOrigin(address)
		if err != nil || got != want {
			t.Fatal(address, got, err)
		}
	}
	s, _, r := gatewayForTest(t)
	s.mu.Lock()
	c := s.gate.sessions[r.Session]
	c.owned = &ownedBrowserAdmission{id: "owned:test", scope: OwnedBrowserScope{Generation: r.Generation, Session: r.Session, Lifetime: r.Lifetime}}
	s.mu.Unlock()
	r.Turn = "new-turn"
	r.Lifetime = "new-life"
	if _, err := s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ownedLocked(r) == nil || s.browserBindingMatchesLocked(r, "owned:test") || s.browserBindingMatchesLocked(r, s.profiles.prefs.Selected) {
		t.Fatal("new lifetime fell back to external or reused old owned scope")
	}
}

func TestOwnedPolicyChangeInvalidatesOnlyOwnedExecutors(t *testing.T) {
	s, resource, r := gatewayForTest(t)
	makeEntry := func() *entry {
		ctx, cancel := context.WithCancel(s.root)
		e := &entry{ctx: ctx, cancel: cancel, ready: make(chan struct{}), closed: make(chan struct{}), resource: resource}
		close(e.ready)
		return e
	}
	owned, external := makeEntry(), makeEntry()
	externalRequest := r
	externalRequest.Session = "external-session"
	s.mu.Lock()
	c := s.gate.sessions[r.Session]
	c.owned = &ownedBrowserAdmission{id: "owned:test"}
	c.browser = true
	c.active = true
	c.sites["http://localhost:1234"] = true
	s.entries[key(r)] = owned
	s.entries[key(externalRequest)] = external
	revision := s.gate.policyRevision
	s.mu.Unlock()
	if _, err := s.SetBrowserPolicy(context.Background(), revision, settings.DefaultBrowserPolicy()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-owned.ctx.Done():
	default:
		t.Fatal("owned executor not canceled")
	}
	select {
	case <-owned.closed:
	default:
		t.Fatal("owned cleanup not completed")
	}
	select {
	case <-external.ctx.Done():
		t.Fatal("existing external executor canceled by new owned rule")
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.entries[key(r)] != nil || s.entries[key(externalRequest)] != external || c.owned == nil || c.browser || len(c.sites) != 0 {
		t.Fatal("policy change did not preserve provider pin and revoke only owned grants")
	}
}

func TestOwnedUploadRequiresFileResourceBeforeInspection(t *testing.T) {
	s, resource, r := gatewayForTest(t)
	s.mu.Lock()
	s.gate.sessions[r.Session].owned = &ownedBrowserAdmission{id: "owned:test"}
	s.mu.Unlock()
	for _, tool := range []string{"browser_file_upload", "browser_drop"} {
		r.Tool = tool
		r.Arguments = map[string]any{"paths": []any{`C:\not-a-user-selected-file.txt`}}
		if _, err := s.AuthorizeBrowser(context.Background(), r); err == nil || err.Error() != "BROWSER_FILE_RESOURCE_REQUIRED" {
			t.Fatalf("%s: %v", tool, err)
		}
	}
	if resource.calls.Load() != 0 {
		t.Fatalf("file-resource denial reached backend: %v", resource.calls)
	}
}

func TestOwnedDataDropKeepsAuthorizationPath(t *testing.T) {
	for _, paths := range []any{nil, []any{}, []string{}} {
		s, _, r := gatewayForTest(t)
		target := targetForSelection()
		s.mu.Lock()
		c := s.gate.sessions[r.Session]
		c.owned = &ownedBrowserAdmission{id: "owned:data-drop", scope: OwnedBrowserScope{Generation: r.Generation, Session: r.Session, Lifetime: r.Lifetime}, target: target, initial: target.identity}
		c.browser = true
		c.binding = "owned:data-drop"
		c.sites["http://localhost:8123"] = true
		s.browserSchemas = append(s.browserSchemas, json.RawMessage(`{"name":"browser_drop"}`))
		s.mu.Unlock()
		s.ConfigureBrowserBackend(func(context.Context, Request) (Resource, error) { return &selectionInspection{target.identity}, nil }, s.browserSchemas)
		r.Tool = "browser_drop"
		r.Arguments = map[string]any{"target": "#drop", "data": map[string]any{"text/plain": "owned inline data"}}
		if paths != nil {
			r.Arguments["paths"] = paths
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		decision, err := s.AuthorizeBrowser(ctx, r)
		cancel()
		if err != nil || decision.Permit == "" {
			t.Fatalf("inline drop with paths=%#v: %+v %v", paths, decision, err)
		}
	}
}
