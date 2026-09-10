package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workergateway"
	"github.com/local/dsh-work/internal/workspacecontext"
)

// DSHAdapter is the dsh-work-owned contract for discovery, launch construction,
// readiness validation and shutdown. It deliberately contains no Wails or
// operating-system types.
type DSHAdapter interface {
	Discover(context.Context) (dshadapter.Runtime, error)
	BuildLaunchPlan(dshadapter.LaunchContext) (supervisor.LaunchPlan, error)
	ParseReadyAnnouncement(string) (dshadapter.ReadyAnnouncement, bool)
	ValidateReady(dshadapter.ReadyAnnouncement, supervisor.LaunchPlan) error
	Probe(context.Context, dshadapter.ReadyAnnouncement, supervisor.LaunchPlan) error
	RequestShutdown(context.Context, supervisor.Worker) error
}

type WorkerGateway interface {
	Start(context.Context, string) (workergateway.Session, error)
}

// ProfilePluginManager is the manager surface used by the trusted Settings
// service. The Host wraps mutations in the same boundary as Worker cleanup so
// an unexpected process exit cannot race a profile write.
type ProfilePluginManager interface {
	InstallPlugin(context.Context, dshmanager.PluginInstallRequest) (dshmanager.PluginResult, error)
	UpgradePlugin(context.Context, dshmanager.PluginUpgradeRequest) (dshmanager.PluginResult, error)
	RemovePlugin(context.Context, dshmanager.PluginRemoveRequest) (dshmanager.PluginResult, error)
}

// LaunchManager is the platform-neutral Run-context selection seam. The Host
// consumes a resolved tuple and never edits profile files or invokes a package
// manager.
type LaunchManager interface {
	Snapshot(context.Context) (dshmanager.Snapshot, error)
	ResolveLaunch(context.Context, dshmanager.LaunchRequest) (dshmanager.ResolvedLaunch, error)
}

// RunContextManager adds the commit and mutation-lock operations needed by the
// Host's serialized context-switch transaction. Keeping it separate lets the
// Host depend on the smallest read-only manager seam during ordinary startup.
type RunContextManager interface {
	LaunchManager
	BeginRunContextSwitch(context.Context) error
	EndRunContextSwitch(context.Context) error
	CommitCurrent(context.Context, *dshmanager.RunContext) (dshmanager.Snapshot, error)
	ClearCurrent(context.Context) (dshmanager.Snapshot, error)
	RecordSwitchAttempt(context.Context, dshmanager.SwitchAttempt, bool) (dshmanager.Snapshot, error)
	ClearSwitchAttempt(context.Context) (dshmanager.Snapshot, error)
}

// RunContextPreparer is an optional switch-time seam. A manager may use the
// resolved DSH runtime and profile to rebuild missing generated dependencies
// before Host stops the current Worker.
type RunContextPreparer interface {
	PrepareRunContext(context.Context, dshmanager.ResolvedLaunch) error
}

type Dependencies struct {
	DSH                      DSHAdapter
	Manager                  LaunchManager
	ManagerError             error
	WorkspaceResolver        workspacecontext.Resolver
	Supervisor               supervisor.Adapter
	Gateway                  WorkerGateway
	PlatformError            error
	AutomaticRuntimeRollback func() bool
}

type Config struct {
	DiscoveryRoot       string
	BootstrapDirectory  string
	DSHDataDirectory    string
	SettingsPath        string
	ExpectedDSHVersion  string
	ReadinessTimeout    time.Duration
	ProbeTimeout        time.Duration
	GracefulStopTimeout time.Duration
	ForceStopTimeout    time.Duration
	EmptyTimeout        time.Duration
	ShutdownTimeout     time.Duration
	configError         error
}

func DefaultConfig(discoveryRoot string) Config {
	if discoveryRoot == "" {
		discoveryRoot, _ = os.Getwd()
	}
	discoveryRoot, _ = filepath.Abs(discoveryRoot)
	configRoot, err := os.UserConfigDir()
	if err != nil || configRoot == "" {
		return Config{
			DiscoveryRoot:       discoveryRoot,
			ExpectedDSHVersion:  dshadapter.SupportedVersion,
			configError:         errors.New("the operating system did not provide a per-user application-data directory"),
			ReadinessTimeout:    20 * time.Second,
			ProbeTimeout:        750 * time.Millisecond,
			GracefulStopTimeout: 4 * time.Second,
			ForceStopTimeout:    4 * time.Second,
			EmptyTimeout:        4 * time.Second,
			ShutdownTimeout:     12 * time.Second,
		}
	}
	return Config{
		DiscoveryRoot:       discoveryRoot,
		BootstrapDirectory:  filepath.Join(configRoot, "dsh-work", "bootstrap"),
		DSHDataDirectory:    filepath.Join(configRoot, "dsh-work", "environment"),
		SettingsPath:        filepath.Join(configRoot, "dsh-work", "settings.json"),
		ExpectedDSHVersion:  dshadapter.SupportedVersion,
		ReadinessTimeout:    20 * time.Second,
		ProbeTimeout:        750 * time.Millisecond,
		GracefulStopTimeout: 4 * time.Second,
		ForceStopTimeout:    4 * time.Second,
		EmptyTimeout:        4 * time.Second,
		ShutdownTimeout:     12 * time.Second,
	}
}

func (c Config) withDefaults() Config {
	defaults := DefaultConfig(c.DiscoveryRoot)
	needsApplicationData := c.BootstrapDirectory == "" || c.DSHDataDirectory == "" || c.SettingsPath == ""
	if needsApplicationData && defaults.configError != nil {
		c.configError = defaults.configError
	}
	if c.DiscoveryRoot == "" {
		c.DiscoveryRoot = defaults.DiscoveryRoot
	}
	if c.BootstrapDirectory == "" {
		c.BootstrapDirectory = defaults.BootstrapDirectory
	}
	if c.DSHDataDirectory == "" {
		c.DSHDataDirectory = defaults.DSHDataDirectory
	}
	if c.SettingsPath == "" {
		c.SettingsPath = defaults.SettingsPath
	}
	if c.ExpectedDSHVersion == "" {
		c.ExpectedDSHVersion = defaults.ExpectedDSHVersion
	}
	if c.ReadinessTimeout <= 0 {
		c.ReadinessTimeout = defaults.ReadinessTimeout
	}
	if c.ProbeTimeout <= 0 {
		c.ProbeTimeout = defaults.ProbeTimeout
	}
	if c.GracefulStopTimeout <= 0 {
		c.GracefulStopTimeout = defaults.GracefulStopTimeout
	}
	if c.ForceStopTimeout <= 0 {
		c.ForceStopTimeout = defaults.ForceStopTimeout
	}
	if c.EmptyTimeout <= 0 {
		c.EmptyTimeout = defaults.EmptyTimeout
	}
	if c.ShutdownTimeout <= 0 {
		c.ShutdownTimeout = defaults.ShutdownTimeout
	}
	return c
}

type Host struct {
	machine *lifecycle.Machine
	deps    Dependencies
	config  Config

	mu                   sync.Mutex
	switchMu             sync.Mutex
	workerBoundaryMu     sync.Mutex
	switching            bool
	managerSwitchGuarded bool
	shutdownRequested    bool
	current              *generationRun
	configError          error
	publish              func(lifecycle.Status)
	onReady              func(string)
	recovery             func()
	quit                 func()
	shutdownMu           sync.Mutex
	lastDiagnostics      supervisor.Diagnostics
	debug                func(string)
}

type generationRun struct {
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
	ready      chan struct{}
	readyOnce  sync.Once

	mu                sync.RWMutex
	worker            supervisor.Worker
	plan              supervisor.LaunchPlan
	readiness         <-chan dshadapter.ReadyAnnouncement
	gateway           workergateway.Session
	workspaceRequest  workspacecontext.Request
	workspace         workspacecontext.Context
	launch            *dshmanager.ResolvedLaunch
	target            *dshmanager.RunContext
	readyWorkspaceURL string
	readyHandoffURL   string
	managerLive       bool
	managerGuarded    bool
	cleanupDone       bool
}

type hostLaunch struct {
	runtime       dshadapter.Runtime
	dataDirectory dshmanager.DataDirectoryInfo
	profile       string
	workspace     workspacecontext.Context
	resolved      *dshmanager.ResolvedLaunch
}

func NewHost(deps Dependencies, config Config) *Host {
	config = config.withDefaults()
	return &Host{
		machine:     lifecycle.NewMachine(),
		deps:        deps,
		config:      config,
		configError: config.configError,
	}
}

func (h *Host) SetPublish(fn func(lifecycle.Status)) {
	h.mu.Lock()
	h.publish = fn
	h.mu.Unlock()
}

func (h *Host) SetReadyHandler(fn func(string)) {
	h.mu.Lock()
	h.onReady = fn
	h.mu.Unlock()
}

// SetRecoveryHandler restores the trusted Host surface after a Worker that
// was already ready exits or disconnects. The callback is a UI composition
// concern; lifecycle state remains platform-neutral.
func (h *Host) SetRecoveryHandler(fn func()) {
	h.mu.Lock()
	h.recovery = fn
	h.mu.Unlock()
}

func (h *Host) SetQuitHandler(fn func()) {
	h.mu.Lock()
	h.quit = fn
	h.mu.Unlock()
}

// SetDebug installs an optional bounded smoke-test hook. It never receives
// raw process output or session credentials.
func (h *Host) SetDebug(fn func(string)) {
	h.mu.Lock()
	h.debug = fn
	h.mu.Unlock()
}

func (h *Host) Status() lifecycle.Status {
	return h.machine.Snapshot()
}

// Diagnostics returns the last bounded supervisor record. It is intentionally
// not exposed through HostService; it exists for local smoke tests and logs,
// while the frontend receives only the stable lifecycle contract.
func (h *Host) Diagnostics() supervisor.Diagnostics {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		h.current.mu.RLock()
		worker := h.current.worker
		h.current.mu.RUnlock()
		if worker != nil {
			return worker.Diagnostics()
		}
	}
	return h.lastDiagnostics
}

func (h *Host) Start() lifecycle.Status {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	return h.start(workspacecontext.Request{})
}

