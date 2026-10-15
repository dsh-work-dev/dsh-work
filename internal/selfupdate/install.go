package selfupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/local/dsh-work/internal/release"
	"github.com/local/dsh-work/internal/version"
)

var (
	// ErrPayloadNotInstaller reports a staged payload that is not the signed
	// per-user installer the desktop release unit requires.
	ErrPayloadNotInstaller = errors.New("update payload is not a Windows installer")
	// ErrPayloadIdentityMismatch reports a payload whose own embedded version
	// does not match the release the feed advertised. A signature proves who
	// produced the bytes, not what they install.
	ErrPayloadIdentityMismatch = errors.New("update payload identity does not match the advertised release")
	// ErrWrongPlatform reports an artifact published for another platform or
	// architecture. A wildcard entry is not a compatibility guarantee.
	ErrWrongPlatform = errors.New("update payload targets another platform or architecture")
	// ErrUnsupportedPlatform reports an installer handoff on a host that has
	// no installer release unit yet.
	ErrUnsupportedPlatform = errors.New("installer updates are not supported on this platform yet")
	// ErrInstallerUnavailable reports a missing or unreadable installer file.
	ErrInstallerUnavailable = errors.New("staged installer is not available")
)

// windowsPlatform is the only platform with a release unit today.
const windowsPlatform = "windows"

// Installer describes one verified installer payload that is ready to run.
type Installer struct {
	// Version is the release version the feed advertised.
	Version string
	// Channel is the release channel recorded by the feed.
	Channel string
	// Path is the absolute path of the verified installer.
	Path string
	// Kind records which artifact kind the feed published.
	Kind Kind
}

// Kind is the artifact kind implied by an update feed entry.
type Kind string

const (
	// KindUnknown is an artifact this build cannot install.
	KindUnknown Kind = ""
	// KindInstaller is a Windows per-user installer.
	KindInstaller Kind = "installer"
	// KindExecutable is a single executable that would only replace one file
	// of the release unit.
	KindExecutable Kind = "executable"
)

// buildVersion reports the version this binary was built as. A released build
// substitutes a semantic version; anything else is a development build, which
// has no signing identity and may therefore install its own local test
// packages.
var buildVersion = version.String

// buildPublisher reports the code-signing publisher compiled into this build.
var buildPublisher = version.Publisher

// Options controls the checks a caller insists on.
type Options struct {
	// RequireOSSignature insists that the payload carries a trusted Windows
	// signature from Publisher. A released build that names a publisher always
	// insists on it. A released build without a publisher is distributed
	// unsigned and trusts the payload by its compiled-in HTTPS feed, the feed
	// digest and the payload's embedded version; a development build installs
	// local test packages.
	RequireOSSignature bool
	// Publisher is the name the Authenticode signer certificate must carry
	// (its simple display name, normally the subject CN).
	Publisher string
}

// ProductionOptions returns the checks a released build must apply at the
// minimum, whatever the caller asked for. The signature check follows the
// compiled-in publisher: once the release pipeline names one, every update must
// be signed by it, and nothing at runtime can turn that off.
func ProductionOptions() Options {
	publisher := buildPublisher()
	return Options{
		RequireOSSignature: !isDevelopmentBuild() && strings.TrimSpace(publisher) != "",
		Publisher:          publisher,
	}
}

// isDevelopmentBuild reports whether this binary was built by the release
// pipeline. Only such a build may weaken a production check.
func isDevelopmentBuild() bool {
	reported := strings.TrimSpace(buildVersion())
	return reported == "" || reported == release.DevelopmentVersion
}

// Prepare validates a staged payload and returns the verified installer.
//
// Every check runs before the caller is allowed to stop the UI, the daemon or
// the Worker: the feed identity, the payload digest, the payload's own embedded
// release version, the target machine architecture and, when the build names a
// publisher, the payload's Authenticode publisher.
func Prepare(payloadPath string, feed *FeedRelease, options Options) (*Installer, error) {
	if feed == nil {
		return nil, fmt.Errorf("%w: no release", ErrUnverifiableRelease)
	}
	if !isDevelopmentBuild() && strings.TrimSpace(buildPublisher()) != "" && !options.RequireOSSignature {
		return nil, fmt.Errorf("%w: a build with a publisher must require a signature on the installer", ErrUnverifiedPayload)
	}
	if options.RequireOSSignature && strings.TrimSpace(options.Publisher) == "" {
		return nil, fmt.Errorf("%w: no installer publisher is configured for this build", ErrUnverifiedPayload)
	}
	if err := release.ValidateBuildVersion(buildVersion()); err != nil {
		return nil, fmt.Errorf("%w: build version: %v", ErrUnverifiableRelease, err)
	}
	if err := release.ValidateBuildVersion(feed.Version); err != nil {
		return nil, fmt.Errorf("%w: advertised version %q: %v", ErrUnverifiableRelease, feed.Version, err)
	}
	version, err := release.Parse(feed.Version)
	if err != nil {
		return nil, fmt.Errorf("%w: advertised version %q: %v", ErrUnverifiableRelease, feed.Version, err)
	}
	if runtime.GOOS != windowsPlatform {
		return nil, fmt.Errorf("%w: host is %s", ErrUnsupportedPlatform, runtime.GOOS)
	}
	if err := requireInstallerArtifact(feed); err != nil {
		return nil, err
	}

	absolute, err := filepath.Abs(payloadPath)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInstallerUnavailable, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrInstallerUnavailable, absolute)
	}
	if feed.Artifact.Size > 0 && info.Size() != feed.Artifact.Size {
		return nil, fmt.Errorf("%w: staged size %d does not match the advertised size %d", ErrUnverifiedPayload, info.Size(), feed.Artifact.Size)
	}

	if err := VerifyDigestFile(absolute, feed); err != nil {
		return nil, err
	}
	if err := verifyInstallerIdentity(absolute, version, options); err != nil {
		return nil, err
	}

	return &Installer{
		Version: version.String(),
		Channel: feed.Channel,
		Path:    absolute,
		Kind:    KindInstaller,
	}, nil
}

