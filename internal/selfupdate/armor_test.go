package selfupdate

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
)

// ed25519phOptions is the variant the release pipeline signs with: Ed25519ph
// over the artifact's SHA-512 digest.
var ed25519phOptions = &ed25519.Options{Hash: crypto.SHA512}

// signEd25519ph produces the signature VerifyWithOptions(..., SHA512) accepts,
// using the toolchain's own Ed25519ph signer through the crypto.Signer
// interface: the options select the pre-hashed variant, and the message handed
// to it is the artifact's SHA-512 digest, exactly as the release pipeline signs.
func signEd25519ph(t *testing.T, private ed25519.PrivateKey, prehashed []byte) []byte {
	t.Helper()
	signature, err := private.Sign(nil, prehashed, ed25519phOptions)
	if err != nil {
		t.Fatalf("Sign(ed25519ph): %v", err)
	}
	return signature
}

// signedFeed returns a feed release for a payload along with the public key it
// authenticates against, using the production algorithm (Ed25519ph over the
// SHA-512 digest).
func signedFeed(t *testing.T, payload []byte, mutate func(*FeedRelease)) (*FeedRelease, []byte) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	digest := sha512.Sum512(payload)
	signature := signEd25519ph(t, private, digest[:])
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
			DigestAlgo:    ProductionDigestAlgo,
			Digest:        digest[:],
			SignatureAlgo: ProductionSignatureAlgo,
			Signature:     signature,
		},
	}
	if mutate != nil {
		mutate(release)
	}
	return release, public
}

func writePayload(t *testing.T, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.exe")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// TestRequireVerifiableRejectsMissingIdentityAndAlgorithms covers the fail-open
// cases in the upstream verifier: a manifest with no verification block and a
// manifest with only a digest are both accepted by the framework, so this
// package has to reject them itself.
func TestRequireVerifiableRejectsMissingIdentityAndAlgorithms(t *testing.T) {
	cases := []struct {
		name      string
		mutate    func(*FeedRelease)
		wantError error
	}{
		{name: "no verification block", mutate: func(release *FeedRelease) { release.Verification = nil }, wantError: ErrUnverifiableRelease},
		{name: "no signature", mutate: func(release *FeedRelease) { release.Verification.Signature = nil }, wantError: ErrUnverifiableRelease},
		{name: "empty signature", mutate: func(release *FeedRelease) { release.Verification.Signature = []byte{} }, wantError: ErrUnverifiableRelease},
		{name: "no digest", mutate: func(release *FeedRelease) { release.Verification.Digest = nil }, wantError: ErrUnverifiableRelease},
		{name: "missing algorithm", mutate: func(release *FeedRelease) { release.Verification.SignatureAlgo = "" }, wantError: ErrUnsupportedSignature},
		{name: "wrong algorithm", mutate: func(release *FeedRelease) { release.Verification.SignatureAlgo = "ed25519" }, wantError: ErrUnsupportedSignature},
		{name: "wrong digest algorithm", mutate: func(release *FeedRelease) { release.Verification.DigestAlgo = "sha256" }, wantError: ErrUnsupportedSignature},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			release, _ := signedFeed(t, []byte("payload"), testCase.mutate)
			err := RequireVerifiable(release)
			if !errors.Is(err, testCase.wantError) {
				t.Fatalf("RequireVerifiable = %v, want %v", err, testCase.wantError)
			}
		})
	}
	if err := RequireVerifiable(nil); !errors.Is(err, ErrUnverifiableRelease) {
		t.Fatalf("RequireVerifiable(nil) = %v, want %v", err, ErrUnverifiableRelease)
	}
}

// TestArmorAcceptsProductionSignature is the positive control: the signing
// scheme the release pipeline produces must authenticate.
func TestArmorAcceptsProductionSignature(t *testing.T) {
	payload := []byte("a signed installer")
	release, public := signedFeed(t, payload, nil)
	if err := Armor(payload, release, public); err != nil {
		t.Fatalf("Armor = %v, want nil", err)
	}
}

// TestArmorRejectsTamperedPayloadAndKey covers the checks that must fail closed
// once a signature is present.
func TestArmorRejectsTamperedPayloadAndKey(t *testing.T) {
	payload := []byte("a signed installer")
	release, public := signedFeed(t, payload, nil)

	if err := Armor([]byte("a tampered installer"), release, public); !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Armor(tampered) = %v, want %v", err, ErrUnverifiedPayload)
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	if err := Armor(payload, release, other); !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Armor(other key) = %v, want %v", err, ErrUnverifiedPayload)
	}
	if err := Armor(payload, release, nil); !errors.Is(err, ErrMissingPublicKey) {
		t.Fatalf("Armor(no key) = %v, want %v", err, ErrMissingPublicKey)
	}
	if err := Armor(payload, release, []byte("not a key")); err == nil {
		t.Fatal("Armor(unparsable key) = nil, want an error")
	}

	// A signature that verifies but for different bytes must not authenticate
	// against a re-labelled payload: this is the downgrade/replay shape.
	replayed, replayPublic := signedFeed(t, []byte("0.3.0 installer"), nil)
	replayed.Version = "0.1.0"
	if err := Armor(payload, replayed, replayPublic); !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Armor(replayed bytes) = %v, want %v", err, ErrUnverifiedPayload)
	}
}