// StartWithWorkspace is the explicit launch/session action for callers that
// already have a DSH Workspace record. The request is resolved for this new
// Worker generation and is never written to global settings.
func (h *Host) StartWithWorkspace(request workspacecontext.Request) lifecycle.Status {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	return h.start(request)
}

func (h *Host) start(workspaceRequest workspacecontext.Request) lifecycle.Status {
	for {
		h.mu.Lock()
		if h.switching || h.shutdownRequested {
			status := h.machine.Snapshot()
			h.mu.Unlock()
			return status
		}
		if h.current == nil {
			run, status, err := h.beginRunLocked(context.Background(), workspaceRequest, nil)
			if err != nil {
				h.mu.Unlock()
				return status
			}
			h.mu.Unlock()
			h.emit(status)
			go h.runStartup(run)
			return status
		}
		run := h.current
		status := h.machine.Snapshot()
		h.mu.Unlock()

		select {
		case <-run.done:
			if !run.cleanupPending() {
				return status
			}
			if failure := h.cleanupWorker(run, run.getWorker()); failure != nil {
				h.emit(h.Status())
				return h.Status()
			}
			h.releaseManagerStopGuard(run)
			h.mu.Lock()
			if h.current == run {
				h.current = nil
			}
			h.mu.Unlock()
		default:
			return status
		}
	}
}

func (h *Host) beginRunLocked(parent context.Context, workspaceRequest workspacecontext.Request, launch *dshmanager.ResolvedLaunch) (*generationRun, lifecycle.Status, error) {
	generation, status, err := h.machine.BeginStart()
	if err != nil {
		return nil, status, err
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	run := &generationRun{
		generation:       generation,
		ctx:              ctx,
		cancel:           cancel,
		done:             make(chan struct{}),
		ready:            make(chan struct{}),
		workspaceRequest: workspaceRequest,
		launch:           cloneResolvedLaunch(launch),
	}
	h.current = run
	if launch != nil {
		status, _ = h.machine.SetLaunchSelection(generation, launchSelection(*launch))
	}
	return run, status, nil
}

func (h *Host) beginResolvedRun(ctx context.Context, launch dshmanager.ResolvedLaunch) (*generationRun, lifecycle.Status, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		return nil, h.machine.Snapshot(), &lifecycle.TransitionError{
			From: h.machine.Snapshot().State,
			To:   lifecycle.StateStarting,
			Why:  "a Worker generation is still owned by the Host",
		}
	}
	return h.beginRunLocked(ctx, workspacecontext.Request{}, &launch)
}

func cloneResolvedLaunch(launch *dshmanager.ResolvedLaunch) *dshmanager.ResolvedLaunch {
	if launch == nil {
		return nil
	}
	copy := *launch
	if launch.Node.ChildEnvironment != nil {
		copy.Node.ChildEnvironment = make(map[string]string, len(launch.Node.ChildEnvironment))
		for key, value := range launch.Node.ChildEnvironment {
			copy.Node.ChildEnvironment[key] = value
		}
	}
	return &copy
}

func (h *Host) setSwitching(value bool) {
	h.mu.Lock()
	h.switching = value
	h.mu.Unlock()
}

func (h *Host) setManagerSwitchGuarded(value bool) {
	h.mu.Lock()
	h.managerSwitchGuarded = value
	h.mu.Unlock()
}

func (h *Host) isSwitching() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.switching
}

func (h *Host) isManagerSwitchGuarded() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.managerSwitchGuarded
}

func (h *Host) setShutdownRequested(value bool) {
	h.mu.Lock()
	h.shutdownRequested = value
	h.mu.Unlock()
}

func (h *Host) isShutdownRequested() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.shutdownRequested
}

func (h *Host) knownGoodRunContextLocked() *dshmanager.RunContext {
	if h.current == nil {
		return nil
	}
	return h.current.targetCopy()
}

func knownGoodRunContext(manager RunContextManager) *dshmanager.RunContext {
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil || snapshot.KnownGood == nil {
		return nil
	}
	copy := *snapshot.KnownGood
	return &copy
}

func failureFromStatus(status lifecycle.Status) *lifecycle.Failure {
	if status.Error != nil {
		failure := *status.Error
		return &failure
	}
	return &lifecycle.Failure{
		Code:           lifecycle.ErrorDSHStartFailed,
		Summary:        "The candidate Run context did not become ready.",
		Retryable:      true,
		EffectOccurred: true,
		CorrelationID:  status.CorrelationID,
		Detail:         "Retry the context switch.",
	}
}

func (h *Host) waitForRun(ctx context.Context, run *generationRun) (lifecycle.Status, *lifecycle.Failure) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-run.ready:
		status := h.machine.Snapshot()
		if status.Error != nil {
			return status, failureFromStatus(status)
		}
		return status, nil
	case <-ctx.Done():
		run.cancel()
		cleanupTimer := time.NewTimer(h.config.ShutdownTimeout)
		defer cleanupTimer.Stop()
		select {
		case <-run.done:
			status := h.machine.Snapshot()
			if status.Error != nil {
				return status, failureFromStatus(status)
			}
			return status, h.failureFor(ctx.Err(), lifecycle.ErrorCancelled, "The Run context operation was cancelled.", true)
		case <-cleanupTimer.C:
			return h.Status(), h.failureFor(errors.New("Run context operation timed out"), lifecycle.ErrorProcessCleanupFailed, "dsh-work could not finish the Run context operation.", true)
		}
	}
}

func (h *Host) stopRun(ctx context.Context, run *generationRun) *lifecycle.Failure {
	status, err := h.machine.BeginStop(run.generation)
	if err != nil && status.State != lifecycle.StateStopping {
		if status.Error != nil {
			failure := *status.Error
			return &failure
		}
		return h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not stop the previous Worker generation.", true)
	}
	h.emit(status)
	run.cancel()
	select {
	case <-run.done:
	case <-ctx.Done():
		return h.failureFor(ctx.Err(), lifecycle.ErrorProcessCleanupFailed, "dsh-work could not verify the previous Worker cleanup.", true)
	}
	if run.cleanupPending() {
		if failure := h.cleanupWorker(run, run.getWorker()); failure != nil {
			return failure
		}
		if _, err := h.machine.BeginStop(run.generation); err == nil {
			status, completeErr := h.machine.CompleteStop(run.generation, nil)
			if completeErr == nil {
				h.emit(status)
			}
		}
		h.mu.Lock()
		if h.current == run {
			h.current = nil
		}
		h.mu.Unlock()
	}
	status = h.Status()
	if status.State == lifecycle.StateFailed && status.Error != nil {
		failure := *status.Error
		return &failure
	}
	return nil
}

func (h *Host) finishContextSwitchFailure(manager RunContextManager, candidateFailure, rollbackFailure error) (dshmanager.Snapshot, error) {
	failure := h.failureFor(rollbackFailure, lifecycle.ErrorProcessStartFailed, "dsh-work could not restore the known-good Run context.", true)
	failure.Retryable = true
	if candidateFailure != nil {
		candidate := h.failureFor(candidateFailure, lifecycle.ErrorDSHStartFailed, "The candidate Run context could not start.", true)
		if candidate.Code != failure.Code {
			if failure.Detail == "" {
				failure.Detail = fmt.Sprintf("Rollback failed after candidate error %s.", candidate.Code)
			} else {
				failure.Detail = fmt.Sprintf("Rollback failed after candidate error %s. %s", candidate.Code, failure.Detail)
			}
		}
	}
	if failure.Detail == "" {
		failure.Detail = "The previous Run context was retained for a later recovery attempt."
	}
	status := h.Status()
	if status.State == lifecycle.StateFailed && status.GenerationID != "" {
		if updated, err := h.machine.ReplaceFailure(status.GenerationID, *failure); err == nil {
			status = updated
			h.emit(status)
		}
	}
	snapshot, err := manager.Snapshot(context.Background())
	if err != nil {
		return snapshot, failure
	}
	return snapshot, failure
}

func (h *Host) Cancel() lifecycle.Status {
	return h.requestStop(false)
}

func (h *Host) InstallPlugin(ctx context.Context, request dshmanager.PluginInstallRequest) (dshmanager.PluginResult, error) {
	return h.applyPlugin(ctx, request.Target, request.Package, "add")
}
func (h *Host) RemovePlugin(ctx context.Context, request dshmanager.PluginRemoveRequest) (dshmanager.PluginResult, error) {
	return h.applyPlugin(ctx, request.Target, request.Package, "remove")
}
func (h *Host) UpgradePlugin(ctx context.Context, request dshmanager.PluginUpgradeRequest) (dshmanager.PluginResult, error) {
	return h.applyPlugin(ctx, request.Target, request.Package, "update")
}
func (h *Host) applyPlugin(ctx context.Context, target dshmanager.PluginTarget, spec, operation string) (dshmanager.PluginResult, error) {
	if failure := h.readyWorkerForPluginMutation(); failure != nil {
		return dshmanager.PluginResult{}, failure
	}
	manager, ok := h.deps.Manager.(interface {
		ApplyPlugin(context.Context, dshmanager.ResolvedLaunch, string, string) (dshmanager.PluginResult, error)
	})
	if !ok {
		return dshmanager.PluginResult{}, errors.New("transactional plugin manager is unavailable")
	}
	snapshot, err := h.deps.Manager.Snapshot(ctx)
	if err != nil {
		return dshmanager.PluginResult{}, err
	}
	if snapshot.Current == nil || snapshot.Current.Profile != target.Profile {
		return dshmanager.PluginResult{}, h.failureFor(errors.New("switch to this profile before changing plugins"), lifecycle.ErrorManagerOperationBusy, "The current profile is unavailable for plugin changes.", true)
	}
	var result dshmanager.PluginResult
	_, err = h.applyRunContext(ctx, *snapshot.Current, func(ctx context.Context, launch dshmanager.ResolvedLaunch) error {
		var err error
		result, err = manager.ApplyPlugin(ctx, launch, spec, operation)
		return err
	}, nil, false)
	result.RestartRequired = false
	return result, err
}

