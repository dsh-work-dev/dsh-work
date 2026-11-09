// Package automation owns capability intent, preparation and execution lifetimes.
// Adapters carry out work; neither the UI nor the DSH plugin decides availability.
package automation

import (
	"context"
	"encoding/json"
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
	AUV      Capability = "computer-auv"
)

func (c Capability) Valid() bool { return c == Browser || c == Computer || c == AUV }

type State struct {
	RepairFailure string `json:"repairFailure,omitempty"`
	Enabled       bool   `json:"enabled"`
	Dependency    string `json:"dependency"` // unprepared, preparing, prepared, failed
	Step          string `json:"step,omitempty"`
	Failure       string `json:"failure,omitempty"`
	Availability  string `json:"availability"` // disabled, preparing, unavailable, available
	Epoch         uint64 `json:"epoch"`
	ReceivedBytes int64  `json:"receivedBytes"`
	TotalBytes    int64  `json:"totalBytes"`
}
type Snapshot struct {
	Components            []Component             `json:"components"`
	ComputerEngine        string                  `json:"computerEngine"`
	GatewayConnected      bool                    `json:"gatewayConnected"`
	DefaultBrowserEnabled bool                    `json:"defaultBrowserEnabled"`
	Revision              uint64                  `json:"revision"`
	Generation            string                  `json:"generation"`
	AvailableProfile      bool                    `json:"availableProfile"`
	CanChange             bool                    `json:"canChange"`
	Browser               State                   `json:"browser"`
	Computer              State                   `json:"computer"`
	Profiles              BrowserProfilesSnapshot `json:"profiles"`
	BrowserAccess         *BrowserGatewaySnapshot `json:"browserAccess,omitempty"`
	ComputerAccess        *ComputerSnapshot       `json:"computerAccess,omitempty"`
	ComputerTools         []json.RawMessage       `json:"computerTools,omitempty"`
	BrowserTools          []json.RawMessage       `json:"browserTools,omitempty"`
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
	Generation       string         `json:"generation"`
	Epoch            uint64         `json:"epoch"`
	Capability       Capability     `json:"capability"`
	Session          string         `json:"session"`
	Lifetime         string         `json:"lifetime"`
	Operation        string         `json:"operation"`
	Tool             string         `json:"tool"`
	Arguments        map[string]any `json:"arguments"`
	Title            string         `json:"title,omitempty"`
	WorkingDirectory string         `json:"workingDirectory,omitempty"`
	Turn             string         `json:"turn,omitempty"`
	Permit           string         `json:"permit,omitempty"`
	PreviousLifetime string         `json:"previousLifetime,omitempty"`
	UserTurn         bool           `json:"userTurn,omitempty"`
}
type entry struct {
	call                   sync.Mutex
	ctx                    context.Context
	ready                  chan struct{}
	cancel                 context.CancelFunc
	resource               Resource
	err                    error
	revoked                bool
	once                   sync.Once
	closed                 chan struct{}
	waiters                int
	initializationCanceled bool
	closeErr               error
	desktopRelease         func() error
	beforeDesktopRelease   func() error
}
type slot struct {
	repair       bool
	lastProgress time.Time
	preparation  uint64
	state        State
	cancel       context.CancelFunc
	done         chan struct{}
}
type Service struct {
	op                 sync.Mutex
	mu                 sync.Mutex
	manager            *settings.Manager
	prepare            Preparer
	root               context.Context
	cancel             context.CancelFunc
	state              Snapshot
	slots              map[Capability]*slot
	entries            map[string]*entry
	stopped            map[string]bool
	factory            Factory // validation probes only; browser execution uses its own backend
	changed            func(Snapshot)
	profiles           profileState
	browserFactory     Factory
	browserSchemas     []json.RawMessage
	computer           *computerGateway
	gate               *browserGateway
	development        bool
	browserMaintenance bool
	componentInspector func() []Component
	componentChecks    map[string]componentCheck
	lastComponentCheck time.Time
	checkMu            sync.Mutex
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
	s.state.ComputerEngine = values.Automation.Computer.Engine
	if s.state.ComputerEngine != "auv" {
		s.state.ComputerEngine = "cua-driver"
	}
	s.state.DefaultBrowserEnabled = values.Automation.Browser.Default.Enabled
	s.profiles = newProfileState(values.BrowserProfiles)
	for _, c := range []Capability{Browser, Computer, AUV} {
		enabled := values.Automation.Browser.Enabled
		if c != Browser {
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
	if s.state.ComputerEngine == "auv" {
		v.Computer = s.slots[AUV].state
		v.Computer.Enabled = s.slots[Computer].state.Enabled
	}
	if !v.Computer.Enabled {
		v.Computer.Availability = "disabled"
	} else if v.Computer.Dependency != "preparing" {
		v.Computer.Availability = "unavailable"
	}
	v.ComputerAccess = s.computerSnapshotLocked()
	if s.computer != nil && s.computer.config.Factory != nil && v.ComputerAccess.Failure == "" && s.state.CanChange && s.state.GatewayConnected && v.Computer.Enabled && v.Computer.Dependency == "prepared" {
		v.Computer.Availability = "available"
		v.ComputerTools = ComputerToolSchemas(s.state.ComputerEngine)
	}
	v.Components = s.componentsLocked()
	v.Profiles = s.profileSnapshotLocked()
	v.BrowserAccess = s.browserGatewaySnapshotLocked()
	if !s.browserMaintenance && s.gate != nil && s.browserFactory != nil && s.state.CanChange && s.state.GatewayConnected && v.Browser.Enabled && v.Browser.Dependency == "prepared" && s.state.DefaultBrowserEnabled {
		if id, e := s.selectedConnectionLocked(); e == nil && (s.gate.config.ExtensionSource == "" || s.profiles.connections[id].hello.Source == s.gate.config.ExtensionSource) {
			v.Browser.Availability = "available"
		}
	}

	if s.development || s.gate != nil {
		for _, schema := range s.browserSchemas {
			v.BrowserTools = append(v.BrowserTools, append(json.RawMessage(nil), schema...))
		}
	}
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
	if c != Browser && c != Computer {
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
	if c == Computer {
		s.slots[AUV].state.Enabled = enabled
		s.slots[AUV].state.Epoch++
	}
	if !enabled {
		if c == Browser {
			s.cancelBrowserOpeningLocked()
			if s.gate != nil {
				for _, intent := range s.gate.ownedIntents {
					intent.invalid = true
				}
			}
		}
		slot.state.Availability = "disabled"
		slot.state.Step = ""
	}
	old := s.revokeLocked(c, "")
	if enabled {
		s.probeLocked(c)
		if c == Computer {
			s.probeLocked(AUV)
		}
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
	if !enabled {
		s.cancelBrowserOpeningLocked()
		if s.gate != nil {
			for _, intent := range s.gate.ownedIntents {
				intent.invalid = true
			}
		}
	}
	slot := s.slots[Browser]
	slot.state.Epoch++
	if slot.state.Dependency != "preparing" {
		slot.state.Step = ""
	}
	slot.state.Availability = "disabled"
	old := s.revokeLocked(Browser, "")
	if enabled {
		s.probeLocked(Browser)
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
	if !s.state.CanChange {
		return s.snapshotLocked(), ErrRevoked
	}
	if s.componentWorkLocked(c) {
		return s.snapshotLocked(), errors.New("AUTOMATION_UPDATE_BUSY")
	}
	if slot.state.Dependency != "preparing" {
		s.probeLocked(c)
		s.startLocked(c)
		s.publishLocked()
	}
	return s.snapshotLocked(), nil
}

// Repair stages a replacement without deleting any published set. Active
// browser work must be stopped explicitly rather than disrupted by maintenance.
func (s *Service) Repair(c Capability) (Snapshot, error) {
	s.op.Lock()
	defer s.op.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.slots[c]
	if v == nil || !s.state.CanChange {
		return s.snapshotLocked(), ErrRevoked
	}
	if v.state.Dependency == "preparing" {
		return s.snapshotLocked(), errors.New("AUTOMATION_PREPARATION_BUSY")
	}
	if s.componentWorkLocked(c) {
		return s.snapshotLocked(), errors.New("BROWSER_UPDATE_BUSY: stop browser use before repairing components")
	}
	v.repair = true
	v.state.Dependency = "unprepared"
	s.startLocked(c)
	s.publishLocked()
	return s.snapshotLocked(), nil
}

func (s *Service) browserWorkLocked() bool {
	if s.browserMaintenance {
		return true
	}
	if s.gate != nil {
		for id, p := range s.gate.permits {
			if time.Now().After(p.expires) {
				delete(s.gate.permits, id)
			}
		}
	}
	for k := range s.entries {
		if strings.HasPrefix(k, "browser/") {
			return true
		}
	}
	return s.gate != nil && (len(s.gate.pending) > 0 || len(s.gate.permits) > 0)
}
func (s *Service) BrowserMaintenanceBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.browserWorkLocked()
}

func (s *Service) BeginBrowserMaintenance() (func(), error) {
	s.mu.Lock()
	if s.browserWorkLocked() {
		s.mu.Unlock()
		return nil, errors.New("BROWSER_UPDATE_BUSY")
	}
	s.browserMaintenance = true
	s.publishLocked()
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() { s.mu.Lock(); s.browserMaintenance = false; s.publishLocked(); s.mu.Unlock() })
	}, nil
}
func (s *Service) startLocked(c Capability) {
	slot := s.slots[c]
	if !s.state.CanChange || slot.state.Dependency == "preparing" {
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
	repair := slot.repair
	slot.repair = false
	slot.state.Dependency = "preparing"
	slot.state.Availability = "preparing"
	slot.state.Step = "download"
	slot.state.Failure = ""
	slot.state.RepairFailure = ""
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
			runPrepare := s.prepare.Prepare
			if repair {
				if r, ok := s.prepare.(interface {
					Repair(context.Context, Capability, func(Progress)) error
				}); ok {
					runPrepare = r.Repair
				}
			}
			err = runPrepare(ctx, c, func(p Progress) {
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
		if !slot.state.Enabled || c == Browser && !s.state.DefaultBrowserEnabled {
			slot.state.Availability = "disabled"
		}
		if err != nil {
			if repair {
				if checker, ok := s.prepare.(interface{ Ready(Capability) bool }); ok && checker.Ready(c) {
					slot.state.Dependency = "prepared"
					slot.state.Failure = ""
					slot.state.RepairFailure = FailureCode(err)
					s.publishLocked()
					return
				}
			}
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
	if s.state.Generation != generation || !ready || !profile {
		s.cancelBrowserOpeningLocked()
	}
	if s.computer != nil && s.state.Generation != generation {
		s.computer.sessions = map[string]*computerConversation{}
	}
	if s.gate != nil && (!ready || !profile) {
		for _, intent := range s.gate.ownedIntents {
			intent.invalid = true
		}
	}
	if s.gate != nil && s.state.Generation != generation {
		for _, conversation := range s.gate.sessions {
			if conversation.inspection != nil {
				conversation.inspection.cancel()
			}
		}
		s.gate.sessions = map[string]*browserConversation{}
		s.gate.ownedIntents = map[string]*ownedBrowserIntent{}
	}
	s.state.Generation = generation
	s.state.CanChange = ready && profile
	s.state.AvailableProfile = profile
	old := s.revokeLocked("", "")
	s.stopped = map[string]bool{}
	for _, c := range []Capability{Browser, Computer, AUV} {
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
			s.probeLocked(c)
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
		if s.gate != nil {
			for _, intent := range s.gate.ownedIntents {
				intent.invalid = true
			}
		}
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
	if r.Operation == "computer" {
		return s.computerValidLocked(r)
	}
	if r.Capability == Browser && s.browserMaintenance {
		return false
	}
	if s.gate != nil && r.Capability == Browser && (r.Operation == "browser" || r.Operation == "authorize") {
		c := s.gate.sessions[r.Session]
		if c == nil || c.life != r.Lifetime || c.turn != r.Turn || c.stopped {
			return false
		}
	}
	v := s.slots[r.Capability]
	return v != nil && (r.Capability != Browser || s.state.DefaultBrowserEnabled) && s.state.CanChange && s.state.GatewayConnected && s.state.Generation == r.Generation && v.state.Enabled && v.state.Epoch == r.Epoch && v.state.Dependency == "prepared" && r.Session != "" && r.Lifetime != "" && !s.stopped[revocationKey(r)]
}
func revocationKey(r Request) string {
	return fmt.Sprintf("%s/%d/%s/%s", r.Capability, r.Epoch, r.Session, r.Lifetime)
}
func key(r Request) string {
	return string(r.Capability) + "/" + r.Operation + "/" + r.Session + "/" + r.Lifetime
}
func (s *Service) Execute(ctx context.Context, r Request) (any, error) {
	if r.Capability == Computer {
		if r.Operation == "computer-turn" {
			return s.ComputerTurn(r)
		}
		if r.Operation == "computer-authorize" {
			r.Operation = "computer"
			return s.AuthorizeComputer(ctx, r)
		}
		if r.Operation == "computer" {
			return s.executeComputerPermit(ctx, r)
		}
	}
	if r.Capability == Browser {
		s.mu.Lock()
		product := s.gate != nil
		s.mu.Unlock()
		if product {
			if r.Operation == "turn" {
				return s.BrowserTurn(r)
			}
			if r.Operation == "authorize" {
				r.Operation = "browser"
				return s.AuthorizeBrowser(ctx, r)
			}
			if r.Operation == "browser" {
				return s.executeBrowserPermit(ctx, r)
			}
		}
	}
	return s.executeOwned(ctx, r)
}
func (s *Service) executeOwned(ctx context.Context, r Request) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if !s.validLocked(r) || !s.validInspectionContextLocked(ctx) {
		s.mu.Unlock()
		return nil, ErrRevoked
	}
	f := s.factory
	if r.Operation != "probe" {
		f = nil
		if (s.development || s.gate != nil) && r.Capability == Browser && r.Operation == "browser" {
			f = s.browserFactory
		}
	}
	if r.Capability == Computer && r.Operation == "computer" && s.computer != nil {
		engine := s.state.ComputerEngine
		f = func(ctx context.Context, _ Request) (Resource, error) { return s.computer.config.Factory(ctx, engine) }
	}
	if f == nil {
		s.mu.Unlock()
		return nil, ErrUnavailable
	}
	e := s.entries[key(r)]
	if r.Operation == "browser" && e != nil && e.initializationCanceled {
		select {
		case <-e.closed:
			delete(s.entries, key(r))
			e = nil
		default:
			done := e.closed
			s.mu.Unlock()
			select {
			case <-done:
				return s.Execute(ctx, r)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	if e == nil {
		create, cancel := context.WithCancel(s.root)
		e = &entry{ctx: create, ready: make(chan struct{}), closed: make(chan struct{}), cancel: cancel}
		s.entries[key(r)] = e
		if r.Operation == "computer" {
			s.computer.lease = e
			e.desktopRelease = s.computer.releaseDesktop
			gate := s.computer
			e.beforeDesktopRelease = func() error { gate.probes.Wait(); s.mu.Lock(); defer s.mu.Unlock(); return gate.desktopFailure }
		}
		go func() {
			s.mu.Lock()
			valid := !e.revoked && create.Err() == nil && s.validInspectionContextLocked(ctx)
			s.mu.Unlock()
			var resource Resource
			var err error
			if valid {
				resource, err = f(create, r)
			} else {
				err = ErrRevoked
			}
			s.mu.Lock()
			if r.Operation == "browser" && create.Err() != nil && err == nil {
				err = create.Err()
			}
			e.resource, e.err = resource, err
			close(e.ready)
			if s.gate != nil && r.Operation == "browser" && err == nil {
				if c := s.gate.sessions[r.Session]; c != nil && c.life == r.Lifetime {
					c.active = true
					s.publishLocked()
				}
			}
			s.mu.Unlock()
			if err != nil {
				s.closeEntry(e)
			}
		}()
	}
	e.waiters++
	s.mu.Unlock()
	select {
	case <-ctx.Done():
		s.mu.Lock()
		e.waiters--
		if r.Operation == "browser" && e.waiters == 0 {
			select {
			case <-e.ready:
			default:
				e.initializationCanceled = true
				e.cancel()
			}
		}
		s.mu.Unlock()
		return nil, ctx.Err()
	case <-e.ready:
	}
	s.mu.Lock()
	e.waiters--
	s.mu.Unlock()
	e.call.Lock()
	defer e.call.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	valid := s.validLocked(r) && !e.revoked && s.validInspectionContextLocked(ctx)
	resource, err := e.resource, e.err
	s.mu.Unlock()
	if !valid {
		return nil, ErrRevoked
	}
	if err != nil {
		s.failOwnedAutomationEntry(r, e)
		return nil, err
	}
	if resource == nil {
		return nil, ErrUnavailable
	}
	if r.Capability == Browser {
		s.mu.Lock()
		admission := s.ownedLocked(r)
		s.mu.Unlock()
		if admission != nil {
			bound, ok := resource.(interface{ OwnedBrowserBinding() OwnedBrowserIdentity })
			if !ok {
				return nil, ErrUnavailable
			}
			identity := bound.OwnedBrowserBinding()
			if identity.BrokerGeneration != admission.initial.BrokerGeneration || identity.Target != admission.initial.Target || identity.ArtifactLease != admission.initial.ArtifactLease {
				return nil, ErrRevoked
			}
		}
	}
	callCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	defer stop()
	defer cancel()
	s.mu.Lock()
	valid = s.validLocked(r) && !e.revoked && s.validInspectionContextLocked(ctx)
	s.mu.Unlock()
	if !valid || callCtx.Err() != nil {
		return nil, ErrRevoked
	}
	value, err := resource.Execute(callCtx, r.Tool, r.Arguments)
	if err != nil {
		if terminal, ok := resource.(interface{ AutomationTerminal() bool }); ok && terminal.AutomationTerminal() {
			if s.failOwnedAutomationEntry(r, e) {
				return nil, err
			}
		}
	}
	s.mu.Lock()
	valid = s.validLocked(r) && !e.revoked && s.validInspectionContextLocked(ctx)
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
	if s.computer != nil {
		if c := s.computer.sessions[session]; c != nil && c.life == lifetime {
			c.stopped = true
		}
	}
	for _, c := range []Capability{Browser, Computer, AUV} {
		s.stopped[revocationKey(Request{Capability: c, Epoch: s.slots[c].state.Epoch, Session: session, Lifetime: lifetime})] = true
	}
	if s.gate != nil {
		if intent := s.gate.ownedIntents[session]; intent != nil {
			intent.invalid = true
		}
		if c := s.gate.sessions[session]; c != nil && c.life == lifetime {
			c.stopped = true
		}
	}
	old := s.revokeLocked("", session+"/"+lifetime)
	if s.gate != nil {
		s.browserActivityLocked(Request{Session: session}, "", "", "stopped")
		s.publishLocked()
	}
	s.mu.Unlock()
	s.release(old)
}
func (s *Service) revokeLocked(c Capability, session string) []*entry {
	s.revokeBrowserGrantsLocked(c, session)
	s.revokeComputerLocked(c, session)
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
				e.closeErr = err
				logging.Module(logging.ModSettings).Warn("automation resource release failed", "err", err)
			}
		}
		if e.beforeDesktopRelease != nil {
			e.closeErr = errors.Join(e.closeErr, e.beforeDesktopRelease())
		}
		if e.closeErr == nil && e.desktopRelease != nil {
			e.closeErr = e.desktopRelease()
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
	s.finishComputerEntries(entries)
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
	if s.gate != nil {
		s.gate.auditWG.Wait()
	}
}

func (s *Service) failOwnedAutomationEntry(r Request, e *entry) bool {
	s.mu.Lock()
	if (r.Operation != "browser" || s.gate == nil) && (r.Operation != "computer" || s.computer == nil) || s.entries[key(r)] != e {
		s.mu.Unlock()
		return false
	}
	old := s.revokeLocked(r.Capability, r.Session+"/"+r.Lifetime)
	s.publishLocked()
	s.mu.Unlock()
	s.release(old)
	return true
}
