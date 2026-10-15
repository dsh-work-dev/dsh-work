//go:build windows

package selfupdate

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unsafe"
)

// embeddedSignedCandidates lists binaries whose Authenticode signature is
// embedded in the file, which is the form an installer can carry. Windows-owned
// system binaries such as notepad.exe are instead covered by a catalog
// signature, and WinVerifyTrust deliberately reports those as having no
// signature, so they cannot be used as positive evidence here.
func embeddedSignedCandidates() []string {
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "dotnet", "dotnet.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "nodejs", "node.exe"),
		filepath.Join(os.Getenv("ProgramFiles"), "Git", "cmd", "git.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Microsoft VS Code", "Code.exe"),
	}
	present := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			present = append(present, candidate)
		}
	}
	return present
}

// TestAuthenticodeTrustedAcceptsEmbeddedSignature is positive evidence for the
// raw WinVerifyTrust binding: a binary that carries its own Authenticode
// signature must be accepted. A binding whose struct layout were wrong would
// not reach this verdict, and the unsigned case in the companion test proves
// the call is not simply reporting success for everything.
func TestAuthenticodeTrustedAcceptsEmbeddedSignature(t *testing.T) {
	candidates := embeddedSignedCandidates()
	if len(candidates) == 0 {
		t.Skip("no embedded-signed binary available on this machine")
	}
	for _, path := range candidates {
		trusted, err := authenticodeTrusted(path)
		if err != nil {
			t.Fatalf("authenticodeTrusted(%s): %v", path, err)
		}
		if !trusted {
			t.Errorf("authenticodeTrusted(%s) = false, want true for an embedded-signed binary", path)
		}
		if err := verifyAuthenticode(path); err != nil {
			t.Errorf("verifyAuthenticode(%s) = %v, want nil", path, err)
		}
	}
}

// TestAuthenticodeTrustedRejectsCatalogOnlySignature records the measured
// behaviour that a catalog-signed system binary reports no signature at all.
// An installer that was merely covered by a catalog entry would therefore be
// refused, which is the intended outcome: the update path must demand an
// embedded signature it can verify on its own.
func TestAuthenticodeTrustedRejectsCatalogOnlySignature(t *testing.T) {
	notepad := filepath.Join(os.Getenv("SystemRoot"), "System32", "notepad.exe")
	if _, err := os.Stat(notepad); err != nil {
		t.Skip("no catalog-signed system binary available")
	}
	trusted, err := authenticodeTrusted(notepad)
	if err != nil {
		t.Fatalf("authenticodeTrusted(%s): %v", notepad, err)
	}
	if trusted {
		t.Errorf("authenticodeTrusted(%s) = true; the binding is accepting files with no embedded signature", notepad)
	}
	status, err := authenticodeStatus(notepad)
	if err != nil {
		t.Fatalf("authenticodeStatus(%s): %v", notepad, err)
	}
	if status == 0 {
		t.Error("authenticodeStatus reported success for a file with no embedded signature")
	}
	t.Logf("catalog-only binary refused with %s", trustResultName(status))
}

// TestAuthenticodeTrustedRejectsUnsignedBytes covers the case the update path
// exists to stop: bytes that were never signed.
func TestAuthenticodeTrustedRejectsUnsignedBytes(t *testing.T) {
	unsigned := filepath.Join(t.TempDir(), "unsigned.exe")
	if err := os.WriteFile(unsigned, []byte("MZ this is not a signed executable"), 0o600); err != nil {
		t.Fatal(err)
	}
	trusted, err := authenticodeTrusted(unsigned)
	if err != nil {
		t.Fatalf("authenticodeTrusted(unsigned): %v", err)
	}
	if trusted {
		t.Error("authenticodeTrusted(unsigned) = true, want false")
	}
	if err := verifyAuthenticode(unsigned); err == nil {
		t.Error("verifyAuthenticode accepted an unsigned file")
	}
	if trusted, err := authenticodeTrusted(filepath.Join(t.TempDir(), "missing.exe")); err == nil && trusted {
		t.Error("authenticodeTrusted(missing) = true, want false")
	}
}

