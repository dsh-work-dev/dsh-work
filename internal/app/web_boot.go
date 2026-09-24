package app

import (
	"errors"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/supervisor"
)

// WebBootURL exposes only the probed generation while the browser activates
// plugins. Publishing this URL must not commit a successful version record.
func (h *Host) WebBootURL() string {
	h.mu.Lock()
	run := h.current
	h.mu.Unlock()
	if run == nil || run.ctx.Err() != nil {
		return ""
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	return run.readyWorkspaceURL
}

// ReportWebBoot accepts one terminal result from the current Worker WebView.
func (h *Host) ReportWebBoot(generation, detail string) error {
	h.mu.Lock()
	run := h.current
	h.mu.Unlock()
	if run == nil || run.generation != generation || run.ctx.Err() != nil || !h.config.RequireWebBoot {
		return errors.New("expired web boot generation")
	}
	run.mu.RLock()
	pending := run.readyWorkspaceURL != ""
	run.mu.RUnlock()
	if !pending {
		return errors.New("web boot is not pending")
	}
	if len(detail) > 4096 {
		detail = detail[:4096]
	}
	run.webBootOnce.Do(func() { run.webBoot <- supervisor.Redact(detail) })
	return nil
}

func (h *Host) waitWebBoot(run *generationRun, worker supervisor.Worker) *lifecycle.Failure {
	timer := time.NewTimer(h.config.ReadinessTimeout)
	defer timer.Stop()
	select {
	case <-run.ctx.Done():
		return nil
	case <-worker.Exited():
		return h.failureFor(errors.New("DSH exited during web boot"), lifecycle.ErrorDSHEarlyExit, "The DSH process exited before its workspace became ready.", true)
	case detail := <-run.webBoot:
		if detail == "" {
			return nil
		}
		return &lifecycle.Failure{Code: lifecycle.ErrorDSHStartFailed, Summary: "DSH could not load its workspace plugins.", Detail: detail, Retryable: true}
	case <-timer.C:
		return &lifecycle.Failure{Code: lifecycle.ErrorDSHReadinessTimeout, Summary: "DSH did not finish loading its workspace.", Detail: "The WebView did not confirm plugin activation and application mount before the startup deadline.", Retryable: true}
	}
}