// ValidateRelease refuses a feed record before any bytes are downloaded. It is
// the check the update flow runs on every offered release, so a feed entry that
// carries no digest, claims a version this build cannot reason about, or
// advertises an artifact that is not a Windows installer for this host never
// reaches the download step.
func ValidateRelease(feed *FeedRelease) error {
	if feed == nil {
		return fmt.Errorf("%w: no release", ErrUnverifiableRelease)
	}
	if err := RequireDigest(feed); err != nil {
		return err
	}
	if err := release.ValidateBuildVersion(feed.Version); err != nil {
		return fmt.Errorf("%w: advertised version %q: %v", ErrUnverifiableRelease, feed.Version, err)
	}
	if _, err := release.Parse(feed.Version); err != nil {
		return fmt.Errorf("%w: advertised version %q: %v", ErrUnverifiableRelease, feed.Version, err)
	}
	if err := requireInstallerArtifact(feed); err != nil {
		return err
	}
	if runtime.GOOS != windowsPlatform {
		return fmt.Errorf("%w: host is %s", ErrUnsupportedPlatform, runtime.GOOS)
	}
	if advertised, host := normalizeArchitecture(feed.Artifact.Arch), normalizeArchitecture(runtime.GOARCH); advertised != host {
		return fmt.Errorf("%w: release %s targets %s, this host is %s", ErrWrongPlatform, feed.Version, feed.Artifact.Arch, runtime.GOARCH)
	}
	return nil
}

// normalizeArchitecture maps the architecture spellings a feed may use onto the
// Go name, so a feed cannot rely on a wildcard or an alias to match a host it
// does not actually support.
func normalizeArchitecture(arch string) string {
	switch strings.ToLower(strings.TrimSpace(arch)) {
	case "amd64", "x86_64", "x64":
		return "amd64"
	case "arm64", "aarch64":
		return "arm64"
	default:
		return strings.ToLower(strings.TrimSpace(arch))
	}
}

// requireInstallerArtifact rejects a feed entry that is not the signed per-user
// installer: the release unit is dsh-work.exe plus dsh-work-cli.exe, and only
// the installer replaces both under the maintenance boundary. A single
// executable entry is refused instead of being handed to a self-swap.
func requireInstallerArtifact(feed *FeedRelease) error {
	platform := strings.ToLower(strings.TrimSpace(feed.Artifact.Platform))
	if platform == "" || platform != windowsPlatform {
		// An empty platform used to mean "matches anything" in the feed
		// schema; for this product it means the publisher did not state a
		// platform, which is not a compatibility guarantee.
		return fmt.Errorf("%w: release %s advertises platform %q", ErrWrongPlatform, feed.Version, feed.Artifact.Platform)
	}
	arch := strings.ToLower(strings.TrimSpace(feed.Artifact.Arch))
	if arch == "" {
		return fmt.Errorf("%w: release %s does not pin an architecture", ErrWrongPlatform, feed.Version)
	}
	filename := strings.ToLower(filepath.Base(strings.TrimSpace(feed.Artifact.Filename)))
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		return fmt.Errorf("%w: release %s carries no artifact filename", ErrUnverifiableRelease, feed.Version)
	}
	if !strings.HasSuffix(filename, ".exe") {
		return fmt.Errorf("%w: release %s offers %q", ErrPayloadNotInstaller, feed.Version, feed.Artifact.Filename)
	}
	return nil
}

// InstallerArgs returns the arguments an unattended update must pass to the
// verified installer.
//
// Silent mode is what makes an in-app update possible: the installer's own
// maintenance handshake asks for confirmation in a window when it is not
// silent, and asks for a click on its finish page before continuing. Silent
// mode still starts the new workbench itself, because the installer can wait
// for the maintenance boundary it owns to be free before it launches.
//
// The install directory is deliberately not passed. The installer resolves the
// directory this user recorded (HKCU Install_Dir) and only falls back to its
// default, so a daemon that was started from an unexpected environment cannot
// create a second copy of the release unit.
func (i *Installer) InstallerArgs() ([]string, error) {
	if i == nil || i.Path == "" {
		return nil, ErrInstallerUnavailable
	}
	return []string{"/S"}, nil
}

// InstallDirFor returns the directory the installed release unit occupies.
// The user's recorded install location wins; otherwise the installer default
// is used. An empty or unusable location is reported as an error so a caller
// never starts the wrong executable.
func InstallDirFor() (string, error) {
	directory, err := installDir()
	if err != nil {
		return "", err
	}
	return cleanInstallDir(directory)
}

// LaunchPath is the executable an update must start once the installer has
// finished replacing the release unit.
func LaunchPath() (string, error) {
	directory, err := InstallDirFor()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "dsh-work.exe"), nil
}

// cleanInstallDir validates a directory that came from outside this package
// before it is used to locate an installed release unit. It must be absolute
// and free of characters that a command line or an installer cannot carry.
func cleanInstallDir(dir string) (string, error) {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		return "", fmt.Errorf("install directory is empty")
	}
	if strings.ContainsAny(cleaned, "\"|<>?*") || strings.ContainsRune(cleaned, 0) {
		return "", fmt.Errorf("install directory %q contains characters an installer cannot use", dir)
	}
	if !filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("install directory %q must be absolute", dir)
	}
	return filepath.Clean(cleaned), nil
}
