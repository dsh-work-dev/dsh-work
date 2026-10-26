package app

import (
	"context"
	"errors"
	"testing"

	"github.com/local/dsh-work/internal/settings"
)

func TestSetAppearanceRequiresTheWorkbenchAndPublishesAfterSave(t *testing.T) {
	manager := newPetServiceManager(t, settings.DefaultValues())
	service := NewSettingsService(manager, nil, nil)
	var published []settings.Appearance
	SetAppearanceChanged(service, func(appearance settings.Appearance) { published = append(published, appearance) })

	if _, err := service.SetAppearance(LocalClientContext(context.Background(), "worker"), "monochrome", "dark"); err == nil {
		t.Fatal("an untrusted surface changed the appearance")
	}
	if _, err := service.SetAppearance(LocalClientContext(context.Background(), "workspace"), "monochrome", "sepia"); err == nil {
		t.Fatal("an unsupported mode was accepted")
	}
	if len(published) != 0 {
		t.Fatalf("published before a successful save: %#v", published)
	}
	values, err := service.SetAppearance(LocalClientContext(context.Background(), "workspace"), "monochrome", "dark")
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Appearance{Theme: settings.ThemeMonochrome, Mode: settings.AppearanceDark}
	if values.Appearance != want || len(published) != 1 || published[0] != want {
		t.Fatalf("values = %#v, published = %#v", values.Appearance, published)
	}
}

func TestAcknowledgeBackgroundNoticeRequiresTheWorkbenchAndPublishesSavedValues(t *testing.T) {
	values := settings.DefaultValues()
	store := &petServiceStore{values: &values}
	manager := newPetServiceManagerWithStore(t, store, values)
	service := NewSettingsService(manager, nil, nil)
	published := 0
	manager.OnChange(func(values settings.Values) {
		if !values.BackgroundNoticeAcknowledged || store.saves != 1 {
			t.Error("acknowledgement published before persistence")
		}
		published++
	})
	if _, err := service.AcknowledgeBackgroundNotice(LocalClientContext(context.Background(), "worker")); err == nil {
		t.Fatal("untrusted caller acknowledged notice")
	}
	if store.saves != 0 || published != 0 {
		t.Fatal("untrusted call changed settings")
	}
	got, err := service.AcknowledgeBackgroundNotice(LocalClientContext(context.Background(), "workspace"))
	if err != nil || !got.BackgroundNoticeAcknowledged || !store.values.BackgroundNoticeAcknowledged || published != 1 {
		t.Fatalf("got=%+v published=%d err=%v", got, published, err)
	}
}

func TestSetVerboseLoggingRequiresTheWorkbenchAndNotifies(t *testing.T) {
	manager := newPetServiceManager(t, settings.DefaultValues())
	service := NewSettingsService(manager, nil, nil)
	var applied []bool
	SetVerboseLoggingChanged(service, func(enabled bool) { applied = append(applied, enabled) })

	if _, err := service.SetVerboseLogging(LocalClientContext(context.Background(), "worker"), true); err == nil {
		t.Fatal("an untrusted surface changed verbose logging")
	}
	if len(applied) != 0 {
		t.Fatalf("verbose logging applied before a successful trusted save: %v", applied)
	}
	values, err := service.SetVerboseLogging(LocalClientContext(context.Background(), "workspace"), true)
	if err != nil {
		t.Fatal(err)
	}
	if !values.VerboseLogging || len(applied) != 1 || !applied[0] {
		t.Fatalf("values = %v, applied = %v", values.VerboseLogging, applied)
	}
}

func TestSetUpdateChannelSwitchesTheUpdaterBeforeSaving(t *testing.T) {
	values := settings.DefaultValues()
	store := &petServiceStore{values: &values}
	manager := newPetServiceManagerWithStore(t, store, values)
	service := NewSettingsService(manager, nil, nil)
	var switches []settings.UpdateChannel
	var savesAtSwitch []int
	SetUpdateChannelChanged(service, func(_ context.Context, channel settings.UpdateChannel) error {
		switches = append(switches, channel)
		savesAtSwitch = append(savesAtSwitch, store.saves)
		return nil
	})

	if _, err := service.SetUpdateChannel(LocalClientContext(context.Background(), "worker"), "nightly"); err == nil {
		t.Fatal("an untrusted surface changed the update channel")
	}
	saved, err := service.SetUpdateChannel(LocalClientContext(context.Background(), "workspace"), "nightly")
	if err != nil {
		t.Fatal(err)
	}
	if len(switches) != 1 || switches[0] != settings.UpdateChannelNightly {
		t.Fatalf("updater switches = %v, want [nightly]", switches)
	}
	if savesAtSwitch[0] != 0 {
		t.Fatal("the channel was saved before the updater switched")
	}
	if saved.UpdateChannel != settings.UpdateChannelNightly || store.saves != 1 {
		t.Fatalf("saved channel = %q with %d saves", saved.UpdateChannel, store.saves)
	}
	if got, err := manager.Snapshot(context.Background()); err != nil || got.UpdateChannel != settings.UpdateChannelNightly {
		t.Fatalf("stored channel = %q (%v)", got.UpdateChannel, err)
	}
}

