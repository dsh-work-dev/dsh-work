// Package automation owns capability intent, preparation and execution lifetimes.
// Adapters carry out work; neither the UI nor the DSH plugin decides availability.
package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/logging"
	"github.com/local/dsh-work/internal/settings"
)

type Capability string

const (
	Browser  Capability = "browser"
	Computer Capability = "computer"
)

func (c Capability) Valid() bool { return c == Browser || c == Computer }

type State struct {
	Enabled       bool   `json:"enabled"`
	Dependency    string `json:"dependency"` // unprepared, preparing, prepared, failed
	Step          string `json:"step,omitempty"`
	Failure       string `json:"failure,omitempty"`
	Availability  string `json:"availability"` // disabled, preparing, unavailable
	Epoch         uint64 `json:"epoch"`
	ReceivedBytes int64  `json:"receivedBytes"`
	TotalBytes    int64  `json:"totalBytes"`
}
type Snapshot struct {
	GatewayConnected      bool   `json:"gatewayConnected"`
	DefaultBrowserEnabled bool   `json:"defaultBrowserEnabled"`
	Revision              uint64 `json:"revision"`
	Generation            string `json:"generation"`
	AvailableProfile      bool   `json:"availableProfile"`
	CanChange             bool   `json:"canChange"`
	Browser               State  `json:"browser"`
	Computer              State  `json:"computer"`
}
type Progress struct {
	Step                      string
	ReceivedBytes, TotalBytes int64
}
type Preparer interface {
	Prepare(context.Context, Capability, func(Progress)) error
}
type Resource interface {
	Execute(context.Context, string, map[string]any) (any, error)
	Close() error
}
type Factory func(context.Context, Request) (Resource, error)
type Request struct {
	Generation string         `json:"generation"`
	Epoch      uint64         `json:"epoch"`
	Capability Capability     `json:"capability"`
	Session    string         `json:"session"`
	Lifetime   string         `json:"lifetime"`
	Operation  string         `json:"operation"`
	Tool       string         `json:"tool"`
	Arguments  map[string]any `json:"arguments"`
}
type entry struct {
	call     sync.Mutex
	ctx      context.Context
	ready    chan struct{}
	cancel   context.CancelFunc
	resource Resource
	err      error
	revoked  bool
	once     sync.Once
	closed   chan struct{}
}
type slot struct {
	lastProgress time.Time
	preparation  uint64
	state        State
	cancel       context.CancelFunc
	done         chan struct{}
}
type Service struct {
	op      sync.Mutex
	mu      sync.Mutex
	manager *settings.Manager
	prepare Preparer
	root    context.Context
	cancel  context.CancelFunc
	state   Snapshot
	slots   map[Capability]*slot
	entries map[string]*entry
	stopped map[string]bool
	factory Factory // nil in product: real execution is not integrated yet
	changed func(Snapshot)
}

var ErrRevoked = errors.New("AUTOMATION_REVOKED")
var ErrUnavailable = errors.New("AUTOMATION_BACKEND_UNAVAILABLE")

func New(manager *settings.Manager, prepare Preparer) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{manager: manager, prepare: prepare, root: ctx, cancel: cancel, slots: map[Capability]*slot{}, entries: map[string]*entry{}, stopped: map[string]bool{}}
	values := settings.DefaultValues()
	if manager != nil {
		if v, e := manager.Snapshot(ctx); e == nil {
			values = v
		}
	}
	s.state.DefaultBrowserEnabled = values.Automation.Browser.Default.Enabled
	for _, c := range []Capability{Browser, Computer} {
		enabled := values.Automation.Browser.Enabled
		if c == Computer {
			enabled = values.Automation.Computer.Enabled
		}
		s.slots[c] = &slot{state: State{Enabled: enabled, Dependency: "unprepared", Availability: "disabled", Epoch: 1}}
	}
	return s
}

