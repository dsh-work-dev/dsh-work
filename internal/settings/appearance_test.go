package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAppearancePersistsAndRejectsUnsupportedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := manager.Snapshot(context.Background()); got.Appearance != DefaultAppearance() {
		t.Fatalf("default appearance = %#v", got.Appearance)
	}
	if _, err := manager.SetAppearance(context.Background(), Appearance{Theme: ThemeMonochrome, Mode: AppearanceDark}); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Appearance{
		{Theme: "unknown", Mode: AppearanceDark},
		{Theme: ThemeMonochrome, Mode: "sepia"},
		{Theme: ThemeInk, Mode: AppearanceDark},
		{Theme: ThemeTerminal, Mode: AppearanceSystem},
	} {
		if _, err := manager.SetAppearance(context.Background(), invalid); err == nil {
			t.Fatalf("SetAppearance(%#v) succeeded", invalid)
		}
	}
	reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := reloaded.Snapshot(context.Background())
	if values.Appearance != (Appearance{Theme: ThemeMonochrome, Mode: AppearanceDark}) {
		t.Fatalf("reloaded appearance = %#v", values.Appearance)
	}
}

func TestAppearanceImportRunsOnceForSettingsWithoutAppearance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"locale":"en"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ImportAppearanceMode(context.Background(), AppearanceDark); err != nil {
		t.Fatal(err)
	}
	if err := manager.ImportAppearanceMode(context.Background(), AppearanceLight); err != nil {
		t.Fatal(err)
	}
	values, _ := manager.Snapshot(context.Background())
	if values.Appearance.Mode != AppearanceDark {
		t.Fatalf("imported mode = %q, want dark from the first import", values.Appearance.Mode)
	}
	reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.ImportAppearanceMode(context.Background(), AppearanceLight); err != nil {
		t.Fatal(err)
	}
	values, _ = reloaded.Snapshot(context.Background())
	if values.Appearance.Mode != AppearanceDark {
		t.Fatalf("a later start imported again: mode = %q", values.Appearance.Mode)
	}
}

func TestAppearanceImportSkipsNewInstallsAndUserChoices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ImportAppearanceMode(context.Background(), AppearanceDark); err != nil {
		t.Fatal(err)
	}
	if values, _ := manager.Snapshot(context.Background()); values.Appearance.Mode != AppearanceSystem {
		t.Fatalf("new install imported %q", values.Appearance.Mode)
	}
}

func TestAppearanceUnknownStoredThemeKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"appearance":{"theme":"future","mode":"light"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := manager.Snapshot(context.Background())
	if values.Appearance != (Appearance{Theme: DefaultTheme, Mode: AppearanceLight}) {
		t.Fatalf("appearance = %#v", values.Appearance)
	}
}

func TestAppearanceStoredModeFollowsSingleModeTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":2,"appearance":{"theme":"terminal","mode":"light"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	values, _ := manager.Snapshot(context.Background())
	if values.Appearance != (Appearance{Theme: ThemeTerminal, Mode: AppearanceDark}) {
		t.Fatalf("appearance = %#v", values.Appearance)
	}
}
