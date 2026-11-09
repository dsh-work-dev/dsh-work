package automation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/settings"
)

type BrowserConsent struct {
	Category    string    `json:"category,omitempty"`
	Action      string    `json:"action,omitempty"`
	AllowAlways bool      `json:"allowAlways"`
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Session     string    `json:"session"`
	Title       string    `json:"title"`
	Browser     string    `json:"browser"`
	Profile     string    `json:"profile"`
	Site        string    `json:"site"`
	Phase       string    `json:"phase"`
	Expires     time.Time `json:"expires"`
}
type BrowserUse struct {
	Session  string `json:"session"`
	Lifetime string `json:"lifetime"`
	Title    string `json:"title"`
	Site     string `json:"site"`
}
type BrowserActivity struct {
	At       time.Time `json:"at"`
	Session  string    `json:"session"`
	Title    string    `json:"title"`
	Site     string    `json:"site"`
	Category string    `json:"category"`
	Event    string    `json:"event"`
	Tool     string    `json:"tool"`
}
type BrowserGatewaySnapshot struct {
	NativePrompts  bool                   `json:"nativePrompts"`
	PendingActions int                    `json:"pendingActions"`
	PolicyRevision uint64                 `json:"policyRevision"`
	Policy         settings.BrowserPolicy `json:"policy"`
	Requests       []BrowserConsent       `json:"requests"`
	Sessions       []BrowserUse           `json:"sessions"`
	Activity       []BrowserActivity      `json:"activity"`
}
type BrowserDecision struct {
	Kind          string            `json:"kind"`
	Reason        string            `json:"reason,omitempty"`
	DisplayReason map[string]string `json:"displayReason,omitempty"`
	Permit        string            `json:"permit"`
	Epoch         uint64            `json:"epoch"`
}
type BrowserGatewayConfig struct {
	ExtensionSource string
	NativePrompts   bool
	ActivityPath    string
	Replacer        settings.FileReplacer
	Notify          func(BrowserConsent)
}
type browserConversation struct {
	owned                            *ownedBrowserAdmission
	serial                           chan struct{}
	life, turn, title, binding, host string
	browser                          bool
	active                           bool
	stopped                          bool
	turnSeen                         bool
	sites, denied                    map[string]bool
	last                             time.Time
	inspection                       *ownedInspection
}

// One read-only inspection belongs to the serialized conversation and the
// policy revision that approved it. Revocation cancels queued and active work.
type ownedInspection struct {
	conversation *browserConversation
	request      Request
	revision     uint64
	binding      string
	principal    string
	approved     bool
	ctx          context.Context
	cancel       context.CancelFunc
}
type ownedInspectionKey struct{}

func (s *Service) validOwnedInspectionLocked(o *ownedInspection) bool {
	return o != nil && o.ctx.Err() == nil && s.gate != nil && s.gate.policyRevision == o.revision && s.gate.sessions[o.request.Session] == o.conversation && o.conversation.inspection == o && o.conversation.browser && !o.conversation.stopped && s.validLocked(o.request) && s.browserBindingMatchesLocked(o.request, o.binding)
}
func (s *Service) validInspectionContextLocked(ctx context.Context) bool {
	o, ok := ctx.Value(ownedInspectionKey{}).(*ownedInspection)
	return !ok || s.validOwnedInspectionLocked(o) && o.approved && o.conversation.sites[o.principal]
}

type pendingBrowserConsent struct {
	view    BrowserConsent
	request Request
	binding string
	ctx     context.Context
	cancel  context.CancelFunc
	choice  chan string
	decided bool
}
type browserPermit struct {
	owned                    *OwnedBrowserCall
	policyRevision           uint64
	request                  Request
	binding                  string
	evidence                 BrowserEvidence
	category, host, decision string
	expires                  time.Time
}
type browserGateway struct {
	policyRevision uint64
	policy         settings.BrowserPolicy
	sessions       map[string]*browserConversation
	ownedIntents   map[string]*ownedBrowserIntent
	permits        map[string]browserPermit
	pending        map[string]*pendingBrowserConsent
	queue          []string
	config         BrowserGatewayConfig
	activity       []BrowserActivity
	auditMu        sync.Mutex
	auditWG        sync.WaitGroup
}

