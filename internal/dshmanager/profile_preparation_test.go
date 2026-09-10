package dshmanager

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfilePreparationPreservesUnavailableUserHomeAndExistingFile(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "user-home")
	if err := prepareProfileWorkingDirectory(DataDirectoryInfo{Path: missing, Ownership: DataDirectoryOwnershipUser}); err == nil {
		t.Fatal("missing user directory was accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("user directory was created: %v", err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareProfileWorkingDirectory(DataDirectoryInfo{Path: file, Ownership: DataDirectoryOwnershipDSHWork}); err == nil {
		t.Fatal("file was accepted as working directory")
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "keep" {
		t.Fatalf("existing file changed: %q, %v", data, err)
	}
}
