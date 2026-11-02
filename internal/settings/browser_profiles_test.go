package settings

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidBrowserBindingsPreserveSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if e := os.WriteFile(path, []byte(`{"version":2,"locale":"ja-JP","automation":{"browser":{"enabled":true}},"browserProfiles":{"schemaVersion":99,"selected":"wrong"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	m, e := New(Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	v, e := m.Snapshot(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if v.Locale != LocaleJapanese || !v.Automation.Browser.Enabled || v.BrowserProfiles != nil {
		t.Fatal("corrupt bindings discarded unrelated settings", v)
	}
	if _, e := m.SetBrowserProfiles(context.Background(), BrowserProfiles{SchemaVersion: 1}); e != nil {
		t.Fatal(e)
	}
	loaded, e := New(Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	v, e = loaded.Snapshot(context.Background())
	if e != nil || v.BrowserProfiles != nil || v.Locale != LocaleJapanese || !v.Automation.Browser.Enabled {
		t.Fatal("empty binding cleanup changed legacy settings", v, e)
	}
}
