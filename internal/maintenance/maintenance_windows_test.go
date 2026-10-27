//go:build windows

package maintenance

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

func TestInstallerMutexNameUsesCurrentTokenSID(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	want := installerMutexPrefix + user.User.Sid.String()
	name, err := installerMutexName()
	if err != nil {
		t.Fatal(err)
	}
	if name != want {
		t.Fatalf("mutex name = %q, want %q", name, want)
	}

	original := os.Getenv("USERNAME")
	t.Cleanup(func() { _ = os.Setenv("USERNAME", original) })
	_ = os.Setenv("USERNAME", "another-user")
	nameAfterEnvironmentChange, err := installerMutexName()
	if err != nil {
		t.Fatal(err)
	}
	if nameAfterEnvironmentChange != name {
		t.Fatalf("mutex name changed with USERNAME: %q -> %q", name, nameAfterEnvironmentChange)
	}
}

func TestAcquireInstallerBoundaryIsVisibleUntilReleased(t *testing.T) {
	if InstallerInProgress() {
		t.Skip("the current user's installer boundary is already active")
	}
	release, err := AcquireInstallerBoundary()
	if err != nil {
		t.Fatal(err)
	}
	if !InstallerInProgress() {
		release()
		t.Fatal("installer boundary is not visible while acquired")
	}
	release()
	if InstallerInProgress() {
		t.Fatal("installer boundary remained after release")
	}
}

func TestAcquireInstallerBoundaryRejectsExistingMutexWithoutLeakingHandle(t *testing.T) {
	if InstallerInProgress() {
		t.Skip("the current user's installer boundary is already active")
	}
	release, err := AcquireInstallerBoundary()
	if err != nil {
		t.Fatal(err)
	}
	released := false
	releaseOnce := func() {
		if !released {
			release()
			released = true
		}
	}
	t.Cleanup(releaseOnce)

	secondRelease, err := AcquireInstallerBoundary()
	releaseOnce()
	boundaryRemains := InstallerInProgress()
	if secondRelease != nil {
		secondRelease()
	}
	if !errors.Is(err, ErrInstallerBusy) {
		t.Fatalf("second AcquireInstallerBoundary() error = %v, want ErrInstallerBusy", err)
	}
	if secondRelease != nil {
		t.Fatal("second AcquireInstallerBoundary() returned a release function for an existing mutex")
	}
	if boundaryRemains {
		t.Fatal("installer boundary remained after releasing the first handle")
	}
}
