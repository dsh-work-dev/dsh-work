package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/dshmanager"
	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/supervisor"
	"github.com/local/work/internal/workergateway"
)

// DSHAdapter is the Work-owned contract for discovery, launch construction,
// readiness validation and shutdown. It deliberately contains no Wails or
// operating-system types.
type DSHAdapter interface {
	Discover(context.Context) (dshadapter.Runtime, error)
	BuildLaunchPlan(dshadapter.Runtime, string, string, string, int) (supervisor.LaunchPlan, error)
	ParseReadyAnnouncement(string) (dshadapter.ReadyAnnouncement, bool)
	ValidateReady(dshadapter.ReadyAnnouncement, supervisor.LaunchPlan) error
	Probe(context.Context, dshadapter.ReadyAnnouncement, supervisor.LaunchPlan) error
	RequestShutdown(context.Context, supervisor.Worker) error
}

type WorkerGateway interface {
	Start(context.Context, string) (workergateway.Session, error)
}

// LaunchManager is the platform-neutral selection seam. The Host consumes a
// resolved tuple and never edits profile files or invokes a package manager.
type LaunchManager interface {
	Snapshot(context.Context) (dshmanager.Snapshot, error)
	ResolveLaunch(context.Context, dshmanager.LaunchRequest) (dshmanager.ResolvedLaunch, error)
	MarkActive(context.Context, *dshmanager.LaunchSelection) (dshmanager.Snapshot, error)
}

type Dependencies struct {
	DSH           DSHAdapter
	Manager       LaunchManager
	Supervisor    supervisor.Adapter
	Gateway       WorkerGateway
	PlatformError error
}

type Config struct {
	WorkspaceRoot       string
	DSHHome             string
	ExpectedDSHVersion  string
	ReadinessTimeout    time.Duration
	ProbeTimeout        time.Duration
	GracefulStopTimeout time.Duration
	ForceStopTimeout    time.Duration
	EmptyTimeout        time.Duration
	ShutdownTimeout     time.Duration
}

func DefaultConfig(workspaceRoot string) Config {
	if workspaceRoot == "" {
		workspaceRoot, _ = os.Getwd()
	}
	workspaceRoot, _ = filepath.Abs(workspaceRoot)
	configRoot, err := os.UserConfigDir()
	if err != nil || configRoot == "" {
		configRoot = filepath.Join(workspaceRoot, ".work")
	}
	return Config{
		WorkspaceRoot:       workspaceRoot,
		DSHHome:             filepath.Join(configRoot, "Work", "dsh"),
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
	defaults := DefaultConfig(c.WorkspaceRoot)
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = defaults.WorkspaceRoot
	}
	if c.DSHHome == "" {
		c.DSHHome = defaults.DSHHome
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

	mu              sync.Mutex
	current         *generationRun
	publish         func(lifecycle.Status)
	onReady         func(string)
	recovery        func()
	quit            func()
	shutdownMu      sync.Mutex
	lastDiagnostics supervisor.Diagnostics
	debug           func(string)
}

type generationRun struct {
	generation string
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}

	mu          sync.RWMutex
	worker      supervisor.Worker
	plan        supervisor.LaunchPlan
	readiness   <-chan dshadapter.ReadyAnnouncement
	gateway     workergateway.Session
	selection   *dshmanager.LaunchSelection
	managerLive bool
	cleanupDone bool
}

type hostLaunch struct {
	runtime   dshadapter.Runtime
	home      dshmanager.HomeInfo
	profile   string
	selection *dshmanager.LaunchSelection
}

