package embeddedbrowser

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// InstanceRegistryConfig is daemon-owned. A tool or frame cannot choose a
// profile directory, broker factory, or persistent instance identity.
type InstanceRegistryConfig struct {
	Store          *InstanceStore
	AcquireProfile func(string) (io.Closer, error)
	StartBroker    func(context.Context, InstanceConfig) (*Broker, error)
	OnFailure      func(string, error)
}

type instancePresenter struct {
	serial            sync.Mutex
	config            InstanceConfig
	peer              PresenterPeer
	owner             presenterOwner
	profile           io.Closer
	views             *PresenterRegistry
	broker            *Broker
	cancel            context.CancelFunc
	lifetime          context.Context
	retired           bool
	cleanupErr        error
	cleanupPriorErr   error
	cleanupGeneration string
	lastCleanupErr    error
}

// InstancePresenterRegistry is the sole multi-instance lifetime owner. The
// existing presenter module remains local to exactly one broker namespace.
type InstancePresenterRegistry struct {
	mu            sync.Mutex
	shutdown      sync.Mutex
	config        InstanceRegistryConfig
	instances     map[string]*instancePresenter
	surfaces      map[string]*instancePresenter
	processes     map[PresenterPeer]*instancePresenter
	closed        bool
	WindowRetired func(PresenterPeer, string)
}

func NewInstancePresenterRegistry(c InstanceRegistryConfig) (*InstancePresenterRegistry, error) {
	if c.Store == nil || c.AcquireProfile == nil || c.StartBroker == nil {
		return nil, errors.New("CEF_INSTANCE_REGISTRY_CONFIG_REQUIRED")
	}
	return &InstancePresenterRegistry{config: c, instances: map[string]*instancePresenter{}, surfaces: map[string]*instancePresenter{}, processes: map[PresenterPeer]*instancePresenter{}}, nil
}
func (r *InstancePresenterRegistry) report(i *instancePresenter, err error) {
	if err != nil && r.config.OnFailure != nil {
		r.config.OnFailure(i.config.ID, err)
	}
}

func (r *InstancePresenterRegistry) RegisterInstanceWindow(ctx context.Context, peer PresenterPeer, key, surface string, parent uintptr) (WindowAdmission, error) {
	// Check native evidence before allocating any persistent identity.
	verified, err := admitPresenter(peer, parent)
	if err != nil {
		return WindowAdmission{}, err
	}
	defer verified.Close()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return WindowAdmission{}, ErrClosed
	}
	i := r.instances[key]
	if i == nil {
		c, e := r.config.Store.Resolve(key)
		if e != nil {
			r.mu.Unlock()
			return WindowAdmission{}, e
		}
		i = &instancePresenter{config: c, retired: true}
		r.instances[key] = i
	}
	r.mu.Unlock()
	i.serial.Lock()
	defer i.serial.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return WindowAdmission{}, ErrClosed
	}
	if previous := r.processes[peer]; previous != nil && previous != i {
		r.mu.Unlock()
		return WindowAdmission{}, errors.New("CEF_PROCESS_INSTANCE_ALREADY_BOUND")
	}
	if previous := r.surfaces[surface]; previous != nil && previous != i {
		r.mu.Unlock()
		return WindowAdmission{}, errors.New("CEF_WINDOW_SURFACE_ALREADY_OWNED")
	}
	priorBinding := r.processes[peer]
	r.processes[peer] = i
	r.mu.Unlock()
	admitted := false
	defer func() {
		if !admitted && priorBinding == nil {
			r.mu.Lock()
			if r.processes[peer] == i {
				delete(r.processes, peer)
			}
			r.mu.Unlock()
		}
	}()
	if err := ctx.Err(); err != nil {
		return WindowAdmission{}, err
	}
	if i.peer.PID != 0 && (i.peer != peer || i.retired) {
		if presenterAlive(i.peer) {
			return WindowAdmission{}, errors.New("CEF_INSTANCE_ALREADY_RUNNING")
		}
		if err := r.retireLocked(i, true); err != nil && i.profile != nil {
			return WindowAdmission{}, err
		}
	}
	if i.owner == nil {
		owner, e := admitPresenter(peer, parent)
		if e != nil {
			return WindowAdmission{}, e
		}
		lease, e := r.config.AcquireProfile(i.config.directory)
		if e != nil {
			owner.Close()
			return WindowAdmission{}, fmt.Errorf("CEF_INSTANCE_PROFILE_BUSY: %w", e)
		}
		rows, e := r.config.Store.Descriptors(i.config)
		if e != nil {
			owner.Close()
			lease.Close()
			return WindowAdmission{}, e
		}
		r.mu.Lock()
		if i.peer != peer {
			delete(r.processes, i.peer)
		}
		r.mu.Unlock()
		i.lastCleanupErr = errors.Join(i.lastCleanupErr, i.cleanupErr)
		i.cleanupErr = nil
		i.cleanupPriorErr = nil
		i.cleanupGeneration = ""
		i.peer = peer
		i.owner = owner
		i.profile = lease
		i.retired = false
		i.lifetime, i.cancel = context.WithCancel(context.Background())
		i.views = NewPresenterRegistry(nil)
		i.views.WindowRetired = func(p PresenterPeer, s string) {
			if r.WindowRetired != nil {
				r.WindowRetired(p, s)
			}
		}
		for _, d := range rows {
			i.views.views[presentationKey(surface, d.Slot)] = &presentation{peer: peer, surface: surface, parent: parent, view: ViewState{Lease: rand.Text(), State: "NeedsReload", URL: d.URL, Title: d.Title}, cleaned: true}
		}
		cancel := i.cancel
		go func() {
			<-owner.Done()
			cancel()
			i.serial.Lock()
			defer i.serial.Unlock()
			if i.owner == owner {
				r.report(i, r.retireLocked(i, true))
			}
		}()
	}
	admission, err := i.views.RegisterWindow(ctx, peer, surface, parent)
	if err == nil {
		admitted = true
		r.mu.Lock()
		r.surfaces[surface] = i
		r.mu.Unlock()
	}
	return admission, err
}

