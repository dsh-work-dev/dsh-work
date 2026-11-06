package automation

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/settings"
	"reflect"
	"strings"
	"sync"
	"time"
)

type ComputerElementIdentity struct {
	RuntimeID string `json:"runtimeId"`
	Password  bool   `json:"password"`
}
type ComputerElementProbe func(context.Context, ComputerTarget, map[string]any) (ComputerElementIdentity, error)

type ComputerFactory func(context.Context, string) (Resource, error)

// computerAppKind is supplied by the native inspector, not inferred from a
// process path by the shared authorization gateway.
type computerAppKind uint8

const (
	computerAppOrdinary computerAppKind = iota
	computerAppBrowser
	computerAppTerminal
)

type ComputerTarget struct {
	kind           computerAppKind
	Identity       string `json:"identity"`
	Warning        bool   `json:"warning"`
	PID            uint32 `json:"pid"`
	WindowID       uint64 `json:"windowId"`
	Executable     string `json:"executable"`
	Created        uint64 `json:"created"`
	WindowIdentity string `json:"windowIdentity"`
	App            string `json:"app"`
	Title          string `json:"title"`
	Level          string `json:"level"`
}
type ComputerConsent struct {
	ID      string         `json:"id"`
	Session string         `json:"session"`
	Title   string         `json:"title"`
	Target  ComputerTarget `json:"target"`
	Expires time.Time      `json:"expires"`
}
type ComputerUse struct {
	Session  string          `json:"session"`
	Lifetime string          `json:"lifetime"`
	Title    string          `json:"title"`
	Engine   string          `json:"engine"`
	Target   *ComputerTarget `json:"target,omitempty"`
	Stopping bool            `json:"stopping"`
	Running  bool            `json:"running"`
	Action   string          `json:"action,omitempty"`
	Rect     map[string]any  `json:"rect,omitempty"`
}
type ComputerSnapshot struct {
	Failure        string                  `json:"failure,omitempty"`
	Policy         settings.ComputerPolicy `json:"policy"`
	PolicyRevision uint64                  `json:"policyRevision"`
	Requests       []ComputerConsent       `json:"requests"`
	Use            *ComputerUse            `json:"use,omitempty"`
}
type ComputerGatewayConfig struct {
	Factory ComputerFactory
	Acquire func() (func() error, error)
	Probe   ComputerElementProbe
	Inspect func(uint32, uint64) (ComputerTarget, error)
}
type computerPending struct {
	view   ComputerConsent
	choice chan bool
	ctx    context.Context
}
type computerPermit struct {
	request    Request
	target     ComputerTarget
	snapshot   string
	identity   ComputerElementIdentity
	element    map[string]any
	actionArgs map[string]any
	expires    time.Time
}
type computerObservation struct {
	target        ComputerTarget
	snapshot      string
	elements      []map[string]any
	bounds        map[string]any
	width, height float64
	at            time.Time
}
type computerConversation struct {
	life, turn string
	stopped    bool
}
type computerGateway struct {
	sessions       map[string]*computerConversation
	policy         settings.ComputerPolicy
	policyRevision uint64
	config         ComputerGatewayConfig
	owner          Request
	lease          *entry
	serial         chan struct{}
	grants         map[string]bool
	denied         map[string]bool
	pending        map[string]*computerPending
	permits        map[string]computerPermit
	observation    *computerObservation
	use            *ComputerUse
	releaseDesktop func() error
	desktopFailure error
	runCtx         context.Context
	runCancel      context.CancelFunc
	probes         sync.WaitGroup
}

