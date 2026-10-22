package dshmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestResolveImportSourceListsProfilesFromChosenHome(t *testing.T) {
	root := t.TempDir()
	managed := DataDirectoryInfo{ID: "dsh-work", Path: filepath.Join(root, "managed")}
	chosen := filepath.Join(root, "chosen")
	profiles := filepath.Join(chosen, "profiles")
	for _, name := range []string{"zeta", "alpha", ".private", "node_modules"} {
		if err := os.MkdirAll(filepath.Join(profiles, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(profiles, "not-a-profile"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(profiles, ".linked-target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(profiles, ".linked-target"), filepath.Join(profiles, "linked-profile")); err != nil {
		t.Logf("symlink creation unavailable; continuing without the symlink case: %v", err)
	}

	source, err := resolveImportSource("  "+chosen+"  ", managed)
	if err != nil {
		t.Fatal(err)
	}
	if source.Home != chosen {
		t.Fatalf("Home = %q, want %q", source.Home, chosen)
	}
	if !reflect.DeepEqual(source.Profiles, []string{"alpha", "zeta"}) {
		t.Fatalf("Profiles = %#v, want [alpha zeta]", source.Profiles)
	}
}

func TestResolveImportSourceFindsLegacyEnvironmentHome(t *testing.T) {
	root := t.TempDir()
	managed := DataDirectoryInfo{ID: "dsh-work", Path: filepath.Join(root, "managed")}
	chosen := filepath.Join(root, "legacy")
	home := filepath.Join(chosen, "environment")
	if err := os.MkdirAll(filepath.Join(home, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}

	source, err := resolveImportSource(chosen, managed)
	if err != nil {
		t.Fatal(err)
	}
	if source.Home != home || !reflect.DeepEqual(source.Profiles, []string{"web"}) {
		t.Fatalf("resolved source = %#v, want Home %q and [web]", source, home)
	}
}

func TestResolveImportSourceRejectsNonDSHFolders(t *testing.T) {
	root := t.TempDir()
	managed := DataDirectoryInfo{ID: "dsh-work", Path: filepath.Join(root, "managed")}
	cases := []struct {
		name string
		path string
	}{
		{name: "relative", path: "relative"},
		{name: "missing", path: filepath.Join(root, "missing")},
		{name: "no profiles", path: root},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveImportSource(tc.path, managed)
			assertFailureCode(t, err, lifecycle.ErrorProfileNotFound)
		})
	}
}

func TestResolveImportSourceRejectsPathsOverlappingManagedData(t *testing.T) {
	root := t.TempDir()
	managedPath := filepath.Join(root, "managed")
	cases := []struct {
		name string
		home string
	}{
		{name: "same", home: managedPath},
		{name: "inside", home: filepath.Join(managedPath, "profiles", "nested")},
		{name: "contains", home: root},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.MkdirAll(filepath.Join(tc.home, "profiles", "source"), 0o700); err != nil {
				t.Fatal(err)
			}
			managed := DataDirectoryInfo{ID: "dsh-work", Path: managedPath}
			_, err := resolveImportSource(tc.home, managed)
			assertFailureCode(t, err, lifecycle.ErrorProfileInvalid)
		})
	}
}

func TestResolveImportSourceAppliesLegacyUserDataRedirects(t *testing.T) {
	root := t.TempDir()
	managed := DataDirectoryInfo{ID: "dsh-work", Path: filepath.Join(root, "managed")}
	home := filepath.Join(root, "legacy", "environment")
	if err := os.MkdirAll(filepath.Join(home, "profiles", "web"), 0o700); err != nil {
		t.Fatal(err)
	}
	userData := filepath.Join(root, "legacy", "user-data")
	redirects := []map[string]any{
		{"id": "session-persistence-jsonl", "config": map[string]string{"root": filepath.Join(userData, "sessions")}},
		{"id": "storage-json", "config": map[string]string{"root": filepath.Join(userData, "storages")}},
		{"id": "attachment-local", "config": map[string]string{"dshHome": userData}},
		{"id": "credentials", "config": map[string]string{"path": filepath.Join(userData, ".credentials.yaml")}},
	}
	data, err := json.Marshal(redirects)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "user-data.patch.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	source, err := resolveImportSource(filepath.Dir(home), managed)
	if err != nil {
		t.Fatal(err)
	}
	if source.Sessions != filepath.Join(userData, "sessions") ||
		source.Storages != filepath.Join(userData, "storages") ||
		source.Attachments != filepath.Join(userData, "attachments", "v1") ||
		source.Credentials != filepath.Join(userData, ".credentials.yaml") {
		t.Fatalf("redirected source paths = %#v", source)
	}
}

func TestResolveImportSourceIgnoresMalformedAndRelativeRedirects(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "malformed", data: "[broken"},
		{name: "relative", data: `[{"id":"session-persistence-jsonl","config":{"root":"relative/sessions"}},{"id":"storage-json","config":{"root":"relative/storages"}},{"id":"attachment-local","config":{"dshHome":"relative/home"}},{"id":"credentials","config":{"path":"relative/.credentials.yaml"}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			managed := DataDirectoryInfo{ID: "dsh-work", Path: filepath.Join(root, "managed")}
			home := filepath.Join(root, "home")
			if err := os.MkdirAll(filepath.Join(home, "profiles"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "user-data.patch.json"), []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			source, err := resolveImportSource(home, managed)
			if err != nil {
				t.Fatal(err)
			}
			want := importSource{
				Home: home, Profiles: []string{},
				Sessions:    filepath.Join(home, "sessions"),
				Storages:    filepath.Join(home, "storages"),
				Attachments: filepath.Join(home, "attachments", "v1"),
				Credentials: filepath.Join(home, ".credentials.yaml"),
			}
			if !reflect.DeepEqual(source, want) {
				t.Fatalf("source = %#v, want %#v", source, want)
			}
		})
	}
}