func TestSetUpdateChannelRefusalKeepsTheSavedChannel(t *testing.T) {
	values := settings.DefaultValues()
	store := &petServiceStore{values: &values}
	manager := newPetServiceManagerWithStore(t, store, values)
	service := NewSettingsService(manager, nil, nil)
	var switches []settings.UpdateChannel
	SetUpdateChannelChanged(service, func(_ context.Context, channel settings.UpdateChannel) error {
		switches = append(switches, channel)
		return errors.New("the update channel cannot change while an update is installing")
	})

	if _, err := service.SetUpdateChannel(LocalClientContext(context.Background(), "workspace"), "nightly"); err == nil {
		t.Fatal("a channel switch the updater refused was accepted")
	}
	if len(switches) != 1 || switches[0] != settings.UpdateChannelNightly {
		t.Fatalf("updater switches = %v, want [nightly]", switches)
	}
	if store.saves != 0 {
		t.Fatalf("a refused switch saved settings %d times", store.saves)
	}
	if got := values.UpdateChannel; got != settings.UpdateChannelStable {
		t.Fatalf("stored channel = %q after a refused switch, want stable", got)
	}
}

func TestSetUpdateChannelRestoresTheUpdaterWhenTheSaveFails(t *testing.T) {
	values := settings.DefaultValues()
	store := &petServiceStore{values: &values, err: errors.New("settings file is not writable")}
	manager := newPetServiceManagerWithStore(t, store, values)
	service := NewSettingsService(manager, nil, nil)
	var switches []settings.UpdateChannel
	SetUpdateChannelChanged(service, func(_ context.Context, channel settings.UpdateChannel) error {
		switches = append(switches, channel)
		return nil
	})

	if _, err := service.SetUpdateChannel(LocalClientContext(context.Background(), "workspace"), "beta"); err == nil {
		t.Fatal("a channel that could not be saved was reported as saved")
	}
	if len(switches) != 2 || switches[0] != settings.UpdateChannelBeta || switches[1] != settings.UpdateChannelStable {
		t.Fatalf("updater switches = %v, want [beta stable]", switches)
	}
	if got := values.UpdateChannel; got != settings.UpdateChannelStable {
		t.Fatalf("stored channel = %q after a failed save, want stable", got)
	}
}

func TestSetUpdateChannelRejectsUnknownChannelsAndRepeats(t *testing.T) {
	values := settings.DefaultValues()
	store := &petServiceStore{values: &values}
	manager := newPetServiceManagerWithStore(t, store, values)
	service := NewSettingsService(manager, nil, nil)
	var switches []settings.UpdateChannel
	SetUpdateChannelChanged(service, func(_ context.Context, channel settings.UpdateChannel) error {
		switches = append(switches, channel)
		return nil
	})

	if _, err := service.SetUpdateChannel(LocalClientContext(context.Background(), "workspace"), "canary"); err == nil {
		t.Fatal("an unknown update channel was accepted")
	}
	repeat, err := service.SetUpdateChannel(LocalClientContext(context.Background(), "workspace"), "stable")
	if err != nil {
		t.Fatal(err)
	}
	if len(switches) != 0 || store.saves != 0 {
		t.Fatalf("switches = %v with %d saves, want no work", switches, store.saves)
	}
	if repeat.UpdateChannel != settings.UpdateChannelStable {
		t.Fatalf("channel = %q, want stable", repeat.UpdateChannel)
	}
}

func TestHostAppearanceIsReadOnlyForTheStartupSurface(t *testing.T) {
	service := NewHostService(&Host{}, nil)
	stored := settings.Appearance{Theme: settings.ThemeMonochrome, Mode: settings.AppearanceLight}
	SetHostAppearanceProvider(service, func() settings.Appearance { return stored })
	if got := service.GetAppearance(LocalClientContext(context.Background(), "workspace")); got != stored {
		t.Fatalf("startup appearance = %#v", got)
	}
	if got := service.GetAppearance(context.Background()); got != settings.DefaultAppearance() {
		t.Fatalf("untrusted caller read %#v", got)
	}
}