func (s *Service) EnableBrowserGateway(config BrowserGatewayConfig) {
	p := settings.DefaultBrowserPolicy()
	if s.manager != nil {
		if v, e := s.manager.Snapshot(s.root); e == nil {
			p = settings.NormalizeBrowserPolicy(v.Automation.Browser.Policy)
		}
	}
	s.mu.Lock()
	if s.gate != nil {
		s.mu.Unlock()
		return
	}
	s.gate = &browserGateway{policyRevision: 1, policy: p, sessions: map[string]*browserConversation{}, permits: map[string]browserPermit{}, pending: map[string]*pendingBrowserConsent{}, config: config, activity: loadBrowserActivity(config.ActivityPath)}
	s.publishLocked()
	s.mu.Unlock()
	go s.browserIdleLoop()
}
func (s *Service) browserGatewaySnapshotLocked() *BrowserGatewaySnapshot {
	if s.gate == nil {
		return nil
	}
	g := s.gate
	v := &BrowserGatewaySnapshot{NativePrompts: g.config.NativePrompts, PolicyRevision: g.policyRevision, Policy: settings.NormalizeBrowserPolicy(&g.policy), Requests: []BrowserConsent{}, Sessions: []BrowserUse{}, Activity: append([]BrowserActivity{}, g.activity...)}
	for _, id := range g.queue {
		if p := g.pending[id]; p != nil {
			v.Requests = append(v.Requests, p.view)
		}
	}
	for id, c := range g.sessions {
		if c.browser && c.active {
			site := c.host
			if c.owned != nil && strings.HasPrefix(site, "artifact:") {
				site = "本地产物"
			}
			v.Sessions = append(v.Sessions, BrowserUse{id, c.life, c.title, site})
		}
	}
	for i := range v.Activity {
		if strings.HasPrefix(v.Activity[i].Site, "artifact:") {
			v.Activity[i].Site = "本地产物"
		}
	}
	for _, p := range g.permits {
		if p.decision == "ask" && time.Now().Before(p.expires) {
			v.PendingActions++
		}
	}
	sort.Slice(v.Sessions, func(i, j int) bool { return v.Sessions[i].Session < v.Sessions[j].Session })
	return v
}
func (s *Service) browserConversationLocked(r Request) (*browserConversation, error) {
	if r.Turn == "" || r.Session == "" || r.Lifetime == "" {
		return nil, ErrRevoked
	}
	c := s.gate.sessions[r.Session]
	if c == nil {
		c = &browserConversation{serial: make(chan struct{}, 1), life: r.Lifetime, turn: r.Turn, sites: map[string]bool{}, denied: map[string]bool{}}
		s.gate.sessions[r.Session] = c
	}
	if c.stopped {
		return nil, errors.New("BROWSER_STOPPED: The user stopped browser use for this conversation. Do not use browser tools again unless the user asks.")
	}
	if c.life != r.Lifetime || c.turn != r.Turn {
		return nil, ErrRevoked
	}
	return c, nil
}

