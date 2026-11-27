package desktopapp

import (
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/logging"
	"os"
	"runtime"
	"sync"
	"time"
)

// Candidate-fixture-only bounded metadata. This is not a settings/UI switch.
// No route values, parameters, result values, tokens or raw stderr are logged.
type embeddedAgentDiagnostic struct {
	enabled        bool
	mu             sync.Mutex
	count, brokers int
	started        time.Time
}

func newEmbeddedAgentDiagnostic() *embeddedAgentDiagnostic {
	return &embeddedAgentDiagnostic{enabled: runtime.GOOS == "linux" && os.Getenv("DSH_WORK_EMBEDDED_CHECKPOINT") == "1" && os.Getenv("DSH_WORK_LINUX_AGENT_DIAGNOSTICS") == "1", started: time.Now()}
}
func (d *embeddedAgentDiagnostic) environment(original map[string]string) map[string]string {
	if !d.enabled {
		return original
	}
	env := make(map[string]string, len(original)+1)
	for k, v := range original {
		env[k] = v
	}
	env["DSH_WORK_BROWSER_DIAGNOSTICS"] = "1"
	return env
}
func (d *embeddedAgentDiagnostic) stage(stage string) {
	if !d.enabled {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.count >= 1024 {
		return
	}
	d.count++
	logging.Module(logging.ModRuntime).Info("browser adapter metadata", "stage", stage, "elapsed_us", time.Since(d.started).Microseconds())
}
func (d *embeddedAgentDiagnostic) commands() func(embeddedbrowser.ProtocolObservation) {
	if !d.enabled {
		return nil
	}
	d.mu.Lock()
	d.brokers++
	broker := d.brokers
	d.mu.Unlock()
	return func(value embeddedbrowser.ProtocolObservation) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.count >= 1024 {
			return
		}
		d.count++
		logging.Module(logging.ModRuntime).Info("browser CDP metadata", "broker", broker, "stage", value.Stage, "route", value.Route, "id", value.ID, "method", value.Method, "observed_at", value.At.UTC().Format(time.RFC3339Nano), "elapsed_us", value.At.Sub(d.started).Microseconds())
	}
}
