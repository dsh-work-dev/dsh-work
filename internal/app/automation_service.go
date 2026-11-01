package app

import (
	"context"
	"github.com/local/dsh-work/internal/automation"
)

type AutomationService struct{ service *automation.Service }

func NewAutomationService(service *automation.Service) *AutomationService {
	return &AutomationService{service: service}
}
func (s *AutomationService) GetAutomation(ctx context.Context) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	return s.service.Snapshot(), nil
}
func (s *AutomationService) SetAutomationEnabled(ctx context.Context, capability string, enabled bool) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	return s.service.SetEnabled(ctx, automation.Capability(capability), enabled)
}
func (s *AutomationService) RetryAutomation(ctx context.Context, capability string) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	return s.service.Retry(automation.Capability(capability))
}

func (s *AutomationService) SetDefaultBrowserEnabled(ctx context.Context, enabled bool) (automation.Snapshot, error) {
	if !isTrustedWindow(ctx, "workspace") {
		return automation.Snapshot{}, trustedSurfaceRequired("Open DSH Work Settings.")
	}
	return s.service.SetDefaultBrowser(ctx, enabled)
}
