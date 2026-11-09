package automation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/idna"
)

// OwnedBrowserIdentity is engine/manager evidence, never a Worker tool argument.
type OwnedBrowserIdentity struct {
	BrokerGeneration  string `json:"brokerGeneration"`
	Target            string `json:"target"`
	Frame             string `json:"frame"`
	Document          string `json:"document"`
	URL               string `json:"url"`
	ArtifactLease     string `json:"artifactLease,omitempty"`
	ArtifactOrigin    string `json:"artifactOrigin,omitempty"`
	ViewLease         string `json:"viewLease,omitempty"`
	PresentationEpoch string `json:"presentationEpoch,omitempty"`
	ViewVisible       bool   `json:"viewVisible"`
	ScopeEpoch        string `json:"scopeEpoch"`
	FrameScopeReady   bool   `json:"frameScopeReady"`
}
type OwnedBrowserTarget interface {
	OwnedIdentity(context.Context) (OwnedBrowserIdentity, error)
	OpenOwnedRoute(context.Context, string) (BrowserRoute, error)
	BeginOwnedCall(context.Context, OwnedBrowserCall) (func() error, error)
	RevokeOwned(context.Context) error
}
type OwnedBrowserNavigation struct {
	Identity OwnedBrowserIdentity
	Result   any
}
type OwnedBrowserCall struct {
	Owner            Request              `json:"-"`
	Identity         OwnedBrowserIdentity `json:"identity"`
	NavigationOrigin string               `json:"navigationOrigin,omitempty"`
	Token            string               `json:"token"`
}
type ownedCallKey struct{}

func WithOwnedBrowserCall(ctx context.Context, call OwnedBrowserCall) context.Context {
	return context.WithValue(ctx, ownedCallKey{}, call)
}
func OwnedBrowserCallFromContext(ctx context.Context) (OwnedBrowserCall, bool) {
	v, ok := ctx.Value(ownedCallKey{}).(OwnedBrowserCall)
	return v, ok
}

type OwnedBrowserScope struct{ Generation, Session, Lifetime string }
type ownedBrowserAdmission struct {
	id        string
	scope     OwnedBrowserScope
	target    OwnedBrowserTarget
	initial   OwnedBrowserIdentity
	selection string
}

// AdmitOwnedBrowser is a daemon-internal dependency seam. It requires a real
// existing BrowserTurn and cannot be invoked through Request/Agent JSON.
func (s *Service) AdmitOwnedBrowser(ctx context.Context, scope OwnedBrowserScope, target OwnedBrowserTarget) error {
	return s.admitOwnedBrowser(ctx, scope, target, nil)
}
func (s *Service) admitOwnedBrowser(ctx context.Context, scope OwnedBrowserScope, target OwnedBrowserTarget, intent *ownedBrowserIntent) error {
	if target == nil {
		return ErrUnavailable
	}
	identity, err := target.OwnedIdentity(ctx)
	if err != nil {
		return err
	}
	if identity.BrokerGeneration == "" || identity.Target == "" || identity.Frame == "" || identity.Document == "" {
		return ErrRevoked
	}
	if identity.ViewLease != "" && !identity.ViewVisible {
		return errors.New("BROWSER_VIEW_HIDDEN")
	}
	if !identity.FrameScopeReady {
		return errors.New("BROWSER_FRAME_SCOPE_UNAVAILABLE")
	}
	if _, err := ownedPrincipal(identity, identity.URL); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gate == nil || scope.Generation != s.state.Generation || !s.state.CanChange || !s.state.GatewayConnected {
		return ErrRevoked
	}
	if intent != nil && (s.gate.ownedIntents[scope.Session] != intent || intent.invalid || !selectionDocumentMatches(identity, intent.initial)) {
		return ErrRevoked
	}
	c := s.gate.sessions[scope.Session]
	if intent != nil && c != nil && c.owned != nil && c.owned.selection == intent.id && c.owned.scope == scope {
		return nil
	}
	if c == nil || !c.turnSeen || c.life != scope.Lifetime || c.stopped || c.active || c.browser || s.entries[key(Request{Capability: Browser, Operation: "browser", Session: scope.Session, Lifetime: scope.Lifetime})] != nil {
		return ErrRevoked
	}
	for _, other := range s.gate.sessions {
		if other.owned != nil && other != c && !other.stopped && other.owned.initial.BrokerGeneration == identity.BrokerGeneration && other.owned.initial.Target == identity.Target {
			return errors.New("BROWSER_TARGET_ALREADY_CONTROLLED")
		}
	}
	selection := ""
	if intent != nil {
		selection = intent.id
	}
	c.owned = &ownedBrowserAdmission{selection: selection, id: "owned:" + rand.Text(), scope: scope, target: target, initial: identity}
	s.publishLocked()
	return nil
}