func (h *Host) readyWorkerForPluginMutation() *lifecycle.Failure {
	if h.isShutdownRequested() {
		return h.failureFor(errors.New("dsh-work shutdown is in progress"), lifecycle.ErrorManagerOperationBusy, "dsh-work is finishing the current Run context.", true)
	}
	if h.isSwitching() {
		return h.failureFor(errors.New("Run context switching is in progress"), lifecycle.ErrorManagerOperationBusy, "dsh-work is finishing the current Run context.", true)
	}
	if h.Status().State != lifecycle.StateReady {
		return h.failureFor(errors.New("profile plugins require a Ready Worker"), lifecycle.ErrorManagerOperationBusy, "Profile plugins can be changed only while the current Run context is Ready.", true)
	}
	run := h.activeRun()
	if run == nil {
		return h.failureFor(errors.New("the Ready Worker is not owned by the Host"), lifecycle.ErrorManagerOperationBusy, "Profile plugins can be changed only while the current Run context is Ready.", true)
	}
	worker := run.getWorker()
	if worker == nil {
		return h.failureFor(errors.New("the Ready Worker is unavailable"), lifecycle.ErrorManagerOperationBusy, "Profile plugins can be changed only while the current Run context is Ready.", true)
	}
	select {
	case <-worker.Exited():
		return h.failureFor(errors.New("the Ready Worker has exited"), lifecycle.ErrorManagerOperationBusy, "dsh-work is finishing the current Run context.", true)
	default:
		return nil
	}
}

// Restart converges an active generation through the normal cleanup boundary
// before starting another one. If cleanup cannot be verified, the existing
// generation remains the cleanup owner and no overlapping Worker is created.
func (h *Host) Restart() lifecycle.Status {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	if h.isShutdownRequested() {
		return h.Status()
	}
	h.mu.Lock()
	if h.switching {
		status := h.machine.Snapshot()
		h.mu.Unlock()
		return status
	}
	h.mu.Unlock()
	run := h.activeRun()
	if run == nil {
		return h.start(workspacecontext.Request{})
	}
	status := h.requestStop(false)
	timer := time.NewTimer(h.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-run.done:
		if run.cleanupPending() || h.isShutdownRequested() {
			return h.Status()
		}
		return h.start(workspacecontext.Request{})
	case <-timer.C:
		return status
	}
}

// SwitchRunContext performs one serialized replacement of the complete
// runtime/data-directory/profile tuple. The returned snapshot is always the
// post-transaction manager view; when the candidate fails after rollback, the
// error is the terminal switch result and the configured context remains the
// rollback source.
func (h *Host) SwitchRunContext(ctx context.Context, target dshmanager.RunContext) (dshmanager.Snapshot, error) {
	return h.applyRunContext(ctx, target, nil, nil, false)
}

func (h *Host) applyRunContext(ctx context.Context, target dshmanager.RunContext, mutate func(context.Context, dshmanager.ResolvedLaunch) error, startupFailure *lifecycle.Failure, restore bool) (result dshmanager.Snapshot, resultErr error) {
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	return h.applyRunContextLocked(ctx, target, mutate, startupFailure, restore)
}

func (h *Host) applyRunContextLocked(ctx context.Context, target dshmanager.RunContext, mutate func(context.Context, dshmanager.ResolvedLaunch) error, startupFailure *lifecycle.Failure, restore bool) (result dshmanager.Snapshot, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	manager, ok := h.deps.Manager.(RunContextManager)
	if !ok {
		return dshmanager.Snapshot{}, h.failureFor(errors.New("Run context manager is unavailable"), lifecycle.ErrorManagerStateInvalid, "dsh-work could not switch its Run context.", true)
	}

	if startupFailure != nil && h.Status().State != lifecycle.StateFailed {
		return manager.Snapshot(context.Background())
	}
	if mutate != nil {
		if failure := h.readyWorkerForPluginMutation(); failure != nil {
			return dshmanager.Snapshot{}, failure
		}
		snapshot, err := manager.Snapshot(ctx)
		if err != nil {
			return dshmanager.Snapshot{}, err
		}
		if snapshot.Current == nil || *snapshot.Current != target {
			return dshmanager.Snapshot{}, errors.New("the current Run context changed")
		}
	}
	h.mu.Lock()
	if h.switching || h.shutdownRequested {
		h.mu.Unlock()
		return dshmanager.Snapshot{}, h.failureFor(lifecycle.Failure{
			Code:    lifecycle.ErrorManagerOperationBusy,
			Summary: "Another Run context switch is already in progress.",
		}, lifecycle.ErrorManagerOperationBusy, "dsh-work is already switching its Run context.", true)
	}
	h.switching = true
	knownGood := h.knownGoodRunContextLocked()
	automaticRollback := true
	if h.deps.AutomaticRuntimeRollback != nil {
		automaticRollback = h.deps.AutomaticRuntimeRollback()
	}
	h.mu.Unlock()

	switchTimeout := h.config.ShutdownTimeout +
		2*(h.config.ReadinessTimeout+h.config.GracefulStopTimeout+h.config.ForceStopTimeout+h.config.EmptyTimeout)
	switchCtx, cancelSwitch := context.WithTimeout(ctx, switchTimeout)
	defer cancelSwitch()
	if err := manager.BeginRunContextSwitch(switchCtx); err != nil {
		h.setSwitching(false)
		return dshmanager.Snapshot{}, err
	}
	h.setManagerSwitchGuarded(true)
	defer func() {
		keepGuard := false
		status := h.Status()
		if run := h.activeRun(); run != nil && run.cleanupPending() &&
			(run.ctx.Err() != nil || status.State == lifecycle.StateStopping || status.State == lifecycle.StateFailed) {
			// A failed cleanup must keep the manager mutation guard after the
			// enclosing switch ends. The cleanup owner will release it only after
			// the Worker boundary is verified empty and closed.
			run.setManagerGuarded(true)
			keepGuard = true
		}
		if !keepGuard {
			if err := manager.EndRunContextSwitch(context.Background()); err != nil {
				releaseFailure := h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "dsh-work could not release the Run context operation guard.", true)
				if resultErr == nil {
					resultErr = releaseFailure
				} else {
					resultErr = errors.Join(resultErr, releaseFailure)
				}
			}
		}
		h.setManagerSwitchGuarded(false)
		h.setSwitching(false)
	}()
	var resolved dshmanager.ResolvedLaunch
	var candidateFailure *lifecycle.Failure
	var candidateStatus lifecycle.Status
	activeRun := h.activeRun()
	if startupFailure == nil && !restore {
		var err error
		resolved, err = manager.ResolveLaunch(switchCtx, dshmanager.LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
		if err != nil {
			if activeRun != nil {
				return h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptResolving, err, automaticRollback, dshmanager.RollbackNotNeeded, nil, false)
			}
			candidateFailure = h.failureFor(err, lifecycle.ErrorDSHStartFailed, "The candidate Run context could not be resolved.", true)
		}
	} else {
		candidateFailure = startupFailure
	}
	if activeRun != nil {
		if failure := h.stopRun(switchCtx, activeRun); failure != nil {
			snapshot, _ := manager.Snapshot(context.Background())
			return snapshot, failure
		}
	}
	if candidateFailure == nil && restore {
		if recovery, ok := manager.(interface {
			RestoreHealthy(context.Context) (dshmanager.ResolvedLaunch, error)
		}); ok {
			var err error
			resolved, err = recovery.RestoreHealthy(switchCtx)
			if err != nil {
				candidateFailure = h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "The healthy environment could not be restored.", true)
			}
		} else {
			var err error
			resolved, err = manager.ResolveLaunch(switchCtx, dshmanager.LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
			if err != nil {
				candidateFailure = h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "The healthy environment could not be resolved.", true)
			}
		}
	}
	if candidateFailure == nil && mutate != nil {
		if err := mutate(switchCtx, resolved); err != nil {
			candidateFailure = h.failureFor(err, lifecycle.ErrorPluginCommandFailed, "The plugin change could not be applied.", true)
		}
	}
	if candidateFailure == nil && !restore {
		if preparer, ok := manager.(RunContextPreparer); ok {
			dshadapter.ReportCommandOutput(switchCtx, "Preparing the selected profile…")
			if err := preparer.PrepareRunContext(switchCtx, resolved); err != nil {
				candidateFailure = h.failureFor(err, lifecycle.ErrorProfilePreparationFailed, "The candidate DSH profile could not be prepared.", true)
			}
		}
	}
	if candidateFailure == nil {
		var beginErr error
		dshadapter.ReportCommandOutput(switchCtx, "Starting the candidate and checking health…")
		candidateStatus, candidateFailure, beginErr = h.runResolvedContext(switchCtx, resolved)
		if beginErr != nil {
			// The previous Worker has already been stopped at this point. Treat a
			// candidate generation-boundary failure exactly like any other
			// candidate startup failure so the known-good context is restored.
			candidateFailure = h.failureFor(beginErr, lifecycle.ErrorDSHStartFailed, "The candidate Run context could not start.", true)
		}
	}
	if candidateFailure == nil && candidateStatus.State == lifecycle.StateReady {
		snapshot, snapshotErr := manager.ClearSwitchAttempt(context.Background())
		if snapshotErr != nil {
			return dshmanager.Snapshot{}, snapshotErr
		}
		return snapshot, nil
	}
	if candidateFailure == nil {
		candidateFailure = failureFromStatus(candidateStatus)
	}
	if !automaticRollback {
		return h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptCandidate, candidateFailure, false, dshmanager.RollbackDisabled, nil, true)
	}
	if knownGood == nil {
		knownGood = knownGoodRunContext(manager)
	}
	if knownGood == nil {
		return h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptCandidate, candidateFailure, true, dshmanager.RollbackUnavailable, nil, false)
	}

	if run := h.activeRun(); run != nil && run.cleanupPending() {
		return h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptRollback, candidateFailure, true, dshmanager.RollbackFailed, errors.New("candidate cleanup has not completed"), false)
	}
	rollbackCtx, cancelRollback := context.WithTimeout(context.WithoutCancel(ctx), switchTimeout)
	defer cancelRollback()
	dshadapter.ReportCommandOutput(rollbackCtx, "Candidate failed: "+candidateFailure.Summary)
	dshadapter.ReportCommandOutput(rollbackCtx, "Restoring the previous healthy environment…")
	var rollbackLaunch dshmanager.ResolvedLaunch
	var rollbackErr error
	if recovery, ok := manager.(interface {
		RestoreHealthy(context.Context) (dshmanager.ResolvedLaunch, error)
	}); ok {
		rollbackLaunch, rollbackErr = recovery.RestoreHealthy(rollbackCtx)
	} else {
		rollbackLaunch, rollbackErr = manager.ResolveLaunch(rollbackCtx, dshmanager.LaunchRequest{RuntimeID: knownGood.RuntimeID, Node: knownGood.Node, Profile: knownGood.Profile})
	}
	if rollbackErr != nil {
		_, _ = h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptRollback, candidateFailure, true, dshmanager.RollbackFailed, rollbackErr, false)
		return h.finishContextSwitchFailure(manager, candidateFailure, rollbackErr)
	}
	rollbackStatus, rollbackFailure, beginErr := h.runResolvedContext(rollbackCtx, rollbackLaunch)
	if beginErr != nil {
		_, _ = h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptRollback, candidateFailure, true, dshmanager.RollbackFailed, beginErr, false)
		return h.finishContextSwitchFailure(manager, candidateFailure, beginErr)
	}
	if rollbackFailure != nil || rollbackStatus.State != lifecycle.StateReady {
		if rollbackFailure == nil {
			rollbackFailure = failureFromStatus(rollbackStatus)
		}
		_, _ = h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptRollback, candidateFailure, true, dshmanager.RollbackFailed, rollbackFailure, false)
		return h.finishContextSwitchFailure(manager, candidateFailure, rollbackFailure)
	}
	dshadapter.ReportCommandOutput(rollbackCtx, "The previous healthy environment is running.")
	return h.recordSwitchAttempt(manager, target, dshmanager.SwitchAttemptCandidate, candidateFailure, true, dshmanager.RollbackRestored, nil, false)
}

