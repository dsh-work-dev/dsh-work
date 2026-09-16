//go:build windows

package maintenance

import (
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
