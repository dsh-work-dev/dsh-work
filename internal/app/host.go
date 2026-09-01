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
	"github.com/local/work/internal/lifecycle"
	"github.com/local/work/internal/supervisor"
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

type Dependencies struct {
	DSH           DSHAdapter
	Supervisor    supervisor.Adapter
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

	mu     sync.RWMutex
	worker supervisor.Worker
	plan   supervisor.LaunchPlan
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
	generation, status, err := h.machine.BeginStart()
	if err != nil {
		return status
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &generationRun{
		generation: generation,
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	h.mu.Lock()
	h.current = run
	h.mu.Unlock()
	h.emit(status)
	go h.run(run)
	return status
}

func (h *Host) Cancel() lifecycle.Status {
	return h.requestStop(false)
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
	h.requestStop(false)
	timer := time.NewTimer(h.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-run.done:
		return nil
	case <-timer.C:
		return fmt.Errorf("host shutdown timed out")
	}
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
	if err := os.MkdirAll(h.config.DSHHome, 0o700); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "Work could not prepare the DSH workspace home.", true)
	}

	if err := h.setPhase(run, lifecycle.PhaseRuntime); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter runtime discovery.", false)
	}
	runtime, err := h.deps.DSH.Discover(run.ctx)
	if err != nil {
		if run.ctx.Err() != nil {
			return nil, nil
		}
		return nil, h.failureFor(err, lifecycle.ErrorDSHRuntimeNotFound, "Work could not discover a compatible DSH runtime.", false)
	}
	if err := h.checkCancelled(run); err != nil {
		return nil, nil
	}

	port, err := dshadapter.AllocateLoopbackPort()
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "Work could not allocate a loopback port for DSH.", true)
	}
	plan, err := h.deps.DSH.BuildLaunchPlan(runtime, run.generation, h.config.WorkspaceRoot, h.config.DSHHome, port)
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorDSHStartFailed, "Work could not construct the DSH launch plan.", false)
	}
	if err := h.setPhase(run, lifecycle.PhaseWorker); err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter worker startup.", false)
	}
	if err := h.checkCancelled(run); err != nil {
		return nil, nil
	}
	worker, err := h.deps.Supervisor.Start(run.ctx, plan)
	if err != nil {
		return nil, h.failureFor(err, lifecycle.ErrorProcessStartFailed, "Work could not start the managed DSH process.", true)
	}
	run.mu.Lock()
	run.plan = plan
	run.mu.Unlock()
	h.debugf("managed DSH worker started for expected port %d", plan.ExpectedPort)
	return worker, nil
}

