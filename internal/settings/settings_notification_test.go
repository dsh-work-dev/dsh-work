package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/notifications"
)

func TestSettingsDefaultToDesktopNotificationPreferences(t *testing.T) {
	manager, err := New(Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if values.Notifications != notifications.DefaultPreferences() {
		t.Fatalf("default notifications = %#v, want %#v", values.Notifications, notifications.DefaultPreferences())
	}
}

func TestSettingsPersistNotificationPreference(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.SetNotificationPreference(context.Background(), notifications.PreferenceCompleted, false)
	if err != nil {
		t.Fatalf("SetNotificationPreference() error = %v", err)
	}
	if values.Notifications.Completed {
		t.Fatal("completed preference = true, want false")
	}

	reloaded, err := New(Config{Path: path})
	if err != nil {
		t.Fatalf("reload New() error = %v", err)
	}
	reloadedValues, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("reload Snapshot() error = %v", err)
	}
	if reloadedValues.Notifications.Completed {
		t.Fatal("reloaded completed preference = true, want false")
	}
}

func TestSettingsInvalidNotificationObjectFallsBackWithoutDiscardingOtherValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := map[string]any{
		"version":     stateVersion,
		"closeToTray": false,
		"locale":      LocaleJapanese,
		"notifications": map[string]any{
			"enabled": "not-a-bool",
		},
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
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
	if values.CloseToTray || values.Locale != LocaleJapanese {
		t.Fatalf("recoverable settings = %#v, want closeToTray=false and locale=%q", values, LocaleJapanese)
	}
	if values.Notifications != notifications.DefaultPreferences() {
		t.Fatalf("invalid notifications = %#v, want defaults %#v", values.Notifications, notifications.DefaultPreferences())
	}
}
