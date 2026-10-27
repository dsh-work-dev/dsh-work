//go:build windows

package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/maintenance"
	"github.com/local/dsh-work/internal/storagepaths"
)

func TestDataPurgeDuringUninstallUsesExistingInstallerBoundary(t *testing.T) {
	if maintenance.InstallerInProgress() {
		t.Skip("the current user's installer boundary is already active")
	}
	defaults, directories, markers := dataPurgeWindowsFixture(t)
	release, err := maintenance.AcquireInstallerBoundary()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	if err := runDataPurgeWith([]string{"--yes", "--during-uninstall"}, &bytes.Buffer{}, defaults); err != nil {
		t.Fatalf("uninstaller purge: %v", err)
	}
	for _, path := range append(directories, defaults.ConfigFile) {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("purge left managed path %q (stat error %v)", path, err)
		}
	}
	assertDataPurgeWindowsMarkers(t, markers, false)
}

func TestDataPurgeRejectsOrdinaryPurgeDuringInstallerBoundary(t *testing.T) {
	if maintenance.InstallerInProgress() {
		t.Skip("the current user's installer boundary is already active")
	}
	defaults, _, markers := dataPurgeWindowsFixture(t)
	release, err := maintenance.AcquireInstallerBoundary()
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	err = runDataPurgeWith([]string{"--yes"}, &bytes.Buffer{}, defaults)
	var failure lifecycle.Failure
	if !errors.As(err, &failure) {
		t.Fatalf("purge while installer is active error = %v, want lifecycle.Failure", err)
	}
	if failure.Code != lifecycle.ErrorManagerOperationBusy {
		t.Fatalf("purge while installer is active code = %q, want %q", failure.Code, lifecycle.ErrorManagerOperationBusy)
	}
	if failure.Summary != "DSH Work is being updated." || !failure.Retryable || failure.CorrelationID == "" || failure.Detail != "Wait for the installer to finish, then retry the command." {
		t.Fatalf("purge while installer is active failure = %+v", failure)
	}
	assertDataPurgeWindowsMarkers(t, markers, true)
}

func TestDataPurgeDuringUninstallRequiresInstallerBoundary(t *testing.T) {
	if maintenance.InstallerInProgress() {
		t.Skip("the current user's installer boundary is already active")
	}
	defaults, _, markers := dataPurgeWindowsFixture(t)
	err := runDataPurgeWith([]string{"--yes", "--during-uninstall"}, &bytes.Buffer{}, defaults)
	if err == nil || !strings.Contains(err.Error(), "only valid while the DSH Work uninstaller is running") {
		t.Fatalf("purge outside uninstall boundary error = %v", err)
	}
	assertDataPurgeWindowsMarkers(t, markers, true)
}

func dataPurgeWindowsFixture(t *testing.T) (storagepaths.Directories, []string, []string) {
	t.Helper()
	defaults := purgeTestDirectories(t)
	if err := os.MkdirAll(filepath.Dir(defaults.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(defaults.ConfigFile, []byte("[storage]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	directories := []string{defaults.Root, defaults.UserDataRoot, defaults.CacheRoot, defaults.LogRoot}
	markers := make([]string, 0, len(directories))
	for _, directory := range directories {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		marker := filepath.Join(directory, "keep.txt")
		if err := os.WriteFile(marker, []byte("test"), 0o600); err != nil {
			t.Fatal(err)
		}
		markers = append(markers, marker)
	}
	return defaults, directories, markers
}

func assertDataPurgeWindowsMarkers(t *testing.T, paths []string, wantPresent bool) {
	t.Helper()
	for _, path := range paths {
		_, err := os.Stat(path)
		if wantPresent && err != nil {
			t.Errorf("marker %q is missing: %v", path, err)
		} else if !wantPresent && !os.IsNotExist(err) {
			t.Errorf("marker %q remains after purge (stat error %v)", path, err)
		}
	}
}
