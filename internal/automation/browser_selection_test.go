package automation

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// These tests isolate admission ordering, not real CEF/MCP acceptance.
type selectionTarget struct {
	identity OwnedBrowserIdentity
	routes   int
}

type selectionInspection struct{ identity OwnedBrowserIdentity }

func (r *selectionInspection) OwnedBrowserBinding() OwnedBrowserIdentity { return r.identity }
func (r *selectionInspection) Close() error                              { return nil }
func (r *selectionInspection) Execute(context.Context, string, map[string]any) (any, error) {
	return BrowserEvidence{URL: r.identity.URL}, nil
}

func TestOwnedSiteConsentPrecedesFactoryAndInspection(t *testing.T) {
	for _, mode := range []string{"allow", "deny", "origin-changed"} {
		t.Run(mode, func(t *testing.T) {
			s, _, r := gatewayForTest(t)
			target := targetForSelection()
			if _, err := s.SelectOwnedBrowser(context.Background(), selectionForTest(r, target)); err != nil {
				t.Fatal(err)
			}
			var factories atomic.Int32
			s.ConfigureBrowserBackend(func(context.Context, Request) (Resource, error) {
				factories.Add(1)
				s.mu.Lock()
				approved := s.gate.sessions[r.Session].sites["http://localhost:8123"]
				s.mu.Unlock()
				if !approved {
					return nil, errors.New("inspection before exact-origin consent")
				}
				return &selectionInspection{target.identity}, nil
			}, s.browserSchemas)
			r.Tool = "browser_snapshot"
			r.Arguments = map[string]any{}
			done := make(chan error, 1)
			go func() { _, err := s.AuthorizeBrowser(context.Background(), r); done <- err }()
			browser := awaitState(t, s, func(v Snapshot) bool { return len(v.BrowserAccess.Requests) > 0 }).BrowserAccess.Requests[0]
			if browser.Kind != "browser" || factories.Load() != 0 {
				t.Fatal("factory before conversation consent", browser)
			}
			if _, err := s.AnswerBrowserConsent(context.Background(), browser.ID, "allow"); err != nil {
				t.Fatal(err)
			}
			site := awaitState(t, s, func(v Snapshot) bool {
				return len(v.BrowserAccess.Requests) > 0 && v.BrowserAccess.Requests[0].Kind == "site"
			}).BrowserAccess.Requests[0]
			if site.Site != "http://localhost:8123" || site.AllowAlways || factories.Load() != 0 {
				t.Fatal("site not exact or factory already started", site)
			}
			choice := "allow"
			if mode == "deny" {
				choice = "deny"
			}
			if mode == "origin-changed" {
				target.identity.URL = "http://localhost:8124/other"
				target.identity.Document = "next"
			}
			if _, err := s.AnswerBrowserConsent(context.Background(), site.ID, choice); err != nil {
				t.Fatal(err)
			}
			err := <-done
			if mode == "allow" {
				if err != nil || factories.Load() != 1 {
					t.Fatal("approved inspection failed", err, factories.Load())
				}
			} else if err == nil || factories.Load() != 0 {
				t.Fatal("unapproved page inspected", err, factories.Load())
			}
		})
	}
}

func (t *selectionTarget) OwnedIdentity(context.Context) (OwnedBrowserIdentity, error) {
	return t.identity, nil
}
func (t *selectionTarget) OpenOwnedRoute(context.Context, string) (BrowserRoute, error) {
	t.routes++
	return nil, errors.New("no resource should open during selection")
}
func (t *selectionTarget) BeginOwnedCall(context.Context, OwnedBrowserCall) (func() error, error) {
	return nil, ErrRevoked
}
func (t *selectionTarget) RevokeOwned(context.Context) error { return nil }
func selectionForTest(r Request, target *selectionTarget) OwnedSelectionIntent {
	return OwnedSelectionIntent{Generation: r.Generation, Session: r.Session, Source: "window/document/view", Resolve: func(context.Context) (OwnedBrowserTarget, error) { return target, nil }}
}
func targetForSelection() *selectionTarget {
	return &selectionTarget{identity: OwnedBrowserIdentity{BrokerGeneration: "broker", Target: "tab", Frame: "main", Document: "document", URL: "http://localhost:8123/page", ViewLease: "view", PresentationEpoch: "1", ViewVisible: true, FrameScopeReady: true, ScopeEpoch: "1"}}
}