func (h *Host) recordSwitchAttempt(manager RunContextManager, target dshmanager.RunContext, stage dshmanager.SwitchAttemptStage, candidateFailure error, automaticRollback bool, rollback dshmanager.RollbackOutcome, rollbackFailure error, configureTarget bool) (dshmanager.Snapshot, error) {
	failure := h.failureFor(candidateFailure, lifecycle.ErrorDSHStartFailed, "The candidate Run context could not start.", true)
	attempt := dshmanager.SwitchAttempt{
		Target: target, Stage: stage, Failure: *failure,
		AutomaticRollback: automaticRollback, Rollback: rollback,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if rollbackFailure != nil {
		projected := h.failureFor(rollbackFailure, lifecycle.ErrorProcessStartFailed, "dsh-work could not restore the known-good Run context.", true)
		attempt.RollbackFailure = projected
	}
	snapshot, err := manager.RecordSwitchAttempt(context.Background(), attempt, configureTarget)
	if err != nil {
		return snapshot, errors.Join(failure, err)
	}
	return snapshot, failure
}

func (h *Host) RetryLastSwitch(ctx context.Context) (dshmanager.Snapshot, error) {
	manager, ok := h.deps.Manager.(RunContextManager)
	if !ok {
		return dshmanager.Snapshot{}, errors.New("Run context manager is unavailable")
	}
	snapshot, err := manager.Snapshot(ctx)
	if err != nil {
		return snapshot, err
	}
	if snapshot.LastSwitchAttempt == nil {
		return snapshot, lifecycle.Failure{Code: lifecycle.ErrorManagerStateInvalid, Summary: "There is no failed Run context to retry.", CorrelationID: lifecycle.NewCorrelationID()}
	}
	return h.SwitchRunContext(ctx, snapshot.LastSwitchAttempt.Target)
}

func (h *Host) RestoreKnownGood(ctx context.Context) (dshmanager.Snapshot, error) {
	manager, ok := h.deps.Manager.(RunContextManager)
	if !ok {
		return dshmanager.Snapshot{}, errors.New("Run context manager is unavailable")
	}
	snapshot, err := manager.Snapshot(ctx)
	if err != nil {
		return snapshot, err
	}
	if snapshot.KnownGood == nil {
		return snapshot, lifecycle.Failure{Code: lifecycle.ErrorManagerStateInvalid, Summary: "There is no known-good Run context to restore.", CorrelationID: lifecycle.NewCorrelationID()}
	}
	return h.applyRunContext(ctx, *snapshot.KnownGood, nil, nil, true)
}

func (h *Host) runResolvedContext(ctx context.Context, resolved dshmanager.ResolvedLaunch) (lifecycle.Status, *lifecycle.Failure, error) {
	// The Host owns a generation beyond the request that made it ready.
	// waitForRun cancels and drains startup when the operation is cancelled;
	// after readiness, only Host lifecycle actions should stop this Worker.
	run, status, err := h.beginResolvedRun(context.WithoutCancel(ctx), resolved)
	if err != nil {
		return status, h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not start the Run context.", true), err
	}
	h.emit(status)
	go h.run(run)
	status, failure := h.waitForRun(ctx, run)
	return status, failure, nil
}

func (h *Host) Quit() lifecycle.Status {
	h.setShutdownRequested(true)
	h.switchMu.Lock()
	status := h.requestStop(true)
	h.switchMu.Unlock()
	if quit := h.quitHandler(); quit != nil {
		quit()
	}
	return status
}

// ShutdownForApp is called from the Wails application shutdown hook. It is
// not part of the frontend service surface.
func (h *Host) ShutdownForApp() error {
	h.shutdownMu.Lock()
	defer h.shutdownMu.Unlock()
	h.setShutdownRequested(true)

	// Do not let the Wails shutdown hook return while a context transaction can
	// still create a candidate or rollback Worker.
	h.switchMu.Lock()
	defer h.switchMu.Unlock()
	shutdownComplete := false
	defer func() {
		if !shutdownComplete {
			h.setShutdownRequested(false)
		}
	}()

	run := h.activeRun()
	if run == nil {
		shutdownComplete = true
		return nil
	}
	select {
	case <-run.done:
		if run.cleanupPending() {
			err := h.shutdownPendingRun(run)
			shutdownComplete = err == nil
			return err
		}
		shutdownComplete = true
		return nil
	default:
	}
	h.requestStop(false)
	timer := time.NewTimer(h.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-run.done:
		if run.cleanupPending() {
			err := h.shutdownPendingRun(run)
			shutdownComplete = err == nil
			return err
		}
		shutdownComplete = true
		return nil
	case <-timer.C:
		return h.failureFor(context.DeadlineExceeded, lifecycle.ErrorProcessCleanupFailed, "dsh-work could not finish host shutdown.", true)
	}
}

func (h *Host) shutdownPendingRun(run *generationRun) error {
	if _, err := h.machine.BeginStop(run.generation); err == nil {
		h.emit(h.Status())
	}
	failure := h.cleanupWorker(run, run.getWorker())
	if failure != nil {
		h.finish(run, failure)
		return failure
	}
	h.releaseManagerStopGuard(run)
	status, err := h.machine.CompleteStop(run.generation, nil)
	if err != nil {
		return err
	}
	h.mu.Lock()
	if h.current == run {
		h.current = nil
	}
	h.mu.Unlock()
	h.emit(status)
	return nil
}

func (h *Host) requestStop(_ bool) lifecycle.Status {
	h.mu.Lock()
	if h.switching {
		status := h.machine.Snapshot()
		h.mu.Unlock()
		return status
	}
	h.mu.Unlock()
	run := h.activeRun()
	if run == nil {
		return h.Status()
	}
	h.workerBoundaryMu.Lock()
	defer h.workerBoundaryMu.Unlock()
	status, err := h.machine.BeginStop(run.generation)
	if err != nil {
		return h.Status()
	}
	h.emit(status)
	// A startup commit can hold the manager operation gate. Cancel it before
	// waiting for that gate; cleanup stays behind the Worker boundary mutex.
	run.cancel()
	h.beginManagerStopGuard(run)
	return status
}

func (h *Host) activeRun() *generationRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.current
}

// Ordinary startup and Restart enter the same bounded recovery tail as a switch.
func (h *Host) runStartup(run *generationRun) {
	go h.run(run)
	select {
	case <-run.ready:
		if h.Status().State == lifecycle.StateReady {
			return
		}
		<-run.done
	case <-run.done:
	}
	status := h.Status()
	if status.State != lifecycle.StateFailed || run.cleanupPending() || h.isShutdownRequested() || h.deps.Manager == nil {
		return
	}
	snapshot, err := h.deps.Manager.Snapshot(context.Background())
	if err != nil || snapshot.Configured == nil || snapshot.KnownGood == nil {
		return
	}
	_, _ = h.applyRunContext(context.Background(), *snapshot.Configured, nil, failureFromStatus(status), false)
}

func (h *Host) run(run *generationRun) {
	defer func() {
		run.cancel()
		close(run.done)
	}()

	worker, failure := h.startWorker(run)
	if failure != nil {
		h.finish(run, failure)
		return
	}
	if worker == nil {
		h.finish(run, nil)
		return
	}
	run.setWorker(worker)

	announcement, failure := h.waitReady(run, worker)
	if failure != nil {
		cleanupFailure := h.cleanupWorker(run, worker)
		if cleanupFailure != nil {
			failure = cleanupFailure
		} else if failure.Code == lifecycle.ErrorDSHEarlyExit {
			run.mu.RLock()
			launch := cloneResolvedLaunch(run.launch)
			run.mu.RUnlock()
			stderr := worker.Diagnostics().StderrTail
			if launch != nil && strings.Contains(stderr, "ERR_MODULE_NOT_FOUND") && strings.Contains(strings.ReplaceAll(stderr, "\\", "/"), filepath.ToSlash(filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name))+"/") {
				failure.Code = lifecycle.ErrorProfilePreparationFailed
				failure.Summary = "The selected profile dependencies could not be loaded."
				failure.Detail = "DSH reported ERR_MODULE_NOT_FOUND while loading the selected profile."
			}
		}
		h.finish(run, failure)
		return
	}

	if run.ctx.Err() != nil {
		cleanupFailure := h.cleanupWorker(run, worker)
		h.finish(run, cleanupFailure)
		return
	}
	if failure := h.commitReady(run); failure != nil {
		if run.ctx.Err() != nil {
			failure = nil
		}
		cleanupFailure := h.cleanupWorker(run, worker)
		if cleanupFailure != nil {
			failure = cleanupFailure
		}
		h.finish(run, failure)
		return
	}

	select {
	case <-run.ctx.Done():
		cleanupFailure := h.cleanupWorker(run, worker)
		h.finish(run, cleanupFailure)
	case <-worker.Exited():
		cleanupFailure := h.cleanupWorker(run, worker)
		if cleanupFailure != nil {
			h.finish(run, cleanupFailure)
			return
		}
		failure := lifecycle.Failure{
			Code:           lifecycle.ErrorDSHEarlyExit,
			Summary:        "The DSH workspace stopped unexpectedly.",
			Retryable:      true,
			EffectOccurred: true,
		}
		_ = announcement
		h.finish(run, &failure)
	}
}