func NewHost(deps Dependencies, config Config) *Host {
	config = config.withDefaults()
	return &Host{
		machine: lifecycle.NewMachine(),
		deps:    deps,
		config:  config,
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
	for {
		h.mu.Lock()
		if h.current == nil {
			generation, status, err := h.machine.BeginStart()
			if err != nil {
				h.mu.Unlock()
				return status
			}
			ctx, cancel := context.WithCancel(context.Background())
			run := &generationRun{
				generation: generation,
				ctx:        ctx,
				cancel:     cancel,
				done:       make(chan struct{}),
			}
			h.current = run
			h.mu.Unlock()
			h.emit(status)
			go h.run(run)
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

func (h *Host) Cancel() lifecycle.Status {
	return h.requestStop(false)
}

// Restart converges an active generation through the normal cleanup boundary
// before starting another one. If cleanup cannot be verified, the existing
// generation remains the cleanup owner and no overlapping Worker is created.
func (h *Host) Restart() lifecycle.Status {
	run := h.activeRun()
	if run == nil {
		return h.Start()
	}
	status := h.requestStop(false)
	go func() {
		<-run.done
		if run.cleanupPending() {
			return
		}
		_ = h.Start()
	}()
	return status
}

func (h *Host) Quit() lifecycle.Status {
	status := h.requestStop(true)
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

	run := h.activeRun()
	if run == nil {
		return nil
	}
	select {
	case <-run.done:
		if run.cleanupPending() {
			return h.shutdownPendingRun(run)
		}
		return nil
	default:
	}
	h.requestStop(false)
	timer := time.NewTimer(h.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-run.done:
		if run.cleanupPending() {
			return h.shutdownPendingRun(run)
		}
		return nil
	case <-timer.C:
		return fmt.Errorf("host shutdown timed out")
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
	run := h.activeRun()
	if run == nil {
		return h.Status()
	}
	status, err := h.machine.BeginStop(run.generation)
	if err != nil {
		return h.Status()
	}
	h.emit(status)
	run.cancel()
	return status
}

func (h *Host) activeRun() *generationRun {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.current
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
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter configuration.", false)
	}
	if h.deps.PlatformError != nil {
		return nil, h.failureFor(h.deps.PlatformError, lifecycle.ErrorPlatformUnsupported, "This platform does not have a native Work process adapter.", false)
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
	if err := h.setPhase(run, lifecycle.PhaseRuntime); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter runtime discovery.", false)
	}
	launch, failure := h.resolveLaunch(run)
	if failure != nil {
		return nil, failure
	}
	if launch.home.Path == "" {
		return nil, h.failureFor(errors.New("DSH home is empty"), lifecycle.ErrorDSHStartFailed, "Work could not prepare the selected DSH home.", false)
	}
	if launch.home.Ownership == dshmanager.HomeOwnershipUser {
		info, err := os.Stat(launch.home.Path)
		if err != nil || !info.IsDir() {
			if err == nil {
				err = errors.New("selected user DSH home is not a directory")
			}
			return nil, h.failureFor(err, lifecycle.ErrorProfileNotFound, "The selected user DSH home is unavailable.", false)
		}
	} else if err := os.MkdirAll(launch.home.Path, 0o700); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "Work could not prepare the selected DSH home.", true)
	}
	if err := h.checkCancelled(run); err != nil {
		return nil, nil
	}

	port, err := dshadapter.AllocateLoopbackPort()
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "Work could not allocate a loopback port for DSH.", true)
	}
	var plan supervisor.LaunchPlan
	if launch.selection == nil {
		plan, err = h.deps.DSH.BuildLaunchPlan(launch.runtime, run.generation, h.config.WorkspaceRoot, launch.home.Path, port)
	} else {
		profileBuilder, ok := h.deps.DSH.(interface {
			BuildLaunchPlanForProfile(dshadapter.Runtime, string, string, string, string, int) (supervisor.LaunchPlan, error)
		})
		if !ok {
			return nil, h.failureFor(errors.New("DSH adapter does not support explicit profiles"), lifecycle.ErrorDSHStartFailed, "The selected DSH adapter cannot launch an explicit profile.", false)
		}
		plan, err = profileBuilder.BuildLaunchPlanForProfile(launch.runtime, run.generation, launch.selection.Workspace, launch.home.Path, launch.profile, port)
	}
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "Work could not construct the DSH launch plan.", false)
	}
	if err := h.setPhase(run, lifecycle.PhaseWorker); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter worker startup.", false)
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
		return nil, h.failureFor(err, lifecycle.ErrorProcessStartFailed, "Work could not start the managed DSH process.", true)
	}
	run.mu.Lock()
	run.plan = plan
	run.readiness = readiness
	run.mu.Unlock()
	if launch.selection != nil {
		if _, err := h.deps.Manager.MarkActive(run.ctx, launch.selection); err != nil {
			cleanupFailure := h.cleanupWorker(run, worker)
			if cleanupFailure != nil {
				return nil, cleanupFailure
			}
			return nil, h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "Work could not record the active DSH selection.", true)
		}
		run.setSelection(launch.selection, true)
	}
	h.debugf("managed DSH worker started for expected port %d", plan.ExpectedPort)
	return worker, nil
}