// lockSurface never holds the registry map lock while waiting for an instance.
func (r *InstancePresenterRegistry) lockSurface(peer PresenterPeer, surface string) (*instancePresenter, error) {
	r.mu.Lock()
	i := r.surfaces[surface]
	closed := r.closed
	r.mu.Unlock()
	if closed || i == nil {
		return nil, errors.New("CEF_INSTANCE_ADMISSION_REQUIRED")
	}
	i.serial.Lock()
	if i.peer != peer || i.retired {
		i.serial.Unlock()
		return nil, errors.New("CEF_INSTANCE_OWNER_REJECTED")
	}
	return i, nil
}
func (r *InstancePresenterRegistry) markLostLocked(i *instancePresenter) {
	if i.views == nil {
		return
	}
	i.views.mu.Lock()
	defer i.views.mu.Unlock()
	for _, p := range i.views.views {
		p.serial.Lock()
		p.view.State = "NeedsReload"
		p.view.Frame = ""
		p.view.Visible = false
		p.epoch++
		p.serial.Unlock()
	}
}
func (r *InstancePresenterRegistry) markPausedLocked(i *instancePresenter) {
	if i.views == nil {
		return
	}
	i.views.mu.Lock()
	defer i.views.mu.Unlock()
	for _, p := range i.views.views {
		p.serial.Lock()
		p.view.State = "controlPaused"
		p.view.Failure = ErrControlPaused.Error()
		p.serial.Unlock()
	}
}
func (r *InstancePresenterRegistry) startLocked(ctx context.Context, i *instancePresenter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i.broker != nil {
		select {
		case <-i.broker.done:
			select {
			case <-i.broker.Closed():
			default:
				return errors.New("CEF_BROKER_CONTROL_PAUSED")
			}
			r.markLostLocked(i)
			err := i.broker.Close()
			r.recordBrokerCleanupLocked(i, i.broker, err)
			if i.broker.CleanupPending() {
				return err
			}
			i.broker = nil
		default:
			return nil
		}
	}
	b, err := r.config.StartBroker(i.lifetime, i.config)
	if err != nil {
		return err
	}
	i.broker = b
	i.views.broker = b
	// Join an unexpected broker death in its own instance; never invalidate a
	// different registry entry or treat a cached view as a live document.
	go func() {
		<-b.done
		i.serial.Lock()
		if i.broker == b && !i.retired {
			if b.ControlPaused() {
				r.markPausedLocked(i)
			} else {
				r.markLostLocked(i)
			}
			r.report(i, r.rememberLocked(i))
			r.report(i, b.failure())
		}
		i.serial.Unlock()
		// A renderer-scope pause invalidates control but preserves pages. Only an
		// actual broker terminal cleanup attempt may proceed to its join barrier.
		<-b.Closed()
		i.serial.Lock()
		defer i.serial.Unlock()
		if i.broker == b && !i.retired {
			closeErr := b.Close()
			r.recordBrokerCleanupLocked(i, b, closeErr)
			r.report(i, closeErr)
			if !b.CleanupPending() {
				r.report(i, r.retireJoinedTargetsLocked(i))
			}
		}
	}()

	return ctx.Err()
}
func (r *InstancePresenterRegistry) rememberLocked(i *instancePresenter) error {
	if i.views == nil {
		return nil
	}
	i.views.mu.Lock()
	defer i.views.mu.Unlock()
	var result error
	for key, p := range i.views.views {
		p.serial.Lock()
		slot := key[len(p.surface)+1:]
		if p.view.URL != "" {
			result = errors.Join(result, r.config.Store.Remember(i.config, TabDescriptor{Slot: slot, URL: p.view.URL, Title: p.view.Title}))
		}
		p.serial.Unlock()
	}
	return result
}
func (r *InstancePresenterRegistry) retireLocked(i *instancePresenter, force bool) error {
	err := r.retireInstanceLocked(i, force, true)
	if err != nil && i.cleanupCompleteLocked() {
		return fmt.Errorf("CEF_CLEANUP_COMPLETED_ABNORMALLY: %w", err)
	}
	return err
}
func (r *InstancePresenterRegistry) retireInstanceLocked(i *instancePresenter, force, finish bool) error {
	// Failed descriptor persistence must not irreversibly retire a live UI.
	result := r.rememberLocked(i)
	if result != nil && !force {
		return errors.Join(i.cleanupErr, result)
	}
	i.retired = true
	recordBroker := func() { r.recordBrokerCleanupLocked(i, i.broker, i.broker.Close()) }
	if force && i.cancel != nil {
		i.cancel()
	}
	if force && i.broker != nil {
		recordBroker()
		if !i.broker.CleanupPending() {
			result = errors.Join(result, r.retireJoinedTargetsLocked(i))
			i.broker = nil
		}
	}
	if i.views != nil {
		if err := i.views.drain(); err != nil {
			result = errors.Join(result, fmt.Errorf("CEF_INSTANCE_VIEWS_CLOSE: %w", err))
			if !force {
				return errors.Join(i.cleanupErr, result)
			}
		}
	}
	if i.broker != nil {
		recordBroker()
		if i.broker.CleanupPending() {
			return errors.Join(i.cleanupErr, result)
		}
		result = errors.Join(result, r.retireJoinedTargetsLocked(i))
		i.broker = nil
	}
	if result != nil && !force {
		return errors.Join(i.cleanupErr, result)
	}
	if i.cancel != nil {
		i.cancel()
	}
	if i.owner != nil {
		err := i.owner.Close()
		result = errors.Join(result, err)
		if err == nil {
			i.owner = nil
		}
	}
	if i.profile != nil {
		err := i.profile.Close()
		result = errors.Join(result, err)
		if err == nil {
			i.profile = nil
		}
	}
	if result != nil && !force {
		return errors.Join(i.cleanupErr, result)
	}
	if finish && i.views != nil {
		result = errors.Join(result, i.views.finish())
	}
	return errors.Join(i.cleanupErr, result)
}
func (i *instancePresenter) resourcesCompleteLocked() bool {
	if i.broker != nil || i.owner != nil || i.profile != nil {
		return false
	}
	if i.views != nil {
		i.views.mu.Lock()
		defer i.views.mu.Unlock()
		for _, p := range i.views.views {
			p.serial.Lock()
			complete := p.target == nil && p.owner == nil
			p.serial.Unlock()
			if !complete {
				return false
			}
		}
	}
	return true
}

