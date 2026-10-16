package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsDefaultUpdateChannelIsStable(t *testing.T) {
	manager, err := New(Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
	if values.UpdateChannel != UpdateChannelStable {
		t.Fatalf("update channel = %q, want %q", values.UpdateChannel, UpdateChannelStable)
	}
}

// A settings file written before the channel existed must decode as stable
// instead of failing the load or adopting an empty channel.
func TestSettingsWithoutUpdateChannelDecodeAsStable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	legacy := `{"version":2,"locale":"en","automaticRuntimeRollback":true,"workspaceWindow":{"x":0,"y":0,"width":1200,"height":800}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if values.UpdateChannel != UpdateChannelStable {
		t.Fatalf("update channel = %q, want %q", values.UpdateChannel, UpdateChannelStable)
	}
	if values.Locale != LocaleEnglish {
		t.Fatalf("locale = %q, want %q", values.Locale, LocaleEnglish)
	}
}

// An unsupported persisted value is replaced by the default rather than
// discarding the rest of the settings.
func TestSettingsUnsupportedUpdateChannelFallsBackWithoutDiscardingOtherValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	stored := `{"version":2,"locale":"ja-JP","automaticRuntimeRollback":false,"updateChannel":"canary"}`
	if err := os.WriteFile(path, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if values.UpdateChannel != UpdateChannelStable {
		t.Fatalf("update channel = %q, want %q", values.UpdateChannel, UpdateChannelStable)
	}
	if values.Locale != LocaleJapanese || values.AutomaticRuntimeRollback {
		t.Fatalf("settings = %#v, want the other values preserved", values)
	}
}

func TestSettingsPersistUpdateChannel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	manager, err := New(Config{Path: path, Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, channel := range []UpdateChannel{UpdateChannelNightly, UpdateChannelBeta, UpdateChannelStable} {
		values, err := manager.SetUpdateChannel(context.Background(), channel)
		if err != nil {
			t.Fatalf("SetUpdateChannel(%q) error = %v", channel, err)
		}
		if values.UpdateChannel != channel {
			t.Fatalf("returned channel = %q, want %q", values.UpdateChannel, channel)
		}
		reloaded, err := New(Config{Path: path, Replacer: renameReplacer{}})
		if err != nil {
			t.Fatal(err)
		}
		persisted, err := reloaded.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if persisted.UpdateChannel != channel {
			t.Fatalf("persisted channel = %q, want %q", persisted.UpdateChannel, channel)
		}
	}
}

func TestSettingsRejectUnsupportedUpdateChannel(t *testing.T) {
	manager, err := New(Config{Path: filepath.Join(t.TempDir(), "settings.json"), Replacer: renameReplacer{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetUpdateChannel(context.Background(), UpdateChannel("canary")); err == nil {
		t.Fatal("SetUpdateChannel accepted an unsupported channel")
	}
	values, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if values.UpdateChannel != UpdateChannelStable {
		t.Fatalf("update channel = %q, want the previous value %q", values.UpdateChannel, UpdateChannelStable)
	}
}
