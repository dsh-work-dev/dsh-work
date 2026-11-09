package browserruntime

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/supervisor"
	"testing"
)

func TestRuntimeDiagnosticReportsFirstFixedFailureOnly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var got []string
	r := &resource{ctx: ctx, cancel: cancel, done: make(chan struct{}), observeFailure: func(stage string) { got = append(got, stage) }}
	first := errors.New("private error containing a page body")
	r.failAt("failure-adapter-read", first)
	r.failAt("failure-route-done", errors.New("cleanup consequence"))
	if len(got) != 1 || got[0] != "failure-adapter-read" || !errors.Is(r.failure(), first) {
		t.Fatal(got, r.failure())
	}
	for _, stage := range []string{"", "failure-adapter-read private body", "failure-unknown"} {
		if runtimeFailureStage(stage) {
			t.Fatal("accepted uncontrolled diagnostic", stage)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterDiagnosticAcceptsOnlyFixedStageOnce(t *testing.T) {
	var got []string
	receive := adapterDiagnosticOutput(true, func(value string) { got = append(got, value) })
	for _, value := range []string{"raw page or secret", "DSH_MCP_EVENT inspect-start private body", "DSH_MCP_EVENT inspect-start", "DSH_MCP_EVENT inspect-start", "DSH_MCP_EVENT inspect-complete"} {
		receive(supervisor.StreamStderr, value)
	}
	receive(supervisor.StreamStdout, "DSH_MCP_EVENT entry")
	if len(got) != 2 || got[0] != "inspect-start" || got[1] != "inspect-complete" {
		t.Fatal(got)
	}
	if adapterDiagnosticOutput(false, func(string) { t.Fatal("disabled diagnostics") }) != nil {
		t.Fatal("disabled handler installed")
	}
}
