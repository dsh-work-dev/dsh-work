package app

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/lifecycle"
)

func TestSafeModeRejectedSwitchDiscardsReservation(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	before, _ := f.manager.Snapshot(context.Background())
	f.host.mu.Lock()
	f.host.shutdownRequested = true
	f.host.mu.Unlock()
	_, err := f.host.EnterSafeMode(context.Background())
	if err == nil {
		t.Fatal("shutdown accepted a new switch")
	}
	after, _ := f.manager.Snapshot(context.Background())
	if after.SafeMode != nil || after.Configured == nil || *after.Configured != *before.Configured || len(after.DataDirectories) != len(before.DataDirectories) {
		t.Fatalf("failed entry retained reservation: %#v", after)
	}
}

func TestSafeModeSwitchAndFailedReturnRetainRecovery(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	snapshot, err := f.host.EnterSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || snapshot.Current.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || snapshot.SafeMode == nil {
		t.Fatalf("safe mode = %#v", snapshot)
	}
	f.supervisor.startErrors["alpha"] = errors.New("original plugins still broken")
	snapshot, err = f.host.ExitSafeMode(context.Background())
	if err == nil {
		t.Fatal("failed original environment accepted")
	}
	if snapshot.Current != nil || snapshot.SafeMode == nil || snapshot.Configured == nil || snapshot.Configured.Profile.DataDirectoryID != dshmanager.SafeModeDataDirectoryID || f.host.Status().State != lifecycle.StateFailed {
		t.Fatalf("failed return lost its safe-mode reservation: %#v", snapshot)
	}
	delete(f.supervisor.startErrors, "alpha")
	snapshot, err = f.host.ExitSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current == nil || snapshot.Current.Profile.Name != "alpha" || snapshot.SafeMode != nil {
		t.Fatalf("exit = %#v", snapshot)
	}
}

func TestSafeModeExitDiscardsRescueEnvironment(t *testing.T) {
	f := newRunContextSwitchFixture(t)
	defer f.close()
	f.startReady(t)
	snapshot, err := f.host.EnterSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	session := ""
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == dshmanager.SafeModeDataDirectoryID {
			session = directory.Path
		}
	}
	if session == "" {
		t.Fatalf("safe mode data directory missing: %#v", snapshot.DataDirectories)
	}
	snapshot, err = f.host.ExitSafeMode(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range snapshot.DataDirectories {
		if directory.ID == dshmanager.SafeModeDataDirectoryID {
			t.Fatalf("safe mode data directory retained after exit: %#v", snapshot.DataDirectories)
		}
	}
	for _, profile := range snapshot.Profiles {
		if profile.Ref.DataDirectoryID == dshmanager.SafeModeDataDirectoryID {
			t.Fatalf("safe mode profile retained after exit: %#v", profile)
		}
	}
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Fatalf("safe mode session directory retained: %v", err)
	}
}
