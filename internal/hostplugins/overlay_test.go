package hostplugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverlayMountsEveryHostPluginWithItsLaunchConfig(t *testing.T) {
	root := t.TempDir()
	overlay := NewOverlay(root, "1.2.3")
	patch, err := overlay.Prepare("g2", map[string]map[string]string{
		"shell": {"generation": "g2"},
		"pet":   {"generation": "g2", "token": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(patch) != root {
		t.Fatalf("patch escaped the host plugin root: %s", patch)
	}
	content, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(content, &rows); err != nil || len(rows) != 1 {
		t.Fatalf("invalid launch patch: %s (%v)", content, err)
	}
	insert, ok := rows[0]["insert"].([]any)
	if !ok || len(insert) != 3 {
		t.Fatalf("launch patch did not mount three host plugins: %#v", rows)
	}
	want := []struct {
		id     string
		config map[string]any
	}{
		{"dsh-work-shell", map[string]any{"generation": "g2"}},
		{"dsh-work-account", map[string]any{}},
		{"dsh-work-pet", map[string]any{"generation": "g2", "token": "secret"}},
	}
	for i, expected := range want {
		entry, _ := insert[i].(map[string]any)
		if entry["id"] != expected.id {
			t.Fatalf("plugin %d = %#v, want %s", i, entry, expected.id)
		}
		name, _ := entry["name"].(string)
		if !strings.HasPrefix(name, "file:///") || !strings.Contains(name, "/versions/1.2.3/") {
			t.Fatalf("%s is not a versioned module URL: %s", expected.id, name)
		}
		config, _ := entry["config"].(map[string]any)
		if len(config) != len(expected.config) {
			t.Fatalf("%s config = %#v, want %#v", expected.id, config, expected.config)
		}
		for key, value := range expected.config {
			if config[key] != value {
				t.Fatalf("%s config = %#v, want %#v", expected.id, config, expected.config)
			}
		}
	}
}

func TestOverlayRemovesEarlierLaunchesAndPluginVersions(t *testing.T) {
	root := t.TempDir()
	first, err := NewOverlay(root, "1.0.0").Prepare("g1", nil)
	if err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "versions", "1.1.0", "old-digest")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	second, err := NewOverlay(root, "1.2.0").Prepare("g2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("earlier launch patch kept: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("current launch patch missing: %v", err)
	}
	versions, err := os.ReadDir(filepath.Join(root, "versions"))
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Name() != "1.2.0" {
		t.Fatalf("plugin versions after prune = %v, want only 1.2.0", versions)
	}
}

func TestOverlayRejectsUnsafeGenerations(t *testing.T) {
	for _, generation := range []string{"", "../escape", "a/b", strings.Repeat("x", 65)} {
		if _, err := NewOverlay(t.TempDir(), "1.0.0").Prepare(generation, nil); err == nil {
			t.Fatalf("accepted generation %q", generation)
		}
	}
}