func (i *instancePresenter) cleanupCompleteLocked() bool {
	complete := i.resourcesCompleteLocked()
	if complete && i.views != nil {
		i.views.mu.Lock()
		defer i.views.mu.Unlock()
		for _, w := range i.views.windows {
			w.serial.Lock()
			complete = complete && w.finished && w.owner == nil
			w.serial.Unlock()
		}
	}
	return complete
}

// CleanupComplete is independent of Close's retained abnormal result. Only
// the trusted, already-authorized exit boundary may use it to finish an exit
// after reporting a terminal failure. It never admits a new browser action.
func (r *InstancePresenterRegistry) CleanupComplete() bool {
	r.mu.Lock()
	list := make([]*instancePresenter, 0, len(r.instances))
	for _, i := range r.instances {
		list = append(list, i)
	}
	r.mu.Unlock()
	for _, i := range list {
		i.serial.Lock()
		complete := i.cleanupCompleteLocked()
		i.serial.Unlock()
		if !complete {
			return false
		}
	}

	return true
}

// CloseInstance is called while the UI message pump still runs. Exact native
// peer evidence, not a caller-selected persistent key, selects the owner.
func (r *InstancePresenterRegistry) CloseInstance(ctx context.Context, peer PresenterPeer, surface string) error {
	r.mu.Lock()
	i := r.surfaces[surface]
	r.mu.Unlock()
	if i == nil {
		return nil
	}
	i.serial.Lock()
	defer i.serial.Unlock()
	if i.peer != peer {
		return errors.New("CEF_INSTANCE_OWNER_REJECTED")
	}
	return r.retireLocked(i, false)
}
func (r *InstancePresenterRegistry) Close() error {
	r.shutdown.Lock()
	defer r.shutdown.Unlock()
	r.mu.Lock()
	r.closed = true
	instances := make([]*instancePresenter, 0, len(r.instances))
	for _, i := range r.instances {
		instances = append(instances, i)
	}
	r.mu.Unlock()
	var result error
	allComplete, anyRetired := true, false
	for _, i := range instances {
		i.serial.Lock()
		result = errors.Join(result, r.retireInstanceLocked(i, false, false))
		allComplete = allComplete && i.resourcesCompleteLocked()
		anyRetired = anyRetired || i.retired
		i.serial.Unlock()
	}
	if !allComplete {
		if !anyRetired {
			r.mu.Lock()
			r.closed = false
			r.mu.Unlock()
		}
		return result
	}

	// No UI may lose its stream until the shared daemon's entire native drain
	// has succeeded. Retrying a failed drain retains this same set of owners.
	for _, i := range instances {
		i.serial.Lock()
		if i.views != nil {
			result = errors.Join(result, i.views.finish())
		}
		i.serial.Unlock()
	}

	if result != nil && r.CleanupComplete() {
		return fmt.Errorf("CEF_CLEANUP_COMPLETED_ABNORMALLY: %w", result)
	}
	return result
}
func (r *InstancePresenterRegistry) Update(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, lease, slot, address string, b SlotBounds, z float64, visible bool) (ViewState, error) {
	i, e := r.lockSurface(peer, surface)
	if e != nil {
		return ViewState{}, e
	}
	defer i.serial.Unlock()
	if address != "" {
		p := i.views.get(surface, slot)
		needs := false
		if p != nil {
			p.serial.Lock()
			needs = p.view.State == "NeedsReload"
			p.serial.Unlock()
		}
		if !needs {
			if e = r.startLocked(ctx, i); e != nil {
				return ViewState{}, e
			}
		}
	}
	v, e := i.views.Update(ctx, peer, surface, parent, lease, slot, address, b, z, visible)
	r.report(i, r.rememberLocked(i))
	return r.identityLocked(i, v), e
}
func (r *InstancePresenterRegistry) Recover(ctx context.Context, peer PresenterPeer, surface string, parent uintptr, lease, slot, address string) (ViewState, error) {
	i, e := r.lockSurface(peer, surface)
	if e != nil {
		return ViewState{}, e
	}
	defer i.serial.Unlock()
	if e = r.startLocked(ctx, i); e != nil {
		return ViewState{}, e
	}
	// An old broker has joined before its old native targets can be discarded.
	p := i.views.get(surface, slot)
	if p != nil {
		p.serial.Lock()
		if p.target != nil && p.target.broker != i.broker {
			p.target = nil
			if p.owner != nil {
				if err := p.owner.Close(); err != nil {
					p.serial.Unlock()
					return ViewState{}, err
				}
			}
			p.owner = nil
			p.cleaned = true
		}
		p.serial.Unlock()
	}
	v, e := i.views.Recover(ctx, peer, surface, parent, lease, slot, address)
	r.report(i, r.rememberLocked(i))
	return r.identityLocked(i, v), e
}
func (r *InstancePresenterRegistry) State(ctx context.Context, p PresenterPeer, s, slot string) (ViewState, error) {
	i, e := r.lockSurface(p, s)
	if e != nil {
		return ViewState{}, e
	}
	defer i.serial.Unlock()
	v, e := i.views.State(ctx, p, s, slot)
	r.report(i, r.rememberLocked(i))
	return r.identityLocked(i, v), e
}
func (r *InstancePresenterRegistry) Hide(ctx context.Context, p PresenterPeer, s string) error {
	i, e := r.lockSurface(p, s)
	if e != nil {
		return e
	}
	defer i.serial.Unlock()
	return i.views.Hide(ctx, p, s)
}
func (r *InstancePresenterRegistry) Detach(ctx context.Context, p PresenterPeer, s, slot string) error {
	i, e := r.lockSurface(p, s)
	if e != nil {
		return e
	}
	defer i.serial.Unlock()
	return i.views.Detach(ctx, p, s, slot)
}
func (r *InstancePresenterRegistry) Action(ctx context.Context, p PresenterPeer, s, slot, a string) error {
	i, e := r.lockSurface(p, s)
	if e != nil {
		return e
	}
	defer i.serial.Unlock()
	return i.views.Action(ctx, p, s, slot, a)
}
func (r *InstancePresenterRegistry) RetireDocument(ctx context.Context, p PresenterPeer, s string, parent uintptr, lease string) error {
	i, e := r.lockSurface(p, s)
	if e != nil {
		return e
	}
	defer i.serial.Unlock()
	return i.views.RetireDocument(ctx, p, s, parent, lease)
}
func (r *InstancePresenterRegistry) CloseWindow(ctx context.Context, p PresenterPeer, s string) error {
	r.mu.Lock()
	i := r.surfaces[s]
	r.mu.Unlock()
	if i == nil {
		return nil
	}
	i.serial.Lock()
	defer i.serial.Unlock()
	if i.peer != p {
		return errors.New("CEF_INSTANCE_OWNER_REJECTED")
	}
	if i.retired {
		return r.retireLocked(i, false)
	}
	return i.views.CloseWindow(ctx, p, s)
}