func (h *Host) startWorker(run *generationRun) (supervisor.Worker, *lifecycle.Failure) {
	if err := h.setPhase(run, lifecycle.PhaseConfiguration); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not enter configuration.", false)
	}
	if h.configError != nil {
		return nil, h.failureFor(h.configError, lifecycle.ErrorManagerStateInvalid, "dsh-work could not locate its per-user application-data directory.", false)
	}
	if h.deps.ManagerError != nil {
		return nil, h.failureFor(h.deps.ManagerError, lifecycle.ErrorManagerStateInvalid, "dsh-work could not load its DSH Run context manager.", true)
	}
	if h.deps.PlatformError != nil {
		return nil, h.failureFor(h.deps.PlatformError, lifecycle.ErrorPlatformUnsupported, "This platform does not have a native dsh-work process adapter.", false)
	}
	if h.deps.Manager == nil {
		return nil, h.failureFor(errors.New("Run context manager is unavailable"), lifecycle.ErrorManagerStateInvalid, "dsh-work could not load its DSH Run context manager.", true)
	}
	if h.deps.DSH == nil {
		return nil, h.failureFor(errors.New("DSH adapter is unavailable"), lifecycle.ErrorPlatformUnsupported, "The native DSH adapter is unavailable.", false)
	}
	if h.deps.Supervisor == nil {
		return nil, h.failureFor(errors.New("process supervisor is unavailable"), lifecycle.ErrorPlatformUnsupported, "The native process supervisor is unavailable.", false)
	}
	if h.deps.Gateway == nil {
		return nil, h.failureFor(errors.New("trusted DSH workspace gateway is unavailable"), lifecycle.ErrorGatewayUnavailable, "The trusted DSH workspace gateway is unavailable.", false)
	}
	if err := h.setPhase(run, lifecycle.PhaseNode); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not enter runtime discovery.", false)
	}
	workspace, failure := h.resolveWorkspace(run)
	if failure != nil {
		return nil, failure
	}
	launch, failure := h.resolveLaunch(run)
	if failure != nil {
		return nil, failure
	}
	if launch.resolved != nil {
		run.setLaunch(launch.resolved)
		if status, err := h.machine.SetLaunchSelection(run.generation, launchSelection(*launch.resolved)); err == nil {
			h.emit(status)
		}
	}
	launch.workspace = workspace
	if workspace.State == workspacecontext.StateSelected && filepath.Clean(workspace.Path) == filepath.Clean(launch.dataDirectory.Path) {
		return nil, h.failureFor(errors.New("Workspace path must be separate from the DSH data directory"), lifecycle.ErrorWorkspaceInvalid, "dsh-work could not use the DSH data directory as a Workspace.", false)
	}
	if status, err := h.machine.SetWorkspaceContext(run.generation, workspace); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorWorkspaceInvalid, "dsh-work could not record the DSH Workspace context.", false)
	} else {
		h.emit(status)
	}
	if launch.dataDirectory.Path == "" {
		return nil, h.failureFor(errors.New("DSH data directory is empty"), lifecycle.ErrorDSHStartFailed, "dsh-work could not prepare the selected DSH data directory.", false)
	}
	if launch.dataDirectory.Ownership == dshmanager.DataDirectoryOwnershipUser {
		info, err := os.Stat(launch.dataDirectory.Path)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("selected user DSH data directory is not a directory")
			}
			return nil, h.failureFor(err, lifecycle.ErrorProfileNotFound, "The selected user DSH data directory is unavailable.", false)
		}
	} else if err := os.MkdirAll(launch.dataDirectory.Path, 0o700); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "dsh-work could not prepare the selected DSH data directory.", true)
	}
	if h.config.BootstrapDirectory == "" {
		return nil, h.failureFor(errors.New("dsh-work bootstrap directory is empty"), lifecycle.ErrorDSHStartFailed, "dsh-work could not prepare the DSH bootstrap directory.", false)
	}
	if err := os.MkdirAll(h.config.BootstrapDirectory, 0o700); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "dsh-work could not prepare the DSH bootstrap directory.", true)
	}
	if err := h.checkCancelled(run); err != nil {
		return nil, nil
	}

	port, err := dshadapter.AllocateLoopbackPort()
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "dsh-work could not allocate a loopback port for DSH.", true)
	}
	plan, err := h.deps.DSH.BuildLaunchPlan(dshadapter.LaunchContext{
		SafeMode:           launch.dataDirectory.ID == dshmanager.SafeModeDataDirectoryID,
		GenerationID:       run.generation,
		Runtime:            launch.runtime,
		BootstrapDirectory: h.config.BootstrapDirectory,
		DataDirectory:      launch.dataDirectory.Path,
		Profile:            launch.profile,
		Workspace:          launch.workspace,
		Port:               port,
	})
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "dsh-work could not construct the DSH launch plan.", false)
	}
	if err := h.setPhase(run, lifecycle.PhaseWorker); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not enter worker startup.", false)
	}
	if err := h.checkCancelled(run); err != nil {
		return nil, nil
	}
	readiness := make(chan dshadapter.ReadyAnnouncement, 8)
	rawHandler := func(_ supervisor.OutputStream, text string) {
		candidate, ok := h.deps.DSH.ParseReadyAnnouncement(text)
		if !ok {
			return
		}
		select {
		case readiness <- candidate:
		default:
			// A noisy process cannot block the native output reader. The Host
			// consumes the first bounded set of structured candidates.
		}
	}
	worker, err := h.deps.Supervisor.Start(run.ctx, plan, rawHandler)
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorProcessStartFailed, "dsh-work could not start the managed DSH process.", true)
	}
	run.mu.Lock()
	run.plan = plan
	run.readiness = readiness
	run.workspace = launch.workspace
	run.mu.Unlock()
	h.debugf("managed DSH worker started for expected port %d", plan.ExpectedPort)
	return worker, nil
}

func (h *Host) resolveWorkspace(run *generationRun) (workspacecontext.Context, *lifecycle.Failure) {
	resolver := h.deps.WorkspaceResolver
	if resolver == nil {
		resolver = workspacecontext.SurfaceResolver{}
	}
	run.mu.RLock()
	request := run.workspaceRequest
	run.mu.RUnlock()
	workspace, err := resolver.Resolve(run.ctx, run.generation, request)
	if err != nil {
		if run.ctx.Err() != nil {
			return workspacecontext.Context{}, nil
		}
		return workspacecontext.Context{}, h.failureFor(err, lifecycle.ErrorWorkspaceInvalid, "dsh-work could not resolve the DSH Workspace context.", false)
	}
	if err := workspace.ValidateForGeneration(run.generation); err != nil {
		return workspacecontext.Context{}, h.failureFor(err, lifecycle.ErrorWorkspaceInvalid, "dsh-work received an invalid DSH Workspace context.", false)
	}
	return workspace, nil
}

