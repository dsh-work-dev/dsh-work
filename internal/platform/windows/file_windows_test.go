//go:build windows

package windows

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileReplacerReplacesExistingFile(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "settings.tmp")
	destination := filepath.Join(directory, "settings.json")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile(source) error = %v", err)
	}
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(destination) error = %v", err)
	}

	if err := NewFileReplacer().Replace(source, destination); err != nil {
		t.Fatalf("Replace() error = %v", err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("ReadFile(destination) error = %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("destination contents = %q, want %q", data, "new")
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source still exists or returned unexpected error: %v", err)
	}
}
