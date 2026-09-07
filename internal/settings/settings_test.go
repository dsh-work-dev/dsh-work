package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestSettingsDefaultToKeepingTheAppInTheTray(t *testing.T) {
	manager, err := New(Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if !values.CloseToTray || !values.AutomaticRuntimeRollback || values.Version != stateVersion || values.Locale != DefaultLocale {
		t.Fatalf("default values = %#v, want version %d, closeToTray=true and locale=%q", values, stateVersion, DefaultLocale)
	}
}

func TestSettingsVersionOneMigratesAutomaticRuntimeRollbackToEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"closeToTray":false,"locale":"ja-JP"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if values.Version != 2 || !values.AutomaticRuntimeRollback || values.CloseToTray || values.Locale != LocaleJapanese {
		t.Fatalf("migrated settings = %#v", values)
	}
}

func TestSettingsPersistAutomaticRuntimeRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.SetAutomaticRuntimeRollback(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if values.AutomaticRuntimeRollback {
		t.Fatal("automatic rollback remained enabled")
	}
	reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	values, err = reloaded.Snapshot(context.Background())
	if err != nil || values.AutomaticRuntimeRollback {
		t.Fatalf("reloaded settings = %#v error=%v", values, err)
	}
}

func TestSettingsPetDefaultsStartHiddenWithoutASelection(t *testing.T) {
	manager, err := New(Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if values.Pet.SchemaVersion != petPreferenceVersion || values.Pet.SelectedKey != nil || values.Pet.VisibilityIntent != PetVisibilityHidden {
		t.Fatalf("default Pet preference = %#v, want schema %d, no selection and hidden", values.Pet, petPreferenceVersion)
	}
}

func TestSettingsPetPreferenceNormalizesAndPreservesUnavailableSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{"version":1,"closeToTray":true,"locale":"zh-CN","pet":{"schemaVersion":99,"selectedKey":"codex:pets:missing","visibilityIntent":"visible","position":{"anchorX":-2,"anchorY":3,"width":0,"height":99999,"scale":0}}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if values.Pet.SelectedKey == nil || *values.Pet.SelectedKey != "codex:pets:missing" || values.Pet.VisibilityIntent != PetVisibilityVisible {
		t.Fatalf("normalized unavailable selection = %#v, want key and visible intent preserved", values.Pet)
	}
	defaults := DefaultPetPosition()
	if values.Pet.SchemaVersion != petPreferenceVersion || values.Pet.Position != defaults {
		t.Fatalf("normalized position/schema = %#v, want %#v and schema %d", values.Pet.Position, defaults, petPreferenceVersion)
	}
}

func TestSettingsPetPreferenceRejectsPathLikeSelectionAndForcesHidden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"pet":{"selectedKey":"C:\\outside","visibilityIntent":"visible"}}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if values.Pet.SelectedKey != nil || values.Pet.VisibilityIntent != PetVisibilityHidden {
		t.Fatalf("unsafe selection = %#v, want nil/hidden", values.Pet)
	}
}

func TestSettingsPetPreferencePersistsAsOneVersionedValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	selected := "codex:pets:chosen"
	preference := DefaultPetPreference()
	preference.SelectedKey = &selected
	preference.VisibilityIntent = PetVisibilityVisible
	preference.Position.MonitorID = "display-2"
	if _, err := manager.SetPetPreference(context.Background(), preference); err != nil {
		t.Fatalf("SetPetPreference() error = %v", err)
	}
	reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatalf("reload New() error = %v", err)
	}
	values, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reload Snapshot() error = %v", err)
	}
	if values.Pet.SelectedKey == nil || *values.Pet.SelectedKey != selected || values.Pet.VisibilityIntent != PetVisibilityVisible || values.Pet.Position.MonitorID != "display-2" {
		t.Fatalf("reloaded Pet preference = %#v", values.Pet)
	}
}

func TestSettingsPersistClosePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.SetCloseToTray(context.Background(), false)
	if err != nil {
		t.Fatalf("SetCloseToTray() error = %v", err)
	}
	if values.CloseToTray {
		t.Fatal("SetCloseToTray(false) kept closeToTray=true")
	}
	if _, err := manager.SetCloseToTray(context.Background(), true); err != nil {
		t.Fatalf("SetCloseToTray(true) error = %v", err)
	}

	reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatalf("reload New() error = %v", err)
	}
	reloadedValues, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reload Snapshot() error = %v", err)
	}
	if !reloadedValues.CloseToTray {
		t.Fatal("reloaded closeToTray=false, want true")
	}
}

func TestSettingsPersistLocale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.SetLocale(context.Background(), LocaleJapanese)
	if err != nil {
		t.Fatalf("SetLocale() error = %v", err)
	}
	if values.Locale != LocaleJapanese {
		t.Fatalf("locale = %q, want %q", values.Locale, LocaleJapanese)
	}

	reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatalf("reload New() error = %v", err)
	}
	reloadedValues, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reload Snapshot() error = %v", err)
	}
	if reloadedValues.Locale != LocaleJapanese {
		t.Fatalf("reloaded locale = %q, want %q", reloadedValues.Locale, LocaleJapanese)
	}
}

func TestSettingsFallbackToDefaultLocale(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"closeToTray":true,"locale":"fr-FR"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if values.Locale != DefaultLocale {
		t.Fatalf("locale = %q, want default %q", values.Locale, DefaultLocale)
	}
}

type renameReplacer struct{}

func (renameReplacer) Replace(source, destination string) error {
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(source, destination)
}

func TestSettingsRejectUnsupportedState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"closeToTray":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	_, err := New(Config{Path: path})
	if err == nil {
		t.Fatal("New() error = nil, want unsupported-state error")
	}
	var failureValue lifecycle.Failure
	if !asFailure(err, &failureValue) || failureValue.Code != lifecycle.ErrorSettingsStateInvalid {
		t.Fatalf("New() error = %v, want %s", err, lifecycle.ErrorSettingsStateInvalid)
	}
}

func asFailure(err error, target *lifecycle.Failure) bool {
	value, ok := err.(lifecycle.Failure)
	if !ok {
		return false
	}
	*target = value
	return true
}
