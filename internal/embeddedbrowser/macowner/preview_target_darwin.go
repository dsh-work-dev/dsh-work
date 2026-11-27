//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package macowner

import "github.com/local/dsh-work/internal/localipc"

// PreviewBrokerTarget returns only the guard's authenticated native broker.
func (a *Adapter) PreviewBrokerTarget() (localipc.ProcessTarget, error) {
	a.mu.Lock()
	worker := a.worker
	a.mu.Unlock()
	if worker == nil {
		return localipc.ProcessTarget{}, ErrCleanupPending
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	record := worker.state.Owned[processKey(worker.state.Broker)]
	if record.Target == nil || record.Role != "broker:" {
		return localipc.ProcessTarget{}, ErrCleanupPending
	}
	return *record.Target, nil
}
