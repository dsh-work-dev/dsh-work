package browserruntime

import (
	"github.com/local/dsh-work/internal/supervisor"
	"testing"
)

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
