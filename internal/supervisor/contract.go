package supervisor

import (
	"context"
	"fmt"
	"net/url"
)

// LaunchPlan is the platform-neutral process launch contract produced by the
// DSH Adapter and consumed by one native Supervisor Adapter.
type LaunchPlan struct {
	GenerationID     string            `json:"generationId"`
	Executable       string            `json:"executable"`
	Args             []string          `json:"args"`
	Env              map[string]string `json:"env,omitempty"`
	WorkingDirectory string            `json:"workingDirectory"`
	ExpectedOrigin   string            `json:"expectedOrigin"`
}

func (p LaunchPlan) Validate() error {
	if p.GenerationID == "" {
		return fmt.Errorf("generation ID is required")
	}
	if p.Executable == "" {
		return fmt.Errorf("executable is required")
	}
	if p.WorkingDirectory == "" {
		return fmt.Errorf("working directory is required")
	}
	u, err := url.Parse(p.ExpectedOrigin)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" && u.Path != "/" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("expected Worker routing identity is invalid")
	}
	return nil
}

type OutputStream string

const (
	StreamStdout OutputStream = "stdout"
	StreamStderr OutputStream = "stderr"
)

// RawOutputHandler is a synchronous, machine-only readiness seam. A native
// adapter invokes it while a bounded output line is still on its reader stack;
// callers must parse it immediately and must not retain the raw value.
type RawOutputHandler func(OutputStream, string)

// OutputEvent is bounded, redacted process output. It is diagnostic input,
// never a command channel.
type OutputEvent struct {
	Stream OutputStream
	Text   string
}

type ExitResult struct {
	Code    int
	Started bool
	Err     string
}

type Diagnostics struct {
	PID             uint32
	ActiveProcesses uint32
	StdoutTail      string
	StderrTail      string
	Exit            ExitResult
}

// Worker owns one process boundary for one immutable generation.
type Worker interface {
	Events() <-chan OutputEvent
	Exited() <-chan struct{}
	ExitResult() ExitResult
	RequestStop(context.Context) error
	ForceStop(context.Context) error
	WaitEmpty(context.Context) error
	Diagnostics() Diagnostics
	Close() error
}

// Adapter is the only process ownership seam. Native handles, signals,
// process groups and job objects must remain behind this interface.
type Adapter interface {
	Start(context.Context, LaunchPlan, RawOutputHandler) (Worker, error)
}
