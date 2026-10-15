//go:build windows

package selfupdate

import (
	"errors"
	"os"
	"testing"
)

// TestStagingRecheckDetectsReplacedBytes proves the second verification the
// install handshake performs: the staged copy is a durable file of this
// process's own, and a payload swapped after staging is refused. It needs a
// payload that satisfies the Windows installer identity checks, so it runs on
// the platform that owns those checks.
func TestStagingRecheckDetectsReplacedBytes(t *testing.T) {
	base := t.TempDir()
	staging, err := NewStaging(base)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	if info := fileMode(t, staging.Dir()); !info.IsDir() {
		t.Fatalf("staging directory is not a directory: %v", info)
	}
	payload := fakeInstallerPayload(t, "0.2.0.0", "0.2.0-beta.1")
	feed := digestFeed(t, payload, nil)
	installer, err := staging.Stage(writePayload(t, payload), feed)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if installer.Path == "" {
		t.Fatal("Stage returned an empty path")
	}
	if err := staging.Recheck(installer, feed); err != nil {
		t.Fatalf("Recheck = %v, want nil", err)
	}
	if err := os.WriteFile(installer.Path, []byte("swapped bytes"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := staging.Recheck(installer, feed); !errors.Is(err, ErrStagedPayloadChanged) {
		t.Fatalf("Recheck(replaced) = %v, want %v", err, ErrStagedPayloadChanged)
	}
}

// TestStagingClearsStagedInstaller proves the staged payload can be discarded
// and that nothing runnable is left behind.
func TestStagingClearsStagedInstaller(t *testing.T) {
	staging, err := NewStaging(t.TempDir())
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	payload := fakeInstallerPayload(t, "0.2.0.0", "0.2.0-beta.1")
	feed := digestFeed(t, payload, nil)
	installer, err := staging.Stage(writePayload(t, payload), feed)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if err := staging.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(installer.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged installer still present: %v", err)
	}
	if err := staging.Recheck(installer, feed); !errors.Is(err, ErrStagedPayloadChanged) {
		t.Fatalf("Recheck(cleared) = %v, want %v", err, ErrStagedPayloadChanged)
	}
}
