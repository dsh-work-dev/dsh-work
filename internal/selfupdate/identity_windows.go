//go:build windows

package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/local/dsh-work/internal/release"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// hostMachines lists the machine types an installer for this host may declare.
//
// The installer is an NSIS stub, which is a 32-bit x86 executable even when it
// installs a 64-bit release unit: a real installer built with the project's own
// script reports machine 0x014C and runs under WOW64 (or emulation on ARM64).
// Refusing it would refuse every genuine installer, so the x86 stub is accepted
// on both hosts. The host architecture is accepted as well, for an installer
// that is natively targeted, and the payload's release identity is still proved
// separately by its signed version resource.
var hostMachines = map[string][]uint16{
	"amd64": {machineI386, machineAMD64},
	"arm64": {machineI386, machineARM64},
}

// installRegistryKey is where the installer records the per-user install
// directory. It is the same key the installer reads on its next run, so both
// sides of an update agree on which copy of the release unit is current.
//
// The installer composes it as Software\<companyName>\<productName> from
// build/config.yml (build/windows/nsis/project.nsi:14
// DSH_WORK_INSTALL_REG_KEY), which is "Software\dsh-work\dsh-work" for this
// product. A wrong key would silently fall back to the default directory and
// then launch a stale copy after an update.
const installRegistryKey = `Software\dsh-work\dsh-work`

// verifyInstallerIdentity proves the payload installs the release the feed
// advertised and that it can run on this machine. A signature only proves who
// produced the bytes: without this check a publisher (or a tampered feed) could
// point the update at an older, still correctly signed payload.
func verifyInstallerIdentity(path string, version release.Version, options Options) error {
	machine, err := readMachine(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPayloadNotInstaller, err)
	}
	expected, ok := hostMachines[runtime.GOARCH]
	if !ok {
		return fmt.Errorf("%w: unsupported host architecture %s", ErrUnsupportedPlatform, runtime.GOARCH)
	}
	matched := false
	for _, candidate := range expected {
		if machine == candidate {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("%w: installer targets machine 0x%04X, host is %s", ErrWrongPlatform, machine, runtime.GOARCH)
	}

	identity, err := readPayloadIdentity(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPayloadIdentityMismatch, err)
	}
	if err := matchPayloadVersion(identity, version); err != nil {
		return err
	}
	if options.RequireOSSignature {
		if err := verifyAuthenticode(path); err != nil {
			return err
		}
	}
	return nil
}

// matchPayloadVersion requires the payload to declare the advertised release
// itself, not merely a version of the same base.
//
// The numeric four-field version cannot carry a channel: 0.2.0, 0.2.0-beta.1 and
// 0.2.0-nightly.20261013.1 all map to 0.2.0.0, so it is only checked for
// consistency. The identity comes from the version strings the packaging chain
// writes into the payload (build/windows/nsis/project.nsi), which name the exact
// release, and the bytes carrying them are covered by the payload signature — a
// feed cannot re-label one signed installer as a different release.
func matchPayloadVersion(identity payloadIdentity, version release.Version) error {
	expectedFile, err := version.WindowsFileVersion()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPayloadIdentityMismatch, err)
	}
	declared := identity.versionStrings()
	if len(declared) == 0 {
		return fmt.Errorf("%w: payload reports file version %q but no release version string, release is %s",
			ErrPayloadIdentityMismatch, identity.fixed, version.String())
	}
	matched := false
	for _, value := range declared {
		if value == version.String() {
			matched = true
			continue
		}
		// A string that names a different release is contradictory evidence and
		// is refused even when another string matches.
		if other, err := release.Parse(value); err == nil && !release.Equal(other, version) {
			return fmt.Errorf("%w: payload reports release %s, release is %s",
				ErrPayloadIdentityMismatch, other.String(), version.String())
		}
	}
	if !matched {
		return fmt.Errorf("%w: payload reports release %q, release is %s",
			ErrPayloadIdentityMismatch, strings.Join(declared, `" / "`), version.String())
	}
	if fixed := strings.TrimSpace(identity.fixed); fixed != "" && fixed != expectedFile {
		return fmt.Errorf("%w: payload reports file version %s, release is %s (%s)",
			ErrPayloadIdentityMismatch, fixed, version.String(), expectedFile)
	}
	return nil
}

// verifyAuthenticode requires a signature that chains to a trusted publisher.
// The payload is handled by the operating system's own installer, so the same
// policy a user's machine applies to a downloaded installer is applied here.
func verifyAuthenticode(path string) error {
	trusted, err := authenticodeTrusted(path)
	if err != nil {
		return fmt.Errorf("%w: could not read the installer signature: %v", ErrUnverifiedPayload, err)
	}
	if !trusted {
		return fmt.Errorf("%w: installer is not signed by a trusted publisher", ErrUnverifiedPayload)
	}
	return nil
}