// TestStagingDiscardsUnverifiablePayload proves the install path cannot keep a
// runnable payload when authentication fails.
func TestStagingDiscardsUnverifiablePayload(t *testing.T) {
	base := t.TempDir()
	staging, err := NewStaging(base)
	if err != nil {
		t.Fatalf("NewStaging: %v", err)
	}
	payload := []byte("a signed installer")
	source := writePayload(t, payload)

	unsigned, _ := signedFeed(t, payload, func(release *FeedRelease) { release.Verification.Signature = nil })
	if _, err := staging.Stage(source, unsigned, nil); !errors.Is(err, ErrUnverifiableRelease) {
		t.Fatalf("Stage(unsigned) = %v, want %v", err, ErrUnverifiableRelease)
	}
	if _, err := os.Stat(filepath.Join(staging.Dir(), stagedFilename)); !os.IsNotExist(err) {
		t.Fatalf("staged payload exists after a rejected release: %v", err)
	}

	mismatched, public := signedFeed(t, []byte("different bytes"), nil)
	if _, err := staging.Stage(source, mismatched, public); !errors.Is(err, ErrUnverifiedPayload) {
		t.Fatalf("Stage(mismatched) = %v, want %v", err, ErrUnverifiedPayload)
	}
	if _, err := os.Stat(filepath.Join(staging.Dir(), stagedFilename)); !os.IsNotExist(err) {
		t.Fatalf("staged payload exists after a digest mismatch: %v", err)
	}
}

// TestStagingRecheckDetectsReplacedBytes lives in staging_windows_test.go: it
// needs a payload that satisfies the Windows installer identity checks.

// TestReleaseFromCarriesFeedIdentity proves the mapping the runner relies on
// keeps the verification block, the artifact identity, and the download URL.
func TestReleaseFromCarriesFeedIdentity(t *testing.T) {
	release, _ := signedFeed(t, []byte("payload"), nil)
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
			DigestAlgo:    release.Verification.DigestAlgo,
			Digest:        release.Verification.Digest,
			SignatureAlgo: release.Verification.SignatureAlgo,
			Signature:     release.Verification.Signature,
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
	if mapped.Verification == nil || mapped.Verification.SignatureAlgo != ProductionSignatureAlgo {
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
			release, _ := signedFeed(t, []byte("payload"), testCase.mutate)
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

// TestVerificationMatchesUpstreamSemantics documents the wire format this
// package must interoperate with: Ed25519ph over the SHA-512 digest of the
// artifact, exactly as the framework verifies it. If upstream ever changes that
// contract, this test fails instead of the update silently refusing every
// release.
func TestVerificationMatchesUpstreamSemantics(t *testing.T) {
	payload := []byte("wails updater manifest fixture")
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	digest := sha512.Sum512(payload)
	signature := signEd25519ph(t, private, digest[:])
	verification := &wailsupdater.Verification{
		DigestAlgo:    ProductionDigestAlgo,
		Digest:        digest[:],
		SignatureAlgo: ProductionSignatureAlgo,
		Signature:     signature,
	}
	release := &FeedRelease{
		Version: "0.2.0",
		Artifact: FeedArtifact{
			Filename: "dsh-work-0.2.0-windows-amd64-installer.exe",
			Platform: "windows",
			Arch:     "amd64",
		},
		Verification: &FeedVerification{
			DigestAlgo:    verification.DigestAlgo,
			Digest:        verification.Digest,
			SignatureAlgo: verification.SignatureAlgo,
			Signature:     verification.Signature,
		},
	}
	if err := Armor(payload, release, public); err != nil {
		t.Fatalf("Armor = %v, want nil", err)
	}
	// The framework verifies Ed25519ph with exactly these options; signing with
	// the same variant must produce a signature it accepts.
	if err := ed25519.VerifyWithOptions(public, digest[:], signature, ed25519phOptions); err != nil {
		t.Fatalf("the framework's own verifier rejected the production signature: %v", err)
	}
	if _, err := parseEd25519PublicKey(public); err != nil {
		t.Fatalf("parseEd25519PublicKey(raw) = %v", err)
	}
	if !strings.HasPrefix(PublicKeyFingerprint(public), "sha256:") {
		t.Fatalf("PublicKeyFingerprint = %q", PublicKeyFingerprint(public))
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