func TestPendingSelectionUsesCurrentLayoutWithoutGrant(t *testing.T) {
	for _, change := range []string{"layout", "hidden", "document", "view", "scope", "frame-scope", "broker"} {
		t.Run(change, func(t *testing.T) {
			s, _, r := gatewayForTest(t)
			r.Session = "pending-layout"
			target := targetForSelection()
			if _, err := s.SelectOwnedBrowser(context.Background(), selectionForTest(r, target)); err != nil {
				t.Fatal(err)
			}
			target.identity.PresentationEpoch = "2"
			switch change {
			case "hidden":
				target.identity.ViewVisible = false
			case "document":
				target.identity.Document = "other"
			case "view":
				target.identity.ViewLease = "other"
			case "scope":
				target.identity.ScopeEpoch = "2"
			case "frame-scope":
				target.identity.FrameScopeReady = false
			case "broker":
				target.identity.BrokerGeneration = "other"
			}
			if _, err := s.BrowserTurn(r); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			c := s.gate.sessions[r.Session]
			admitted := c.owned != nil
			fresh := admitted && c.owned.initial == target.identity
			granted := c.browser || c.active || len(c.sites) != 0 || len(s.gate.permits) != 0
			s.mu.Unlock()
			if admitted != (change == "layout") || admitted && !fresh || granted || target.routes != 0 {
				t.Fatal("pending continuity or permission boundary", admitted, fresh, granted, target.routes)
			}
			if !admitted {
				if _, err := s.AuthorizeBrowser(context.Background(), r); err == nil {
					t.Fatal("invalid selection authorized")
				}
			}
		})
	}
}
func TestSelectionJoinsOnlyTrustedTurnAndNeverGrants(t *testing.T) {
	s, _, r := gatewayForTest(t)
	target := targetForSelection()
	r.Session = "not-yet-a-turn"
	state, err := s.SelectOwnedBrowser(context.Background(), selectionForTest(r, target))
	if err != nil || state.State != "waiting-for-turn" {
		t.Fatal(state, err)
	}
	if _, owned, err := s.OwnedBrowserTarget(r); !owned || err == nil {
		t.Fatal("pending choice silently fell back external")
	}
	if _, err = s.AuthorizeBrowser(context.Background(), r); err == nil {
		t.Fatal("UI session string created authoritative turn")
	}
	if _, err = s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	c := s.gate.sessions[r.Session]
	admitted := c.owned != nil && c.owned.scope == (OwnedBrowserScope{r.Generation, r.Session, r.Lifetime})
	granted := c.browser || c.active || len(c.sites) > 0 || len(s.gate.permits) > 0
	s.mu.Unlock()
	if !admitted || granted || target.routes != 0 {
		t.Fatal("selection failed to join or signed permission", admitted, granted, target.routes)
	}
	s.StopSession(r.Generation, r.Session, r.Lifetime)
	r.Turn = "next"
	reply, err := s.BrowserTurn(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Lifetime = reply["lifetime"]
	if _, err = s.AuthorizeBrowser(context.Background(), r); err == nil {
		t.Fatal("stopped selection captured new lifetime")
	}
	if _, owned, err := s.OwnedBrowserTarget(r); !owned || err == nil {
		t.Fatal("retired selection fell back external")
	}
}
func TestSelectionRejectsHiddenChangedGenerationAndFixedScope(t *testing.T) {
	for _, kind := range []string{"hidden", "changed", "generation", "fixed-source", "active", "disabled", "components"} {
		t.Run(kind, func(t *testing.T) {
			s, _, r := gatewayForTest(t)
			target := targetForSelection()
			intent := selectionForTest(r, target)
			if kind == "hidden" {
				target.identity.ViewVisible = false
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err == nil {
					t.Fatal("hidden admitted")
				}
				return
			}
			if kind == "active" {
				s.mu.Lock()
				s.gate.sessions[r.Session].browser = true
				s.mu.Unlock()
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err == nil {
					t.Fatal("fixed external scope redirected")
				}
				return
			}
			if kind == "disabled" {
				s.mu.Lock()
				s.state.DefaultBrowserEnabled = false
				s.mu.Unlock()
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err == nil || err.Error() != "BROWSER_CONTROL_DISABLED" {
					t.Fatal("disabled browser missing actionable result", err)
				}
				return
			}
			if kind == "components" {
				s.mu.Lock()
				s.slots[Browser].state.Dependency = "unprepared"
				s.mu.Unlock()
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err == nil || err.Error() != "BROWSER_COMPONENTS_REQUIRED" {
					t.Fatal("missing components result", err)
				}
				return
			}
			if kind == "fixed-source" {
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err != nil {
					t.Fatal(err)
				}
				intent.Source = "other-window/document/view"
				if _, err := s.SelectOwnedBrowser(context.Background(), intent); err == nil {
					t.Fatal("other UI redirected admitted scope")
				}
				return
			}
			r.Session = "pending"
			intent.Session = r.Session
			if _, err := s.SelectOwnedBrowser(context.Background(), intent); err != nil {
				t.Fatal(err)
			}
			if kind == "changed" {
				target.identity.Document = "new-document"
			}
			if kind == "generation" {
				s.Worker("new-generation", true, true)
			}
			if kind == "generation" {
				if _, err := s.BrowserTurn(r); err == nil {
					t.Fatal("old generation admitted")
				}
				return
			}
			if _, err := s.BrowserTurn(r); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AuthorizeBrowser(context.Background(), r); err == nil {
				t.Fatal("invalid selection authorized")
			}
			if _, owned, err := s.OwnedBrowserTarget(r); !owned || err == nil {
				t.Fatal("invalid selection fell back external")
			}
		})
	}
}
func TestOwnedSelectionDoesNotRequireExternalBindingOrChangeDefault(t *testing.T) {
	s, _, r := gatewayForTest(t)
	target := targetForSelection()
	s.mu.Lock()
	old := s.profiles.prefs.Selected
	s.profiles.prefs.Selected = ""
	s.mu.Unlock()
	if _, err := s.SelectOwnedBrowser(context.Background(), selectionForTest(r, target)); err != nil {
		t.Fatal("owned choice required Chrome binding", err)
	}
	if len(s.Snapshot().BrowserTools) == 0 {
		t.Fatal("locked tool schemas absent without external binding")
	}
	s.mu.Lock()
	if s.profiles.prefs.Selected != "" {
		t.Fatal("owned rewrote external preference")
	}
	s.profiles.prefs.Selected = old
	s.mu.Unlock()
	external := r
	external.Session = "unchosen"
	if _, owned, err := s.OwnedBrowserTarget(external); owned || err != nil {
		t.Fatal("unchosen session lost external default")
	}
}

func TestStopThenChooseAnotherPageWaitsForNewTrustedLifetime(t *testing.T) {
	s, _, r := gatewayForTest(t)
	a, b := targetForSelection(), targetForSelection()
	b.identity.Target = "tab-B"
	b.identity.ViewLease = "view-B"
	if _, err := s.SelectOwnedBrowser(context.Background(), selectionForTest(r, a)); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	oldAdmission := s.gate.sessions[r.Session].owned
	s.mu.Unlock()
	if err := s.StopBrowserUse(r.Session, r.Lifetime); err != nil {
		t.Fatal(err)
	}
	choice := selectionForTest(r, b)
	choice.Source = "window/document/view-B"
	state, err := s.SelectOwnedBrowser(context.Background(), choice)
	if err != nil || state.State != "waiting-for-turn" {
		t.Fatal("stopped conversation cannot select", state, err)
	}
	s.mu.Lock()
	c := s.gate.sessions[r.Session]
	unchanged := c.stopped && c.life == r.Lifetime && c.owned == oldAdmission
	granted := c.active || c.browser || len(c.sites) > 0 || len(s.gate.permits) > 0
	s.mu.Unlock()
	if !unchanged || granted || b.routes != 0 {
		t.Fatal("new choice revived old lifetime or granted access")
	}
	if _, err = s.AuthorizeBrowser(context.Background(), r); err == nil {
		t.Fatal("old turn revived stopped choice")
	}
	if _, err = s.BrowserTurn(r); err != nil {
		t.Fatal(err)
	} // Duplicate old turn still cannot revive it.
	s.mu.Lock()
	stillStopped := s.gate.sessions[r.Session].stopped && s.gate.sessions[r.Session].owned == oldAdmission
	s.mu.Unlock()
	if !stillStopped {
		t.Fatal("old BrowserTurn revived")
	}
	r.Turn = "new-real-user-turn"
	reply, err := s.BrowserTurn(r)
	if err != nil {
		t.Fatal(err)
	}
	if reply["lifetime"] == r.Lifetime {
		t.Fatal("new scope reused stopped lifetime")
	}
	r.Lifetime = reply["lifetime"]
	s.mu.Lock()
	c = s.gate.sessions[r.Session]
	joined := c.owned.target == b && c.owned.scope.Lifetime == r.Lifetime && !c.stopped
	granted = c.active || c.browser || len(c.sites) > 0 || len(s.gate.permits) > 0
	s.mu.Unlock()
	if !joined || granted || b.routes != 0 {
		t.Fatal("new turn did not join B without granting", joined, granted, b.routes)
	}
}
