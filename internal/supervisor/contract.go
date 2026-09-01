package supervisor

import (
	"context"
	"fmt"
	"net/url"
	"strings"
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
	ExpectedHost     string            `json:"expectedHost"`
	ExpectedPort     int               `json:"expectedPort"`
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
	if p.ExpectedHost != "127.0.0.1" {
		return fmt.Errorf("expected host must be 127.0.0.1")
	}
	if p.ExpectedPort < 1 || p.ExpectedPort > 65535 {
		return fmt.Errorf("expected port is outside the TCP range")
	}
	u, err := url.Parse(p.ExpectedOrigin)
	if err != nil || u.Scheme != "http" || u.Hostname() != p.ExpectedHost || u.Port() != fmt.Sprint(p.ExpectedPort) || u.Path != "" && u.Path != "/" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("expected origin is not a loopback HTTP origin")
	}
	return nil
}

type OutputStream string

const (
	StreamStdout OutputStream = "stdout"
	StreamStderr OutputStream = "stderr"
)

// OutputEvent is bounded, redacted process output. It is diagnostic input,
// never a command channel.
type OutputEvent struct {
	Stream OutputStream
	Text   string
	// RawText is a transient machine-only view for a trusted Adapter that must
	// parse a one-launch credential from process output. It must never be
	// buffered, logged or projected; Text is the redacted diagnostic view.
	RawText string `json:"-"`
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
	Start(context.Context, LaunchPlan) (Worker, error)
}

// Redact is intentionally conservative: it removes common secret-shaped
// values before output is retained in a diagnostic buffer or projected.
func Redact(input string) string {
	if input == "" {
		return ""
	}
	redacted := input
	for _, key := range []string{"api_key", "apikey", "api-key", "token", "password", "secret", "authorization", "cookie"} {
		redacted = redactKeyValue(redacted, key)
	}
	return redacted
}

func redactKeyValue(input, key string) string {
	lower := strings.ToLower(input)
	searchFrom := 0
	for {
		index := strings.Index(lower[searchFrom:], key)
		if index < 0 {
			return input
		}
		index += searchFrom
		if (index > 0 && isWordByte(input[index-1])) || (index+len(key) < len(input) && isWordByte(input[index+len(key)])) {
			searchFrom = index + len(key)
			continue
		}
		separator := index + len(key)
		for separator < len(input) && (input[separator] == ' ' || input[separator] == '\t' || input[separator] == ':' || input[separator] == '=') {
			separator++
		}
		if separator == index+len(key) {
			searchFrom = separator
			continue
		}
		end := separator
		for end < len(input) && input[end] != ' ' && input[end] != '\t' && input[end] != '\r' && input[end] != '\n' && input[end] != ',' && input[end] != ';' {
			end++
		}
		input = input[:separator] + "[REDACTED]" + input[end:]
		lower = strings.ToLower(input)
		searchFrom = separator + len("[REDACTED]")
	}
}

func isWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_'
}
