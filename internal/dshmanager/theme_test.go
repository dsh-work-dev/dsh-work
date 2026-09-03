package dshmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFileThemeReaderReadsDshYamlPreference(t *testing.T) {
	home := t.TempDir()
	settings := []byte("ui-theme:\n  preference: dark\n  fontSize: 14\n")
	if err := os.WriteFile(filepath.Join(home, "settings.yaml"), settings, 0o600); err != nil {
		t.Fatal(err)
	}

	preference, err := (FileThemeReader{}).Read(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if preference != ThemePreferenceDark {
		t.Fatalf("preference = %q, want %q", preference, ThemePreferenceDark)
	}
}

func TestFileThemeReaderAcceptsDshJsonSettings(t *testing.T) {
	home := t.TempDir()
	data, err := json.Marshal(map[string]any{
		"ui-theme": map[string]any{"preference": "light", "fontSize": 14},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "settings.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	preference, err := (FileThemeReader{}).Read(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if preference != ThemePreferenceLight {
		t.Fatalf("preference = %q, want %q", preference, ThemePreferenceLight)
	}
}

func TestFileThemeReaderDefaultsToSystem(t *testing.T) {
	preference, err := (FileThemeReader{}).Read(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if preference != ThemePreferenceSystem {
		t.Fatalf("preference = %q, want %q", preference, ThemePreferenceSystem)
	}
}

func TestManagerThemeReReadsTheSelectedDataDirectory(t *testing.T) {
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	if err := os.MkdirAll(filepath.Join(homePath, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homePath, "settings.yaml"), []byte("ui-theme:\n  preference: dark\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "work", Name: "Work", Path: homePath, Ownership: DataDirectoryOwnershipWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}},
		DefaultTarget: LaunchTarget{
			RuntimeID: "dsh-test",
			Profile:   ProfileRef{DataDirectoryID: "work", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	preference, err := manager.Theme(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preference != ThemePreferenceDark {
		t.Fatalf("preference = %q, want %q", preference, ThemePreferenceDark)
	}
	if err := os.WriteFile(filepath.Join(homePath, "settings.yaml"), []byte("ui-theme:\n  preference: light\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	preference, err = manager.Theme(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if preference != ThemePreferenceLight {
		t.Fatalf("updated preference = %q, want %q", preference, ThemePreferenceLight)
	}
}