func (h *Host) resolveLaunch(run *generationRun) (hostLaunch, *lifecycle.Failure) {
	run.mu.RLock()
	prepared := cloneResolvedLaunch(run.launch)
	run.mu.RUnlock()
	if prepared != nil {
		target := prepared.Target
		return hostLaunch{
			runtime:       runtimeAdapterValue(prepared.Runtime, prepared.Node),
			dataDirectory: prepared.DataDirectory,
			profile:       target.Profile.Name,
			resolved:      prepared,
		}, nil
	}
	if recovery, ok := h.deps.Manager.(interface {
		ResumeRecovery(context.Context) (*dshmanager.ResolvedLaunch, error)
	}); ok {
		restored, err := recovery.ResumeRecovery(run.ctx)
		if err != nil {
			return hostLaunch{}, h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "The interrupted recovery could not be resumed.", true)
		}
		if restored != nil {
			return hostLaunch{runtime: runtimeAdapterValue(restored.Runtime, restored.Node), dataDirectory: restored.DataDirectory, profile: restored.Target.Profile.Name, resolved: restored}, nil
		}
	}
	snapshot, err := h.deps.Manager.Snapshot(run.ctx)
	if err != nil {
		return hostLaunch{}, h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "dsh-work could not read the DSH launch selection.", false)
	}
	if snapshot.Configured == nil {
		return hostLaunch{}, h.failureFor(lifecycle.Failure{
			Code:    lifecycle.ErrorProfileRequired,
			Summary: "Choose a DSH runtime and profile before starting dsh-work.",
		}, lifecycle.ErrorProfileRequired, "Choose a DSH runtime and profile before starting dsh-work.", false)
	}
	resolve := h.deps.Manager.ResolveLaunch
	if startup, ok := h.deps.Manager.(interface {
		ResolveStartupLaunch(context.Context, dshmanager.LaunchRequest) (dshmanager.ResolvedLaunch, error)
	}); ok {
		resolve = startup.ResolveStartupLaunch
	}
	resolved, err := resolve(dshmanager.WithLaunchProgress(run.ctx, func(phase lifecycle.Phase) { _ = h.setPhase(run, phase) }), dshmanager.LaunchRequest{
		RuntimeID: snapshot.Configured.RuntimeID,
		Node:      snapshot.Configured.Node,
		Profile:   snapshot.Configured.Profile,
	})
	if err != nil {
		return hostLaunch{}, h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "dsh-work could not resolve the selected DSH runtime and profile.", false)
	}
	runtime := runtimeAdapterValue(resolved.Runtime, resolved.Node)
	if verifier, ok := h.deps.DSH.(interface {
		DiscoverSelectedPathWithEnvironment(context.Context, string, string, map[string]string) (dshadapter.Runtime, error)
	}); ok {
		verified, verifyErr := verifier.DiscoverSelectedPathWithEnvironment(run.ctx, runtime.Path, resolved.Runtime.Version, runtime.Env)
		if verifyErr != nil {
			if run.ctx.Err() != nil {
				return hostLaunch{}, nil
			}
			return hostLaunch{}, h.failureFor(verifyErr, lifecycle.ErrorDSHRuntimeNotFound, "dsh-work could not verify the selected DSH runtime.", false)
		}
		runtime = dshadapter.Runtime{Path: verified.Path, Version: verified.Version, Env: runtime.Env}
	} else if verifier, ok := h.deps.DSH.(interface {
		DiscoverPathWithEnvironment(context.Context, string, map[string]string) (dshadapter.Runtime, error)
	}); ok {
		verified, verifyErr := verifier.DiscoverPathWithEnvironment(run.ctx, runtime.Path, runtime.Env)
		if verifyErr != nil {
			if run.ctx.Err() != nil {
				return hostLaunch{}, nil
			}
			return hostLaunch{}, h.failureFor(verifyErr, lifecycle.ErrorDSHRuntimeNotFound, "dsh-work could not verify the selected DSH runtime.", false)
		}
		if verified.Version != resolved.Runtime.Version {
			return hostLaunch{}, h.failureFor(lifecycle.Failure{
				Code:    lifecycle.ErrorDSHUnsupportedVersion,
				Summary: "The selected DSH runtime does not match the catalog.",
			}, lifecycle.ErrorDSHUnsupportedVersion, "The selected DSH runtime does not match the catalog.", false)
		}
		runtime = dshadapter.Runtime{Path: verified.Path, Version: verified.Version, Env: runtime.Env}
	} else if verifier, ok := h.deps.DSH.(interface {
		DiscoverPath(context.Context, string) (dshadapter.Runtime, error)
	}); ok {
		verified, verifyErr := verifier.DiscoverPath(run.ctx, runtime.Path)
		if verifyErr != nil {
			if run.ctx.Err() != nil {
				return hostLaunch{}, nil
			}
			return hostLaunch{}, h.failureFor(verifyErr, lifecycle.ErrorDSHRuntimeNotFound, "dsh-work could not verify the selected DSH runtime.", false)
		}
		if verified.Version != resolved.Runtime.Version {
			return hostLaunch{}, h.failureFor(lifecycle.Failure{
				Code:    lifecycle.ErrorDSHUnsupportedVersion,
				Summary: "The selected DSH runtime does not match the catalog.",
			}, lifecycle.ErrorDSHUnsupportedVersion, "The selected DSH runtime does not match the catalog.", false)
		}
		runtime = dshadapter.Runtime{Path: verified.Path, Version: verified.Version, Env: runtime.Env}
	}
	target := resolved.Target
	return hostLaunch{
		runtime:       runtime,
		dataDirectory: resolved.DataDirectory,
		profile:       target.Profile.Name,
		resolved:      &resolved,
	}, nil
}

func launchSelection(launch dshmanager.ResolvedLaunch) lifecycle.LaunchSelection {
	nodeID := launch.Node.Selection.InstallationID
	if launch.Node.Selection.Kind == dshmanager.NodeSelectionSystem {
		nodeID = "system"
	}
	return lifecycle.LaunchSelection{
		RuntimeID: launch.Target.RuntimeID, RuntimeVersion: launch.Runtime.Version, RuntimePath: launch.Runtime.Path,
		NodeID: nodeID, NodeVersion: launch.Node.Version, NodePath: launch.Node.NodePath,
		ProfileName: launch.Target.Profile.Name, DataDirectoryPath: launch.DataDirectory.Path,
	}
}

func runtimeAdapterValue(runtime dshmanager.RuntimeInfo, node dshmanager.ResolvedNode) dshadapter.Runtime {
	env := make(map[string]string, len(node.ChildEnvironment))
	for key, value := range node.ChildEnvironment {
		env[key] = value
	}
	return dshadapter.Runtime{Path: runtime.Path, Version: runtime.Version, Env: env}
}

func (h *Host) commitReady(run *generationRun) *lifecycle.Failure {
	run.mu.RLock()
	launch := cloneResolvedLaunch(run.launch)
	handoffURL := run.readyHandoffURL
	workspaceURL := run.readyWorkspaceURL
	run.mu.RUnlock()
	target := run.targetCopy()
	if launch != nil {
		target = &launch.Target
	}
	if target != nil {
		manager, ok := h.deps.Manager.(RunContextManager)
		if !ok {
			return h.failureFor(errors.New("Run context commit boundary is unavailable"), lifecycle.ErrorManagerStateInvalid, "dsh-work could not publish the ready Run context.", true)
		}
		var commitErr error
		if healthy, ok := manager.(interface {
			CommitHealthy(context.Context, dshmanager.ResolvedLaunch) (dshmanager.Snapshot, error)
		}); ok && launch != nil {
			_, commitErr = healthy.CommitHealthy(run.ctx, *launch)
		} else {
			_, commitErr = manager.CommitCurrent(run.ctx, target)
		}
		if err := commitErr; err != nil {
			detail := supervisor.Redact(err.Error())
			if len(detail) > 4096 {
				detail = detail[:4096]
			}
			return h.failureFor(lifecycle.Failure{Code: lifecycle.ErrorRecoveryPointSaveFailed, Summary: "The recovery environment could not be saved.", Detail: detail, Retryable: true, EffectOccurred: true}, lifecycle.ErrorRecoveryPointSaveFailed, "The recovery environment could not be saved.", true)
		}
		run.setTarget(target)
	}
	status, err := h.machine.MarkReady(run.generation, workspaceURL)
	if err != nil {
		return h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not publish DSH readiness.", false)
	}
	if ready := h.readyHandler(); ready != nil {
		ready(handoffURL)
	}
	h.emit(status)
	run.signalReady()
	return nil
}

func (h *Host) waitReady(run *generationRun, worker supervisor.Worker) (dshadapter.ReadyAnnouncement, *lifecycle.Failure) {
	if err := h.setPhase(run, lifecycle.PhaseReadiness); err != nil {
		return dshadapter.ReadyAnnouncement{}, h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not enter DSH readiness validation.", false)
	}
	deadline := time.NewTimer(h.config.ReadinessTimeout)
	defer deadline.Stop()
	deadlineAt := time.Now().Add(h.config.ReadinessTimeout)
	readiness := run.readinessChannel()
	var output <-chan supervisor.OutputEvent = worker.Events()
	var announcement dshadapter.ReadyAnnouncement
	plan := run.launchPlan()
	for {
		select {
		case <-run.ctx.Done():
			return announcement, nil
		case <-worker.Exited():
			return announcement, h.failureFor(errors.New("DSH exited before readiness"), lifecycle.ErrorDSHEarlyExit, "The DSH process exited before its workspace became ready.", true)
		case candidate := <-readiness:
			announcement = candidate
			h.debugf("readiness signal observed at %s", readinessOrigin(candidate.URL))
			if err := h.deps.DSH.ValidateReady(candidate, plan); err != nil {
				h.debugf("readiness validation rejected the announced origin")
				return announcement, h.failureFor(err, lifecycle.ErrorDSHInvalidReadiness, "DSH announced an untrusted workspace origin.", false)
			}
			if failure := h.probeUntilReady(run, worker, candidate, plan, deadlineAt); failure == nil {
				h.debugf("active readiness probe passed")
				gateway, err := h.startGateway(run, candidate.URL)
				if err != nil {
					return announcement, h.failureFor(err, lifecycle.ErrorGatewayStartFailed, "dsh-work could not establish the trusted DSH workspace path.", true)
				}
				if err := h.markReady(run, gateway.Origin(), gateway.URL()); err != nil {
					return announcement, h.failureFor(err, lifecycle.ErrorInvalidTransition, "dsh-work could not publish DSH readiness.", false)
				}
				return announcement, nil
			} else if failure.Code == lifecycle.ErrorDSHInvalidReadiness {
				return announcement, failure
			} else {
				h.debugf("active readiness probe is still pending")
			}
		case _, ok := <-output:
			if !ok {
				output = nil
				continue
			}
		case <-deadline.C:
			return announcement, h.failureFor(errors.New("DSH readiness deadline exceeded"), lifecycle.ErrorDSHReadinessTimeout, "DSH did not become ready before the bounded startup deadline.", true)
		}
	}
}

func (h *Host) probeUntilReady(run *generationRun, worker supervisor.Worker, announcement dshadapter.ReadyAnnouncement, plan supervisor.LaunchPlan, deadline time.Time) *lifecycle.Failure {
	for {
		probeCtx, cancel := context.WithTimeout(run.ctx, h.config.ProbeTimeout)
		err := h.deps.DSH.Probe(probeCtx, announcement, plan)
		cancel()
		if err == nil {
			select {
			case <-worker.Exited():
				return h.failureFor(errors.New("DSH exited after readiness probe"), lifecycle.ErrorDSHEarlyExit, "The DSH process exited before its workspace became ready.", true)
			default:
			}
			return nil
		}
		if run.ctx.Err() != nil {
			return nil
		}
		if isInvalidReadiness(err) {
			return h.failureFor(err, lifecycle.ErrorDSHInvalidReadiness, "DSH announced an untrusted workspace origin.", false)
		}
		select {
		case <-worker.Exited():
			return h.failureFor(errors.New("DSH exited during readiness probe"), lifecycle.ErrorDSHEarlyExit, "The DSH process exited before its workspace became ready.", true)
		default:
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return h.failureFor(err, lifecycle.ErrorDSHReadinessTimeout, "DSH has announced a workspace but its loopback endpoint is not ready yet.", true)
		}
		delay := 150 * time.Millisecond
		if remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-run.ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-worker.Exited():
			if !timer.Stop() {
				<-timer.C
			}
			return h.failureFor(errors.New("DSH exited during readiness probe"), lifecycle.ErrorDSHEarlyExit, "The DSH process exited before its workspace became ready.", true)
		case <-timer.C:
		}
	}
}