// BrowserTurn is delivered only for actual user-source messages, not the
// runtime-context messages rc.5 appends before model steps. The trusted plugin
// serializes round changes. A new message never clears a stopped lifetime.
func (s *Service) BrowserTurn(r Request) (map[string]string, error) {
	s.mu.Lock()
	if s.gate == nil || r.Generation != s.state.Generation || r.Session == "" || r.Lifetime == "" || r.Turn == "" {
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	c := s.gate.sessions[r.Session]
	var old []*entry
	if c != nil && c.turn == r.Turn {
		life := c.life
		s.mu.Unlock()
		return map[string]string{"lifetime": life, "turn": r.Turn}, nil
	}
	life := r.Lifetime
	if c != nil {
		if c.stopped || c.life != life {
			life = rand.Text()
		}
		if c.stopped || c.life != life || s.gate.policy.GrantLasts == "turn" {
			old = s.revokeLocked(Browser, r.Session+"/"+c.life)
		}
	}
	if c == nil || c.life != life {
		var owned *ownedBrowserAdmission
		if c != nil {
			owned = c.owned
		}
		c = &browserConversation{owned: owned, serial: make(chan struct{}, 1), life: life, sites: map[string]bool{}, denied: map[string]bool{}}
		s.gate.sessions[r.Session] = c
	}
	c.turn = r.Turn
	c.turnSeen = true
	c.denied = map[string]bool{}
	s.cancelBrowserConsentsLocked(r.Session)
	for id, p := range s.gate.permits {
		if p.request.Session == r.Session {
			delete(s.gate.permits, id)
		}
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	r.Lifetime = life
	_ = s.admitOwnedIntent(s.root, r) // Failed selection never changes the trusted turn lifetime.
	return map[string]string{"lifetime": life, "turn": r.Turn}, nil
}

func (s *Service) cancelBrowserConsentsLocked(session string) {
	if s.gate == nil {
		return
	}
	for _, p := range s.gate.pending {
		if session == "" || p.request.Session == session {
			p.cancel()
		}
	}
}
func (s *Service) revokeBrowserGrantsLocked(capability Capability, scope string) {
	if s.gate == nil || (capability != "" && capability != Browser) {
		return
	}
	session := ""
	if scope != "" {
		session = strings.SplitN(scope, "/", 2)[0]
	}
	for id, c := range s.gate.sessions {
		if session == "" || session == id {
			if c.inspection != nil {
				c.inspection.cancel()
			}
			c.browser = false
			c.active = false
			c.sites = map[string]bool{}
			c.host = ""
		}
	}
	for id, p := range s.gate.permits {
		if session == "" || p.request.Session == session {
			delete(s.gate.permits, id)
		}
	}
	for _, p := range s.gate.pending {
		if session != "" && p.request.Session != session {
			continue
		}
		c := s.gate.sessions[p.request.Session]
		_, connected := s.selectedConnectionLocked()
		// A successful reconnect of the already-approved binding advances epoch.
		// Keep only that connection wait; every other revocation cancels the request.
		keep := p.decided && p.view.Phase == "connecting" && p.binding == s.profiles.prefs.Selected && p.request.Generation == s.state.Generation && s.state.CanChange && s.state.GatewayConnected && s.slots[Browser].state.Enabled && s.state.DefaultBrowserEnabled && connected == nil && c != nil && !c.stopped
		if !keep {
			p.cancel()
		}
	}
}
func (s *Service) browserIdleLoop() {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-s.root.Done():
			return
		case <-tick.C:
			s.mu.Lock()
			var old []*entry
			for id, c := range s.gate.sessions {
				if s.gate.policy.GrantLasts == "conversation" && c.browser && time.Since(c.last) >= 15*time.Minute {
					old = append(old, s.revokeLocked(Browser, id+"/"+c.life)...)
					s.browserActivityLocked(Request{Session: id, Title: c.title}, c.host, "", "expired")
				}
			}
			for id, p := range s.gate.permits {
				if time.Now().After(p.expires) {
					delete(s.gate.permits, id)
				}
			}
			s.publishLocked()
			s.mu.Unlock()
			s.release(old)
		}
	}
}
func (s *Service) SetBrowserPolicy(ctx context.Context, expected uint64, p settings.BrowserPolicy) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	valid := s.gate != nil && s.gate.policyRevision == expected
	s.mu.Unlock()
	if !valid {
		return s.Snapshot(), errors.New("BROWSER_POLICY_CHANGED")
	}
	if s.manager == nil {
		return s.Snapshot(), ErrUnavailable
	}
	if _, err := s.manager.SetBrowserPolicy(ctx, p); err != nil {
		return s.Snapshot(), err
	}
	s.mu.Lock()
	if s.gate == nil {
		s.mu.Unlock()
		return s.Snapshot(), ErrUnavailable
	}
	s.gate.policy = settings.NormalizeBrowserPolicy(&p)
	s.gate.policyRevision++
	var ownedResources []*entry
	for id, c := range s.gate.sessions {
		if c.owned != nil {
			ownedResources = append(ownedResources, s.revokeLocked(Browser, id+"/"+c.life)...)
		}
	}
	s.gate.permits = map[string]browserPermit{}
	s.cancelBrowserConsentsLocked("")
	s.publishLocked()
	s.mu.Unlock()
	s.release(ownedResources)
	var cleanup error
	for _, e := range ownedResources {
		cleanup = errors.Join(cleanup, e.closeErr)
	}
	return s.Snapshot(), cleanup
}
func (s *Service) browserConsent(ctx context.Context, r Request, c *browserConversation, binding, kind, site string, connect func(context.Context) error, action ...BrowserConsent) error {
	s.mu.Lock()
	if !s.validLocked(r) || !s.browserBindingMatchesLocked(r, binding) {
		s.mu.Unlock()
		return ErrRevoked
	}
	key := kind + ":" + site
	if kind == "action" {
		b, _ := json.Marshal(r.Arguments)
		key += ":" + r.Tool + ":" + string(b)
	}
	if c.denied[key] {
		s.mu.Unlock()
		return errors.New("BROWSER_ACCESS_DENIED: The user declined access. Don't ask again this turn.")
	}
	if len(s.gate.pending) >= 64 {
		s.mu.Unlock()
		return errors.New("BROWSER_BUSY")
	}
	run, cancel := context.WithTimeout(ctx, 2*time.Minute)
	p := &pendingBrowserConsent{request: r, binding: binding, ctx: run, cancel: cancel, choice: make(chan string, 1)}
	p.view = BrowserConsent{ID: rand.Text(), Kind: kind, Session: r.Session, Title: r.Title, Site: site, Phase: "waiting", Expires: time.Now().Add(2 * time.Minute)}
	if len(action) > 0 {
		p.view.Category = action[0].Category
		p.view.Action = action[0].Action
	}
	p.view.AllowAlways = kind == "browser"
	if s.ownedLocked(r) != nil {
		p.view.AllowAlways = false
		p.view.Browser = "embedded"
		p.view.Profile = "内置浏览器"
		if strings.HasPrefix(site, "artifact:") {
			p.view.Site = "本地产物"
		}
	}
	if kind == "site" {
		_, err := settings.NormalizeBrowserHost(site)
		p.view.AllowAlways = err == nil && s.ownedLocked(r) == nil
	}
	for _, b := range s.profiles.prefs.Bindings {
		if b.ID == binding {
			p.view.Profile = b.Name
			p.view.Browser = b.Browser
		}
	}
	s.gate.pending[p.view.ID] = p
	s.gate.queue = append(s.gate.queue, p.view.ID)
	notify := s.gate.config.Notify
	s.publishLocked()
	s.mu.Unlock()
	if notify != nil {
		go notify(p.view)
	}
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.gate.pending, p.view.ID)
		for i, id := range s.gate.queue {
			if id == p.view.ID {
				s.gate.queue = append(s.gate.queue[:i], s.gate.queue[i+1:]...)
				break
			}
		}
		s.publishLocked()
		s.mu.Unlock()
	}()
	select {
	case <-run.Done():
		s.mu.Lock()
		if errors.Is(run.Err(), context.DeadlineExceeded) {
			c.denied[key] = true
			s.browserActivityLocked(r, site, "", "expired")
		}
		s.mu.Unlock()
		if !errors.Is(run.Err(), context.DeadlineExceeded) {
			return ErrRevoked
		}
		return errors.New("BROWSER_ACCESS_EXPIRED: The user did not respond to the access request. Ask the user before trying again.")
	case choice := <-p.choice:
		if choice == "deny" {
			s.mu.Lock()
			c.denied[key] = true
			event := "denied"
			if kind == "action" {
				event = "confirmation-denied"
			}
			s.browserActivityLocked(r, site, p.view.Category, event)
			s.mu.Unlock()
			return errors.New("BROWSER_ACCESS_DENIED: The user declined access. Don't ask again this turn.")
		}
		if connect != nil {
			if err := connect(run); err != nil {
				return err
			}
		}
		if err := run.Err(); err != nil {
			return ErrRevoked
		}
		s.mu.Lock()
		event := "granted"
		if kind == "action" {
			event = "confirmed"
		}
		if choice == "always" {
			event = "granted-always"
		}
		s.browserActivityLocked(r, site, p.view.Category, event)
		s.mu.Unlock()
		return nil
	}
}
func (s *Service) AnswerBrowserConsent(ctx context.Context, id, choice string) (Snapshot, error) {
	if choice != "allow" && choice != "always" && choice != "deny" {
		return s.Snapshot(), errors.New("BROWSER_CONSENT_INVALID")
	}
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.gate == nil {
		s.mu.Unlock()
		return s.Snapshot(), ErrUnavailable
	}
	p := s.gate.pending[id]
	if p == nil || len(s.gate.queue) == 0 || s.gate.queue[0] != id || p.ctx.Err() != nil || p.request.Generation != s.state.Generation || !s.browserBindingMatchesLocked(p.request, p.binding) || !s.state.CanChange || !s.slots[Browser].state.Enabled || !s.state.DefaultBrowserEnabled {
		s.mu.Unlock()
		return s.Snapshot(), ErrRevoked
	}
	if choice == "always" && !p.view.AllowAlways {
		s.mu.Unlock()
		return s.Snapshot(), errors.New("BROWSER_CONSENT_INVALID")
	}
	if p.decided {
		if choice == "deny" {
			p.cancel()
			s.mu.Unlock()
			return s.Snapshot(), nil
		}
		s.mu.Unlock()
		return s.Snapshot(), ErrRevoked
	}
	policy := settings.NormalizeBrowserPolicy(&s.gate.policy)
	s.mu.Unlock()
	if choice == "always" {
		if p.view.Kind == "browser" {
			policy.AskEachConversation = false
		} else {
			policy.Sites = append(policy.Sites, settings.BrowserSiteRule{Host: p.view.Site, Rule: "allow"})
		}
		if s.manager == nil {
			return s.Snapshot(), ErrUnavailable
		}
		if _, err := s.manager.SetBrowserPolicy(ctx, policy); err != nil {
			return s.Snapshot(), err
		}
	}
	s.mu.Lock()
	if p.ctx.Err() != nil {
		s.mu.Unlock()
		return s.Snapshot(), ErrRevoked
	}
	if choice == "always" {
		s.gate.policy = settings.NormalizeBrowserPolicy(&policy)
		s.gate.policyRevision++
		s.gate.permits = map[string]browserPermit{}
	}
	p.decided = true
	if choice != "deny" && p.view.Kind == "browser" {
		p.view.Phase = "connecting"
	}
	p.choice <- choice
	s.publishLocked()
	s.mu.Unlock()
	return s.Snapshot(), nil
}
func (s *Service) inspectBrowser(ctx context.Context, r Request) (BrowserEvidence, error) {
	if r.Tool == "browser_navigate" || r.Tool == "browser_tabs" && r.Arguments["action"] == "new" {
		address, _ := r.Arguments["url"].(string)
		if address == "" && r.Tool == "browser_tabs" {
			address = "about:blank"
		}
		return BrowserEvidence{URL: address}, nil
	}
	targets := []any{}
	for _, key := range []string{"target", "startTarget", "endTarget"} {
		if v, ok := r.Arguments[key].(string); ok {
			targets = append(targets, v)
		}
	}
	if fields, ok := r.Arguments["fields"].([]any); ok {
		for _, field := range fields {
			if m, ok := field.(map[string]any); ok {
				if v, ok := m["target"].(string); ok {
					targets = append(targets, v)
				}
			}
		}
	}
	copy := r
	copy.Tool = "__dsh_inspect"
	copy.Arguments = map[string]any{"targets": targets, "tabs": r.Tool == "browser_tabs"}
	if _, owned := OwnedBrowserCallFromContext(ctx); owned && r.Tool == "browser_handle_dialog" {
		copy.Arguments["modal"] = "dialog"
	}
	if r.Tool == "browser_file_upload" {
		// Locked MCP permits context-level tabs/list during a chooser, while
		// tab-level evaluate and snapshot are blocked. Require its live modal
		// and current-URL metadata both before and after upload approval.
		copy.Arguments["modal"] = "file-upload"
	}
	value, err := s.executeOwned(ctx, copy)
	if err != nil {
		return BrowserEvidence{}, err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return BrowserEvidence{}, err
	}
	var evidence BrowserEvidence
	err = json.Unmarshal(data, &evidence)
	return evidence, err
}
func (s *Service) AuthorizeBrowser(ctx context.Context, r Request) (BrowserDecision, error) {
	if err := s.admitOwnedIntent(ctx, r); err != nil {
		return BrowserDecision{}, err
	}
	s.mu.Lock()
	if s.gate == nil {
		s.mu.Unlock()
		return BrowserDecision{}, ErrUnavailable
	}
	c, err := s.browserConversationLocked(r)
	if err != nil {
		s.mu.Unlock()
		return BrowserDecision{}, err
	}
	if !s.validLocked(r) {
		s.mu.Unlock()
		return BrowserDecision{}, ErrRevoked
	}
	known := false
	for _, schema := range s.browserSchemas {
		var v struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(schema, &v) == nil && v.Name == r.Tool {
			known = true
			break
		}
	}
	if !known {
		s.mu.Unlock()
		return BrowserDecision{}, errors.New("BROWSER_TOOL_UNAVAILABLE")
	}
	binding := s.browserBindingLocked(r)
	owned := s.ownedLocked(r) != nil
	if want := s.gate.config.ExtensionSource; !owned && want != "" {
		if id, e := s.selectedConnectionLocked(); e == nil && s.profiles.connections[id].hello.Source != want {
			s.mu.Unlock()
			return BrowserDecision{}, errors.New("BROWSER_EXTENSION_RELOAD_REQUIRED: reload the supplied extension in the selected profile, then reconnect")
		}
	}
	if binding == "" {
		s.mu.Unlock()
		return BrowserDecision{}, errors.New("BROWSER_SETUP_REQUIRED: Open Settings > Browser and select a linked profile before trying again.")
	}
	s.mu.Unlock()
	select {
	case c.serial <- struct{}{}:
		defer func() { <-c.serial }()
	case <-ctx.Done():
		return BrowserDecision{}, ctx.Err()
	}
	s.mu.Lock()
	if !s.validLocked(r) {
		s.mu.Unlock()
		return BrowserDecision{}, ErrRevoked
	}
	p := settings.NormalizeBrowserPolicy(&s.gate.policy)
	need := (p.AskEachConversation || owned) && !c.browser
	authorizationRevision := s.gate.policyRevision
	c.title = r.Title
	s.mu.Unlock()
	connect := func(run context.Context) error {
		if owned {
			_, _, err := s.ownedCall(run, r)
			return err
		}
		return s.OpenBrowserProfile(run, binding)
	}
	if need {
		err = s.browserConsent(ctx, r, c, binding, "browser", "", connect)
	} else {
		err = connect(ctx)
	}
	if err != nil {
		return BrowserDecision{}, err
	}
	s.mu.Lock()
	r.Epoch = s.slots[Browser].state.Epoch
	if !s.validLocked(r) || !s.browserBindingMatchesLocked(r, binding) || owned && (s.gate.policyRevision != authorizationRevision || s.gate.sessions[r.Session] != c) {
		s.mu.Unlock()
		return BrowserDecision{}, ErrRevoked
	}
	if want := s.gate.config.ExtensionSource; !owned && want != "" {
		id, e := s.selectedConnectionLocked()
		if e != nil || s.profiles.connections[id].hello.Source != want {
			s.mu.Unlock()
			return BrowserDecision{}, errors.New("BROWSER_EXTENSION_RELOAD_REQUIRED: reload the supplied extension in the selected profile, then reconnect")
		}
	}
	c.browser = true
	c.binding = binding
	c.last = time.Now()
	s.publishLocked()
	s.mu.Unlock()
	call, isOwned, err := s.ownedCall(ctx, r)
	if err != nil {
		return BrowserDecision{}, err
	}
	// The owned engine already supplies an exact principal. Obtain its site
	// consent before creating MCP or reading the page for policy inspection.
	// External browsers still establish their destination through inspection.
	if isOwned && r.Tool != "browser_navigate" && !(r.Tool == "browser_tabs" && r.Arguments["action"] == "new") {
		inspection, err := s.authorizeOwnedInspection(ctx, r, c, binding, call, authorizationRevision)
		if err != nil {
			return BrowserDecision{}, err
		}
		defer func() {
			s.mu.Lock()
			if c.inspection == inspection {
				c.inspection = nil
			}
			inspection.cancel()
			s.mu.Unlock()
		}()
		ctx = context.WithValue(inspection.ctx, ownedInspectionKey{}, inspection)
		current, _, err := s.ownedCall(ctx, r)
		if err != nil {
			return BrowserDecision{}, err
		}
		if current.Identity != call.Identity {
			return BrowserDecision{}, errors.New("BROWSER_PAGE_CHANGED: the selected page changed while waiting for site approval")
		}
		call = current
	}
	if isOwned {
		ctx = WithOwnedBrowserCall(ctx, call)
	}
	evidence, err := s.inspectBrowser(ctx, r)
	if err != nil {
		// Snapshot/list can establish state even when the page is closing or a
		// JavaScript dialog prevents a read-only observation. No write fallback.
		if !owned && (r.Tool == "browser_snapshot" || r.Tool == "browser_handle_dialog" || r.Tool == "browser_tabs" && r.Arguments["action"] == "list") {
			evidence = BrowserEvidence{URL: "about:blank"}
		} else {
			return BrowserDecision{}, fmt.Errorf("BROWSER_TARGET_UNVERIFIED: %w", err)
		}
	}
	address := evidence.URL
	if r.Tool == "browser_tabs" && (r.Arguments["action"] == "select" || r.Arguments["action"] == "close" && r.Arguments["index"] != nil) {
		index := -1
		switch v := r.Arguments["index"].(type) {
		case float64:
			index = int(v)
		case int:
			index = v
		}
		address = ""
		for _, t := range evidence.Tabs {
			if t.Index == index {
				address = t.URL
			}
		}
		if address == "" {
			return BrowserDecision{}, errors.New("BROWSER_TARGET_UNVERIFIED")
		}
	}
	host, err := browserHost(address, r.Tool == "browser_navigate" || r.Tool == "browser_tabs" && r.Arguments["action"] == "new")
	if err != nil {
		return BrowserDecision{}, err
	}
	coarseHost := host
	if owned {
		host, err = ownedPrincipal(call.Identity, address)
		if err != nil {
			return BrowserDecision{}, err
		}
		if call.Identity.ArtifactLease != "" {
			coarseHost = ""
		}
		if r.Tool == "browser_navigate" {
			call.NavigationOrigin, err = BrowserOrigin(address)
			if err != nil {
				return BrowserDecision{}, err
			}
		} else {
			currentOrigin, _ := BrowserOrigin(call.Identity.URL)
			observedOrigin, _ := BrowserOrigin(address)
			if currentOrigin != observedOrigin {
				return BrowserDecision{}, errors.New("BROWSER_FRAME_SCOPE_UNAVAILABLE")
			}
		}
	}
	s.mu.Lock()
	p = settings.NormalizeBrowserPolicy(&s.gate.policy)
	rule := p.SiteRule(coarseHost)
	siteAllowed := c.sites[host]
	s.mu.Unlock()
	if host != "" && rule == "block" {
		s.mu.Lock()
		s.browserActivityLocked(r, host, "", "blocked")
		s.mu.Unlock()
		return BrowserDecision{}, errors.New("BROWSER_SITE_BLOCKED: " + host + " is blocked in Settings. Don't use it.")
	}
	if host != "" && !siteAllowed && (owned || rule != "allow" && p.NewSites == "ask") {
		if err = s.browserConsent(ctx, r, c, binding, "site", host, nil); err != nil {
			return BrowserDecision{}, err
		}
		s.mu.Lock()
		c.sites[host] = true
		s.mu.Unlock()
	}
	category, err := browserCategory(r, evidence)
	if err != nil {
		s.mu.Lock()
		s.browserActivityLocked(r, host, "", "blocked")
		s.mu.Unlock()
		return BrowserDecision{}, err
	}
	decision := "allow"
	if category != "" {
		decision = p.Confirm[category]
		if decision == "" {
			decision = "ask"
		}
	}
	if decision == "block" {
		s.mu.Lock()
		s.browserActivityLocked(r, host, category, "blocked")
		s.mu.Unlock()
		return BrowserDecision{}, errors.New("BROWSER_ACTION_BLOCKED: This action is blocked in Settings. Ask the user to do it.")
	}
	if decision == "ask" && s.gate.config.NativePrompts {
		name := strings.TrimPrefix(r.Tool, "browser_")
		if len(evidence.Elements) > 0 && evidence.Elements[0].Name != "" {
			name = evidence.Elements[0].Name
		}
		if err = s.browserConsent(ctx, r, c, binding, "action", host, nil, BrowserConsent{Category: category, Action: name}); err != nil {
			return BrowserDecision{}, err
		}
		decision = "allow"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validLocked(r) || !s.browserBindingMatchesLocked(r, binding) {
		return BrowserDecision{}, ErrRevoked
	}
	for id, permit := range s.gate.permits {
		if time.Now().After(permit.expires) {
			delete(s.gate.permits, id)
		}
	}
	if len(s.gate.permits) >= 128 {
		return BrowserDecision{}, errors.New("BROWSER_BUSY")
	}
	id := rand.Text()
	r.Permit = ""
	s.gate.permits[id] = browserPermit{policyRevision: s.gate.policyRevision, request: r, binding: binding, evidence: evidence, category: category, host: host, decision: decision, expires: time.Now().Add(2 * time.Minute)}
	if owned {
		p := s.gate.permits[id]
		call.Token = id
		p.owned = &call
		s.gate.permits[id] = p
	}
	if decision == "ask" {
		if owned && strings.HasPrefix(host, "artifact:") {
			host = "本地产物"
		}
		s.publishLocked()
	}
	name := ""
	if len(evidence.Elements) > 0 {
		name = evidence.Elements[0].Name
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40]) + "…"
	}
	if category == "upload" {
		name = ""
		if paths, ok := r.Arguments["paths"].([]any); ok {
			for _, path := range paths {
				if text, ok := path.(string); ok {
					if name != "" {
						name += ", "
					}
					name += filepath.Base(text)
				}
			}
		}
	}
	result := BrowserDecision{Kind: decision, Permit: id, Epoch: r.Epoch}
	if decision == "ask" {
		result.Reason = "Confirm " + category + " on " + host
		result.DisplayReason = browserDisplayReason(category, name, host, r.Tool)
	}
	return result, nil
}

