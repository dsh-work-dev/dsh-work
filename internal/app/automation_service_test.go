package app

import (
	"context"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/browserhost"
	"github.com/local/dsh-work/internal/settings"
	"testing"
)

func TestStandalonePermissionSurfaceHasOnlyNarrowAuthority(t *testing.T) {
	s := automation.New(newPetServiceManager(t, settings.DefaultValues()), nil)
	defer s.Close()
	api := NewAutomationService(s)
	popup := LocalClientContext(context.Background(), "browser-permission")
	if _, e := api.GetBrowserPermission(popup); e != nil {
		t.Fatal(e)
	}
	if _, e := api.GetAutomation(popup); e == nil {
		t.Fatal("permission popup read broad workbench state")
	}
	if _, e := api.RepairAutomation(popup, "browser"); e == nil {
		t.Fatal("permission popup changed component state")
	}
	if _, e := api.GetBrowserPermission(LocalClientContext(context.Background(), "worker")); e == nil {
		t.Fatal("worker read consent")
	}
}

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
	for _, action := range []func(context.Context) error{
		func(ctx context.Context) error { _, e := service.ConfirmBrowserBinding(ctx, "candidate"); return e },
		func(ctx context.Context) error { _, e := service.SelectBrowserBinding(ctx, "binding"); return e },
		func(ctx context.Context) error { _, e := service.CancelBrowserBinding(ctx, "candidate"); return e },
		func(ctx context.Context) error { _, e := service.ForgetBrowserBinding(ctx, "binding"); return e },
		func(ctx context.Context) error { _, e := service.PrepareBrowserConnection(ctx); return e },

		func(ctx context.Context) error {
			_, e := service.RenameBrowserBinding(ctx, "binding", "name")
			return e
		},
		func(ctx context.Context) error { _, e := service.ConfigureBrowserLaunch(ctx, "binding"); return e },
		func(ctx context.Context) error { _, e := service.ClearBrowserLaunch(ctx, "binding"); return e },
		func(ctx context.Context) error { _, e := service.OpenBrowserProfile(ctx, "binding"); return e },
		service.OpenBrowserExtensionFolder,
	} {
		if e := action(worker); e == nil {
			t.Fatal("Worker changed browser setup")
		}
		if e := action(LocalClientContext(context.Background(), "workspace")); e == nil {
			t.Fatal("changed setup before Worker readiness")
		}
	}
	workspace := LocalClientContext(context.Background(), "workspace")
	if _, err := service.GetAutomation(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAutomationEnabled(workspace, "browser", true); err == nil {
		t.Fatal("changed settings before Worker readiness")
	}
}

type setupRegistrar struct{ calls int }

func (r *setupRegistrar) Ensure(context.Context) []browserhost.Registration {
	r.calls++
	return []browserhost.Registration{{Browser: "chrome", Registered: true}}
}
func (*setupRegistrar) Refresh(context.Context) error { return nil }
func (*setupRegistrar) Remove(context.Context) error  { return nil }
func TestEnablingBrowserChecksSetupBeforeAnyToolCall(t *testing.T) {
	state := automation.New(newPetServiceManager(t, settings.DefaultValues()), nil)
	defer state.Close()
	state.Worker("setup", true, true)
	service := NewAutomationService(state)
	registrar := &setupRegistrar{}
	SetBrowserHostRegistrar(service, registrar)
	workspace := LocalClientContext(context.Background(), "workspace")
	snapshot, err := service.SetAutomationEnabled(workspace, "browser", true)
	if err != nil {
		t.Fatal(err)
	}
	if registrar.calls != 1 || snapshot.Profiles.Discovery == nil || len(snapshot.Profiles.Registration) != 1 {
		t.Fatal("enabling did not check browser setup")
	}
	if len(snapshot.Profiles.Bindings) != 0 || snapshot.Profiles.Selected != "" || len(snapshot.BrowserTools) != 0 {
		t.Fatal("setup fabricated binding or granted execution")
	}
	service.SetDefaultBrowserEnabled(workspace, false)
	if _, err = service.SetDefaultBrowserEnabled(workspace, true); err != nil {
		t.Fatal(err)
	}
	if registrar.calls != 2 {
		t.Fatal("reenabling default path skipped setup check")
	}
}
