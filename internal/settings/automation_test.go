package settings

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAutomationPersistsOnlyIntentAndReadsLegacyDefaults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.json")
	m, e := New(Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetAutomationEnabled(ctx, "browser", true); e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetAutomationEnabled(ctx, "computer", true); e != nil {
		t.Fatal(e)
	}
	if _, e = m.SetDefaultBrowserEnabled(ctx, false); e != nil {
		t.Fatal(e)
	}
	loaded, e := New(Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	v, _ := loaded.Snapshot(ctx)
	if !v.Automation.Browser.Enabled || !v.Automation.Computer.Enabled || v.Automation.Browser.Default.Enabled {
		t.Fatal(v.Automation)
	}
	data, _ := os.ReadFile(path)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	a := raw["automation"].(map[string]any)
	if len(a) != 2 {
		t.Fatal("runtime data persisted")
	}
	delete(raw, "automation")
	data, _ = json.Marshal(raw)
	os.WriteFile(path, data, 0600)
	legacy, e := New(Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	v, _ = legacy.Snapshot(ctx)
	if v.Automation != DefaultAutomation() {
		t.Fatal("legacy automation defaults", v.Automation)
	}
}

func TestInvalidAutomationValuesDoNotDiscardOtherPreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(path, []byte(`{"version":2,"locale":"ja-JP","automation":{"browser":{"enabled":"invalid"},"computer":{"enabled":true}}}`), 0600)
	m, err := New(Config{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := m.Snapshot(context.Background())
	if v.Locale != LocaleJapanese || v.Automation.Browser.Enabled || !v.Automation.Browser.Default.Enabled || !v.Automation.Computer.Enabled {
		t.Fatal("invalid capability field discarded valid preferences", v)
	}
}