func (h *Host) waitReady(run *generationRun, worker supervisor.Worker) (dshadapter.ReadyAnnouncement, *lifecycle.Failure) {
	if err := h.setPhase(run, lifecycle.PhaseReadiness); err != nil {
		return dshadapter.ReadyAnnouncement{}, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not enter DSH readiness validation.", false)
	}
	deadline := time.NewTimer(h.config.ReadinessTimeout)
	defer deadline.Stop()
	deadlineAt := time.Now().Add(h.config.ReadinessTimeout)
	var output <-chan supervisor.OutputEvent = worker.Events()
	var announcement dshadapter.ReadyAnnouncement
	plan := run.launchPlan()
	for {
		select {
		case <-run.ctx.Done():
			return announcement, nil
		case <-worker.Exited():
			if output != nil {
				for {
					select {
					case event, ok := <-output:
						if !ok {
							output = nil
							continue
						}
						readinessText := event.Text
						if event.RawText != "" {
							readinessText = event.RawText
						}
						if candidate, ok := h.deps.DSH.ParseReadyAnnouncement(readinessText); ok {
							announcement = candidate
							_ = h.deps.DSH.ValidateReady(candidate, plan)
						}
					default:
						output = nil
					}
					if output == nil {
						break
					}
				}
			}
			return announcement, h.failureFor(errors.New("DSH exited before readiness"), lifecycle.ErrorDSHEarlyExit, "The DSH process exited before its workspace became ready.", true)
		case event, ok := <-output:
			if !ok {
				output = nil
				continue
			}
			readinessText := event.Text
			if event.RawText != "" {
				readinessText = event.RawText
			}
			candidate, ok := h.deps.DSH.ParseReadyAnnouncement(readinessText)
			if !ok {
				continue
			}
			announcement = candidate
			h.debugf("readiness signal observed at %s", readinessOrigin(candidate.URL))
			if err := h.deps.DSH.ValidateReady(candidate, plan); err != nil {
				h.debugf("readiness validation rejected the announced origin")
				return announcement, h.failureFor(err, lifecycle.ErrorDSHInvalidReadiness, "DSH announced an untrusted workspace origin.", false)
			}
			if failure := h.probeUntilReady(run, worker, candidate, plan, deadlineAt); failure == nil {
				h.debugf("active readiness probe passed")
				if err := h.markReady(run, cleanWorkspaceURL(candidate.URL), candidate.URL); err != nil {
					return announcement, h.failureFor(err, lifecycle.ErrorInvalidTransition, "Work could not publish DSH readiness.", false)
				}
				return announcement, nil
			} else if failure.Code == lifecycle.ErrorDSHInvalidReadiness {
				return announcement, failure
			} else {
				h.debugf("active readiness probe is still pending")
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

func (h *Host) markReady(run *generationRun, workspaceURL, handoffURL string) error {
	status, err := h.machine.MarkReady(run.generation, workspaceURL)
	if err != nil {
		return err
	}
	if ready := h.readyHandler(); ready != nil {
		// The WebView receives the one-launch URL so DSH can mint its own
		// browser session cookie. The lifecycle model only carries the clean
		// loopback origin and never projects the launch credential.
		ready(handoffURL)
	}
	h.emit(status)
	return nil
}

func (h *Host) cleanupWorker(run *generationRun, worker supervisor.Worker) *lifecycle.Failure {
	if worker == nil {
		return nil
	}
	gracefulCtx, cancel := context.WithTimeout(context.Background(), h.config.GracefulStopTimeout)
	_ = h.deps.DSH.RequestShutdown(gracefulCtx, worker)
	cancel()
	if !waitForClosed(worker.Exited(), h.config.GracefulStopTimeout) {
		forceCtx, forceCancel := context.WithTimeout(context.Background(), h.config.ForceStopTimeout)
		forceErr := worker.ForceStop(forceCtx)
		forceCancel()
		if forceErr != nil {
			_ = worker.Close()
			return h.failureFor(forceErr, lifecycle.ErrorProcessStopFailed, "Work could not stop the managed DSH process.", true)
		}
	}
	emptyCtx, emptyCancel := context.WithTimeout(context.Background(), h.config.EmptyTimeout)
	emptyErr := worker.WaitEmpty(emptyCtx)
	emptyCancel()
	closeErr := worker.Close()
	h.mu.Lock()
	h.lastDiagnostics = worker.Diagnostics()
	h.mu.Unlock()
	if emptyErr != nil {
		return h.failureFor(emptyErr, lifecycle.ErrorProcessCleanupFailed, "Work could not verify that the managed process boundary is empty.", true)
	}
	if closeErr != nil {
		return h.failureFor(closeErr, lifecycle.ErrorProcessCleanupFailed, "Work could not close the managed DSH process boundary.", true)
	}
	return nil
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
	if status.State == lifecycle.StateStopping {
		status, _ = h.machine.CompleteStop(run.generation, failure)
	} else if failure != nil {
		status, _ = h.machine.Fail(run.generation, *failure)
	} else {
		if _, err := h.machine.BeginStop(run.generation); err == nil {
			status, _ = h.machine.CompleteStop(run.generation, nil)
		}
	}
	h.emit(status)
	h.mu.Lock()
	if h.current == run {
		h.current = nil
	}
	h.mu.Unlock()
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
			return &copy
		}
		var pointer *lifecycle.Failure
		if errors.As(err, &pointer) && pointer != nil {
			copy := *pointer
			if copy.CorrelationID == "" {
				copy.CorrelationID = h.Status().CorrelationID
			}
			return &copy
		}
	}
	failure := &lifecycle.Failure{
		Code:      fallbackCode,
		Summary:   summary,
		Retryable: retryable,
	}
	if h.Status().State == lifecycle.StateStarting || h.Status().State == lifecycle.StateReady {
		failure.EffectOccurred = true
	}
	failure.CorrelationID = h.Status().CorrelationID
	return failure
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

func (r *generationRun) launchPlan() supervisor.LaunchPlan {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.plan
}

// HostService is the intentionally narrow Wails binding surface.
type HostService struct {
	host *Host
}

func NewHostService(host *Host) *HostService {
	return &HostService{host: host}
}

func (s *HostService) GetStatus() lifecycle.Status {
	return s.host.Status()
}

func (s *HostService) Start() lifecycle.Status {
	return s.host.Start()
}

func (s *HostService) Cancel() lifecycle.Status {
	return s.host.Cancel()
}

func (s *HostService) Quit() lifecycle.Status {
	return s.host.Quit()
}

var _ DSHAdapter = (*dshadapter.Adapter)(nil)

func cleanWorkspaceURL(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return value
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}