// SetValidationFactory is an in-process validation seam, never bound to the UI
// or enabled by environment variables in the product daemon.
func (s *Service) SetValidationFactory(f Factory) { s.mu.Lock(); defer s.mu.Unlock(); s.factory = f }
func (s *Service) OnChange(fn func(Snapshot))     { s.mu.Lock(); defer s.mu.Unlock(); s.changed = fn }
func (s *Service) snapshotLocked() Snapshot {
	v := s.state
	v.Browser = s.slots[Browser].state
	v.Computer = s.slots[Computer].state
	return v
}
func (s *Service) Snapshot() Snapshot { s.mu.Lock(); defer s.mu.Unlock(); return s.snapshotLocked() }

// Publisher must not re-enter Service. It receives a value, not mutable state.
func (s *Service) publishLocked() {
	s.state.Revision++
	if s.changed != nil {
		s.changed(s.snapshotLocked())
	}
}
func (s *Service) SetEnabled(ctx context.Context, c Capability, enabled bool) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	if !c.Valid() {
		return s.Snapshot(), errors.New("AUTOMATION_UNKNOWN_CAPABILITY")
	}
	s.mu.Lock()
	allowed := s.state.CanChange
	same := s.slots[c].state.Enabled == enabled
	s.mu.Unlock()
	if !allowed {
		return s.Snapshot(), errors.New("AUTOMATION_NOT_READY")
	}
	if same {
		return s.Snapshot(), nil
	}
	if s.manager == nil {
		return s.Snapshot(), errors.New("AUTOMATION_SETTINGS_UNAVAILABLE")
	}
	if _, err := s.manager.SetAutomationEnabled(ctx, string(c), enabled); err != nil {
		return s.Snapshot(), err
	}
	s.mu.Lock()
	slot := s.slots[c]
	slot.state.Enabled = enabled
	slot.state.Epoch++
	if slot.cancel != nil {
		slot.cancel()
	}
	if !enabled {
		slot.state.Availability = "disabled"
		slot.state.Step = ""
		if slot.state.Dependency == "preparing" {
			slot.state.Dependency = "unprepared"
		}
	}
	old := s.revokeLocked(c, "")
	if enabled {
		s.startLocked(c)
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	return s.Snapshot(), nil
}

