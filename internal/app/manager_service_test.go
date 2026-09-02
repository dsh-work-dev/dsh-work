package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/local/work/internal/dshmanager"
	"github.com/local/work/internal/lifecycle"
)

func TestManagerServiceRequiresTheSettingsWindow(t *testing.T) {
	root := t.TempDir()
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath:     filepath.Join(root, "manager.json"),
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewManagerService(manager).GetSnapshot(context.Background())
	assertServiceFailureCode(t, err, lifecycle.ErrorTrustedSurfaceRequired)
}

func TestHostServiceRefusesControlsFromAnUntrustedSurface(t *testing.T) {
	status := NewHostService(nil, func() bool { return true }, nil).GetStatus(context.Background())
	if status.Error == nil || status.Error.Code != lifecycle.ErrorTrustedSurfaceRequired {
		t.Fatalf("untrusted Host status = %+v", status)
	}
}

func assertServiceFailureCode(t *testing.T, err error, want lifecycle.ErrorCode) {
	t.Helper()
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("error %v is not a lifecycle failure", err)
	}
	if failure.Code != want {
		t.Fatalf("failure code = %s, want %s", failure.Code, want)
	}
}