func (s *Service) ConfigureComputerGateway(c ComputerGatewayConfig) {
	policy := settings.DefaultComputerPolicy()
	if s.manager != nil {
		if values, e := s.manager.Snapshot(s.root); e == nil {
			policy = settings.NormalizeComputerPolicy(values.Automation.Computer.Policy)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.computer = &computerGateway{sessions: map[string]*computerConversation{}, policy: policy, policyRevision: 1, config: c, serial: make(chan struct{}, 1), grants: map[string]bool{}, denied: map[string]bool{}, pending: map[string]*computerPending{}, permits: map[string]computerPermit{}}
	s.publishLocked()
}
func (s *Service) computerSnapshotLocked() *ComputerSnapshot {
	g := s.computer
	if g == nil {
		return nil
	}
	v := &ComputerSnapshot{Policy: settings.NormalizeComputerPolicy(&g.policy), PolicyRevision: g.policyRevision, Requests: []ComputerConsent{}}
	for _, p := range g.pending {
		v.Requests = append(v.Requests, p.view)
	}
	if g.desktopFailure != nil {
		v.Failure = "cleanup"
	}
	if g.lease != nil {
		select {
		case <-g.lease.closed:
			if g.lease.closeErr != nil {
				v.Failure = "cleanup"
			}
		default:
		}
	}
	if g.use != nil {
		u := *g.use
		v.Use = &u
	}
	return v
}
func (s *Service) computerValidLocked(r Request) bool {
	c := Computer
	if s.state.ComputerEngine == "auv" {
		c = AUV
	}
	v := s.slots[c].state
	if s.computer != nil {
		if conversation := s.computer.sessions[r.Session]; conversation != nil && (conversation.stopped || conversation.life != r.Lifetime || conversation.turn != r.Turn) {
			return false
		}
	}
	return s.computer != nil && s.computer.config.Factory != nil && s.computer.config.Inspect != nil && r.Capability == Computer && s.state.CanChange && s.state.GatewayConnected && r.Generation == s.state.Generation && s.slots[Computer].state.Enabled && v.Dependency == "prepared" && v.Epoch == r.Epoch && r.Session != "" && r.Lifetime != "" && !s.stopped[revocationKey(r)]
}
func sameComputerOwner(a, b Request) bool {
	return a.Generation == b.Generation && a.Session == b.Session && a.Lifetime == b.Lifetime
}
func computerGrantKey(r Request, t ComputerTarget) string {
	return r.Session + "/" + r.Lifetime + "/" + strings.ToLower(t.Identity)
}
func (s *Service) claimComputerLocked(r Request) error {
	g := s.computer
	if g != nil && g.desktopFailure != nil {
		return errors.New("COMPUTER_CLEANUP_FAILED")
	}
	if !s.computerValidLocked(r) {
		return ErrRevoked
	}
	if g.lease != nil {
		select {
		case <-g.lease.closed:
			if g.lease.closeErr != nil {
				return errors.New("COMPUTER_CLEANUP_FAILED: the previous engine has not been safely released")
			}
			g.owner = Request{}
			g.lease = nil
			g.releaseDesktop = nil
		default:
		}
	}
	if g.lease != nil && g.lease.revoked {
		return errors.New("COMPUTER_BUSY: the previous engine is stopping")
	}
	if g.owner.Session != "" && !sameComputerOwner(g.owner, r) {
		if g.lease != nil {
			select {
			case <-g.lease.closed:
				g.owner = Request{}
				g.lease = nil
			default:
				return errors.New("COMPUTER_BUSY: another conversation owns this desktop")
			}
		} else {
			return errors.New("COMPUTER_BUSY: another conversation owns this desktop")
		}
	}
	if g.owner.Session == "" {
		if g.config.Acquire != nil {
			release, e := g.config.Acquire()
			if e != nil {
				return e
			}
			if release == nil {
				return errors.New("COMPUTER_DESKTOP_LEASE_UNAVAILABLE")
			}
			g.releaseDesktop = release
		}
		g.runCtx, g.runCancel = context.WithCancel(s.root)
		g.owner = r
		g.use = &ComputerUse{Session: r.Session, Lifetime: r.Lifetime, Title: r.Title, Engine: s.state.ComputerEngine}
		s.publishLocked()
	}
	return nil
}
func (s *Service) revokeComputerLocked(c Capability, scope string) {
	g := s.computer
	if g == nil || (c != "" && c != Computer && c != AUV) {
		return
	}
	match := func(r Request) bool { return scope == "" || r.Session+"/"+r.Lifetime == scope }
	for id, p := range g.permits {
		if match(p.request) {
			delete(g.permits, id)
		}
	}
	for id, p := range g.pending {
		if scope == "" || p.view.Session == strings.SplitN(scope, "/", 2)[0] {
			select {
			case p.choice <- false:
			default:
			}
			delete(g.pending, id)
		}
	}
	if match(g.owner) {
		if g.runCancel != nil {
			g.runCancel()
		}
		g.grants = map[string]bool{}
		g.denied = map[string]bool{}
		g.observation = nil
		if g.use != nil {
			g.use.Running = false
			g.use.Rect = nil
			g.use.Stopping = true
		}
		if g.lease == nil {
			if g.releaseDesktop != nil {
				if e := g.releaseDesktop(); e != nil {
					g.desktopFailure = e
					return
				}
				g.releaseDesktop = nil
			}
			g.owner = Request{}
			g.use = nil
		}
	}
}
func (s *Service) AnswerComputerConsent(ctx context.Context, id, choice string) (Snapshot, error) {
	if choice == "always" {
		s.op.Lock()
		defer s.op.Unlock()
		s.mu.Lock()
		g := s.computer
		if g == nil || g.pending[id] == nil || g.pending[id].ctx.Err() != nil || time.Now().After(g.pending[id].view.Expires) {
			s.mu.Unlock()
			return s.Snapshot(), ErrRevoked
		}
		t := g.pending[id].view.Target
		p := settings.NormalizeComputerPolicy(&g.policy)
		s.mu.Unlock()
		found := false
		for i := range p.Apps {
			if p.Apps[i].ID == t.Identity {
				p.Apps[i].Level = t.Level
				found = true
			}
		}
		if !found {
			p.Apps = append(p.Apps, settings.ComputerAppRule{ID: t.Identity, Name: t.App, Level: t.Level})
		}
		if s.manager == nil {
			return s.Snapshot(), ErrUnavailable
		}
		if _, e := s.manager.SetComputerPolicy(ctx, p); e != nil {
			return s.Snapshot(), e
		}
		s.mu.Lock()
		g.policy = p
		g.policyRevision++
		s.mu.Unlock()
		choice = "allow"
	}

	if err := ctx.Err(); err != nil {
		return s.Snapshot(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.computer == nil {
		return s.snapshotLocked(), ErrRevoked
	}
	p := s.computer.pending[id]
	if p == nil || p.ctx.Err() != nil || time.Now().After(p.view.Expires) {
		return s.snapshotLocked(), ErrRevoked
	}
	if choice != "allow" && choice != "deny" {
		return s.snapshotLocked(), errors.New("COMPUTER_INVALID_CHOICE")
	}
	select {
	case p.choice <- choice == "allow":
	default:
		return s.snapshotLocked(), ErrRevoked
	}
	delete(s.computer.pending, id)
	s.publishLocked()
	return s.snapshotLocked(), nil
}
func (s *Service) computerConsent(ctx context.Context, r Request, t ComputerTarget) error {
	s.mu.Lock()
	g := s.computer
	k := computerGrantKey(r, t)
	if g.policy.GrantLasts == "turn" {
		k += "/" + r.Turn
	}
	if g.grants[k] {
		s.mu.Unlock()
		return nil
	}
	listed := false
	for _, rule := range g.policy.Apps {
		if rule.ID == t.Identity {
			listed = true
		}
	}
	if listed || g.policy.NewApps == "allow" && !t.Warning {
		g.grants[k] = true
		s.mu.Unlock()
		return nil
	}
	if g.denied[k+"/"+r.Turn] {
		s.mu.Unlock()
		return errors.New("COMPUTER_APP_DENIED")
	}
	p := &computerPending{view: ComputerConsent{ID: rand.Text(), Session: r.Session, Title: r.Title, Target: t, Expires: time.Now().Add(2 * time.Minute)}, choice: make(chan bool, 1), ctx: ctx}
	g.pending[p.view.ID] = p
	s.publishLocked()
	s.mu.Unlock()
	allowed := false
	select {
	case allowed = <-p.choice:
	case <-ctx.Done():
	case <-s.root.Done():
	case <-time.After(2 * time.Minute):
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(g.pending, p.view.ID)
	if !s.computerValidLocked(r) || !sameComputerOwner(g.owner, r) {
		s.publishLocked()
		return ErrRevoked
	}
	if err := ctx.Err(); err != nil {
		s.publishLocked()
		return err
	}
	if allowed {
		g.grants[k] = true
	} else {
		g.denied[k+"/"+r.Turn] = true
	}
	s.publishLocked()
	if !allowed {
		return errors.New("COMPUTER_APP_DENIED")
	}
	return nil
}
func computerNumber(v any) (uint64, bool) {
	f, ok := v.(float64)
	if ok && f > 0 && f == float64(uint64(f)) && f <= 9007199254740991 {
		return uint64(f), true
	}
	return 0, false
}
func computerTargetArgs(args map[string]any) (uint32, uint64, error) {
	p, pok := computerNumber(args["pid"])
	w, wok := computerNumber(args["window_id"])
	if !pok || !wok || p > 4294967295 {
		return 0, 0, errors.New("COMPUTER_EXACT_WINDOW_REQUIRED")
	}
	return uint32(p), w, nil
}
func computerAllowed(tool string) bool {
	switch tool {
	case "computer_windows", "computer_observe", "computer_click", "computer_type", "computer_scroll", "computer_key", "computer_release":
		return true
	}
	return false
}
func computerRawTool(tool string) string {
	return map[string]string{"computer_windows": "list_windows", "computer_observe": "get_window_state", "computer_click": "click", "computer_type": "type_text", "computer_scroll": "scroll", "computer_key": "press_key"}[tool]
}
func computerContent(v any) (map[string]any, error) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("COMPUTER_INVALID_RESULT")
	}
	if bad, _ := m["isError"].(bool); bad {
		return nil, errors.New("COMPUTER_OBSERVATION_FAILED")
	}
	x, ok := m["structuredContent"].(map[string]any)
	if !ok {
		return nil, errors.New("COMPUTER_STRUCTURED_RESULT_REQUIRED")
	}
	return x, nil
}
func (s *Service) AuthorizeComputer(ctx context.Context, r Request) (BrowserDecision, error) {
	if err := ctx.Err(); err != nil {
		return BrowserDecision{}, err
	}
	s.mu.Lock()
	engine := s.state.ComputerEngine
	s.mu.Unlock()
	if err := validateComputerArguments(r, engine); err != nil {
		return BrowserDecision{}, err
	}
	if !computerAllowed(r.Tool) {
		return BrowserDecision{}, errors.New("COMPUTER_TOOL_UNAVAILABLE")
	}
	if r.Tool == "computer_release" {
		s.mu.Lock()
		defer s.mu.Unlock()
		g := s.computer
		if !s.computerValidLocked(r) {
			return BrowserDecision{}, ErrRevoked
		}
		if g.owner.Session != "" && !sameComputerOwner(g.owner, r) {
			return BrowserDecision{}, errors.New("COMPUTER_BUSY")
		}
		s.pruneComputerPermitsLocked()
		if len(g.permits) >= 128 {
			return BrowserDecision{}, errors.New("COMPUTER_BUSY")
		}
		id := rand.Text()
		r.Arguments = cloneArguments(r.Arguments)
		g.permits[id] = computerPermit{request: r, expires: time.Now().Add(2 * time.Minute)}
		return BrowserDecision{Kind: "allow", Permit: id, Epoch: r.Epoch}, nil
	}
	s.mu.Lock()
	err := s.claimComputerLocked(r)
	g := s.computer
	s.mu.Unlock()
	if err != nil {
		return BrowserDecision{}, err
	}
	admitted := false
	defer func() {
		if !admitted {
			s.mu.Lock()
			if sameComputerOwner(g.owner, r) && g.lease == nil {
				if g.runCancel != nil {
					g.runCancel()
				}
				if g.releaseDesktop != nil {
					if e := g.releaseDesktop(); e != nil {
						g.desktopFailure = e
					}
					g.releaseDesktop = nil
				}
				g.owner = Request{}
				g.use = nil
				s.publishLocked()
			}
			s.mu.Unlock()
		}
	}()
	select {
	case g.serial <- struct{}{}:
		defer func() { <-g.serial }()
	case <-ctx.Done():
		return BrowserDecision{}, ctx.Err()
	}
	var t ComputerTarget
	var snapshot, category string
	var element map[string]any
	var identity ComputerElementIdentity
	var actionArgs map[string]any
	if r.Tool != "computer_windows" {
		pid, hwnd, e := computerTargetArgs(r.Arguments)
		if e != nil {
			return BrowserDecision{}, e
		}
		t, e = s.inspectComputerTarget(pid, hwnd)
		if e != nil {
			return BrowserDecision{}, e
		}
		if t.Level == "blocked" {
			return BrowserDecision{}, errors.New("COMPUTER_APP_BLOCKED")
		}
		if t.kind == computerAppBrowser {
			return BrowserDecision{}, errors.New("COMPUTER_BROWSER_TOOLS_REQUIRED: Use the browser tools for web pages.")
		}
		if r.Tool != "computer_observe" && t.Level == "view" {
			return BrowserDecision{}, errors.New("COMPUTER_ACCESS_LEVEL: use browser tools for web pages")
		}
		if (r.Tool == "computer_type" || r.Tool == "computer_key") && t.Level == "click" {
			return BrowserDecision{}, errors.New("COMPUTER_ACCESS_LEVEL")
		}
		if e = s.computerConsent(ctx, r, t); e != nil {
			return BrowserDecision{}, e
		}
		if r.Tool != "computer_observe" {
			s.mu.Lock()
			o := g.observation
			s.mu.Unlock()
			if o == nil || o.target != t || time.Since(o.at) > 2*time.Minute {
				return BrowserDecision{}, errors.New("COMPUTER_FRESH_OBSERVATION_REQUIRED")
			}
			snapshot, _ = r.Arguments["snapshot_id"].(string)
			if snapshot == "" || snapshot != o.snapshot {
				return BrowserDecision{}, errors.New("COMPUTER_STALE_OBSERVATION")
			}
			category, e = computerCategory(r, t, o)
			if e != nil {
				return BrowserDecision{}, e
			}
			if r.Tool == "computer_type" || r.Tool == "computer_key" {
				if g.config.Probe == nil {
					return BrowserDecision{}, errors.New("COMPUTER_ELEMENT_VERIFICATION_UNAVAILABLE")
				}
				var source map[string]any
				for _, v := range o.elements {
					if v["element_index"] == r.Arguments["element_index"] {
						source = v
						break
					}
				}
				freshSnapshot, freshElement, e := s.refreshComputerElement(ctx, r, t, source)
				if e != nil {
					return BrowserDecision{}, e
				}
				element = freshElement
				snapshot = freshSnapshot
				identity, e = s.probeComputerElement(ctx, g, t, element)
				if e != nil {
					return BrowserDecision{}, e
				}
				if identity.Password {
					return BrowserDecision{}, errors.New("COMPUTER_SECRET_INPUT_BLOCKED")
				}
				actionArgs = cloneArguments(r.Arguments)
				actionArgs["element_index"] = element["element_index"]
				actionArgs["snapshot_id"] = snapshot
			}

		}
		fresh, e := s.inspectComputerTarget(pid, hwnd)
		if e != nil || fresh != t {
			return BrowserDecision{}, errors.New("COMPUTER_TARGET_CHANGED: observe the target again")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.computerValidLocked(r) || !sameComputerOwner(g.owner, r) {
		return BrowserDecision{}, ErrRevoked
	}
	s.pruneComputerPermitsLocked()
	if len(g.permits) >= 128 {
		return BrowserDecision{}, errors.New("COMPUTER_BUSY")
	}
	permit := rand.Text()
	r.Arguments = cloneArguments(r.Arguments)
	g.permits[permit] = computerPermit{request: r, target: t, snapshot: snapshot, identity: identity, element: element, actionArgs: actionArgs, expires: time.Now().Add(5 * time.Minute)}
	d := BrowserDecision{Kind: "allow", Permit: permit, Epoch: r.Epoch}
	if category != "" && g.policy.Confirm[category] == "block" {
		delete(g.permits, permit)
		return BrowserDecision{}, errors.New("COMPUTER_ACTION_BLOCKED: " + category)
	}
	if category != "" && g.policy.Confirm[category] != "allow" {
		d.Kind = "ask"
		d.Reason = fmt.Sprintf("%s: %s (%s)", category, strings.TrimPrefix(r.Tool, "computer_"), t.App)
		name := ""
		if g.observation != nil {
			for _, el := range g.observation.elements {
				if el["element_index"] == r.Arguments["element_index"] {
					name, _ = el["label"].(string)
					break
				}
			}
		}
		d.DisplayReason = computerDisplayReason(category, name, t.App, r)
	}
	admitted = true
	return d, nil
}
func cloneArguments(v map[string]any) map[string]any {
	b, _ := json.Marshal(v)
	var c map[string]any
	_ = json.Unmarshal(b, &c)
	return c
}
func computerCategory(r Request, t ComputerTarget, o *computerObservation) (string, error) {
	var el map[string]any
	if n, ok := r.Arguments["element_index"].(float64); ok {
		for _, v := range o.elements {
			if v["element_index"] == n {
				el = v
				break
			}
		}
		if el == nil {
			return "", errors.New("COMPUTER_ELEMENT_NOT_FOUND")
		}
	}
	if r.Tool == "computer_type" || r.Tool == "computer_key" {
		if el == nil {
			return "", errors.New("COMPUTER_ELEMENT_REQUIRED")
		}
		password, _ := el["is_password"].(bool)
		name, _ := el["label"].(string)
		if password || secretElement(BrowserElement{Name: name}) {
			return "", errors.New("COMPUTER_SECRET_INPUT_BLOCKED")
		}
	}

	name, _ := el["label"].(string)
	if r.Tool == "computer_type" {
		text, _ := r.Arguments["text"].(string)
		if t.kind == computerAppTerminal && strings.ContainsAny(text, "\n\r") {
			return "command", nil
		}
	}
	if r.Tool == "computer_key" {
		key, _ := r.Arguments["key"].(string)
		key = strings.ToLower(key)
		mods, _ := r.Arguments["modifiers"].([]any)
		ctrl, shift, alt := false, false, false
		for _, v := range mods {
			ctrl = ctrl || v == "ctrl"
			shift = shift || v == "shift"
			alt = alt || v == "alt"
		}
		if ctrl && (key == "c" || key == "v" || key == "x" || key == "insert") || shift && (key == "insert" || key == "delete") || key == "printscreen" {
			return "", errors.New("COMPUTER_CLIPBOARD_UNAVAILABLE")
		}
		if ctrl && alt && key == "delete" || alt && (key == "tab" || key == "escape") || ctrl && key == "escape" || strings.Contains(key, "win") {
			return "", errors.New("COMPUTER_SYSTEM_KEY_UNAVAILABLE")
		}
		if ctrl && key == "s" {
			return "save", nil
		}
		if alt && key == "f4" {
			return "close", nil
		}
		if t.kind == computerAppTerminal && (key == "enter" || key == "return") {
			return "command", nil
		}
	}
	if c := keywordCategory(name, ""); c != "" {
		return c, nil
	}
	lower := strings.ToLower(name)
	if strings.Contains(lower, "save") || strings.Contains(lower, "保存") || strings.Contains(lower, "上書き") {
		return "save", nil
	}
	if r.Tool == "computer_key" {
		key, _ := r.Arguments["key"].(string)
		switch strings.ToLower(key) {
		case "delete", "backspace":
			return "delete", nil
		case "enter", "return":
			return "unknown", nil
		}
		mods, _ := r.Arguments["modifiers"].([]any)
		if len(mods) > 0 {
			return "unknown", nil
		}
	}
	if r.Tool == "computer_type" {
		text, _ := r.Arguments["text"].(string)
		if strings.ContainsAny(text, "\n\r") {
			return "unknown", nil
		}
	}
	if r.Tool == "computer_click" || r.Tool == "computer_scroll" {
		if x, ok := r.Arguments["x"].(float64); ok {
			y, _ := r.Arguments["y"].(float64)
			if o.width <= 0 || o.height <= 0 || x >= o.width || y >= o.height {
				return "", errors.New("COMPUTER_COORDINATES_OUTSIDE_CAPTURE")
			}
		}
		if r.Tool == "computer_click" {
			return "unknown", nil
		}
	}
	return "", nil
}
func (s *Service) DiscardComputerPermit(r Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.computer != nil {
		delete(s.computer.permits, r.Permit)
	}
}
func (s *Service) executeComputerPermit(ctx context.Context, r Request) (any, error) {
	s.mu.Lock()
	g := s.computer
	engine := s.state.ComputerEngine
	if g == nil || !s.computerValidLocked(r) {
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	p, ok := g.permits[r.Permit]
	delete(g.permits, r.Permit)
	s.mu.Unlock()
	expected := r
	expected.Permit = ""
	expected.Operation = "computer"
	original := p.request
	original.Permit = ""
	original.Operation = "computer"
	if !ok || time.Now().After(p.expires) || !reflect.DeepEqual(expected, original) {
		return nil, errors.New("COMPUTER_AUTHORIZATION_REQUIRED")
	}
	select {
	case g.serial <- struct{}{}:
		defer func() { <-g.serial }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if r.Tool == "computer_release" {
		if err := s.ReleaseComputer(r.Session, r.Lifetime); err != nil {
			return nil, err
		}
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": "Computer resources released."}}}, nil
	}
	if r.Tool != "computer_windows" {
		t, e := s.inspectComputerTarget(p.target.PID, p.target.WindowID)
		if e != nil || t != p.target {
			return nil, errors.New("COMPUTER_TARGET_CHANGED: observe the target again")
		}
		s.mu.Lock()
		o := g.observation
		valid := r.Tool == "computer_observe" || o != nil && o.snapshot == p.snapshot && o.target == t
		s.mu.Unlock()
		if !valid {
			return nil, errors.New("COMPUTER_STALE_OBSERVATION")
		}
	}
	if p.identity.RuntimeID != "" {
		fresh, e := s.probeComputerElement(ctx, g, p.target, p.element)
		if e != nil {
			return nil, e
		}
		if fresh.Password {
			return nil, errors.New("COMPUTER_SECRET_INPUT_BLOCKED")
		}
		if fresh.RuntimeID != p.identity.RuntimeID {
			return nil, errors.New("COMPUTER_ELEMENT_CHANGED: observe the target again")
		}
	}
	args := cloneArguments(r.Arguments)
	if p.actionArgs != nil {
		args = cloneArguments(p.actionArgs)
	}
	delete(args, "snapshot_id")
	if r.Tool == "computer_windows" {
		args = map[string]any{"on_screen_only": true}
	} else if r.Tool == "computer_observe" {
		args = map[string]any{"pid": r.Arguments["pid"], "window_id": r.Arguments["window_id"], "max_elements": 500, "max_depth": 20}
	} else {
		if _, element := args["element_index"]; element || engine == "auv" {
			args["snapshot_id"] = p.snapshot
		}
		args["scope"] = "window"
		args["delivery_mode"] = "background"
	}
	s.mu.Lock()
	if time.Now().After(p.expires) || !s.computerValidLocked(r) || !sameComputerOwner(g.owner, r) {
		s.mu.Unlock()
		return nil, ErrRevoked
	}

	if g.use != nil {
		if p.target.PID != 0 {
			g.use.Target = &p.target
		}
		g.use.Running = true
		g.use.Action = r.Tool
		if o := g.observation; o != nil && r.Tool != "computer_observe" && r.Tool != "computer_windows" {
			if n, ok := args["element_index"].(float64); ok {
				for _, el := range o.elements {
					if el["element_index"] == n {
						if f, ok := el["frame"].(map[string]any); ok {
							x, xok := f["x"].(float64)
							y, yok := f["y"].(float64)
							w, wok := f["w"].(float64)
							h, hok := f["h"].(float64)
							if xok && yok && wok && hok {
								g.use.Rect = map[string]any{"x": x + w/2, "y": y + h/2}
							}
						}
					}
				}
			}
			if x, ok := args["x"].(float64); ok {
				if y, ok := args["y"].(float64); ok {
					bx, _ := o.bounds["x"].(float64)
					by, _ := o.bounds["y"].(float64)
					bw, _ := o.bounds["width"].(float64)
					bh, _ := o.bounds["height"].(float64)
					if o.width > 0 && o.height > 0 {
						g.use.Rect = map[string]any{"x": bx + x*bw/o.width, "y": by + y*bh/o.height}
					}
				}
			}
		}
	}

	if r.Tool != "computer_windows" {
		g.observation = nil
	}
	s.publishLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if g.use != nil && sameComputerOwner(g.owner, r) {
			g.use.Running = false
			g.use.Action = ""
			g.use.Rect = nil
		}
		s.publishLocked()
		s.mu.Unlock()
	}()
	raw := r
	raw.Tool = computerRawTool(r.Tool)
	raw.Arguments = args
	value, err := s.executeOwned(ctx, raw)
	if err != nil {
		return nil, err
	}
	content, e := computerContent(value)
	if e != nil {
		if r.Tool == "computer_windows" || r.Tool == "computer_observe" {
			return nil, e
		}
		return value, nil
	}
	if r.Tool == "computer_windows" {
		out := []any{}
		windows, _ := content["windows"].([]any)
		for _, w := range windows {
			m, ok := w.(map[string]any)
			if !ok {
				continue
			}
			pid, hwnd, e := computerTargetArgs(m)
			if e != nil {
				continue
			}
			t, e := s.inspectComputerTarget(pid, hwnd)
			if e == nil && t.Level != "blocked" {
				out = append(out, map[string]any{"pid": t.PID, "window_id": t.WindowID, "app": t.App, "title": t.Title, "level": t.Level})
			}
		}
		return map[string]any{"content": []any{map[string]any{"type": "text", "text": mustJSON(map[string]any{"windows": out})}}, "structuredContent": map[string]any{"windows": out}}, nil
	}
	if r.Tool == "computer_observe" {
		sid, _ := content["snapshot_id"].(string)
		if sid == "" {
			return nil, errors.New("COMPUTER_SNAPSHOT_REQUIRED")
		}
		elements := []map[string]any{}
		list, _ := content["elements"].([]any)
		for _, v := range list {
			if m, ok := v.(map[string]any); ok {
				elements = append(elements, m)
			}
		}
		s.mu.Lock()
		if s.computerValidLocked(r) {
			g.observation = &computerObservation{target: p.target, snapshot: sid, elements: elements, bounds: computerBounds(content), width: computerDimension(content, "screenshot_width"), height: computerDimension(content, "screenshot_height"), at: time.Now()}
		}
		s.mu.Unlock()
		// MCP's human-readable tree omits its structured snapshot credential.
		// Publish the complete next-action contract in model-visible text too.
		m := value.(map[string]any)
		blocks, _ := m["content"].([]any)
		m["content"] = append([]any{map[string]any{"type": "text", "text": computerObservationText(p.target, content, elements)}}, blocks...)
	} else {
		s.mu.Lock()
		g.observation = nil
		s.mu.Unlock()
	}
	return value, nil
}
func computerObservationText(target ComputerTarget, content map[string]any, elements []map[string]any) string {
	controls := make([]map[string]any, 0, len(elements))
	for _, e := range elements {
		controls = append(controls, map[string]any{"element_index": e["element_index"], "role": e["role"], "name": e["label"], "actions": e["actions"], "enabled": e["enabled"]})
	}
	return "computer_observation " + mustJSON(map[string]any{
		"pid": target.PID, "window_id": target.WindowID, "snapshot_id": content["snapshot_id"],
		"screenshot_width": content["screenshot_width"], "screenshot_height": content["screenshot_height"],
		"click_coordinates": "window-local screenshot pixels; origin top-left; x < screenshot_width, y < screenshot_height; use element_index for listed controls",
		"elements_complete": content["elements_complete"], "elements": controls,
	})
}
func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
func validateComputerArguments(r Request, engine ...string) error {
	if !computerAllowed(r.Tool) || r.Arguments == nil {
		return errors.New("COMPUTER_INVALID_ARGUMENT")
	}
	var schema struct {
		Input struct {
			Properties map[string]struct {
				Type      string   `json:"type"`
				Enum      []string `json:"enum"`
				Minimum   *float64 `json:"minimum"`
				Maximum   *float64 `json:"maximum"`
				MaxLength int      `json:"maxLength"`
			} `json:"properties"`
			Required []string `json:"required"`
		} `json:"inputSchema"`
		Name string `json:"name"`
	}
	for _, b := range ComputerToolSchemas(engine...) {
		schema.Input.Properties = nil
		schema.Input.Required = nil
		_ = json.Unmarshal(b, &schema)
		if schema.Name == r.Tool {
			break
		}
	}
	if schema.Name != r.Tool {
		return errors.New("COMPUTER_ACTION_UNSUPPORTED: selected engine has no background text/key or indexed UIA input")
	}
	for _, k := range schema.Input.Required {
		if _, ok := r.Arguments[k]; !ok {
			return errors.New("COMPUTER_INVALID_ARGUMENT: missing " + k)
		}
	}
	for k, v := range r.Arguments {
		p, ok := schema.Input.Properties[k]
		if !ok {
			return errors.New("COMPUTER_INVALID_ARGUMENT: " + k)
		}
		switch p.Type {
		case "string":
			text, ok := v.(string)
			if !ok || p.MaxLength > 0 && len([]rune(text)) > p.MaxLength {
				return errors.New("COMPUTER_INVALID_ARGUMENT: " + k)
			}
		case "array":
			items, ok := v.([]any)
			if !ok || k != "modifiers" || len(items) > 3 {
				return errors.New("COMPUTER_INVALID_ARGUMENT: " + k)
			}
			seen := map[string]bool{}
			for _, item := range items {
				text, ok := item.(string)
				if !ok || seen[text] || text != "ctrl" && text != "shift" && text != "alt" {
					return errors.New("COMPUTER_INVALID_ARGUMENT: " + k)
				}
				seen[text] = true
			}
		case "integer", "number":
			n, ok := v.(float64)
			if !ok || n != n || p.Type == "integer" && n != float64(int64(n)) || p.Minimum != nil && n < *p.Minimum || p.Maximum != nil && n > *p.Maximum {
				return errors.New("COMPUTER_INVALID_ARGUMENT: " + k)
			}
		}
		if len(p.Enum) > 0 {
			match := false
			for _, e := range p.Enum {
				if e == v {
					match = true
				}
			}
			if !match {
				return errors.New("COMPUTER_INVALID_ARGUMENT: " + k)
			}
		}
	}
	if r.Tool == "computer_click" {
		_, element := r.Arguments["element_index"]
		_, x := r.Arguments["x"]
		_, y := r.Arguments["y"]
		if element && (x || y) || !element && (!x || !y) {
			return errors.New("COMPUTER_INVALID_ARGUMENT: choose one element or coordinate pair")
		}
	}
	if r.Tool == "computer_scroll" {
		_, x := r.Arguments["x"]
		_, y := r.Arguments["y"]
		if x != y {
			return errors.New("COMPUTER_INVALID_ARGUMENT: scroll requires both x and y")
		}
	}
	return nil
}

// Refresh the driver-side handle before approval. Native RuntimeId is an
// independent guard; Cua does not expose it, so this is not an atomic identity
// transaction across UIA clients. A changed or ambiguous match fails closed.
func (s *Service) refreshComputerElement(ctx context.Context, r Request, t ComputerTarget, source map[string]any) (string, map[string]any, error) {
	if source == nil {
		return "", nil, errors.New("COMPUTER_ELEMENT_NOT_FOUND")
	}
	raw := r
	raw.Tool = "get_window_state"
	raw.Arguments = map[string]any{"pid": float64(t.PID), "window_id": float64(t.WindowID), "include_screenshot": false, "max_elements": 5000, "max_depth": 25}
	value, e := s.executeOwned(ctx, raw)
	if e != nil {
		return "", nil, e
	}
	content, e := computerContent(value)
	if e != nil {
		return "", nil, e
	}
	snapshot, _ := content["snapshot_id"].(string)
	list, _ := content["elements"].([]any)
	elements := []map[string]any{}
	var selected map[string]any
	matches := 0
	for _, v := range list {
		el, ok := v.(map[string]any)
		if !ok {
			continue
		}
		elements = append(elements, el)
		same := true
		for _, key := range []string{"role", "label", "frame", "automation_id", "enabled"} {
			if !reflect.DeepEqual(source[key], el[key]) {
				same = false
			}
		}
		if same {
			matches++
			selected = el
		}
	}
	if snapshot == "" || matches != 1 {
		return "", nil, errors.New("COMPUTER_ELEMENT_CHANGED: observe the target again")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.computerValidLocked(r) {
		return "", nil, ErrRevoked
	}
	s.computer.observation = &computerObservation{target: t, snapshot: snapshot, elements: elements, bounds: computerBounds(content), width: computerDimension(content, "screenshot_width"), height: computerDimension(content, "screenshot_height"), at: time.Now()}
	return snapshot, selected, nil
}

func (s *Service) inspectComputerTarget(pid uint32, hwnd uint64) (ComputerTarget, error) {
	s.mu.Lock()
	g := s.computer
	s.mu.Unlock()
	if g == nil {
		return ComputerTarget{}, ErrUnavailable
	}
	t, e := g.config.Inspect(pid, hwnd)
	if e != nil {
		return t, e
	}
	if t.Identity == "" {
		t.Identity = "path:" + strings.ToLower(t.Executable)
	}
	if t.Level == "blocked" {
		return t, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rule := range g.policy.Apps {
		if rule.ID == t.Identity {
			if t.Level != "view" || rule.Level == "blocked" {
				t.Level = rule.Level
			}
			break
		}
	}
	return t, nil
}
func (s *Service) SetComputerPolicy(ctx context.Context, revision uint64, p settings.ComputerPolicy) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.computer == nil || s.computer.policyRevision != revision || !s.state.CanChange {
		s.mu.Unlock()
		return s.Snapshot(), ErrRevoked
	}
	s.mu.Unlock()
	if s.manager == nil {
		return s.Snapshot(), ErrUnavailable
	}
	if _, e := s.manager.SetComputerPolicy(ctx, p); e != nil {
		return s.Snapshot(), e
	}
	s.mu.Lock()
	s.computer.policy = settings.NormalizeComputerPolicy(&p)
	s.computer.policyRevision++
	old := s.revokeLocked(Computer, "")
	s.slots[Computer].state.Epoch++
	s.slots[AUV].state.Epoch++
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	return s.Snapshot(), nil
}

func (s *Service) ComputerTurn(r Request) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.computer
	if g == nil || r.Generation != s.state.Generation || r.Session == "" || r.Lifetime == "" || r.Turn == "" {
		return nil, ErrRevoked
	}
	c := g.sessions[r.Session]
	if c != nil && c.turn == r.Turn {
		return map[string]string{"lifetime": c.life, "turn": c.turn}, nil
	}
	if c != nil && c.stopped && !r.UserTurn {
		return nil, ErrRevoked
	}
	life := r.Lifetime
	if c != nil && (c.stopped || c.life != life) {
		life = rand.Text()
	}
	g.sessions[r.Session] = &computerConversation{life: life, turn: r.Turn}
	if g.owner.Session == r.Session && g.owner.Lifetime == r.Lifetime {
		g.observation = nil
	}
	for id, p := range g.permits {
		if p.request.Session == r.Session {
			delete(g.permits, id)
		}
	}
	for id, p := range g.pending {
		if p.view.Session == r.Session {
			select {
			case p.choice <- false:
			default:
			}
			delete(g.pending, id)
		}
	}
	s.publishLocked()
	return map[string]string{"lifetime": life, "turn": r.Turn}, nil
}
func (s *Service) StopComputerUse(session, lifetime string) error {
	s.mu.Lock()
	g := s.computer
	if g == nil {
		s.mu.Unlock()
		return ErrUnavailable
	}
	if c := g.sessions[session]; c != nil && c.life == lifetime {
		c.stopped = true
	}
	for _, c := range []Capability{Computer, AUV} {
		s.stopped[revocationKey(Request{Capability: c, Epoch: s.slots[c].state.Epoch, Session: session, Lifetime: lifetime})] = true
	}
	old := s.revokeLocked(Computer, session+"/"+lifetime)
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	s.mu.Lock()
	failure := g.desktopFailure
	if g.lease != nil {
		select {
		case <-g.lease.closed:
			if g.lease.closeErr != nil {
				failure = g.lease.closeErr
			}
		default:
		}
	}
	if failure != nil && g.use != nil {
		g.use.Stopping = false
		s.publishLocked()
	}
	s.mu.Unlock()
	if failure != nil {
		return errors.New("COMPUTER_CLEANUP_FAILED")
	}
	return nil
}

func computerBounds(content map[string]any) map[string]any {
	b, _ := content["window_bounds"].(map[string]any)
	return b
}
func computerDisplayReason(category, name, app string, r Request) map[string]string {
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40]) + "…"
	}
	if name == "" {
		name = "Click"
	}
	if category != "command" && category != "close" && category != "save" {
		return browserDisplayReason(category, name, app, "click")
	}
	labels := map[string][3]string{"command": {"运行命令", "Run command", "コマンドを実行"}, "close": {"关闭应用", "Close application", "アプリを閉じる"}, "save": {"保存文件", "Save file", "ファイルを保存"}}
	label := labels[category]
	action := name
	if r.Tool == "computer_key" {
		key, _ := r.Arguments["key"].(string)
		mods, _ := r.Arguments["modifiers"].([]any)
		parts := []string{}
		for _, v := range mods {
			if text, ok := v.(string); ok {
				parts = append(parts, text)
			}
		}
		parts = append(parts, key)
		action = strings.Join(parts, "+")
	}
	if category == "command" && r.Tool == "computer_type" {
		action, _ = r.Arguments["text"].(string)
		action = strings.ReplaceAll(strings.ReplaceAll(action, "\r", " "), "\n", " ")
		if len([]rune(action)) > 60 {
			action = string([]rune(action)[:60]) + "…"
		}
	}
	if category == "close" {
		action = app
		app = ""
	}
	suffix := ""
	if app != "" {
		suffix = " (" + app + ")"
	}
	return map[string]string{"zh-CN": label[0] + "：" + action + suffix, "en": label[1] + ": " + action + suffix, "ja-JP": label[2] + "：" + action + suffix}
}

func computerDimension(content map[string]any, key string) float64 {
	n, _ := content[key].(float64)
	return n
}
func (s *Service) pruneComputerPermitsLocked() {
	if s.computer != nil {
		for id, p := range s.computer.permits {
			if time.Now().After(p.expires) {
				delete(s.computer.permits, id)
			}
		}
	}
}

func (s *Service) ReleaseComputer(session, lifetime string) error {
	s.mu.Lock()
	g := s.computer
	if g == nil {
		s.mu.Unlock()
		return ErrUnavailable
	}
	if g.owner.Session == "" {
		s.mu.Unlock()
		return nil
	}
	if g.owner.Session != session || g.owner.Lifetime != lifetime {
		s.mu.Unlock()
		return ErrRevoked
	}
	old := s.revokeLocked(Computer, session+"/"+lifetime)
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	for _, entry := range old {
		if entry.closeErr != nil {
			return errors.New("COMPUTER_CLEANUP_FAILED")
		}
	}
	return nil
}
func (s *Service) probeComputerElement(ctx context.Context, g *computerGateway, target ComputerTarget, element map[string]any) (ComputerElementIdentity, error) {
	s.mu.Lock()
	if g.runCtx == nil || g.runCtx.Err() != nil {
		s.mu.Unlock()
		return ComputerElementIdentity{}, ErrRevoked
	}
	root := g.runCtx
	g.probes.Add(1)
	s.mu.Unlock()
	defer g.probes.Done()
	run, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(root, cancel)
	defer stop()
	defer cancel()
	identity, err := g.config.Probe(run, target, element)
	if err != nil && strings.Contains(err.Error(), "COMPUTER_HELPER_CLEANUP_FAILED") {
		s.mu.Lock()
		g.desktopFailure = err
		s.mu.Unlock()
	}
	return identity, err
}
func (s *Service) finishComputerEntries(entries []*entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.computer
	if g == nil || g.lease == nil {
		return
	}
	for _, entry := range entries {
		if g.lease == entry {
			if entry.closeErr == nil {
				g.use = nil
			} else {
				g.desktopFailure = entry.closeErr
				if g.use != nil {
					g.use.Stopping = false
				}
			}
			s.publishLocked()
			return
		}
	}
}

// Both Host permission types use the same two-minute decision window. Pick
// their oldest pending decision, while keeping an in-progress browser connection.
func FirstAutomationPermission(v Snapshot) (*BrowserConsent, *ComputerConsent) {
	var browser *BrowserConsent
	var computer *ComputerConsent
	if v.BrowserAccess != nil && len(v.BrowserAccess.Requests) > 0 {
		r := v.BrowserAccess.Requests[0]
		browser = &r
	}
	if v.ComputerAccess != nil {
		for _, r := range v.ComputerAccess.Requests {
			if computer == nil || r.Expires.Before(computer.Expires) {
				copy := r
				computer = &copy
			}
		}
	}
	if browser != nil && (computer == nil || browser.Phase == "connecting" || !computer.Expires.Before(browser.Expires)) {
		return browser, nil
	}
	return nil, computer
}
