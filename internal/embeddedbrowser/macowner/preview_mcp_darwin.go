//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package macowner

import "github.com/local/dsh-work/internal/supervisor"

// PreviewMCPOutcome projects the authenticated generation status retained
// after retirement. The fixture can report it without retaining launch inputs.
func PreviewMCPOutcome(worker supervisor.Worker) (phase string, exitCode, signal int, events []string) {
	w, ok := worker.(*mcpWorker)
	if !ok {
		return "", 0, 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, event := range w.state.NativeEvents {
		events = append(events, event.Name)
	}
	return w.state.Phase, w.state.Exit.Code, w.state.ExitSignal, events
}
