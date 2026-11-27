//go:build darwin && cgo && !server

package desktopclient

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMacDownloadSavePathFixturePinsDestination(t *testing.T) {
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(directory, "complete.txt")
	other := filepath.Join(directory, "unrelated.txt")
	for _, path := range []string{target, other} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	host := &macWindowHost{}
	if path, failure := host.macDownloadSavePath(other); path != other || failure != "" {
		t.Fatalf("normal path selection returned %q with error %q", path, failure)
	}
	host.fixtureDownloadTarget = target
	if path, failure := host.macDownloadSavePath(target); path != target || failure != "" {
		t.Fatalf("pinned path selection returned %q with error %q", path, failure)
	}
	if path, failure := host.macDownloadSavePath(other); path != "" || failure != "MAC_FIXTURE_DOWNLOAD_DESTINATION_REJECTED" {
		t.Fatalf("unrelated path selection returned %q with error %q", path, failure)
	}
}