func (r *InstancePresenterRegistry) WindowStreamScope(p PresenterPeer, lease string) (string, <-chan struct{}, error) {
	r.mu.Lock()
	i := r.processes[p]
	closed := r.closed
	r.mu.Unlock()
	if closed || i == nil {
		return "", nil, errors.New("CEF_STREAM_WINDOW_LEASE_REJECTED")
	}
	i.serial.Lock()
	defer i.serial.Unlock()
	if i.peer != p || i.retired || i.views == nil {
		return "", nil, errors.New("CEF_STREAM_WINDOW_LEASE_REJECTED")
	}
	return i.views.WindowStreamScope(p, lease)
}

func (r *InstancePresenterRegistry) WithWindowLease(p PresenterPeer, s, lease string, fn func()) bool {
	i, e := r.lockSurface(p, s)
	if e != nil {
		return false
	}
	defer i.serial.Unlock()
	return i.views.WithWindowLease(p, s, lease, fn)
}

func (r *InstancePresenterRegistry) identityLocked(i *instancePresenter, v ViewState) ViewState {
	v.Instance = i.config.ID
	if i.broker != nil {
		v.BrokerGeneration = i.broker.generation
	}
	return v
}

// OwnedViewTarget is a Go-only dependency seam for typed Service admission.
// It is not registered as an RPC or Wails service; native peer and an existing
// visible view lease select the live target inside its owning instance/broker.
func (r *InstancePresenterRegistry) OwnedViewTarget(peer PresenterPeer, surface, slot, lease string) (*OwnedTarget, error) {
	i, e := r.lockSurface(peer, surface)
	if e != nil {
		return nil, e
	}
	defer i.serial.Unlock()
	p := i.views.get(surface, slot)
	if p == nil {
		return nil, ErrClosed
	}
	p.serial.Lock()
	defer p.serial.Unlock()
	if p.peer != peer || p.view.Lease != lease || !p.view.Visible || p.view.State != "ready" || p.target == nil || p.target.broker != i.broker {
		return nil, errors.New("CEF_VIEW_ADMISSION_REJECTED")
	}
	return p.target, nil
}

