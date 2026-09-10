package nativeui

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryPickerStartsAtExistingParent(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "settings.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, file, filepath.Join(root, "new location", "user-data")} {
		if got := existingDirectory(path); got != root {
			t.Fatalf("initial directory for %q = %q, want %q", path, got, root)
		}
	}
	for _, path := range []string{"", "relative/path"} {
		if got := existingDirectory(path); got != "" {
			t.Fatalf("relative path %q resolved to %q", path, got)
		}
	}
}
