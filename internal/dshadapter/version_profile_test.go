package dshadapter

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersionListRoundTripPreservesUserSettingsAndLock(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profiles", "web")
	write := func(name, data string) {
		t.Helper()
		path := filepath.Join(profile, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("package.json", `{"dependencies":{"plugin":"^1.0.0"},"devDependencies":{"tool":"2.0.0"},"dsh":{"profile":{"bundles":["base","plugin"],"patchReload":"live"}},"userSetting":"before"}`)
	write("node_modules/plugin/package.json", `{"version":"1.2.0"}`)
	write("node_modules/tool/package.json", `{"version":"2.0.0"}`)
	lock := "lockfileVersion: '9.0'\nimporters:\n  .:\n    dependencies:\n      plugin:\n        specifier: ^1.0.0\n        version: 1.2.0\n    devDependencies:\n      tool:\n        specifier: 2.0.0\n        version: 2.0.0\n"
	write("pnpm-lock.yaml", lock)
	write("pnpm-workspace.yaml", "packages: ['.']\nnodeLinker: hoisted\nautoInstallPeers: false\n")
	a := NewPluginCommands()
	input, err := a.CaptureVersions(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(input)
	if strings.Contains(string(encoded), "manifest") || strings.Contains(string(encoded), "userSetting") || input.Lock != lock || len(input.Plugins) != 2 {
		t.Fatalf("unexpected record: %s", encoded)
	}
	write("package.json", `{"dependencies":{"extra":"3.0.0"},"dsh":{"profile":{"bundles":["extra"],"patchReload":"restart"}},"userSetting":"after"}`)
	write("pnpm-lock.yaml", "changed")
	if err := a.ApplyVersions(context.Background(), profile, input); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(profile, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"after"`) || !strings.Contains(string(raw), `"restart"`) || strings.Contains(string(raw), `"extra"`) {
		t.Fatalf("restore did not preserve current settings: %s", raw)
	}
	actual, err := a.CaptureVersions(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(actual)
	if string(result) != string(encoded) {
		t.Fatalf("record drift after apply: %s", result)
	}
}

func TestVersionRecordKeepsLockForEmptyPluginSet(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profiles", "web")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(`{"dependencies":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	lock := "lockfileVersion: '9.0'\nimporters:\n  .: {}\n"
	if err := os.WriteFile(filepath.Join(profile, "pnpm-lock.yaml"), []byte(lock), 0600); err != nil {
		t.Fatal(err)
	}
	input, err := NewPluginCommands().CaptureVersions(context.Background(), profile)
	if err != nil || input.Lock != lock {
		t.Fatalf("empty profile lost lock: %q %v", input.Lock, err)
	}
}

func TestVersionCaptureRejectsExternalDependencyLink(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profiles", "web")
	if err := os.MkdirAll(filepath.Join(profile, "node_modules"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(`{"dependencies":{"plugin":"1.0.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "package.json"), []byte(`{"version":"1.0.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(profile, "node_modules", "plugin")); err != nil {
		t.Skip(err)
	}
	if _, err := NewPluginCommands().CaptureVersions(context.Background(), profile); err == nil {
		t.Fatal("read outside the profile through a package link")
	}
}
