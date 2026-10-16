//go:build windows

package selfupdate

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/release"
)

// The unit tests use synthetic executables because they only need the resource
// layout. The checks that matter for a real release, however, depend on what
// the packaging toolchain actually emits, so this file builds a real installer
// with the project's own NSIS script shape and asserts the identity checks
// accept it. It is skipped when makensis is not installed.

// makensisCandidates lists the locations the packaging documentation uses.
var makensisCandidates = []string{
	`C:\Program Files (x86)\NSIS\makensis.exe`,
	`C:\Program Files\NSIS\makensis.exe`,
}

// lookupMakensis finds the packaging toolchain, falling back to PATH.
func lookupMakensis(t *testing.T) string {
	t.Helper()
	for _, candidate := range makensisCandidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	if found, err := exec.LookPath("makensis"); err == nil {
		return found
	}
	t.Skip("makensis is not installed; the real-installer checks need the packaging toolchain")
	return ""
}

// fixtureScript mirrors the version metadata build/windows/nsis/project.nsi
// writes: the four-component numeric version in the fixed file info and the
// full release version in the FileVersion and ProductVersion strings.
const fixtureScript = `Unicode true
Name "dsh-work-fixture"
OutFile "%OUTPUT%"
InstallDir "$LOCALAPPDATA\Programs\dsh-work-fixture"
RequestExecutionLevel user
VIProductVersion "%FILEVERSION%"
VIFileVersion    "%FILEVERSION%"
VIAddVersionKey "CompanyName"     "dsh-work"
VIAddVersionKey "FileDescription" "dsh-work Installer"
VIAddVersionKey "ProductVersion"  "%RELEASEVERSION%"
VIAddVersionKey "FileVersion"     "%RELEASEVERSION%"
VIAddVersionKey "ProductName"     "dsh-work"
Section
SectionEnd
`

// buildFixtureInstaller compiles a real per-user installer with the supplied
// identity, without running it.
func buildFixtureInstaller(t *testing.T, fileVersion, releaseVersion string) string {
	t.Helper()
	makensis := lookupMakensis(t)
	dir := t.TempDir()
	output := filepath.Join(dir, "dsh-work-fixture-installer.exe")
	script := strings.NewReplacer(
		"%OUTPUT%", output,
		"%FILEVERSION%", fileVersion,
		"%RELEASEVERSION%", releaseVersion,
	).Replace(fixtureScript)
	scriptPath := filepath.Join(dir, "fixture.nsi")
	if err := os.WriteFile(scriptPath, []byte(script), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", scriptPath, err)
	}
	command := exec.Command(makensis, "/V2", scriptPath)
	command.Dir = dir
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("makensis failed: %v\n%s", err, combined)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("makensis did not produce an installer: %v", err)
	}
	return output
}

// TestRealInstallerCarriesReleaseIdentity is the local proof that the identity
// checks accept what the packaging chain emits, rather than only what the
// synthetic fixtures describe.
func TestRealInstallerCarriesReleaseIdentity(t *testing.T) {
	path := buildFixtureInstaller(t, "0.2.0.0", "0.2.0-beta.1")

	machine, err := readMachine(path)
	if err != nil {
		t.Fatalf("readMachine: %v", err)
	}
	identity, err := readPayloadIdentity(path)
	if err != nil {
		t.Fatalf("readPayloadIdentity: %v", err)
	}
	t.Logf("real installer: machine=0x%04X fixed=%q FileVersion=%q ProductVersion=%q",
		machine, identity.fixed, identity.fileVersion, identity.productVersion)

	version, err := release.Parse("0.2.0-beta.1")
	if err != nil {
		t.Fatalf("release.Parse: %v", err)
	}
	if err := verifyInstallerIdentity(path, version, ProductionOptions()); err != nil {
		t.Fatalf("verifyInstallerIdentity(real installer) = %v", err)
	}
	if identity.fixed != "0.2.0.0" {
		t.Errorf("fixed file version = %q, want %q", identity.fixed, "0.2.0.0")
	}
	if identity.fileVersion != "0.2.0-beta.1" {
		t.Errorf("FileVersion string = %q, want %q", identity.fileVersion, "0.2.0-beta.1")
	}
	if identity.productVersion != "0.2.0-beta.1" {
		t.Errorf("ProductVersion string = %q, want %q", identity.productVersion, "0.2.0-beta.1")
	}
}

// TestRealInstallerIsRefusedForAnotherRelease proves the same artifact does not
// authenticate a different release: the feed cannot re-label one signed
// installer as another version.
func TestRealInstallerIsRefusedForAnotherRelease(t *testing.T) {
	path := buildFixtureInstaller(t, "0.2.0.0", "0.2.0-beta.1")

	for _, wrong := range []string{"0.2.0", "0.2.0-nightly.20261013.123456789", "0.3.0-beta.1", "0.2.1"} {
		version, err := release.Parse(wrong)
		if err != nil {
			t.Fatalf("release.Parse(%q): %v", wrong, err)
		}
		err = verifyInstallerIdentity(path, version, ProductionOptions())
		if !errors.Is(err, ErrPayloadIdentityMismatch) {
			t.Errorf("verifyInstallerIdentity(%s) = %v, want %v", wrong, err, ErrPayloadIdentityMismatch)
		}
	}
}