// A verified supervisor process-tree join is the terminal execution-context
// barrier when the native parent already died. Do not send close RPCs to a
// known-joined broker or discard owners before this barrier.
func (r *InstancePresenterRegistry) retireJoinedTargetsLocked(i *instancePresenter) error {
	if i.views == nil {
		return nil
	}
	i.views.mu.Lock()
	defer i.views.mu.Unlock()
	var result error
	for _, p := range i.views.views {
		p.serial.Lock()
		if p.target != nil && p.target.broker == i.broker {
			if p.owner != nil {
				err := p.owner.Close()
				result = errors.Join(result, err)
				if err == nil {
					p.owner = nil
				}
			}
			p.target = nil
			p.cleaned = p.owner == nil
			p.view.State = "NeedsReload"
			p.view.Visible = false
			p.view.Target = ""
			p.view.Document = ""
			p.view.Frame = ""
			p.view.Epoch = ""
		}
		p.serial.Unlock()
	}
	return result
}

func (r *InstancePresenterRegistry) recordBrokerCleanupLocked(i *instancePresenter, b *Broker, err error) {
	if err == nil {
		return
	}
	if i.cleanupGeneration != b.generation || i.cleanupErr == nil {
		i.cleanupPriorErr = i.cleanupErr
		i.cleanupGeneration = b.generation
	}
	i.cleanupErr = errors.Join(i.cleanupPriorErr, fmt.Errorf("CEF_INSTANCE_BROKER_CLOSE: %w", err))
}