// SetDefaultBrowser changes the only browser path in this stage; the browser
// master intent stays intact while this path is disabled.
func (s *Service) SetDefaultBrowser(ctx context.Context, enabled bool) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if !s.state.CanChange || !s.slots[Browser].state.Enabled {
		s.mu.Unlock()
		return s.Snapshot(), ErrRevoked
	}
	if s.state.DefaultBrowserEnabled == enabled {
		s.mu.Unlock()
		return s.Snapshot(), nil
	}
	s.mu.Unlock()
	if s.manager == nil {
		return s.Snapshot(), errors.New("AUTOMATION_SETTINGS_UNAVAILABLE")
	}
	if _, err := s.manager.SetDefaultBrowserEnabled(ctx, enabled); err != nil {
		return s.Snapshot(), err
	}
	s.mu.Lock()
	s.state.DefaultBrowserEnabled = enabled
	slot := s.slots[Browser]
	slot.state.Epoch++
	if slot.cancel != nil {
		slot.cancel()
	}
	if slot.state.Dependency == "preparing" {
		slot.state.Dependency = "unprepared"
	}
	slot.state.Step = ""
	slot.state.Availability = "disabled"
	old := s.revokeLocked(Browser, "")
	if enabled {
		s.startLocked(Browser)
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	return s.Snapshot(), nil
}
func (s *Service) Retry(c Capability) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !c.Valid() {
		return s.snapshotLocked(), errors.New("AUTOMATION_UNKNOWN_CAPABILITY")
	}
	slot := s.slots[c]
	if !s.state.CanChange || !slot.state.Enabled {
		return s.snapshotLocked(), ErrRevoked
	}
	if slot.state.Dependency == "failed" {
		s.startLocked(c)
		s.publishLocked()
	}
	return s.snapshotLocked(), nil
}
func (s *Service) startLocked(c Capability) {
	slot := s.slots[c]
	if !slot.state.Enabled || !s.state.CanChange || slot.state.Dependency == "preparing" {
		return
	}
	if c == Browser && !s.state.DefaultBrowserEnabled {
		slot.state.Availability = "disabled"
		return
	}
	slot.state.Availability = "unavailable"
	if slot.state.Dependency == "prepared" {
		return
	}
	// A cancelled preparer must finish before another one uses its staging tree.
	previous := slot.done
	ctx, cancel := context.WithCancel(s.root)
	slot.cancel = cancel
	slot.preparation++
	preparation := slot.preparation
	slot.state.Dependency = "preparing"
	slot.state.Availability = "preparing"
	slot.state.Step = "download"
	slot.state.Failure = ""
	slot.state.ReceivedBytes = 0
	slot.state.TotalBytes = 0
	done := make(chan struct{})
	slot.done = done
	go func() {
		defer close(done)
		if previous != nil {
			<-previous
			if ctx.Err() != nil {
				return
			}
		}
		var err error
		if s.prepare == nil {
			err = ErrUnavailable
		} else {
			err = s.prepare.Prepare(ctx, c, func(p Progress) {
				s.mu.Lock()
				defer s.mu.Unlock()
				if slot.preparation != preparation || ctx.Err() != nil {
					return
				}
				stepChanged := slot.state.Step != p.Step
				slot.state.Step = p.Step
				slot.state.ReceivedBytes = p.ReceivedBytes
				slot.state.TotalBytes = p.TotalBytes
				now := time.Now()
				if stepChanged || now.Sub(slot.lastProgress) >= 100*time.Millisecond || p.TotalBytes > 0 && p.ReceivedBytes == p.TotalBytes {
					slot.lastProgress = now
					s.publishLocked()
				}
			})
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if slot.preparation != preparation || ctx.Err() != nil {
			return
		}
		slot.cancel = nil
		slot.state.Step = ""
		slot.state.Availability = "unavailable"
		if err != nil {
			slot.state.Dependency = "failed"
			slot.state.Failure = FailureCode(err)
			detail := err
			for errors.Unwrap(detail) != nil {
				detail = errors.Unwrap(detail)
			}
			logging.Module(logging.ModSettings).Error("capability preparation failed", "code", "AUTOMATION_"+strings.ToUpper(string(c))+"_"+strings.ToUpper(slot.state.Failure), "err", detail)
		} else {
			slot.state.Dependency = "prepared"
			slot.state.Failure = ""
		}
		s.publishLocked()
	}()
}
func FailureCode(err error) string {
	var f interface{ AutomationFailure() string }
	if errors.As(err, &f) {
		return f.AutomationFailure()
	}
	return "other"
}

// Worker resets runtime identities, never intent or reusable dependency assets.
// A profile switch or hard exit revokes all prior execution references.
func (s *Service) Worker(generation string, ready, profile bool) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if s.state.Generation == generation && s.state.CanChange == (ready && profile) && s.state.AvailableProfile == profile {
		s.mu.Unlock()
		return
	}
	if s.state.Generation != generation || !ready {
		s.state.GatewayConnected = false
	}
	s.state.Generation = generation
	s.state.CanChange = ready && profile
	s.state.AvailableProfile = profile
	old := s.revokeLocked("", "")
	s.stopped = map[string]bool{}
	for _, c := range []Capability{Browser, Computer} {
		slot := s.slots[c]
		slot.state.Epoch++
		if slot.cancel != nil {
			slot.cancel()
		}
		if slot.state.Dependency == "preparing" {
			slot.state.Dependency = "unprepared"
		}
		slot.state.Availability = "disabled"
		slot.state.Step = ""
		if s.state.CanChange {
			s.startLocked(c)
		}
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
}

// Gateway is connection health, not Worker readiness or user intent. Losing
// this channel revokes execution while leaving settings and preparation usable.
func (s *Service) Gateway(generation string, connected bool) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	if generation != s.state.Generation || s.state.GatewayConnected == connected {
		s.mu.Unlock()
		return
	}
	s.state.GatewayConnected = connected
	var old []*entry
	if !connected {
		old = s.revokeLocked("", "")
		for _, slot := range s.slots {
			slot.state.Epoch++
		}
	}
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
}
func (s *Service) validLocked(r Request) bool {
	v := s.slots[r.Capability]
	return v != nil && (r.Capability != Browser || s.state.DefaultBrowserEnabled) && s.state.CanChange && s.state.GatewayConnected && s.state.Generation == r.Generation && v.state.Enabled && v.state.Epoch == r.Epoch && v.state.Dependency == "prepared" && r.Session != "" && r.Lifetime != "" && !s.stopped[revocationKey(r)]
}
func revocationKey(r Request) string {
	return fmt.Sprintf("%s/%d/%s/%s", r.Capability, r.Epoch, r.Session, r.Lifetime)
}
func key(r Request) string { return string(r.Capability) + "/" + r.Session + "/" + r.Lifetime }
func (s *Service) Execute(ctx context.Context, r Request) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if !s.validLocked(r) {
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	if s.factory == nil || r.Operation != "probe" {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	e := s.entries[key(r)]
	if e == nil {
		create, cancel := context.WithCancel(s.root)
		e = &entry{ctx: create, ready: make(chan struct{}), closed: make(chan struct{}), cancel: cancel}
		s.entries[key(r)] = e
		f := s.factory
		go func() {
			resource, err := f(create, r)
			s.mu.Lock()
			e.resource, e.err = resource, err
			close(e.ready)
			s.mu.Unlock()
			if err != nil {
				s.closeEntry(e)
			}
		}()
	}
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.ready:
	}
	e.call.Lock()
	defer e.call.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	valid := s.validLocked(r) && !e.revoked
	resource, err := e.resource, e.err
	s.mu.Unlock()
	if !valid {
		return nil, ErrRevoked
	}
	if err != nil {
		return nil, err
	}
	if resource == nil {
		return nil, ErrUnavailable
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	defer cancel()
	value, err := resource.Execute(callCtx, r.Tool, r.Arguments)
	s.mu.Lock()
	valid = s.validLocked(r) && !e.revoked
	s.mu.Unlock()
	if !valid {
		return nil, ErrRevoked
	}
	return value, err
}
func (s *Service) StopSession(generation, session, lifetime string) {
	s.mu.Lock()
	if generation != s.state.Generation {
		s.mu.Unlock()
		return
	}
	for _, c := range []Capability{Browser, Computer} {
		s.stopped[revocationKey(Request{Capability: c, Epoch: s.slots[c].state.Epoch, Session: session, Lifetime: lifetime})] = true
	}
	old := s.revokeLocked("", session+"/"+lifetime)
	s.mu.Unlock()
	s.release(old)
}
func (s *Service) revokeLocked(c Capability, session string) []*entry {
	var old []*entry
	for k, e := range s.entries {
		if (c == "" || strings.HasPrefix(k, string(c)+"/")) && (session == "" || strings.HasSuffix(k, "/"+session)) {
			e.revoked = true
			e.cancel()
			old = append(old, e)
			delete(s.entries, k)
		}
	}
	return old
}
func (s *Service) closeEntry(e *entry) {
	e.once.Do(func() {
		defer close(e.closed)
		<-e.ready
		if e.resource != nil {
			if err := e.resource.Close(); err != nil {
				logging.Module(logging.ModSettings).Warn("automation resource release failed", "err", err)
			}
		}
	})
	<-e.closed
}
func (s *Service) release(entries []*entry) {
	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func() { defer wg.Done(); s.closeEntry(e) }()
	}
	wg.Wait()
}
func (s *Service) Close() {
	s.cancel()
	s.Worker("", false, false)
	s.mu.Lock()
	var done []chan struct{}
	for _, v := range s.slots {
		if v.done != nil {
			done = append(done, v.done)
		}
	}
	s.mu.Unlock()
	for _, d := range done {
		<-d
	}
}
