package browserhost

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBundleUpdateRetainsCompletePreviousDirectory(t *testing.T) {
	c := Config{Directory: t.TempDir(), Identity: "local-owner"}
	ctx := context.Background()
	if e := InstallDevelopmentBundle(ctx, c); e != nil {
		t.Fatal(e)
	}
	dir := DevelopmentDirectory(c)
	for _, name := range []string{"icon-16.png", "icon-32.png", "icon-48.png", "icon-128.png"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("published icon %s: %v", name, err)
		}
	}
	old := []byte("previous complete background")
	if e := os.WriteFile(filepath.Join(dir, "background.js"), old, 0600); e != nil {
		t.Fatal(e)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if e := InstallDevelopmentBundle(canceled, c); e == nil {
		t.Fatal("cancel ignored")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "background.js")); string(b) != string(old) {
		t.Fatal("canceled update changed old bundle")
	}
	if e := InstallDevelopmentBundle(ctx, c); e != nil {
		t.Fatal(e)
	}
	backups, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "development-previous-*"))
	if len(backups) != 1 {
		t.Fatal(backups)
	}
	if b, _ := os.ReadFile(filepath.Join(backups[0], "background.js")); string(b) != string(old) {
		t.Fatal("previous bundle not retained")
	}
	if _, e := os.Stat(filepath.Join(dir, "bundle-source.txt")); e != nil {
		t.Fatal(e)
	}
	if e := InstallDevelopmentBundle(ctx, c); e != nil {
		t.Fatal(e)
	}
	after, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "development-previous-*"))
	if len(after) != 1 {
		t.Fatal("identical bundle republished")
	}
}
