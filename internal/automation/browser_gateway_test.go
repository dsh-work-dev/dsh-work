package automation

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/local/dsh-work/internal/settings"
)

type gatewayResource struct {
	calls   atomic.Int32
	element BrowserElement
}

func (r *gatewayResource) Close() error { return nil }
func (r *gatewayResource) Execute(_ context.Context, tool string, _ map[string]any) (any, error) {
	if tool == "__dsh_inspect" {
		return BrowserEvidence{URL: "http://localhost/fixture", Elements: []BrowserElement{r.element}}, nil
	}
	r.calls.Add(1)
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}, nil
}
func gatewayForTest(t *testing.T) (*Service, *gatewayResource, Request) {
	t.Helper()
	s := New(managerForTest(t), prepareFunc(func(context.Context, Capability, func(Progress)) error { return nil }))
	t.Cleanup(s.Close)
	resource := &gatewayResource{element: BrowserElement{Tag: "input", Type: "text", Name: "Search"}}
	var schemas []json.RawMessage
	for _, tool := range []string{"browser_navigate", "browser_type", "browser_click", "browser_snapshot"} {
		b, _ := json.Marshal(map[string]any{"name": tool, "inputSchema": map[string]any{"type": "object"}})
		schemas = append(schemas, b)
	}
	s.ConfigureBrowserBackend(func(context.Context, Request) (Resource, error) { return resource, nil }, schemas)
	s.EnableBrowserGateway(BrowserGatewayConfig{})
	s.Worker("gate", true, true)
	if _, e := enableAndInstallForTest(s, context.Background(), Browser, true); e != nil {
		t.Fatal(e)
	}
	awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	s.Gateway("gate", true)
	connection := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	binding := mustBind(t, s, connection, "")
	if e := s.SelectBrowserBinding(context.Background(), binding); e != nil {
		t.Fatal(e)
	}
	r := Request{Generation: "gate", Capability: Browser, Session: "top", Lifetime: "life", Turn: "one", Title: "Local fixture", Operation: "browser", Tool: "browser_navigate", Arguments: map[string]any{"url": "http://localhost/fixture"}, Epoch: s.Snapshot().Browser.Epoch}
	if _, e := s.BrowserTurn(r); e != nil {
		t.Fatal(e)
	}
	return s, resource, r
}
func allowBrowserTest(t *testing.T, s *Service, r Request) BrowserDecision {
	t.Helper()
	done := make(chan BrowserDecision, 1)
	failed := make(chan error, 1)
	go func() {
		v, e := s.AuthorizeBrowser(context.Background(), r)
		if e != nil {
			failed <- e
			return
		}
		done <- v
	}()
	v := awaitState(t, s, func(v Snapshot) bool { return len(v.BrowserAccess.Requests) > 0 })
	if _, e := s.AnswerBrowserConsent(context.Background(), v.BrowserAccess.Requests[0].ID, "allow"); e != nil {
		t.Fatal(e)
	}
	select {
	case d := <-done:
		return d
	case e := <-failed:
		t.Fatal(e)
	}
	return BrowserDecision{}
}
func TestBrowserGatewayRequiresConsentAndOneUseBoundPermit(t *testing.T) {
	s, resource, r := gatewayForTest(t)
	if _, e := s.Execute(context.Background(), r); e == nil || resource.calls.Load() != 0 {
		t.Fatal("unapproved call executed", e)
	}
	d := allowBrowserTest(t, s, r)
	r.Epoch = d.Epoch
	r.Permit = d.Permit
	wrong := r
	wrong.Arguments = map[string]any{"url": "http://localhost/other"}
	if _, e := s.Execute(context.Background(), wrong); e == nil {
		t.Fatal("permit accepted changed arguments")
	}
	d, e := s.AuthorizeBrowser(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	r.Permit = d.Permit
	if _, e = s.Execute(context.Background(), r); e != nil || resource.calls.Load() != 1 {
		t.Fatal(e, resource.calls.Load())
	}
	if _, e = s.Execute(context.Background(), r); e == nil || resource.calls.Load() != 1 {
		t.Fatal("permit reused")
	}
}
func TestBrowserGatewaySecretAndPageChangeCannotBeApprovedAway(t *testing.T) {
	s, resource, r := gatewayForTest(t)
	allowBrowserTest(t, s, r)
	r.Tool = "browser_type"
	r.Arguments = map[string]any{"target": "#password", "text": "not-a-real-secret"}
	resource.element.Type = "password"
	if _, e := s.AuthorizeBrowser(context.Background(), r); e == nil || !strings.Contains(e.Error(), "SECRET_INPUT_BLOCKED") {
		t.Fatal(e)
	}
	resource.element = BrowserElement{Tag: "button", Name: "Buy now"}
	r.Tool = "browser_click"
	r.Arguments = map[string]any{"target": "#action"}
	d, e := s.AuthorizeBrowser(context.Background(), r)
	if e != nil || d.Kind != "ask" || d.DisplayReason["zh-CN"] == "" {
		t.Fatal(d, e)
	}
	resource.element.Name = "Delete account"
	r.Permit = d.Permit
	if _, e = s.Execute(context.Background(), r); e == nil || !strings.Contains(e.Error(), "PAGE_CHANGED") || resource.calls.Load() != 0 {
		t.Fatal("changed page executed", e)
	}
}
func TestBrowserGatewayStoppedLifetimeNeedsNewUserRound(t *testing.T) {
	s, _, r := gatewayForTest(t)
	allowBrowserTest(t, s, r)
	if e := s.StopBrowserUse(r.Session, r.Lifetime); e != nil {
		t.Fatal(e)
	}
	if _, e := s.AuthorizeBrowser(context.Background(), r); e == nil {
		t.Fatal("stopped lifetime reauthorized")
	}
	r.Turn = "next-user-message"
	renewed, e := s.BrowserTurn(r)
	if e != nil || renewed["lifetime"] == r.Lifetime {
		t.Fatal(renewed, e)
	}
	old := r
	r.Lifetime = renewed["lifetime"]
	if _, e = s.AuthorizeBrowser(context.Background(), old); e == nil {
		t.Fatal("old lifetime revived")
	}
	allowBrowserTest(t, s, r)
}
func TestBrowserPolicyBlocksMostSpecificSiteAndPaymentAllow(t *testing.T) {
	p := settings.DefaultBrowserPolicy()
	p.Sites = []settings.BrowserSiteRule{{Host: "example.com", Rule: "block"}, {Host: "docs.example.com", Rule: "allow"}}
	if p.SiteRule("a.example.com") != "block" || p.SiteRule("docs.example.com") != "allow" {
		t.Fatal(p)
	}
	m := managerForTest(t)
	p.Confirm["payment"] = "allow"
	if _, e := m.SetBrowserPolicy(context.Background(), p); e == nil {
		t.Fatal("payment allow persisted")
	}
	h, e := settings.NormalizeBrowserHost("https://www.例子.测试./path?q=1")
	if e != nil || h == "例子.测试" {
		t.Fatal("IDN normalization failed", h, e)
	}
}

func TestBrowserPolicyRevisionRejectsStaleSettingsAndPermits(t *testing.T) {
	s, resource, r := gatewayForTest(t)
	d := allowBrowserTest(t, s, r)
	before := s.Snapshot().BrowserAccess
	p := before.Policy
	p.NewSites = "ask"
	after, err := s.SetBrowserPolicy(context.Background(), before.PolicyRevision, p)
	if err != nil || after.BrowserAccess.PolicyRevision != before.PolicyRevision+1 {
		t.Fatal(after, err)
	}
	if _, err := s.SetBrowserPolicy(context.Background(), before.PolicyRevision, before.Policy); err == nil || !strings.Contains(err.Error(), "POLICY_CHANGED") {
		t.Fatal("stale settings overwrote newer policy", err)
	}
	r.Permit, r.Epoch = d.Permit, d.Epoch
	if _, err := s.Execute(context.Background(), r); err == nil || resource.calls.Load() != 0 {
		t.Fatal("old policy permit executed", err)
	}
}

func TestNativeRiskConsentDoesNotReturnDSHAsk(t *testing.T) {
	s, resource, r := gatewayForTest(t)
	allowBrowserTest(t, s, r)
	s.mu.Lock()
	s.gate.config.NativePrompts = true
	s.mu.Unlock()
	resource.element = BrowserElement{Tag: "button", Name: "Buy now"}
	r.Tool = "browser_click"
	r.Arguments = map[string]any{"target": "#pay"}
	type result struct {
		d BrowserDecision
		e error
	}
	done := make(chan result, 1)
	go func() { d, e := s.AuthorizeBrowser(context.Background(), r); done <- result{d, e} }()
	v := awaitState(t, s, func(v Snapshot) bool { return len(v.BrowserAccess.Requests) > 0 })
	q := v.BrowserAccess.Requests[0]
	if q.Kind != "action" || q.Category != "payment" || q.Action != "Buy now" || q.AllowAlways {
		t.Fatal(q)
	}
	if _, e := s.AnswerBrowserConsent(context.Background(), q.ID, "allow"); e != nil {
		t.Fatal(e)
	}
	x := <-done
	if x.e != nil || x.d.Kind != "allow" {
		t.Fatal(x)
	}
	r.Permit = x.d.Permit
	r.Epoch = x.d.Epoch
	if _, e := s.Execute(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	go func() { d, e := s.AuthorizeBrowser(context.Background(), r); done <- result{d, e} }()
	v = awaitState(t, s, func(v Snapshot) bool { return len(v.BrowserAccess.Requests) > 0 })
	q = v.BrowserAccess.Requests[0]
	if _, e := s.Repair(Browser); e == nil {
		t.Fatal("repair disturbed pending/active browser work")
	}
	if _, e := s.AnswerBrowserConsent(context.Background(), q.ID, "deny"); e != nil {
		t.Fatal(e)
	}
	x = <-done
	if x.e == nil || resource.calls.Load() != 1 {
		t.Fatal("native denial executed", x, resource.calls.Load())
	}
}

type failedRepairWithReadySet struct{}

func (failedRepairWithReadySet) Prepare(context.Context, Capability, func(Progress)) error {
	return nil
}
func (failedRepairWithReadySet) Repair(context.Context, Capability, func(Progress)) error {
	return ErrUnavailable
}
func (failedRepairWithReadySet) Ready(Capability) bool { return true }
func TestFailedRepairKeepsVerifiedExistingComponentsAvailable(t *testing.T) {
	s, _, _ := gatewayForTest(t)
	s.mu.Lock()
	s.prepare = failedRepairWithReadySet{}
	s.mu.Unlock()
	if _, e := s.Repair(Browser); e != nil {
		t.Fatal(e)
	}
	v := awaitState(t, s, func(v Snapshot) bool { return v.Browser.RepairFailure != "" })
	if v.Browser.Dependency != "prepared" || v.Browser.Failure != "" {
		t.Fatal(v.Browser)
	}
}