func (h *Host) startGateway(run *generationRun, upstreamURL string) (workergateway.Session, error) {
	if h.deps.Gateway == nil {
		return nil, lifecycle.Failure{
			Code:    lifecycle.ErrorGatewayUnavailable,
			Summary: "The trusted DSH workspace gateway is unavailable.",
		}
	}
	gateway, err := h.deps.Gateway.Start(run.ctx, upstreamURL)
	if err != nil {
		return nil, err
	}
	if gateway == nil || gateway.URL() == "" || gateway.Origin() == "" {
		if gateway != nil {
			_ = gateway.Close()
		}
		return nil, lifecycle.Failure{
			Code:    lifecycle.ErrorGatewayStartFailed,
			Summary: "The trusted DSH workspace gateway returned no usable URL.",
		}
	}
	if err := validateGatewaySession(gateway); err != nil {
		_ = gateway.Close()
		return nil, lifecycle.Failure{
			Code:    lifecycle.ErrorGatewayStartFailed,
			Summary: "The trusted DSH workspace gateway returned an invalid session.",
			Detail:  err.Error(),
		}
	}
	run.setGateway(gateway)
	return gateway, nil
}

func validateGatewaySession(gateway workergateway.Session) error {
	origin, err := url.Parse(gateway.Origin())
	if err != nil || origin.Scheme != "http" || origin.Hostname() != "127.0.0.1" || origin.Port() == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" && origin.Path != "/" {
		return errors.New("gateway origin is not an HTTP loopback origin")
	}
	handoff, err := url.Parse(gateway.URL())
	if err != nil || handoff.Scheme != origin.Scheme || handoff.Host != origin.Host || handoff.Path != "/__work/bootstrap" || handoff.Fragment != "" || handoff.User != nil {
		return errors.New("gateway bootstrap URL is not bound to its origin")
	}
	query := handoff.Query()
	if len(query) != 1 || len(query["session"]) != 1 || query.Get("session") == "" || len(query.Get("session")) > 128 {
		return errors.New("gateway bootstrap URL has an invalid session")
	}
	return nil
}

func (h *Host) markReady(run *generationRun, workspaceURL, handoffURL string) error {
	err := h.setPhase(run, lifecycle.PhaseCheckpoint)
	if err != nil {
		return err
	}
	run.mu.Lock()
	run.readyWorkspaceURL = workspaceURL
	run.readyHandoffURL = handoffURL
	run.mu.Unlock()
	return nil
}

func (h *Host) cleanupWorker(run *generationRun, worker supervisor.Worker) *lifecycle.Failure {
	if worker == nil {
		return nil
	}
	h.workerBoundaryMu.Lock()
	defer h.workerBoundaryMu.Unlock()
	if !h.beginManagerStopGuard(run) {
		return h.failureFor(errors.New("Run context mutation guard is unavailable during Worker cleanup"), lifecycle.ErrorManagerOperationBusy, "dsh-work is finishing the previous Run context.", true)
	}
	var cleanupFailure *lifecycle.Failure
	rememberFailure := func(failure *lifecycle.Failure) {
		if cleanupFailure == nil && failure != nil {
			cleanupFailure = failure
		}
	}
	if failure := h.clearCurrent(run); failure != nil {
		rememberFailure(failure)
	}
	gatewayErr := h.closeGateway(run)
	if gatewayErr != nil {
		rememberFailure(h.failureFor(gatewayErr, lifecycle.ErrorGatewayCloseFailed, "dsh-work could not close the trusted DSH workspace path.", true))
	}
	gracefulCtx, cancel := context.WithTimeout(context.Background(), h.config.GracefulStopTimeout)
	_ = h.deps.DSH.RequestShutdown(gracefulCtx, worker)
	cancel()
	if !waitForClosed(worker.Exited(), h.config.GracefulStopTimeout) {
		forceCtx, forceCancel := context.WithTimeout(context.Background(), h.config.ForceStopTimeout)
		forceErr := worker.ForceStop(forceCtx)
		forceCancel()
		if forceErr != nil {
			rememberFailure(h.failureFor(forceErr, lifecycle.ErrorProcessStopFailed, "dsh-work could not stop the managed DSH process.", true))
		}
	}
	emptyCtx, emptyCancel := context.WithTimeout(context.Background(), h.config.EmptyTimeout)
	emptyErr := worker.WaitEmpty(emptyCtx)
	emptyCancel()
	h.mu.Lock()
	h.lastDiagnostics = worker.Diagnostics()
	h.mu.Unlock()
	if emptyErr != nil {
		rememberFailure(h.failureFor(emptyErr, lifecycle.ErrorProcessCleanupFailed, "dsh-work could not verify that the managed process boundary is empty.", true))
		return cleanupFailure
	}
	closeErr := worker.Close()
	if closeErr != nil {
		rememberFailure(h.failureFor(closeErr, lifecycle.ErrorProcessCleanupFailed, "dsh-work could not close the managed DSH process boundary.", true))
	}
	if cleanupFailure != nil {
		return cleanupFailure
	}
	run.setCleanupComplete(true)
	return nil
}

func (h *Host) clearCurrent(run *generationRun) *lifecycle.Failure {
	if !run.isManagerLive() {
		return nil
	}
	manager, ok := h.deps.Manager.(RunContextManager)
	if !ok {
		return h.failureFor(errors.New("Run context clear boundary is unavailable"), lifecycle.ErrorManagerStateInvalid, "dsh-work could not clear the current Run context.", true)
	}
	activeCtx, activeCancel := context.WithTimeout(context.Background(), h.config.ShutdownTimeout)
	_, err := manager.ClearCurrent(activeCtx)
	activeCancel()
	if err != nil {
		return h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "dsh-work could not clear the current Run context.", true)
	}
	run.setManagerLive(false)
	return nil
}

func (h *Host) beginManagerStopGuard(run *generationRun) bool {
	if run.isManagerGuarded() {
		return true
	}
	if h.isSwitching() {
		// The enclosing context switch owns the manager guard. A cleanup that
		// starts during that transaction may use it, but must not end it.
		return h.isManagerSwitchGuarded()
	}
	manager, ok := h.deps.Manager.(RunContextManager)
	if !ok {
		return true
	}
	if err := manager.BeginRunContextSwitch(context.Background()); err != nil {
		h.debugf("could not guard Run context during stop: %s", err)
		return false
	}
	run.setManagerGuarded(true)
	return true
}

func (h *Host) releaseManagerStopGuard(run *generationRun) {
	if !run.takeManagerGuarded() {
		return
	}
	manager, ok := h.deps.Manager.(RunContextManager)
	if !ok {
		return
	}
	if err := manager.EndRunContextSwitch(context.Background()); err != nil {
		h.debugf("could not release Run context stop guard: %s", err)
	}
}

func (h *Host) closeGateway(run *generationRun) error {
	gateway := run.getGateway()
	if gateway == nil {
		return nil
	}
	err := gateway.Close()
	if err == nil {
		run.setGateway(nil)
	}
	return err
}

