package lifecycle

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
)

// State is the platform-neutral lifecycle state projected to the trusted UI.
type State string

const (
	StateStarting State = "Starting"
	StateReady    State = "Ready"
	StateStopping State = "Stopping"
	StateStopped  State = "Stopped"
	StateFailed   State = "Failed"
)

// Phase identifies the bounded startup or shutdown step currently in progress.
type Phase string

const (
	PhaseIdle          Phase = "idle"
	PhaseConfiguration Phase = "configuration"
	PhaseRuntime       Phase = "runtime"
	PhaseWorker        Phase = "worker"
	PhaseReadiness     Phase = "readiness"
	PhaseWorkspace     Phase = "workspace"
	PhaseStopping      Phase = "stopping"
	PhaseFailed        Phase = "failed"
)

// ErrorCode is the stable Work error vocabulary. Dependency-specific detail
// stays at the Adapter boundary and is never used as a UI control protocol.
type ErrorCode string

const (
	ErrorInvalidTransition          ErrorCode = "INVALID_LIFECYCLE_TRANSITION"
	ErrorPlatformUnsupported        ErrorCode = "PLATFORM_UNSUPPORTED"
	ErrorDSHRuntimeNotFound         ErrorCode = "DSH_RUNTIME_NOT_FOUND"
	ErrorDSHVersionCheckFailed      ErrorCode = "DSH_VERSION_CHECK_FAILED"
	ErrorDSHUnsupportedVersion      ErrorCode = "DSH_UNSUPPORTED_VERSION"
	ErrorDSHStartFailed             ErrorCode = "DSH_START_FAILED"
	ErrorDSHEarlyExit               ErrorCode = "DSH_EARLY_EXIT"
	ErrorDSHReadinessTimeout        ErrorCode = "DSH_READINESS_TIMEOUT"
	ErrorDSHInvalidReadiness        ErrorCode = "DSH_INVALID_READINESS"
	ErrorGatewayUnavailable         ErrorCode = "WORKER_GATEWAY_UNAVAILABLE"
	ErrorGatewayStartFailed         ErrorCode = "WORKER_GATEWAY_START_FAILED"
	ErrorGatewayCloseFailed         ErrorCode = "WORKER_GATEWAY_CLOSE_FAILED"
	ErrorProcessStartFailed         ErrorCode = "PROCESS_START_FAILED"
	ErrorProcessCleanupFailed       ErrorCode = "PROCESS_CLEANUP_FAILED"
	ErrorProcessStopFailed          ErrorCode = "PROCESS_STOP_FAILED"
	ErrorCancelled                  ErrorCode = "CANCELLED"
	ErrorProfileRequired            ErrorCode = "PROFILE_REQUIRED"
	ErrorProfileNotFound            ErrorCode = "PROFILE_NOT_FOUND"
	ErrorProfileInvalid             ErrorCode = "PROFILE_INVALID"
	ErrorProfileInUse               ErrorCode = "PROFILE_IN_USE"
	ErrorProfileRenameFailed        ErrorCode = "PROFILE_RENAME_FAILED"
	ErrorRuntimeInUse               ErrorCode = "RUNTIME_IN_USE"
	ErrorRuntimeProfileIncompatible ErrorCode = "RUNTIME_PROFILE_INCOMPATIBLE"
	ErrorManagerOperationBusy       ErrorCode = "MANAGER_OPERATION_BUSY"
	ErrorManagerStateInvalid        ErrorCode = "MANAGER_STATE_INVALID"
	ErrorSettingsStateInvalid       ErrorCode = "SETTINGS_STATE_INVALID"
	ErrorSettingsUnavailable        ErrorCode = "SETTINGS_UNAVAILABLE"
	ErrorNotificationDeliveryFailed ErrorCode = "NOTIFICATION_DELIVERY_FAILED"
	ErrorRestartRequired            ErrorCode = "RESTART_REQUIRED"
	ErrorPluginSpecInvalid          ErrorCode = "PLUGIN_SPEC_INVALID"
	ErrorPluginCommandUnavailable   ErrorCode = "PLUGIN_COMMAND_UNAVAILABLE"
	ErrorPluginCommandFailed        ErrorCode = "PLUGIN_COMMAND_FAILED"
	ErrorRuntimeInstallUnavailable  ErrorCode = "RUNTIME_INSTALL_UNAVAILABLE"
	ErrorRuntimeInstallFailed       ErrorCode = "RUNTIME_INSTALL_FAILED"
	ErrorTrustedSurfaceRequired     ErrorCode = "TRUSTED_SURFACE_REQUIRED"
)

