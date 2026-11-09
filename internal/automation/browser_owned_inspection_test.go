package automation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/settings"
)

type inspectionRaceTarget struct {
	*selectionTarget
	beforeRead func(context.Context) error
}

func (t *inspectionRaceTarget) OwnedIdentity(ctx context.Context) (OwnedBrowserIdentity, error) {
	if t.beforeRead != nil {
		if err := t.beforeRead(ctx); err != nil {
			return OwnedBrowserIdentity{}, err
		}
	}
	return t.selectionTarget.OwnedIdentity(ctx)
}

type inspectionRaceResource struct {
	identity OwnedBrowserIdentity
	calls    atomic.Int32
	started  chan struct{}
}

func (r *inspectionRaceResource) OwnedBrowserBinding() OwnedBrowserIdentity { return r.identity }
func (r *inspectionRaceResource) Close() error                              { return nil }
func (r *inspectionRaceResource) Execute(ctx context.Context, _ string, _ map[string]any) (any, error) {
	r.calls.Add(1)
	if r.started != nil {
		close(r.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return BrowserEvidence{URL: r.identity.URL}, nil
}
func waitInspectionCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("controlled inspection boundary not reached")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestOwnedInspectionRevocationAtPermissionAndDispatchBoundaries(t *testing.T) {
	for _, change := range []string{"policy", "stop"} {
		for _, phase := range []string{"browser-response", "site-response", "post-site", "queued", "active"} {
			t.Run(change+"/"+phase, func(t *testing.T) {
				s, _, request := gatewayForTest(t)
				target := &inspectionRaceTarget{selectionTarget: targetForSelection()}
				intent := selectionForTest(request, target.selectionTarget)
				intent.Resolve = func(context.Context) (OwnedBrowserTarget, error) { return target, nil }
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err != nil {
					t.Fatal(err)
				}
				s.mu.Lock()
				c := s.gate.sessions[request.Session]
				if phase != "browser-response" {
					c.browser = true
					c.binding = s.browserBindingLocked(request)
				}
				s.mu.Unlock()
				resource := &inspectionRaceResource{identity: target.identity}
				var factories atomic.Int32
				if phase == "active" {
					resource.started = make(chan struct{})
				}
				s.ConfigureBrowserBackend(func(ctx context.Context, _ Request) (Resource, error) {
					if ctx.Err() != nil {
						return nil, ctx.Err()
					}
					factories.Add(1)
					return resource, nil
				}, s.browserSchemas)
				request.Tool = "browser_snapshot"
				request.Arguments = map[string]any{}
				var queued *entry
				if phase == "queued" {
					run, cancel := context.WithCancel(s.root)
					queued = &entry{ctx: run, cancel: cancel, ready: make(chan struct{}), closed: make(chan struct{}), resource: resource}
					queued.call.Lock()
					s.mu.Lock()
					s.entries[key(request)] = queued
					s.mu.Unlock()
				}
				reached, release := make(chan struct{}), make(chan struct{})
				if phase == "post-site" {
					target.beforeRead = func(ctx context.Context) error {
						s.mu.Lock()
						approved := c.inspection != nil && c.inspection.approved
						s.mu.Unlock()
						if approved {
							close(reached)
							select {
							case <-release:
							case <-ctx.Done():
								return ctx.Err()
							}
						}
						return nil
					}
				}
				done := make(chan error, 1)
				go func() { _, err := s.AuthorizeBrowser(context.Background(), request); done <- err }()
				consent := awaitState(t, s, func(v Snapshot) bool { return len(v.BrowserAccess.Requests) > 0 }).BrowserAccess.Requests[0]
				expectedKind := "site"
				if phase == "browser-response" {
					expectedKind = "browser"
				}
				if consent.Kind != expectedKind {
					t.Fatal("wrong permission boundary", consent)
				}
				if phase != "browser-response" && phase != "site-response" {
					if _, err := s.AnswerBrowserConsent(context.Background(), consent.ID, "allow"); err != nil {
						t.Fatal(err)
					}
					switch phase {
					case "post-site":
						select {
						case <-reached:
						case <-time.After(3 * time.Second):
							t.Fatal("post-approval identity read missing")
						}
					case "queued":
						waitInspectionCondition(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return queued.waiters == 1 })
						close(queued.ready)
						waitInspectionCondition(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return queued.waiters == 0 })
					case "active":
						select {
						case <-resource.started:
						case <-time.After(3 * time.Second):
							t.Fatal("inspection not started")
						}
					}
				}
				if change == "policy" {
					s.mu.Lock()
					revision := s.gate.policyRevision
					s.mu.Unlock()
					if _, err := s.SetBrowserPolicy(context.Background(), revision, settings.DefaultBrowserPolicy()); err != nil {
						t.Fatal(err)
					}
				} else if err := s.StopBrowserUse(request.Session, request.Lifetime); err != nil {
					t.Fatal(err)
				}
				if phase == "browser-response" || phase == "site-response" {
					if _, err := s.AnswerBrowserConsent(context.Background(), consent.ID, "allow"); err == nil {
						t.Fatal("stale permission accepted")
					}
				}
				if phase == "post-site" {
					close(release)
				}
				if phase == "queued" {
					queued.call.Unlock()
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("revoked inspection authorized")
					}
				case <-time.After(3 * time.Second):
					t.Fatal("revoked work not canceled")
				}
				s.mu.Lock()
				revived := c.browser || c.active || len(c.sites) > 0 || len(s.gate.permits) > 0
				s.mu.Unlock()
				if revived {
					t.Fatal("revoked permission revived")
				}
				want := int32(0)
				if phase == "active" {
					want = 1
				}
				if factories.Load() != want || resource.calls.Load() != want {
					t.Fatal("factory/inspection crossed revoked boundary", factories.Load(), resource.calls.Load(), want)
				}
			})
		}
	}
}