// TestWinTrustStructLayout pins the WINTRUST_FILE_INFO and WINTRUST_DATA field
// offsets. WinVerifyTrust receives only an untyped pointer, so a field that
// drifted by one slot would still compile and would silently verify something
// other than the file; these assertions keep the ABI from changing unnoticed.
// The offsets are those of the documented layout, where hWVTStateData ends at
// 80 and the optional pSignatureSettings tail brings the struct to 88.
func TestWinTrustStructLayout(t *testing.T) {
	if size := unsafe.Sizeof(winTrustFileInfo{}); size != 32 {
		t.Errorf("winTrustFileInfo is %d bytes, want 32", size)
	}
	if size := unsafe.Sizeof(winTrustData{}); size != 88 {
		t.Errorf("winTrustData is %d bytes, want 88", size)
	}
	if offset := unsafe.Offsetof(winTrustData{}.UnionChoice); offset != 32 {
		t.Errorf("winTrustData.UnionChoice is at %d, want 32", offset)
	}
	if offset := unsafe.Offsetof(winTrustData{}.File); offset != 40 {
		t.Errorf("winTrustData.File is at %d, want 40", offset)
	}
	if offset := unsafe.Offsetof(winTrustData{}.StateAction); offset != 48 {
		t.Errorf("winTrustData.StateAction is at %d, want 48", offset)
	}
	if offset := unsafe.Offsetof(winTrustData{}.StateData); offset != 56 {
		t.Errorf("winTrustData.StateData is at %d, want 56", offset)
	}
	if offset := unsafe.Offsetof(winTrustData{}.ProvFlags); offset != 72 {
		t.Errorf("winTrustData.ProvFlags is at %d, want 72", offset)
	}
	if offset := unsafe.Offsetof(winTrustData{}.UIContext); offset != 76 {
		t.Errorf("winTrustData.UIContext is at %d, want 76", offset)
	}
	if offset := unsafe.Offsetof(winTrustFileInfo{}.FilePath); offset != 8 {
		t.Errorf("winTrustFileInfo.FilePath is at %d, want 8", offset)
	}
	if offset := unsafe.Offsetof(winTrustFileInfo{}.KnownSubject); offset != 24 {
		t.Errorf("winTrustFileInfo.KnownSubject is at %d, want 24", offset)
	}
}

// TestInstallRegistryKeyMatchesInstallerDefinition keeps the updater's view of
// the install directory aligned with the installer that writes it. Both derive
// the key from build/config.yml; if they drifted, the lookup would miss and the
// relauncher would start a stale copy of the application.
func TestInstallRegistryKeyMatchesInstallerDefinition(t *testing.T) {
	root := repositoryRoot(t)
	config := readRepositoryFile(t, filepath.Join(root, "build", "config.yml"))
	script := readRepositoryFile(t, filepath.Join(root, "build", "windows", "nsis", "project.nsi"))

	const define = `!define DSH_WORK_INSTALL_REG_KEY "Software\${INFO_COMPANYNAME}\${INFO_PRODUCTNAME}"`
	if !strings.Contains(script, define) {
		t.Fatalf("project.nsi no longer defines the install key as %s", define)
	}
	companyName := yamlValue(t, config, "companyName")
	productName := yamlValue(t, config, "productName")
	want := `Software\` + companyName + `\` + productName
	if installRegistryKey != want {
		t.Errorf("installRegistryKey = %q, want %q (companyName and productName from build/config.yml)", installRegistryKey, want)
	}
}

// repositoryRoot walks up from this test file until it finds the module's
// build/config.yml, so the test works regardless of the working directory go
// test chooses.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	directory := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(directory, "build", "config.yml")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("could not locate build/config.yml above the test file")
		}
		directory = parent
	}
}

func readRepositoryFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

// yamlValue reads a quoted scalar from the indented info block of
// build/config.yml. It is deliberately narrow: the test only needs the two
// names the installer interpolates.
func yamlValue(t *testing.T, document, key string) string {
	t.Helper()
	expression := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(key) + `:\s*"([^"]+)"\s*$`)
	match := expression.FindStringSubmatch(document)
	if match == nil {
		t.Fatalf("build/config.yml has no %s value", key)
	}
	return match[1]
}