// Failure is safe to project to the trusted UI. Detail is bounded diagnostic
// context and must already be redacted by the producing Adapter.
type Failure struct {
	Code           ErrorCode `json:"code"`
	Summary        string    `json:"summary"`
	Retryable      bool      `json:"retryable"`
	EffectOccurred bool      `json:"effectOccurred"`
	CorrelationID  string    `json:"correlationId"`
	Detail         string    `json:"detail,omitempty"`
}

func (f Failure) Error() string {
	if f.Detail == "" {
		return fmt.Sprintf("%s: %s", f.Code, f.Summary)
	}
	return fmt.Sprintf("%s: %s (%s)", f.Code, f.Summary, f.Detail)
}

// Status is the immutable read model consumed by the frontend.
type Status struct {
	State         State    `json:"state"`
	Phase         Phase    `json:"phase"`
	GenerationID  string   `json:"generationId,omitempty"`
	WorkspaceURL  string   `json:"workspaceUrl,omitempty"`
	Error         *Failure `json:"error,omitempty"`
	CanRetry      bool     `json:"canRetry"`
	CanCancel     bool     `json:"canCancel"`
	CorrelationID string   `json:"correlationId,omitempty"`
}

// TransitionError identifies an illegal state transition without exposing
// operating-system or dependency-specific values to callers.
type TransitionError struct {
	From State
	To   State
	Why  string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf("%s: %s -> %s (%s)", ErrorInvalidTransition, e.From, e.To, e.Why)
}

var generationSequence atomic.Uint64

func newGenerationID() string {
	var random [8]byte
	if _, err := rand.Read(random[:]); err == nil {
		return hex.EncodeToString(random[:])
	}
	return fmt.Sprintf("local-%d", generationSequence.Add(1))
}

// NewCorrelationID gives non-lifecycle operations the same opaque diagnostic
// identifier contract as a Host generation. It carries no user or process
// data and is safe to project with a manager failure.
func NewCorrelationID() string {
	return newGenerationID()
}

// Machine serializes all lifecycle transitions. Callers hold no platform or
// UI knowledge; the returned Status is the only state they need to project.
type Machine struct {
	mu     sync.Mutex
	status Status
}

func NewMachine() *Machine {
	return &Machine{status: Status{
		State:     StateStopped,
		Phase:     PhaseIdle,
		CanRetry:  false,
		CanCancel: false,
	}}
}

func (m *Machine) Snapshot() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneStatus(m.status)
}

// BeginStart allocates a new immutable generation and moves the machine into
// Starting. A prior Failed generation may be retried, but it can never emit
// events that mutate the new generation.
func (m *Machine) BeginStart() (string, Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State != StateStopped && m.status.State != StateFailed {
		return "", cloneStatus(m.status), &TransitionError{From: m.status.State, To: StateStarting, Why: "start is already active"}
	}

	generationID := newGenerationID()
	m.status = Status{
		State:         StateStarting,
		Phase:         PhaseConfiguration,
		GenerationID:  generationID,
		CorrelationID: generationID,
		CanCancel:     true,
	}
	return generationID, cloneStatus(m.status), nil
}

func (m *Machine) SetPhase(generationID string, phase Phase) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkGeneration(generationID); err != nil {
		return cloneStatus(m.status), err
	}
	if m.status.State != StateStarting && m.status.State != StateStopping {
		return cloneStatus(m.status), &TransitionError{From: m.status.State, To: m.status.State, Why: "phase cannot change after a terminal result"}
	}
	if !validPhaseTransition(m.status.State, m.status.Phase, phase) {
		return cloneStatus(m.status), &TransitionError{From: m.status.State, To: m.status.State, Why: fmt.Sprintf("phase cannot move from %s to %s", m.status.Phase, phase)}
	}
	m.status.Phase = phase
	return cloneStatus(m.status), nil
}