func (s *Service) authorizeOwnedInspection(ctx context.Context, r Request, c *browserConversation, binding string, call OwnedBrowserCall, revision uint64) (_ *ownedInspection, err error) {
	principal, err := ownedPrincipal(call.Identity, call.Identity.URL)
	if err != nil {
		return nil, err
	}
	coarseHost := ""
	if call.Identity.ArtifactLease == "" {
		coarseHost, err = browserHost(call.Identity.URL, false)
		if err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	run, cancel := context.WithCancel(ctx)
	o := &ownedInspection{conversation: c, request: r, revision: revision, binding: binding, principal: principal, ctx: run, cancel: cancel}
	c.inspection = o
	if !s.validOwnedInspectionLocked(o) {
		c.inspection = nil
		cancel()
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	defer func() {
		if err != nil {
			s.mu.Lock()
			if c.inspection == o {
				c.inspection = nil
			}
			cancel()
			s.mu.Unlock()
		}
	}()
	blocked := settings.NormalizeBrowserPolicy(&s.gate.policy).SiteRule(coarseHost) == "block"
	allowed := c.sites[principal]
	s.mu.Unlock()
	if blocked {
		return nil, errors.New("BROWSER_SITE_BLOCKED: " + principal + " is blocked in Settings. Don't use it.")
	}
	if !allowed {
		if err = s.browserConsent(run, r, c, binding, "site", principal, nil); err != nil {
			return nil, err
		}
		s.mu.Lock()
		if !s.validOwnedInspectionLocked(o) {
			s.mu.Unlock()
			return nil, ErrRevoked
		}
		c.sites[principal] = true
		o.approved = true
		s.mu.Unlock()
	} else {
		s.mu.Lock()
		if !s.validOwnedInspectionLocked(o) {
			s.mu.Unlock()
			return nil, ErrRevoked
		}
		o.approved = true
		s.mu.Unlock()
	}
	return o, nil
}
func browserDisplayReason(category, name, host, tool string) map[string]string {
	labels := map[string][3]string{"payment": {"付款", "Payment", "支払い"}, "send": {"发送", "Send", "送信"}, "publish": {"发布", "Publish", "公開"}, "delete": {"删除", "Delete", "削除"}, "install": {"安装", "Install", "インストール"}, "settings": {"更改设置", "Change settings", "設定の変更"}, "submit": {"提交", "Submit", "確定"}, "upload": {"上传文件", "Upload file", "ファイルをアップロード"}, "script": {"运行页面脚本", "Run page script", "ページスクリプトを実行"}, "unknown": {"无法识别的操作", "Unrecognised action", "認識できない操作"}}
	label := labels[category]
	if name == "" {
		name = strings.TrimPrefix(tool, "browser_")
	}
	return map[string]string{"zh-CN": fmt.Sprintf("%s：%s（%s）", label[0], name, host), "en": fmt.Sprintf("%s: %s (%s)", label[1], name, host), "ja-JP": fmt.Sprintf("%s：%s（%s）", label[2], name, host)}
}
func (s *Service) executeBrowserPermit(ctx context.Context, r Request) (any, error) {
	s.mu.Lock()
	permit, ok := s.gate.permits[r.Permit]
	if ok {
		delete(s.gate.permits, r.Permit)
		if permit.decision == "ask" {
			s.publishLocked()
		}
	}
	copy := r
	copy.Permit = ""
	a, _ := json.Marshal(copy)
	b, _ := json.Marshal(permit.request)
	if !ok || permit.policyRevision != s.gate.policyRevision || !time.Now().Before(permit.expires) || string(a) != string(b) || !s.validLocked(r) || !s.browserBindingMatchesLocked(r, permit.binding) {
		s.mu.Unlock()
		return nil, errors.New("BROWSER_AUTHORIZATION_REQUIRED: access approval expired or does not match this call")
	}
	conversation := s.gate.sessions[r.Session]
	s.mu.Unlock()
	select {
	case conversation.serial <- struct{}{}:
		defer func() { <-conversation.serial }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	valid := time.Now().Before(permit.expires) && s.validLocked(r) && permit.policyRevision == s.gate.policyRevision && s.browserBindingMatchesLocked(r, permit.binding)
	s.mu.Unlock()
	if !valid {
		return nil, ErrRevoked
	}
	if permit.owned != nil {
		ctx = WithOwnedBrowserCall(ctx, *permit.owned)
	}
	current, err := s.inspectBrowser(ctx, r)
	if err != nil {
		if permit.owned == nil && (r.Tool == "browser_snapshot" || r.Tool == "browser_handle_dialog" || r.Tool == "browser_tabs" && r.Arguments["action"] == "list") {
			current = permit.evidence
		} else {
			return nil, errors.New("BROWSER_PAGE_CHANGED: take a fresh snapshot and try again")
		}
	}
	a, _ = json.Marshal(current)
	b, _ = json.Marshal(permit.evidence)
	if string(a) != string(b) {
		return nil, errors.New("BROWSER_PAGE_CHANGED: the page changed while waiting for approval; take a fresh snapshot and try again")
	}
	s.mu.Lock()
	if !s.validLocked(r) || permit.owned != nil && permit.policyRevision != s.gate.policyRevision {
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	if permit.decision == "ask" {
		s.browserActivityLocked(r, permit.host, permit.category, "confirmed")
	}
	s.mu.Unlock()
	value, err := s.executeOwned(ctx, r)
	if permit.owned != nil {
		return s.finishOwnedBrowserCall(ctx, r, permit, value, err)
	}
	actualHost := permit.host
	if address := browserResultURL(value); address != "" {
		if host, hostErr := browserHost(address, false); hostErr == nil {
			actualHost = host
		} else if err == nil {
			err = hostErr
			value = nil
		}
	}
	s.mu.Lock()
	if c := s.gate.sessions[r.Session]; c != nil && c.life == r.Lifetime {
		c.last = time.Now()
		c.host = actualHost
		rule := s.gate.policy.SiteRule(actualHost)
		if actualHost != "" && rule == "block" {
			value = nil
			err = errors.New("BROWSER_SITE_BLOCKED: " + actualHost + " is blocked in Settings. Don't use it.")
		}
		if actualHost != "" && actualHost != permit.host && rule != "allow" && !c.sites[actualHost] && s.gate.policy.NewSites == "ask" {
			value = nil
			err = errors.New("BROWSER_SITE_ACCESS_REQUIRED: The page moved to another site. Take a fresh snapshot to request access before continuing.")
		}
	}
	s.publishLocked()
	s.mu.Unlock()
	return value, err
}
func (s *Service) DiscardBrowserPermit(r Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.gate == nil {
		return
	}
	if p, ok := s.gate.permits[r.Permit]; ok && p.request.Generation == r.Generation && p.request.Session == r.Session && p.request.Lifetime == r.Lifetime {
		delete(s.gate.permits, r.Permit)
		if p.decision == "ask" {
			s.browserActivityLocked(p.request, p.host, p.category, "confirmation-denied")
		}
		s.publishLocked()
	}
}

// StopBrowserUse is the browser-only user control. An empty session stops all
// browser conversations; stopping one never revokes another conversation.
func (s *Service) StopBrowserUse(session, lifetime string) error {
	s.mu.Lock()
	if s.gate == nil {
		s.mu.Unlock()
		return ErrUnavailable
	}
	cleanupOnly := false
	if session != "" {
		c := s.gate.sessions[session]
		if c == nil || c.life != lifetime {
			for _, owner := range s.browserCleanup {
				if owner.session == session && owner.lifetime == lifetime {
					cleanupOnly = true
					break
				}
			}
			if !cleanupOnly {
				s.mu.Unlock()
				return ErrRevoked
			}
		}
	}
	var retry []*browserCleanupOwner
	for _, owner := range s.browserCleanup {
		if session == "" || owner.session == session && owner.lifetime == lifetime {
			retry = append(retry, owner)
		}
	}
	for id, intent := range s.gate.ownedIntents {
		if !cleanupOnly && (session == "" || id == session) {
			intent.invalid = true
		}
	}
	var old []*entry
	var cleanupOwners []*browserCleanupOwner
	for id, c := range s.gate.sessions {
		if cleanupOnly {
			continue
		}
		if session != "" && id != session {
			continue
		}
		if session == "" && !c.browser && s.gate.ownedIntents[id] == nil {
			continue
		}
		host := c.host
		c.stopped = true
		s.stopped[revocationKey(Request{Capability: Browser, Epoch: s.slots[Browser].state.Epoch, Session: id, Lifetime: c.life})] = true
		retired := s.revokeLocked(Browser, id+"/"+c.life)
		old = append(old, retired...)
		if s.browserCleanup == nil {
			s.browserCleanup = map[*entry]*browserCleanupOwner{}
		}
		for _, e := range retired {
			owner := &browserCleanupOwner{entry: e, session: id, lifetime: c.life}
			s.browserCleanup[e] = owner
			cleanupOwners = append(cleanupOwners, owner)
		}
		s.browserActivityLocked(Request{Session: id, Title: c.title}, host, "", "stopped")
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	var cleanup error
	for _, owner := range retry {
		cleanup = errors.Join(cleanup, s.finishBrowserCleanup(owner, true))
	}
	for _, owner := range cleanupOwners {
		cleanup = errors.Join(cleanup, s.finishBrowserCleanup(owner, false))
	}
	return cleanup
}

var browserPageURL = regexp.MustCompile(`(?m)^### Page\r?\n- Page URL: ([^\r\n]+)`)

func browserResultURL(value any) string {
	data, _ := json.Marshal(value)
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(data, &result) != nil {
		return ""
	}
	for _, part := range result.Content {
		if part.Type == "text" {
			if match := browserPageURL.FindStringSubmatch(part.Text); len(match) > 1 {
				return match[1]
			}
		}
	}
	return ""
}