func (s *Service) ownedLocked(r Request) *ownedBrowserAdmission {
	if s.gate != nil {
		if c := s.gate.sessions[r.Session]; c != nil {
			return c.owned
		}
	}
	return nil
}
func (s *Service) browserBindingLocked(r Request) string {
	if a := s.ownedLocked(r); a != nil {
		return a.id
	}
	return s.profiles.prefs.Selected
}
func (s *Service) browserBindingMatchesLocked(r Request, binding string) bool {
	if a := s.ownedLocked(r); a != nil {
		return a.id == binding && a.scope.Generation == r.Generation && a.scope.Session == r.Session && a.scope.Lifetime == r.Lifetime
	}
	return s.profiles.prefs.Selected == binding
}
func (s *Service) OwnedBrowserTarget(r Request) (OwnedBrowserTarget, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.ownedLocked(r)
	if a == nil {
		if s.gate != nil && s.gate.ownedIntents[r.Session] != nil {
			return nil, true, ErrRevoked
		}
		return nil, false, nil
	}
	if !s.validLocked(r) || !s.browserBindingMatchesLocked(r, a.id) {
		return nil, true, ErrRevoked
	}
	return a.target, true, nil
}
func (s *Service) ownedCall(ctx context.Context, r Request) (OwnedBrowserCall, bool, error) {
	target, owned, err := s.OwnedBrowserTarget(r)
	if !owned || err != nil {
		return OwnedBrowserCall{}, owned, err
	}
	identity, err := target.OwnedIdentity(ctx)
	if err != nil {
		return OwnedBrowserCall{}, true, err
	}
	if identity.ViewLease != "" && !identity.ViewVisible {
		return OwnedBrowserCall{}, true, errors.New("BROWSER_VIEW_HIDDEN")
	}
	if !identity.FrameScopeReady {
		return OwnedBrowserCall{}, true, errors.New("BROWSER_FRAME_SCOPE_UNAVAILABLE")
	}
	s.mu.Lock()
	a := s.ownedLocked(r)
	valid := a != nil && identity.BrokerGeneration == a.initial.BrokerGeneration && identity.Target == a.initial.Target && identity.ArtifactLease == a.initial.ArtifactLease && s.browserBindingMatchesLocked(r, a.id)
	s.mu.Unlock()
	if !valid {
		return OwnedBrowserCall{}, true, ErrRevoked
	}
	return OwnedBrowserCall{Identity: identity, Token: rand.Text(), Owner: r}, true, nil
}

