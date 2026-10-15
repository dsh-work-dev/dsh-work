package selfupdate

import (
	"crypto/sha512"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
)

// digestFeed returns a feed release for a payload with the SHA-512 digest the
// release pipeline publishes.
func digestFeed(t *testing.T, payload []byte, mutate func(*FeedRelease)) *FeedRelease {
	t.Helper()
	digest := sha512.Sum512(payload)
	release := &FeedRelease{
		Version:     "0.2.0-beta.1",
		Channel:     "beta",
		PublishedAt: "2026-10-13T12:00:00Z",
		Artifact: FeedArtifact{
			Filename: "dsh-work-0.2.0-beta.1-windows-amd64-installer.exe",
			Filetype: "exe",
			Size:     int64(len(payload)),
			Platform: "windows",
			Arch:     "amd64",
		},
		Verification: &FeedVerification{
			DigestAlgo: ProductionDigestAlgo,
			Digest:     digest[:],
		},
	}
	if mutate != nil {
		mutate(release)
	}
	return release
}

func writePayload(t *testing.T, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.exe")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestRequireDigestRejectsMissingOrForeignDigest covers the fail-open case in
// the upstream verifier: a manifest with no verification block is accepted by
// the framework, so this package has to reject it itself.
func TestRequireDigestRejectsMissingOrForeignDigest(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*FeedRelease)
		wantError error
	}{
		{name: "no verification block", mutate: func(release *FeedRelease) { release.Verification = nil }, wantError: ErrUnverifiableRelease},
		{name: "no digest", mutate: func(release *FeedRelease) { release.Verification.Digest = nil }, wantError: ErrUnverifiableRelease},
		{name: "wrong digest algorithm", mutate: func(release *FeedRelease) { release.Verification.DigestAlgo = "sha256" }, wantError: ErrUnsupportedDigest},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := RequireDigest(digestFeed(t, []byte("payload"), testCase.mutate))
			if !errors.Is(err, testCase.wantError) {
				t.Fatalf("RequireDigest = %v, want %v", err, testCase.wantError)
			}
		})
	}
	if err := RequireDigest(nil); !errors.Is(err, ErrUnverifiableRelease) {
		t.Fatalf("RequireDigest(nil) = %v, want %v", err, ErrUnverifiableRelease)
	}
	unnamed := digestFeed(t, []byte("payload"), func(release *FeedRelease) { release.Verification.DigestAlgo = "" })
	if err := RequireDigest(unnamed); err != nil {
		t.Fatalf("RequireDigest(no algorithm name) = %v, want nil: the manifest default is SHA-512", err)
	}
}

// TestVerifyDigestFile checks the downloaded bytes against the advertised
// digest.
func TestVerifyDigestFile(t *testing.T) {
	payload := []byte("an installer")
	release := digestFeed(t, payload, nil)
	if err := VerifyDigestFile(writePayload(t, payload), release); err != nil {
		t.Fatalf("VerifyDigestFile = %v, want nil", err)
	}
	if err := VerifyDigestFile(writePayload(t, []byte("a tampered installer")), release); !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("VerifyDigestFile(tampered) = %v, want %v", err, ErrUnverifiedPayload)
	}
}

// TestStagingDiscardsUnverifiablePayload proves the install path cannot keep a
// runnable payload when verification fails.
func TestStagingDiscardsUnverifiablePayload(t *testing.T) {
	staging, err := NewStaging(t.TempDir())
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	payload := []byte("an installer")
	source := writePayload(t, payload)

	undigested := digestFeed(t, payload, func(release *FeedRelease) { release.Verification = nil })
	if _, err := staging.Stage(source, undigested); !errors.Is(err, ErrUnverifiableRelease) {
		t.Fatalf("Stage(no digest) = %v, want %v", err, ErrUnverifiableRelease)
	}
	if _, err := os.Stat(filepath.Join(staging.Dir(), stagedFilename)); !os.IsNotExist(err) {
		t.Fatalf("staged payload exists after a rejected release: %v", err)
	}

	mismatched := digestFeed(t, []byte("different bytes"), nil)
	if _, err := staging.Stage(source, mismatched); !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Stage(mismatched) = %v, want %v", err, ErrUnverifiedPayload)
	}
	if _, err := os.Stat(filepath.Join(staging.Dir(), stagedFilename)); !os.IsNotExist(err) {
		t.Fatalf("staged payload exists after a digest mismatch: %v", err)
	}
}