func waitForClosed(done <-chan struct{}, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

func (h *Host) finish(run *generationRun, failure *lifecycle.Failure) {
	status := h.machine.Snapshot()
	if status.GenerationID != run.generation {
		return
	}
	wasReady := status.State == lifecycle.StateReady || status.WorkspaceURL != ""
	if status.State == lifecycle.StateStopping {
		status, _ = h.machine.CompleteStop(run.generation, failure)
	} else if failure != nil {
		status, _ = h.machine.Fail(run.generation, *failure)
	} else {
		if _, err := h.machine.BeginStop(run.generation); err == nil {
			status, _ = h.machine.CompleteStop(run.generation, nil)
		}
	}
	keepCleanupOwner := failure != nil && run.cleanupPending()
	if !run.cleanupPending() {
		h.releaseManagerStopGuard(run)
	}
	h.mu.Lock()
	if h.current == run && !keepCleanupOwner {
		h.current = nil
	}
	h.mu.Unlock()
	if failure != nil && wasReady {
		if recovery := h.recoveryHandler(); recovery != nil {
			recovery()
		}
	}
	h.emit(status)
	run.signalReady()
}

func (h *Host) setPhase(run *generationRun, phase lifecycle.Phase) error {
	status, err := h.machine.SetPhase(run.generation, phase)
	if err == nil {
		h.emit(status)
	}
	return err
}

func (h *Host) checkCancelled(run *generationRun) error {
	select {
	case <-run.ctx.Done():
		return run.ctx.Err()
	default:
		return nil
	}
}

func (h *Host) failureFor(err error, fallbackCode lifecycle.ErrorCode, summary string, retryable bool) *lifecycle.Failure {
	if err != nil {
		var value lifecycle.Failure
		if errors.As(err, &value) {
			copy := value
			if copy.CorrelationID == "" {
				copy.CorrelationID = h.Status().CorrelationID
			}
			if copy.Detail == "" {
				copy.Detail = defaultRemediation(copy.Code)
			}
			return &copy
		}
		var pointer *lifecycle.Failure
		if errors.As(err, &pointer) && pointer != nil {
			copy := *pointer
			if copy.CorrelationID == "" {
				copy.CorrelationID = h.Status().CorrelationID
			}
			if copy.Detail == "" {
				copy.Detail = defaultRemediation(copy.Code)
			}
			return &copy
		}
	}
	failure := &lifecycle.Failure{
		Code:      fallbackCode,
		Summary:   summary,
		Retryable: retryable,
		Detail:    defaultRemediation(fallbackCode),
	}
	if h.Status().State == lifecycle.StateStarting || h.Status().State == lifecycle.StateReady {
		failure.EffectOccurred = true
	}
	failure.CorrelationID = h.Status().CorrelationID
	return failure
}

func defaultRemediation(code lifecycle.ErrorCode) string {
	switch code {
	case lifecycle.ErrorDSHRuntimeNotFound:
		return "Set DSH_WORK_EXECUTABLE or run task setup:dsh, then retry."
	case lifecycle.ErrorDSHUnsupportedVersion, lifecycle.ErrorDSHVersionCheckFailed:
		return "Install the pinned DSH version and retry."
	case lifecycle.ErrorProfileRequired, lifecycle.ErrorProfileNotFound, lifecycle.ErrorProfileInvalid:
		return "Open dsh-work Settings and choose an existing DSH data directory and profile."
	case lifecycle.ErrorRuntimeInUse, lifecycle.ErrorProfileInUse:
		return "Choose another Run context before removing this runtime or DSH data directory."
	case lifecycle.ErrorPluginSpecInvalid, lifecycle.ErrorPluginCommandUnavailable, lifecycle.ErrorPluginCommandFailed:
		return "Check the selected profile and plugin package, then retry the explicit operation."
	case lifecycle.ErrorRuntimeInstallUnavailable, lifecycle.ErrorRuntimeInstallFailed:
		return "Retry the explicit runtime installation on a supported native platform."
	case lifecycle.ErrorDSHReadinessTimeout, lifecycle.ErrorDSHStartFailed, lifecycle.ErrorProcessStartFailed:
		return "Check the DSH installation and loopback port, then retry."
	case lifecycle.ErrorGatewayUnavailable, lifecycle.ErrorGatewayStartFailed, lifecycle.ErrorGatewayCloseFailed:
		return "Retry the trusted local workspace handoff."
	case lifecycle.ErrorProcessStopFailed, lifecycle.ErrorProcessCleanupFailed:
		return "Retry cleanup before starting another workspace."
	case lifecycle.ErrorPlatformUnsupported:
		return "Run the Windows-first dsh-work build on a supported native platform."
	case lifecycle.ErrorTrustedSurfaceRequired:
		return "Use the trusted dsh-work shell or Settings window for this control."
	default:
		return "Retry after checking the current dsh-work configuration."
	}
}

func isInvalidReadiness(err error) bool {
	var failure lifecycle.Failure
	if errors.As(err, &failure) {
		return failure.Code == lifecycle.ErrorDSHInvalidReadiness
	}
	var pointer *lifecycle.Failure
	return errors.As(err, &pointer) && pointer != nil && pointer.Code == lifecycle.ErrorDSHInvalidReadiness
}

func (h *Host) emit(status lifecycle.Status) {
	h.mu.Lock()
	publish := h.publish
	h.mu.Unlock()
	if publish != nil {
		publish(status)
	}
}

func (h *Host) debugf(format string, args ...any) {
	h.mu.Lock()
	debug := h.debug
	h.mu.Unlock()
	if debug != nil {
		debug(fmt.Sprintf(format, args...))
	}
}

func readinessOrigin(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return "invalid-url"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func (h *Host) readyHandler() func(string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.onReady
}

func (h *Host) recoveryHandler() func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.recovery
}

func (h *Host) quitHandler() func() {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.quit
}

func (r *generationRun) setWorker(worker supervisor.Worker) {
	r.mu.Lock()
	r.worker = worker
	r.mu.Unlock()
}

func (r *generationRun) signalReady() {
	r.readyOnce.Do(func() { close(r.ready) })
}

func (r *generationRun) getWorker() supervisor.Worker {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.worker
}

func (r *generationRun) readinessChannel() <-chan dshadapter.ReadyAnnouncement {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.readiness
}

func (r *generationRun) setGateway(gateway workergateway.Session) {
	r.mu.Lock()
	r.gateway = gateway
	r.mu.Unlock()
}

func (r *generationRun) setLaunch(launch *dshmanager.ResolvedLaunch) {
	r.mu.Lock()
	r.launch = cloneResolvedLaunch(launch)
	r.mu.Unlock()
}

func (r *generationRun) usesProfile(profile dshmanager.ProfileRef) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.launch != nil {
		return r.launch.Target.Profile == profile
	}
	return r.target != nil && r.target.Profile == profile
}

func (r *generationRun) setTarget(target *dshmanager.RunContext) {
	r.mu.Lock()
	if target == nil {
		r.target = nil
	} else {
		copy := *target
		r.target = &copy
	}
	r.managerLive = true
	r.mu.Unlock()
}

func (r *generationRun) setManagerLive(value bool) {
	r.mu.Lock()
	r.managerLive = value
	r.mu.Unlock()
}

func (r *generationRun) setManagerGuarded(value bool) {
	r.mu.Lock()
	r.managerGuarded = value
	r.mu.Unlock()
}

func (r *generationRun) isManagerGuarded() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.managerGuarded
}

func (r *generationRun) takeManagerGuarded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.managerGuarded {
		return false
	}
	r.managerGuarded = false
	return true
}

func (r *generationRun) targetCopy() *dshmanager.RunContext {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.target == nil {
		return nil
	}
	copy := *r.target
	return &copy
}

func (r *generationRun) isManagerLive() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.managerLive
}

func (r *generationRun) getGateway() workergateway.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gateway
}

func (r *generationRun) setCleanupComplete(value bool) {
	r.mu.Lock()
	r.cleanupDone = value
	r.mu.Unlock()
}

func (r *generationRun) isCleanupDone() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cleanupDone
}

func (r *generationRun) cleanupPending() bool {
	return !r.isCleanupDone() && r.getWorker() != nil
}

func (r *generationRun) launchPlan() supervisor.LaunchPlan {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.plan
}

// HostService is the intentionally narrow Wails binding surface.
type HostService struct {
	host             *Host
	workspaceTrusted func() bool
	localeProvider   func() settings.Locale
	openSettings     func(string)
}

// StartupOutput is the redacted DSH process output retained by the supervisor
// while dsh-work is starting or recovering. It intentionally omits process
// metadata and exposes no raw, unredacted stream.
type StartupOutput struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func NewHostService(host *Host, workspaceTrusted func() bool, localeProvider func() settings.Locale, openSettings ...func(string)) *HostService {
	service := &HostService{host: host, workspaceTrusted: workspaceTrusted, localeProvider: localeProvider}
	if len(openSettings) > 0 {
		service.openSettings = openSettings[0]
	}
	return service
}

func (s *HostService) GetStatus(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Status()
}

// GetWorkspaceStatus projects the current per-generation Workspace context to
// the trusted Settings window. It is read-only and does not expose Host
// controls from that surface.
func (s *HostService) GetWorkspaceStatus(ctx context.Context) lifecycle.Status {
	if s == nil || s.host == nil || !isTrustedWindow(ctx, "settings") {
		return trustedSurfaceStatus()
	}
	return s.host.Status()
}

// GetTheme projects the selected DSH data directory's appearance preference. DSH owns
// the value; dsh-work only uses it to paint its trusted startup surface.
func (s *HostService) GetTheme(ctx context.Context) dshmanager.ThemePreference {
	if !s.authorized(ctx) || s.host.deps.Manager == nil {
		return dshmanager.ThemePreferenceSystem
	}
	snapshot, err := s.host.deps.Manager.Snapshot(ctx)
	if err != nil || !snapshot.Theme.Valid() {
		return dshmanager.ThemePreferenceSystem
	}
	return snapshot.Theme
}

// GetLocale reads the dsh-work-owned language preference for the trusted startup
// surface. It is read-only here; SettingsService remains the write boundary.
func (s *HostService) GetLocale(ctx context.Context) settings.Locale {
	if !s.authorized(ctx) || s.localeProvider == nil {
		return settings.DefaultLocale
	}
	locale := s.localeProvider()
	if !locale.Valid() {
		return settings.DefaultLocale
	}
	return locale
}

// GetStartupOutput returns the bounded, redacted stdout/stderr tails for the
// active or last startup attempt so the user can inspect and copy them.
func (s *HostService) GetStartupOutput(ctx context.Context) StartupOutput {
	if !s.authorized(ctx) {
		return StartupOutput{}
	}
	diagnostics := s.host.Diagnostics()
	return StartupOutput{Stdout: diagnostics.StdoutTail, Stderr: diagnostics.StderrTail}
}

// OpenRuntimeSettings is the first-use recovery action for a missing local
// runtime. It opens the trusted runtime-management surface but never starts an
// acquisition itself.
func (s *HostService) OpenRuntimeSettings(ctx context.Context) error {
	if !s.authorized(ctx) {
		return trustedSurfaceRequired("DSH runtime management is available only from the trusted dsh-work shell.")
	}
	if s.openSettings == nil {
		return lifecycle.Failure{
			Code:          lifecycle.ErrorManagerStateInvalid,
			Summary:       "dsh-work Settings is unavailable.",
			Retryable:     true,
			CorrelationID: lifecycle.NewCorrelationID(),
		}
	}
	s.openSettings("runtimes")
	return nil
}

func (s *HostService) Start(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Start()
}

// StartWithWorkspace is the explicit session action for a DSH Workspace
// surface. The request is resolved for one Worker generation and is not
// persisted with the global Run context.
func (s *HostService) StartWithWorkspace(ctx context.Context, request workspacecontext.Request) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.StartWithWorkspace(request)
}

func (s *HostService) Cancel(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Cancel()
}

func (s *HostService) Restart(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Restart()
}

func (s *HostService) Quit(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Quit()
}

func (s *HostService) authorized(ctx context.Context) bool {
	if s == nil || s.host == nil || !isTrustedWindow(ctx, "workspace") {
		return false
	}
	return s.workspaceTrusted != nil && s.workspaceTrusted()
}

func trustedSurfaceStatus() lifecycle.Status {
	failure := lifecycle.Failure{
		Code:          lifecycle.ErrorTrustedSurfaceRequired,
		Summary:       "This dsh-work control is unavailable from the current page.",
		Detail:        "Return to the trusted dsh-work shell before using Host controls.",
		CorrelationID: lifecycle.NewCorrelationID(),
	}
	return lifecycle.Status{
		State:         lifecycle.StateFailed,
		Phase:         lifecycle.PhaseFailed,
		Error:         &failure,
		CorrelationID: failure.CorrelationID,
	}
}

var _ DSHAdapter = (*dshadapter.Adapter)(nil)