func validPhaseTransition(state State, from, to Phase) bool {
	if from == to {
		return true
	}
	if state == StateStopping {
		return from == PhaseStopping && to == PhaseStopping
	}
	if state != StateStarting {
		return false
	}
	switch from {
	case PhaseConfiguration:
		return to == PhaseRuntime
	case PhaseRuntime:
		return to == PhaseWorker
	case PhaseWorker:
		return to == PhaseReadiness
	case PhaseReadiness:
		return false
	default:
		return false
	}
}

func (m *Machine) MarkReady(generationID, workspaceURL string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkGeneration(generationID); err != nil {
		return cloneStatus(m.status), err
	}
	if m.status.State != StateStarting {
		return cloneStatus(m.status), &TransitionError{From: m.status.State, To: StateReady, Why: "readiness is only valid during startup"}
	}
	if workspaceURL == "" {
		return cloneStatus(m.status), &TransitionError{From: StateStarting, To: StateReady, Why: "workspace URL is required"}
	}
	m.status.State = StateReady
	m.status.Phase = PhaseWorkspace
	m.status.WorkspaceURL = workspaceURL
	m.status.CanCancel = true
	return cloneStatus(m.status), nil
}

// BeginStop is idempotent for the same generation so window-close, explicit
// quit and application shutdown can converge on one cleanup owner.
func (m *Machine) BeginStop(generationID string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if generationID == "" && m.status.State == StateStopped {
		return cloneStatus(m.status), nil
	}
	if err := m.checkGeneration(generationID); err != nil {
		return cloneStatus(m.status), err
	}
	if m.status.State == StateStopping {
		return cloneStatus(m.status), nil
	}
	if m.status.State != StateStarting && m.status.State != StateReady && m.status.State != StateFailed {
		return cloneStatus(m.status), &TransitionError{From: m.status.State, To: StateStopping, Why: "generation has no active stop boundary"}
	}
	m.status.State = StateStopping
	m.status.Phase = PhaseStopping
	m.status.CanCancel = false
	m.status.CanRetry = false
	return cloneStatus(m.status), nil
}

func (m *Machine) CompleteStop(generationID string, failure *Failure) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkGeneration(generationID); err != nil {
		return cloneStatus(m.status), err
	}
	if m.status.State != StateStopping {
		return cloneStatus(m.status), &TransitionError{From: m.status.State, To: StateStopped, Why: "stop has not been requested"}
	}
	if failure != nil {
		copy := *failure
		m.status.State = StateFailed
		m.status.Phase = PhaseFailed
		m.status.Error = &copy
		m.status.CanRetry = failure.Retryable
		m.status.CanCancel = false
		return cloneStatus(m.status), nil
	}
	m.status.State = StateStopped
	m.status.Phase = PhaseIdle
	m.status.WorkspaceURL = ""
	m.status.Error = nil
	m.status.CanRetry = false
	m.status.CanCancel = false
	return cloneStatus(m.status), nil
}

func (m *Machine) Fail(generationID string, failure Failure) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkGeneration(generationID); err != nil {
		return cloneStatus(m.status), err
	}
	if m.status.State != StateStarting && m.status.State != StateReady {
		return cloneStatus(m.status), &TransitionError{From: m.status.State, To: StateFailed, Why: "failure is already terminal or cleanup is active"}
	}
	if failure.CorrelationID == "" {
		failure.CorrelationID = m.status.CorrelationID
	}
	m.status.State = StateFailed
	m.status.Phase = PhaseFailed
	m.status.Error = &failure
	m.status.CanRetry = failure.Retryable
	m.status.CanCancel = false
	return cloneStatus(m.status), nil
}

func (m *Machine) checkGeneration(generationID string) error {
	if generationID == "" || generationID != m.status.GenerationID {
		return &TransitionError{From: m.status.State, To: m.status.State, Why: "stale or missing generation"}
	}
	return nil
}

func cloneStatus(status Status) Status {
	copy := status
	if status.Error != nil {
		failure := *status.Error
		copy.Error = &failure
	}
	return copy
}