// ExpectedUIOwner constrains shutdown; only native registry evidence admits UI owners.
type ExpectedUIOwner struct {
	PID     uint32
	Started uint64
}

// QuitOwned fences new admissions before inspecting the full live owner set.
// The callback only requests the existing asynchronous quit flow.
func (r *InstancePresenterRegistry) QuitOwned(ctx context.Context, owners []ExpectedUIOwner, expires int64, quit func()) error {
	r.shutdown.Lock()
	defer r.shutdown.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if expires <= time.Now().UnixMilli() || expires > time.Now().Add(3*time.Minute).UnixMilli() || quit == nil {
		return errors.New("CEF_QUIT_REQUEST_EXPIRED")
	}
	r.mu.Lock()
	prior := r.closed
	r.closed = true
	list := make([]*instancePresenter, 0, len(r.instances))
	for _, i := range r.instances {
		list = append(list, i)
	}
	r.mu.Unlock()
	accepted := false
	defer func() {
		if !accepted {
			r.mu.Lock()
			r.closed = prior
			r.mu.Unlock()
		}
	}()
	for _, i := range list {
		i.serial.Lock()
		peer := i.peer
		alive := presenterAlive(peer)
		i.serial.Unlock()
		if !alive {
			continue
		}
		found := false
		for _, expected := range owners {
			if peer.PID == expected.PID && peer.Started == expected.Started {
				found = true
				break
			}
		}
		if !found {
			return errors.New("CEF_FOREIGN_UI_OWNER_QUIT_REJECTED")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if expires <= time.Now().UnixMilli() {
		return errors.New("CEF_QUIT_REQUEST_EXPIRED")
	}
	quit()
	accepted = true
	return nil
}

// OwnedWindowViewTarget additionally binds a selection to its current native
// window admission. The instance serial owner excludes retirement throughout.
func (r *InstancePresenterRegistry) OwnedWindowViewTarget(peer PresenterPeer, surface string, parent uintptr, windowLease, slot, viewLease string) (*OwnedTarget, error) {
	i, err := r.lockSurface(peer, surface)
	if err != nil {
		return nil, err
	}
	defer i.serial.Unlock()
	window, err := i.views.lockWindow(peer, surface, parent, windowLease)
	if err != nil {
		return nil, err
	}
	defer window.serial.Unlock()
	p := i.views.get(surface, slot)
	if p == nil {
		return nil, ErrClosed
	}
	p.serial.Lock()
	defer p.serial.Unlock()
	if p.peer != peer || p.view.Lease != viewLease || !p.view.Visible || p.view.State != "ready" || p.target == nil || p.target.broker != i.broker {
		return nil, errors.New("CEF_VIEW_ADMISSION_REJECTED")
	}
	return p.target, nil
}

func (r *InstancePresenterRegistry) AnswerDialog(ctx context.Context, p PresenterPeer, surface, slot string, dialog WebDialog, accept bool, text ...string) error {
	i, err := r.lockSurface(p, surface)
	if err != nil {
		return err
	}
	defer i.serial.Unlock()
	return i.views.AnswerDialog(ctx, p, surface, slot, dialog, accept, text...)
}

func (r *InstancePresenterRegistry) PresentDialog(ctx context.Context, p PresenterPeer, surface, slot string, dialog WebDialog, ready bool) error {
	i, err := r.lockSurface(p, surface)
	if err != nil {
		return err
	}
	defer i.serial.Unlock()
	return i.views.PresentDialog(ctx, p, surface, slot, dialog, ready)
}

func (r *InstancePresenterRegistry) CancelDownload(ctx context.Context, p PresenterPeer, surface, slot, id string) error {
	i, e := r.lockSurface(p, surface)
	if e != nil {
		return e
	}
	defer i.serial.Unlock()
	return i.views.CancelDownload(ctx, p, surface, slot, id)
}