func (h *Host) resolveLaunch(run *generationRun) (hostLaunch, *lifecycle.Failure) {
	if h.deps.Manager == nil {
		runtime, err := h.deps.DSH.Discover(run.ctx)
		if err != nil {
			if run.ctx.Err() != nil {
				return hostLaunch{}, nil
			}
			return hostLaunch{}, h.failureFor(err, lifecycle.ErrorDSHRuntimeNotFound, "Work could not discover a compatible DSH runtime.", false)
		}
		return hostLaunch{
			runtime: runtime,
			home: dshmanager.HomeInfo{
				ID:        "legacy",
				Name:      "Work DSH home",
				Path:      h.config.DSHHome,
				Ownership: dshmanager.HomeOwnershipWork,
			},
			profile: "web",
		}, nil
	}

	snapshot, err := h.deps.Manager.Snapshot(run.ctx)
	if err != nil {
		return hostLaunch{}, h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "Work could not read the DSH launch selection.", false)
	}
	if snapshot.Desired == nil {
		return hostLaunch{}, h.failureFor(lifecycle.Failure{
			Code:    lifecycle.ErrorProfileRequired,
			Summary: "Choose a DSH runtime and profile before starting Work.",
		}, lifecycle.ErrorProfileRequired, "Choose a DSH runtime and profile before starting Work.", false)
	}
	resolved, err := h.deps.Manager.ResolveLaunch(run.ctx, dshmanager.LaunchRequest{
		RuntimeID: snapshot.Desired.RuntimeID,
		Profile:   snapshot.Desired.Profile,
		Workspace: snapshot.Desired.Workspace,
	})
	if err != nil {
		return hostLaunch{}, h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "Work could not resolve the selected DSH runtime and profile.", false)
	}
	runtime := dshadapter.Runtime{Path: resolved.Runtime.Path, Version: resolved.Runtime.Version}
	if verifier, ok := h.deps.DSH.(interface {
		DiscoverPath(context.Context, string) (dshadapter.Runtime, error)
	}); ok {
		verified, verifyErr := verifier.DiscoverPath(run.ctx, runtime.Path)
		if verifyErr != nil {
			if run.ctx.Err() != nil {
				return hostLaunch{}, nil
			}
			return hostLaunch{}, h.failureFor(verifyErr, lifecycle.ErrorDSHRuntimeNotFound, "Work could not verify the selected DSH runtime.", false)
		}
		if verified.Version != resolved.Runtime.Version {
			return hostLaunch{}, h.failureFor(lifecycle.Failure{
				Code:    lifecycle.ErrorDSHUnsupportedVersion,
				Summary: "The selected DSH runtime does not match the catalog.",
			}, lifecycle.ErrorDSHUnsupportedVersion, "The selected DSH runtime does not match the catalog.", false)
		}
		runtime = verified
	}
	selection := resolved.Selection
	return hostLaunch{
		runtime:   runtime,
		home:      resolved.Home,
		profile:   selection.Profile.Name,
		selection: &selection,
	}, nil
}

