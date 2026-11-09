package supervisor

import "context"

// ProcessInstance names a kernel process within one owned process boundary.
// Started uses Diagnostics.ProcessStarted's platform-specific identity.
type ProcessInstance struct {
	PID     uint32 `json:"pid"`
	Started uint64 `json:"started"`
	Image   string `json:"-"`
}

// ProcessLease retains the actual process object, not just a reusable PID.
// Arguments is machine-only evidence: callers must not log or persist it.
// Close releases this lease, never terminates the process.
type ProcessLease interface {
	Instance() ProcessInstance
	Arguments(context.Context) ([]string, error)
	Alive() (bool, error)
	Close() error
}

// ProcessInventory is an optional extension. Original Workers need not provide
// it. A caller owns every returned lease, including a partial result with an
// error; incomplete inventories cannot establish a complete influence graph.
type ProcessInventory interface {
	OwnedProcesses(context.Context) ([]ProcessLease, error)
}
