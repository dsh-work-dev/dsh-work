package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestManagerProcessLockRejectsASecondOwnerUntilTheFirstReleases(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "dsh-work", "settings.json")
	first, err := AcquireManagerProcessLock(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	second, err := AcquireManagerProcessLock(settingsPath)
	if err == nil {
		_ = second.Close()
		t.Fatal("second manager process lock unexpectedly succeeded")
	}
	var failure lifecycle.Failure
	if !errors.As(err, &failure) || failure.Code != lifecycle.ErrorManagerOperationBusy {
		t.Fatalf("second lock error = %v, want manager-operation-busy", err)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	third, err := AcquireManagerProcessLock(settingsPath)
	if err != nil {
		t.Fatalf("lock after release error = %v", err)
	}
	if err := third.Close(); err != nil {
		t.Fatal(err)
	}
}