// trustResultName describes the WinVerifyTrust status a caller may log. The
// numeric value is kept beside the name because only a handful of the status
// codes are documented; the rest are reported as their hexadecimal form.
func trustResultName(status uintptr) string {
	switch uint32(status) {
	case 0x00000000:
		return "trusted"
	case 0x800B0100:
		return "TRUST_E_NOSIGNATURE"
	case 0x800B0101:
		return "CERT_E_EXPIRED"
	case 0x800B0109:
		return "CERT_E_UNTRUSTEDROOT"
	case 0x800B010A:
		return "CERT_E_CHAINING"
	case 0x800B010C:
		return "TRUST_E_EXPLICIT_DISTRUST"
	case 0x800B0110:
		return "CERT_E_WRONG_USAGE"
	case 0x800B0111:
		return "TRUST_E_SUBJECT_NOT_TRUSTED"
	case 0x80092003:
		return "CRYPT_E_FILE_ERROR"
	case 0x80096005:
		return "TRUST_E_TIME_STAMP"
	case 0x80096010:
		return "TRUST_E_BAD_DIGEST"
	case 0x800B0001:
		return "TRUST_E_PROVIDER_UNKNOWN"
	case 0x800B0003:
		return "TRUST_E_SUBJECT_FORM_UNKNOWN"
	}
	return fmt.Sprintf("0x%08X", uint32(status))
}

// installDir returns the directory the installed release unit occupies. The
// installer's own default is used when this user has not recorded an install
// location yet.
func installDir() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, installRegistryKey, registry.QUERY_VALUE)
	if err == nil {
		defer key.Close()
		value, _, err := key.GetStringValue("Install_Dir")
		if err == nil {
			if directory, err := cleanInstallDir(value); err == nil {
				return directory, nil
			}
		}
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return cleanInstallDir(filepath.Join(local, "Programs", "dsh-work"))
}

// --- WinVerifyTrust ---

var (
	winTrust               = windows.NewLazySystemDLL("wintrust.dll")
	procWinVerifyTrust     = winTrust.NewProc("WinVerifyTrust")
	errWinTrustUnavailable = errors.New("wintrust.dll is unavailable")
)

const (
	// hwndActionIgnore is the hwndAction WinVerifyTrust expects when no
	// originating window exists.
	hwndActionIgnore uintptr = 0
	// wtdUIChoiceNone runs the check without any dialog.
	wtdUIChoiceNone uint32 = 2
	wtdRevokeNone   uint32 = 0
	wtdChoiceFile   uint32 = 1
)

const (
	wtdStateActionIgnore     = 0
	wtdStateActionClose      = 2
	wtdSaferFlag             = 0x00000010
	wtdCacheOnlyURLRetrieval = 0x00001000
	wtdRevokeWholeChain      = 0x00000002
	wtdDisableMD2MD4         = 0x00000020
	wtdUIContextExecute      = 0
)

// winTrustFileInfo mirrors WINTRUST_FILE_INFO.
type winTrustFileInfo struct {
	Size         uint32
	FilePath     *uint16
	FileHandle   windows.Handle
	KnownSubject *windows.GUID
}

// winTrustData mirrors WINTRUST_DATA through hWVTStateData. WinVerifyTrust only
// reads the fields it documents for the requested action, and every action used
// here is a query, so the optional Settings tail is not part of the struct.
type winTrustData struct {
	Size               uint32
	PolicyCallbackData uintptr
	SIPClientData      uintptr
	UIChoice           uint32
	RevocationChecks   uint32
	UnionChoice        uint32
	File               *winTrustFileInfo
	StateAction        uint32
	StateData          windows.Handle
	URLReference       *uint16
	ProvFlags          uint32
	UIContext          uint32
	SignatureSettings  uintptr
}

// authenticodeTrusted reports whether Windows trusts the file's Authenticode
// signature and its chain. Revocation checking is limited to cached
// information so an offline or locked-down machine cannot turn the check into
// a hang; every other chain failure still fails closed.
func authenticodeTrusted(path string) (bool, error) {
	status, err := authenticodeStatus(path)
	if err != nil {
		return false, err
	}
	return status == 0, nil
}

// authenticodeStatus runs the WinVerifyTrust query and returns its raw status,
// where zero means the file carries a trusted signature. The status is kept
// available so a refusal can be logged with the reason Windows gave rather
// than a single generic message.
func authenticodeStatus(path string) (uintptr, error) {
	if err := procWinVerifyTrust.Find(); err != nil {
		return 0, errWinTrustUnavailable
	}
	filePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	fileInfo := winTrustFileInfo{
		Size:     uint32(unsafe.Sizeof(winTrustFileInfo{})),
		FilePath: filePath,
	}
	data := winTrustData{
		Size:             uint32(unsafe.Sizeof(winTrustData{})),
		UIChoice:         wtdUIChoiceNone,
		RevocationChecks: wtdRevokeNone,
		UnionChoice:      wtdChoiceFile,
		File:             &fileInfo,
		StateAction:      wtdStateActionIgnore,
		ProvFlags:        wtdSaferFlag | wtdCacheOnlyURLRetrieval | wtdRevokeWholeChain | wtdDisableMD2MD4,
		UIContext:        wtdUIContextExecute,
	}
	action := windows.GUID{Data1: 0x00AAC56B, Data2: 0xCD44, Data3: 0x11D0, Data4: [8]byte{0x8C, 0xC2, 0x00, 0xC0, 0x4F, 0xC2, 0x95, 0xEE}}
	result, _, _ := procWinVerifyTrust.Call(
		hwndActionIgnore, uintptr(unsafe.Pointer(&action)),
		uintptr(unsafe.Pointer(&data)),
	)
	if result != 0 {
		return result, nil
	}
	// Release the state data the trust provider allocated for this query.
	data.StateAction = wtdStateActionClose
	_, _, _ = procWinVerifyTrust.Call(
		hwndActionIgnore, uintptr(unsafe.Pointer(&action)),
		uintptr(unsafe.Pointer(&data)),
	)
	return 0, nil
}
