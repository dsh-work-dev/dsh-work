package hostplugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallPublishesThreeVersionedDSHPackages(t *testing.T) {
	root := t.TempDir()
	packages, err := Install(root, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shell", "account", "pet"} {
		pkg, ok := packages[name]
		if !ok {
			t.Fatalf("missing %s package", name)
		}
		if filepath.Base(filepath.Dir(filepath.Dir(pkg.Directory))) != "1.2.3" {
			t.Fatalf("%s directory is not versioned: %s", name, pkg.Directory)
		}
		var manifest map[string]any
		data, err := os.ReadFile(filepath.Join(pkg.Directory, "package.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest["name"] != pkg.Name || manifest["version"] != "1.2.3" {
			t.Fatalf("%s manifest = %#v", name, manifest)
		}
		for _, file := range []string{"host.js", "client.js", "README.md"} {
			if _, err := os.Stat(filepath.Join(pkg.Directory, file)); err != nil {
				t.Fatalf("%s package missing %s: %v", name, file, err)
			}
		}
	}
	reused, err := Install(root, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	for name, pkg := range packages {
		if reused[name].Directory != pkg.Directory {
			t.Fatalf("%s path changed for the same version", name)
		}
	}
	next, err := Install(root, "1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	for name, pkg := range packages {
		if next[name].Directory == pkg.Directory {
			t.Fatalf("%s version reused an old package path", name)
		}
	}
}

func TestInstallRepairsAPartiallyWrittenFile(t *testing.T) {
	root := t.TempDir()
	packages, err := Install(root, "1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	client := filepath.Join(packages["shell"].Directory, "client.js")
	want, err := os.ReadFile(client)
	if err != nil {
		t.Fatal(err)
	}
	// An interrupted launch can leave a truncated file in the version directory.
	if err := os.WriteFile(client, want[:len(want)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, "1.2.3"); err != nil {
		t.Fatalf("install did not recover from a partial file: %v", err)
	}
	got, err := os.ReadFile(client)
	if err != nil || string(got) != string(want) {
		t.Fatalf("repaired client.js differs (err=%v)", err)
	}
}
