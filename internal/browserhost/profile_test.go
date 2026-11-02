package browserhost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitProfileDirectory(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "Profile 2")
	os.Mkdir(directory, 0700)
	os.WriteFile(filepath.Join(directory, "Preferences"), []byte("{}"), 0600)
	os.WriteFile(filepath.Join(root, "Local State"), []byte(`{"profile":{"info_cache":{"Profile 2":{}}}}`), 0600)
	h, e := ResolveProfileDirectory(filepath.Join(root, "chrome.exe"), directory)
	if e != nil || h.ProfileDirectory != "Profile 2" || h.UserDataDirectory != root {
		t.Fatal(h, e)
	}
	os.WriteFile(filepath.Join(root, "Local State"), []byte(`{"profile":{"info_cache":{"Default":{}}}}`), 0600)
	if _, e = ResolveProfileDirectory(filepath.Join(root, "chrome.exe"), directory); e == nil {
		t.Fatal("accepted directory absent from explicit metadata")
	}
}