// TestPrepareRefusesReleasedBuildWithoutPublisher proves a released build that
// carries no publisher can never install anything, rather than accepting any
// trusted signature.
func TestPrepareRefusesReleasedBuildWithoutPublisher(t *testing.T) {
	previousVersion, previousPublisher := buildVersion, buildPublisher
	buildVersion = func() string { return "0.2.0-beta.1" }
	buildPublisher = func() string { return "" }
	t.Cleanup(func() { buildVersion, buildPublisher = previousVersion, previousPublisher })

	payload := []byte("an installer")
	_, err := Prepare(writePayload(t, payload), digestFeed(t, payload, nil), ProductionOptions())
	if !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Prepare(no publisher) = %v, want %v", err, ErrUnverifiedPayload)
	}
	_, err = Prepare(writePayload(t, payload), digestFeed(t, payload, nil), Options{Publisher: "dsh-work"})
	if !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Prepare(signature not required) = %v, want %v", err, ErrUnverifiedPayload)
	}
}

// TestStagingRecheckDetectsReplacedBytes lives in staging_windows_test.go: it
// needs a payload that satisfies the Windows installer identity checks.

// TestReleaseFromCarriesFeedIdentity proves the mapping the runner relies on
// keeps the verification block, the artifact identity, and the download URL.
func TestReleaseFromCarriesFeedIdentity(t *testing.T) {
	release := digestFeed(t, []byte("payload"), nil)
	release.Artifact.URL = "https://example.invalid/dsh-work-installer.exe"
	publishedAt, err := time.Parse(time.RFC3339, release.PublishedAt)
	if err != nil {
		t.Fatalf("Parse(%q): %v", release.PublishedAt, err)
	}
	wails := &wailsupdater.Release{
		Version:     release.Version,
		Channel:     release.Channel,
		PublishedAt: publishedAt,
		Artifact: wailsupdater.Artifact{
			Filename: release.Artifact.Filename,
			Filetype: release.Artifact.Filetype,
			Size:     release.Artifact.Size,
			Platform: release.Artifact.Platform,
			Arch:     release.Artifact.Arch,
		},
		Verification: &wailsupdater.Verification{
			DigestAlgo: release.Verification.DigestAlgo,
			Digest:     release.Verification.Digest,
		},
		Metadata: map[string]any{"endpoint.artifact.url": release.Artifact.URL},
	}
	mapped := ReleaseFrom(wails)
	if mapped == nil {
		t.Fatal("ReleaseFrom returned nil")
	}
	if mapped.Version != release.Version || mapped.Channel != release.Channel {
		t.Fatalf("ReleaseFrom version/channel = %q/%q", mapped.Version, mapped.Channel)
	}
	if mapped.Artifact.URL != release.Artifact.URL {
		t.Fatalf("ReleaseFrom artifact URL = %q, want %q", mapped.Artifact.URL, release.Artifact.URL)
	}
	if mapped.Verification == nil || !equalBytes(mapped.Verification.Digest, release.Verification.Digest) {
		t.Fatalf("ReleaseFrom verification = %+v", mapped.Verification)
	}
}

// TestArchiveFeedEntryIsRefused proves a feed entry that would only replace one
// file of the release unit cannot reach the installer handoff.
func TestArchiveFeedEntryIsRefused(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*FeedRelease)
		wantError error
	}{
		{name: "no platform", mutate: func(r *FeedRelease) { r.Artifact.Platform = "" }, wantError: ErrWrongPlatform},
		{name: "macOS", mutate: func(r *FeedRelease) { r.Artifact.Platform = "darwin" }, wantError: ErrWrongPlatform},
		{name: "no architecture", mutate: func(r *FeedRelease) { r.Artifact.Arch = "" }, wantError: ErrWrongPlatform},
		{name: "zip payload", mutate: func(r *FeedRelease) { r.Artifact.Filename = "dsh-work.zip" }, wantError: ErrPayloadNotInstaller},
		{name: "bare executable", mutate: func(r *FeedRelease) { r.Artifact.Filename = "dsh-work.exe" }, wantError: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			release := digestFeed(t, []byte("payload"), testCase.mutate)
			err := requireInstallerArtifact(release)
			if testCase.wantError == nil {
				if err != nil {
					t.Fatalf("requireInstallerArtifact = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, testCase.wantError) {
				t.Fatalf("requireInstallerArtifact = %v, want %v", err, testCase.wantError)
			}
		})
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	return info.Mode()
}
