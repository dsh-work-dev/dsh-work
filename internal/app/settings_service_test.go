package app

import (
	"context"
	"testing"

	"github.com/local/dsh-work/internal/settings"
)

func TestSetAppearanceRequiresSettingsWindowAndPublishesAfterSave(t *testing.T) {
	manager := newPetServiceManager(t, settings.DefaultValues())
	service := NewSettingsService(manager, nil, nil)
	var published []settings.Appearance
	SetAppearanceChanged(service, func(appearance settings.Appearance) { published = append(published, appearance) })

	if _, err := service.SetAppearance(LocalClientContext(context.Background(), "workspace"), "monochrome", "dark"); err == nil {
		t.Fatal("the startup surface changed the appearance")
	}
	if _, err := service.SetAppearance(LocalClientContext(context.Background(), "settings"), "monochrome", "sepia"); err == nil {
		t.Fatal("an unsupported mode was accepted")
	}
	if len(published) != 0 {
		t.Fatalf("published before a successful save: %#v", published)
	}
	values, err := service.SetAppearance(LocalClientContext(context.Background(), "settings"), "monochrome", "dark")
	if err != nil {
		t.Fatal(err)
	}
	want := settings.Appearance{Theme: settings.ThemeMonochrome, Mode: settings.AppearanceDark}
	if values.Appearance != want || len(published) != 1 || published[0] != want {
		t.Fatalf("values = %#v, published = %#v", values.Appearance, published)
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