// BrowserOrigin preserves the exact DNS name, scheme and non-default port. It
// intentionally does not reuse legacy host-rule normalization (which strips www).
func BrowserOrigin(address string) (string, error) {
	u, err := url.Parse(address)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("BROWSER_URL_BLOCKED")
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil {
			return "", errors.New("BROWSER_URL_BLOCKED")
		}
	}
	port := u.Port()
	if port != "" {
		n, e := strconv.Atoi(port)
		if e != nil || n < 1 || n > 65535 {
			return "", errors.New("BROWSER_URL_BLOCKED")
		}
		port = strconv.Itoa(n)
	}
	if u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host, nil
}
func ownedPrincipal(identity OwnedBrowserIdentity, address string) (string, error) {
	origin, err := BrowserOrigin(address)
	if err != nil {
		return "", err
	}
	if identity.ArtifactLease != "" {
		if origin != identity.ArtifactOrigin {
			return "", errors.New("BROWSER_ARTIFACT_SCOPE_REJECTED")
		}
		return "artifact:" + identity.ArtifactLease, nil
	}
	return origin, nil
}

func (s *Service) finishOwnedBrowserCall(ctx context.Context, r Request, p browserPermit, value any, err error) (any, error) {
	if err != nil {
		return value, err
	}
	navigation, navigated := value.(OwnedBrowserNavigation)
	actual := p.host
	address := browserResultURL(value)
	if address == "" {
		address = p.owned.Identity.URL
	}
	if navigated {
		address = navigation.Identity.URL
	}
	if address != "" {
		principal, e := ownedPrincipal(p.owned.Identity, address)
		if e != nil {
			return nil, e
		}
		actual = principal
	}
	s.mu.Lock()
	if !s.validLocked(r) || p.policyRevision != s.gate.policyRevision || !s.browserBindingMatchesLocked(r, p.binding) {
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	c := s.gate.sessions[r.Session]
	blocked := false
	if p.owned.Identity.ArtifactLease == "" {
		host, e := browserHost(address, false)
		blocked = e != nil || s.gate.policy.SiteRule(host) == "block"
	}
	allowed := c.sites[actual]
	if navigated {
		c.host = actual
		c.last = time.Now()
		s.publishLocked()
	}
	s.mu.Unlock()
	if blocked {
		return nil, errors.New("BROWSER_SITE_BLOCKED")
	}
	if navigated || ownedAutomaticSnapshot(r) {
		if !allowed {
			if e := s.browserConsent(ctx, r, c, p.binding, "site", actual, nil); e != nil {
				return map[string]any{"content": []any{map[string]any{"type": "text", "text": "Navigation completed; access to the new site is awaiting approval."}}}, fmt.Errorf("BROWSER_NAVIGATION_COMPLETED_ACCESS_REQUIRED: %w", e)
			}
			s.mu.Lock()
			if !s.validLocked(r) || p.policyRevision != s.gate.policyRevision || !s.browserBindingMatchesLocked(r, p.binding) {
				s.mu.Unlock()
				return nil, ErrRevoked
			}
			c.sites[actual] = true
			s.mu.Unlock()
		}
		// This is a new, fixed read-only observation under the current document and
		// its own native token, not continuation of the old action permit.
		call, _, e := s.ownedCall(ctx, r)
		if e != nil {
			return nil, e
		}
		expected := p.owned.Identity
		if navigated {
			expected = navigation.Identity
		}
		if call.Identity != expected {
			return nil, errors.New("BROWSER_PAGE_CHANGED")
		}
		read := r
		read.Tool = "browser_snapshot"
		read.Arguments = map[string]any{}
		original := value
		if navigated {
			original = navigation.Result
		}
		value, err = s.executeOwned(WithOwnedBrowserCall(ctx, call), read)
		if _, again := value.(OwnedBrowserNavigation); again {
			return nil, errors.New("BROWSER_PAGE_CHANGED")
		}
		if err == nil {
			value = mergeOwnedReadback(original, value)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validLocked(r) || p.policyRevision != s.gate.policyRevision || !s.browserBindingMatchesLocked(r, p.binding) {
		return nil, ErrRevoked
	}
	c.last = time.Now()
	c.host = actual
	if actual != p.host && !c.sites[actual] {
		return nil, errors.New("BROWSER_SITE_ACCESS_REQUIRED")
	}
	s.publishLocked()
	return value, err
}

// Matches the supported locked 0.0.83 tools that request an automatic snapshot.
func ownedAutomaticSnapshot(r Request) bool {
	switch r.Tool {
	case "browser_click", "browser_drag", "browser_hover", "browser_select_option", "browser_file_upload", "browser_drop", "browser_navigate", "browser_navigate_back", "browser_wait_for":
		return true
	case "browser_type":
		return r.Arguments["submit"] == true || r.Arguments["slowly"] == true
	case "browser_press_key":
		return r.Arguments["key"] == "Enter"
	case "browser_tabs":
		return r.Arguments["action"] == "new" && r.Arguments["url"] != ""
	}
	return false
}
func mergeOwnedReadback(original, readback any) any {
	var a, b map[string]any
	raw, _ := json.Marshal(original)
	_ = json.Unmarshal(raw, &a)
	raw, _ = json.Marshal(readback)
	_ = json.Unmarshal(raw, &b)
	if b == nil {
		return readback
	}
	content, _ := a["content"].([]any)
	var prefix []any
	for _, item := range content {
		if part, ok := item.(map[string]any); ok && part["type"] == "text" {
			text, _ := part["text"].(string)
			for _, header := range []string{"### Open tabs", "### Page", "### Snapshot"} {
				if i := strings.Index(text, header); i >= 0 {
					text = text[:i]
				}
			}
			if text = strings.TrimSpace(text); text != "" {
				prefix = append(prefix, map[string]any{"type": "text", "text": text})
			}
		} else {
			prefix = append(prefix, item)
		}
	}
	if tail, ok := b["content"].([]any); ok {
		b["content"] = append(prefix, tail...)
	}
	if a["isError"] == true {
		b["isError"] = true
	}
	return b
}

// OwnedSelectionIntent is a daemon-created native selection, not authority.
// Session is UI intent; lifetime comes only from the authenticated BrowserTurn.
type OwnedSelectionIntent struct {
	Generation, Session, Source string
	Resolve                     func(context.Context) (OwnedBrowserTarget, error)
}
type OwnedSelectionState struct {
	State string `json:"state"`
}
type ownedBrowserIntent struct {
	id                           string
	generation, source, lifetime string
	afterLifetime                string
	initial                      OwnedBrowserIdentity
	resolve                      func(context.Context) (OwnedBrowserTarget, error)
	invalid                      bool
}

// A pending choice identifies a document, not an observation or permission.
// Layout may change when the first message opens the conversation. Admission
// captures the current presentation epoch; call permits and native dispatch
// continue to require that exact epoch.
func selectionDocumentMatches(current, selected OwnedBrowserIdentity) bool {
	if !current.ViewVisible || !current.FrameScopeReady {
		return false
	}
	current.PresentationEpoch = ""
	selected.PresentationEpoch = ""
	return current == selected
}

func (s *Service) SelectOwnedBrowser(ctx context.Context, selection OwnedSelectionIntent) (OwnedSelectionState, error) {
	if selection.Session == "" || len(selection.Session) > 1024 || selection.Source == "" || selection.Resolve == nil {
		return OwnedSelectionState{}, ErrRevoked
	}
	target, err := selection.Resolve(ctx)
	if err != nil {
		return OwnedSelectionState{}, err
	}
	identity, err := target.OwnedIdentity(ctx)
	if err != nil {
		return OwnedSelectionState{}, err
	}
	if identity.ViewLease == "" || !identity.ViewVisible || !identity.FrameScopeReady {
		return OwnedSelectionState{}, errors.New("BROWSER_VIEW_UNAVAILABLE")
	}
	if _, err = ownedPrincipal(identity, identity.URL); err != nil {
		return OwnedSelectionState{}, err
	}
	s.mu.Lock()
	if s.gate == nil || selection.Generation != s.state.Generation || !s.state.CanChange || !s.state.GatewayConnected {
		s.mu.Unlock()
		return OwnedSelectionState{}, ErrUnavailable
	}
	if !s.state.DefaultBrowserEnabled || !s.slots[Browser].state.Enabled {
		s.mu.Unlock()
		return OwnedSelectionState{}, errors.New("BROWSER_CONTROL_DISABLED")
	}
	if s.slots[Browser].state.Dependency != "prepared" {
		s.mu.Unlock()
		return OwnedSelectionState{}, errors.New("BROWSER_COMPONENTS_REQUIRED")
	}
	c := s.gate.sessions[selection.Session]
	lifetime, afterLifetime := "", ""
	if c != nil {
		if c.active || c.browser || s.entries[key(Request{Capability: Browser, Operation: "browser", Session: selection.Session, Lifetime: c.life})] != nil {
			s.mu.Unlock()
			return OwnedSelectionState{}, errors.New("BROWSER_STOP_BEFORE_SWITCH")
		}
		if c.stopped {
			afterLifetime = c.life
		} else {
			lifetime = c.life
		}
	}
	if err := ctx.Err(); err != nil {
		s.mu.Unlock()
		return OwnedSelectionState{}, err
	}
	if prior := s.gate.ownedIntents[selection.Session]; prior != nil && !prior.invalid && prior.generation == selection.Generation && prior.source != selection.Source && (c != nil && !c.stopped && c.owned != nil && c.owned.scope.Lifetime == c.life) {
		s.mu.Unlock()
		return OwnedSelectionState{}, errors.New("BROWSER_STOP_BEFORE_SWITCH")
	}

	if s.gate.ownedIntents == nil {
		s.gate.ownedIntents = map[string]*ownedBrowserIntent{}
	}
	intent := &ownedBrowserIntent{id: rand.Text(), generation: s.state.Generation, source: selection.Source, lifetime: lifetime, afterLifetime: afterLifetime, initial: identity, resolve: selection.Resolve}
	s.gate.ownedIntents[selection.Session] = intent
	var request Request
	if c != nil && c.turnSeen && !c.stopped {
		request = Request{Generation: s.state.Generation, Session: selection.Session, Lifetime: c.life, Turn: c.turn}
	}
	s.publishLocked()
	s.mu.Unlock()
	if request.Session != "" {
		if err = s.admitOwnedIntent(ctx, request); err != nil {
			return OwnedSelectionState{}, err
		}
		return OwnedSelectionState{State: "selected"}, nil
	}
	return OwnedSelectionState{State: "waiting-for-turn"}, nil
}
func (s *Service) admitOwnedIntent(ctx context.Context, r Request) error {
	s.mu.Lock()
	if s.gate == nil {
		s.mu.Unlock()
		return nil
	}
	intent := s.gate.ownedIntents[r.Session]
	if intent == nil {
		s.mu.Unlock()
		return nil
	}
	c := s.gate.sessions[r.Session]
	if c != nil && c.owned != nil && c.owned.selection == intent.id && c.owned.scope == (OwnedBrowserScope{r.Generation, r.Session, r.Lifetime}) {
		s.mu.Unlock()
		return nil
	}
	if intent.invalid || intent.generation != r.Generation || r.Generation != s.state.Generation || c == nil || !c.turnSeen || c.stopped || c.life != r.Lifetime || c.turn != r.Turn || intent.afterLifetime != "" && c.life == intent.afterLifetime || intent.lifetime != "" && intent.lifetime != c.life {
		s.mu.Unlock()
		return errors.New("BROWSER_SELECTION_REVOKED")
	}
	s.mu.Unlock()
	target, err := intent.resolve(ctx)
	if err != nil {
		return err
	}
	identity, err := target.OwnedIdentity(ctx)
	if err != nil {
		return err
	}
	if !selectionDocumentMatches(identity, intent.initial) {
		return errors.New("BROWSER_SELECTION_PAGE_CHANGED")
	}
	return s.admitOwnedBrowser(ctx, OwnedBrowserScope{r.Generation, r.Session, r.Lifetime}, target, intent)
}
