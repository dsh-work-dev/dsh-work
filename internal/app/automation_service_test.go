package app

import (
	"context"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/settings"
	"testing"
)

func TestAutomationBindingsRequireTrustedWorkbench(t *testing.T) {
	state := automation.New(newPetServiceManager(t, settings.DefaultValues()), nil)
	defer state.Close()
	service := NewAutomationService(state)
	worker := LocalClientContext(context.Background(), "worker")
	if _, err := service.GetAutomation(worker); err == nil {
		t.Fatal("Worker read trusted settings")
	}
	if _, err := service.SetAutomationEnabled(worker, "browser", true); err == nil {
		t.Fatal("Worker changed intent")
	}
	if _, err := service.SetDefaultBrowserEnabled(worker, false); err == nil {
		t.Fatal("Worker changed browser path")
	}
	if _, err := service.RetryAutomation(worker, "browser"); err == nil {
		t.Fatal("Worker retried trusted preparation")
	}
	workspace := LocalClientContext(context.Background(), "workspace")
	if _, err := service.GetAutomation(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAutomationEnabled(workspace, "browser", true); err == nil {
		t.Fatal("changed settings before Worker readiness")
	}
}
