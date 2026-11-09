package browserruntime

import (
	"github.com/local/dsh-work/internal/supervisor"
	"strings"
	"sync"
)

// Fixed adapter stages are the only stderr metadata accepted by this seam.
// Raw output is inspected synchronously and never retained or forwarded.
func adapterDiagnosticOutput(enabled bool, observe func(string)) supervisor.RawOutputHandler {
	if !enabled || observe == nil {
		return nil
	}
	var mu sync.Mutex
	seen := map[string]bool{}
	return func(stream supervisor.OutputStream, line string) {
		if stream != supervisor.StreamStderr {
			return
		}
		stage, ok := strings.CutPrefix(line, "DSH_MCP_EVENT ")
		if !ok {
			return
		}
		switch stage {
		case "entry", "private-connected", "hmac-authenticated", "init", "adapter-ready", "route-ready", "sdk-connect-start", "sdk-connected", "tools-ready", "inspect-start", "inspect-complete", "inspect-error", "stop-private-error", "stop-private-close", "stop-private-timeout", "stop-hmac-rejected", "stop-frame-rejected", "stop-protocol-error", "stop-cdp-error", "stop-cdp-close", "stop-wire-timeout", "stop-call-fatal":
		default:
			return
		}
		mu.Lock()
		if seen[stage] {
			mu.Unlock()
			return
		}
		seen[stage] = true
		mu.Unlock()
		observe(stage)
	}
}