func (h *Host) waitReady(run *generationRun, worker supervisor.Worker) (dshadapter.ReadyAnnouncement, *lifecycle.Failure) {
	if err := h.setPhase(run, lifecycle.PhaseReadiness); err != nil {
		return dshadapter.ReadyAnnouncement{}, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter DSH readiness validation.", false)
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
					return announcement, h.failureFor(err, lifecycle.ErrorGatewayStartFailed, "Work could not establish the trusted DSH workspace path.", true)
				}
				if err := h.markReady(run, gateway.Origin(), gateway.URL()); err != nil {
					return announcement, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not publish DSH readiness.", false)
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
	status, err := h.machine.MarkReady(run.generation, workspaceURL)
	if err != nil {
		return err
	}
	if ready := h.readyHandler(); ready != nil {
		// The WebView receives the per-generation gateway bootstrap URL. The
		// DSH launch credential stays inside the gateway and never crosses the
		// Host/UI lifecycle contract.
		ready(handoffURL)
	}
	h.emit(status)
	return nil
}

func (h *Host) cleanupWorker(run *generationRun, worker supervisor.Worker) *lifecycle.Failure {
	if worker == nil {
		return nil
	}
	gatewayErr := h.closeGateway(run)
	if gatewayErr != nil {
		return h.failureFor(gatewayErr, lifecycle.ErrorGatewayCloseFailed, "Work could not close the trusted DSH workspace path.", true)
	}
	gracefulCtx, cancel := context.WithTimeout(context.Background(), h.config.GracefulStopTimeout)
	_ = h.deps.DSH.RequestShutdown(gracefulCtx, worker)
	cancel()
	if !waitForClosed(worker.Exited(), h.config.GracefulStopTimeout) {
		forceCtx, forceCancel := context.WithTimeout(context.Background(), h.config.ForceStopTimeout)
		forceErr := worker.ForceStop(forceCtx)
		forceCancel()
		if forceErr != nil {
			return h.failureFor(forceErr, lifecycle.ErrorProcessStopFailed, "Work could not stop the managed DSH process.", true)
		}
	}
	emptyCtx, emptyCancel := context.WithTimeout(context.Background(), h.config.EmptyTimeout)
	emptyErr := worker.WaitEmpty(emptyCtx)
	emptyCancel()
	h.mu.Lock()
	h.lastDiagnostics = worker.Diagnostics()
	h.mu.Unlock()
	if emptyErr != nil {
		return h.failureFor(emptyErr, lifecycle.ErrorProcessCleanupFailed, "Work could not verify that the managed process boundary is empty.", true)
	}
	closeErr := worker.Close()
	if closeErr != nil {
		return h.failureFor(closeErr, lifecycle.ErrorProcessCleanupFailed, "Work could not close the managed DSH process boundary.", true)
	}
	if run.isManagerLive() {
		activeCtx, activeCancel := context.WithTimeout(context.Background(), h.config.ShutdownTimeout)
		_, err := h.deps.Manager.MarkActive(activeCtx, nil)
		activeCancel()
		if err != nil {
			return h.failureFor(err, lifecycle.ErrorManagerStateInvalid, "Work could not clear the active DSH selection.", true)
		}
		run.setSelection(nil, false)
	}
	run.setCleanupComplete(true)
	return nil
}

func (h *Host) closeGateway(run *generationRun) error {
	gateway := run.getGateway()
	if gateway == nil {
		return nil
	}
	return gateway.Close()
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
	keepCleanupOwner := failure != nil && !run.isCleanupDone() && (failure.Code == lifecycle.ErrorProcessCleanupFailed || failure.Code == lifecycle.ErrorProcessStopFailed || failure.Code == lifecycle.ErrorGatewayCloseFailed)
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
		return "Set WORK_DSH_EXECUTABLE or run task setup:dsh, then retry."
	case lifecycle.ErrorDSHUnsupportedVersion, lifecycle.ErrorDSHVersionCheckFailed:
		return "Install the pinned DSH version and retry."
	case lifecycle.ErrorProfileRequired, lifecycle.ErrorProfileNotFound, lifecycle.ErrorProfileInvalid:
		return "Open Work Manager and choose an existing DSH home and profile."
	case lifecycle.ErrorRuntimeInUse, lifecycle.ErrorProfileInUse:
		return "Choose another launch selection before removing this runtime or DSH home."
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
		return "Run the Windows-first Work build on a supported native platform."
	case lifecycle.ErrorTrustedSurfaceRequired:
		return "Use the trusted Work shell or Manager window for this control."
	default:
		return "Retry after checking the current Work configuration."
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

func (r *generationRun) setSelection(selection *dshmanager.LaunchSelection, active bool) {
	r.mu.Lock()
	if selection == nil {
		r.selection = nil
	} else {
		copy := *selection
		r.selection = &copy
	}
	r.managerLive = active
	r.mu.Unlock()
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
}

func NewHostService(host *Host, workspaceTrusted func() bool) *HostService {
	return &HostService{host: host, workspaceTrusted: workspaceTrusted}
}

func (s *HostService) GetStatus(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Status()
}
func (s *HostService) Start(ctx context.Context) lifecycle.Status {
	if !s.authorized(ctx) {
		return trustedSurfaceStatus()
	}
	return s.host.Start()
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
		Summary:       "This Work control is unavailable from the current page.",
		Detail:        "Return to the trusted Work shell before using Host controls.",
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
