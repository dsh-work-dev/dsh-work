// Package selfupdate turns one verified release from the update feed into a
// verified local installer payload.
//
// The desktop release unit is the signed per-user installer, not a single
// executable: an update must replace the whole dsh-work release unit
// (dsh-work.exe and dsh-work-cli.exe) through the existing maintenance
// boundary and installer, so GUI and CLI can never drift apart.
//
// This package adds the checks the upstream Wails updater does not perform.
// The upstream verifier accepts a release with no Verification at all and
// accepts digest-only verification, so a manifest that simply omits the
// signature would otherwise install unsigned bytes. Nothing here trusts the
// feed: the signature algorithm is allow-listed, the digest and signature are
// recomputed from the staged bytes, and the version identity embedded in the
// signed payload must match the release the feed advertised before any
// installer is started.
package selfupdate

import (
	"crypto"
	"crypto/ed25519"
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"os"

	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
)

var (
	// ErrUnverifiedPayload reports a payload whose digest or signature does
	// not authenticate against the release the feed advertised.
	ErrUnverifiedPayload = errors.New("update payload did not authenticate")
	// ErrUnsupportedSignature reports a release that does not carry the
	// production signing algorithm.
	ErrUnsupportedSignature = errors.New("update payload does not use the supported signature algorithm")
	// ErrUnverifiableRelease reports a release the updater refuses to stage.
	ErrUnverifiableRelease = errors.New("update release carries no verifiable identity")
	// ErrMissingPublicKey reports a signed release with no pinned key.
	ErrMissingPublicKey = errors.New("update payload is signed but no public key is configured")
)

// ProductionSignatureAlgo is the only signature algorithm a release may use.
// It is what `wails3 updater sign` and `wails3 updater manifest -key` produce:
// an Ed25519ph signature over the artifact's SHA-512 digest.
const ProductionSignatureAlgo = "ed25519ph"

// ProductionDigestAlgo is the digest algorithm that accompanies it.
const ProductionDigestAlgo = "sha512"

// FeedVerification describes the identity a feed manifest advertises for one
// artifact. It is copied out of the provider release so the decision about
// whether a payload may be staged can be exercised without the Wails types.
type FeedVerification struct {
	DigestAlgo    string
	Digest        []byte
	SignatureAlgo string
	Signature     []byte
}

// FeedArtifact describes the file a release offers for this platform.
type FeedArtifact struct {
	Filename string
	Filetype string
	Size     int64
	Platform string
	Arch     string
	URL      string
}

// FeedRelease is the part of a provider release this package needs.
type FeedRelease struct {
	Version      string
	Channel      string
	PublishedAt  string
	Artifact     FeedArtifact
	Verification *FeedVerification
}

// ReleaseFrom maps a Wails provider release onto the feed identity this
// package verifies.
func ReleaseFrom(release *wailsupdater.Release) *FeedRelease {
	if release == nil {
		return nil
	}
	mapped := &FeedRelease{
		Version:     release.Version,
		Channel:     release.Channel,
		PublishedAt: release.PublishedAt.UTC().Format("2006-01-02T15:04:05Z"),
		Artifact: FeedArtifact{
			Filename: release.Artifact.Filename,
			Filetype: release.Artifact.Filetype,
			Size:     release.Artifact.Size,
			Platform: release.Artifact.Platform,
			Arch:     release.Artifact.Arch,
		},
	}
	if release.Metadata != nil {
		if url, ok := release.Metadata["endpoint.artifact.url"].(string); ok {
			mapped.Artifact.URL = url
		}
	}
	if release.Verification != nil {
		mapped.Verification = &FeedVerification{
			DigestAlgo:    release.Verification.DigestAlgo,
			Digest:        release.Verification.Digest,
			SignatureAlgo: release.Verification.SignatureAlgo,
			Signature:     release.Verification.Signature,
		}
	}
	return mapped
}

// RequireVerifiable reports whether a release carries the identity a
// production build insists on before it stages anything: the allow-listed
// signature algorithm, a signature, and a digest.
func RequireVerifiable(release *FeedRelease) error {
	if release == nil || release.Verification == nil {
		return fmt.Errorf("%w: the feed manifest carries no verification block", ErrUnverifiableRelease)
	}
	verification := release.Verification
	if len(verification.Signature) == 0 {
		return fmt.Errorf("%w: the feed manifest carries no signature", ErrUnverifiableRelease)
	}
	if len(verification.Digest) == 0 {
		return fmt.Errorf("%w: the feed manifest carries no digest", ErrUnverifiableRelease)
	}
	if algo := normalizeAlgo(verification.SignatureAlgo); algo != ProductionSignatureAlgo {
		return fmt.Errorf("%w: %q", ErrUnsupportedSignature, verification.SignatureAlgo)
	}
	if algo := normalizeAlgo(verification.DigestAlgo); algo != "" && algo != ProductionDigestAlgo {
		return fmt.Errorf("%w: digest algorithm %q", ErrUnsupportedSignature, verification.DigestAlgo)
	}
	return nil
}

// Armor authenticates the bytes of one payload against the release the feed
// advertised. It is deliberately fail-closed for this product: a missing
// verification block, a missing or unexpected algorithm, a missing signature,
// a digest mismatch, a signature that does not verify, or a public key that is
// absent or unparsable all reject the payload before it can be installed.
func Armor(payload []byte, release *FeedRelease, publicKey []byte) error {
	if err := RequireVerifiable(release); err != nil {
		return err
	}
	if len(publicKey) == 0 {
		return ErrMissingPublicKey
	}
	verification := release.Verification
	digestAlgorithm := normalizeAlgo(verification.DigestAlgo)
	if digestAlgorithm == "" {
		digestAlgorithm = ProductionDigestAlgo
	}
	if digestAlgorithm != ProductionDigestAlgo {
		return fmt.Errorf("%w: digest algorithm %q", ErrUnsupportedSignature, verification.DigestAlgo)
	}

	sum := sha512.Sum512(payload)
	if !equalBytes(sum[:], verification.Digest) {
		return fmt.Errorf("%w: digest mismatch", ErrUnverifiedPayload)
	}

	public, err := parseEd25519PublicKey(publicKey)
	if err != nil {
		return err
	}
	if err := ed25519.VerifyWithOptions(public, sum[:], verification.Signature, &ed25519.Options{Hash: crypto.SHA512}); err != nil {
		return fmt.Errorf("%w: %v", ErrUnverifiedPayload, err)
	}
	return nil
}

// ArmorFile authenticates a payload on disk without loading it into memory.
func ArmorFile(path string, release *FeedRelease, publicKey []byte) error {
	if err := RequireVerifiable(release); err != nil {
		return err
	}
	if len(publicKey) == 0 {
		return ErrMissingPublicKey
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hasher := sha512.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return err
	}
	sum := hasher.Sum(nil)
	if !equalBytes(sum, release.Verification.Digest) {
		return fmt.Errorf("%w: digest mismatch", ErrUnverifiedPayload)
	}
	public, err := parseEd25519PublicKey(publicKey)
	if err != nil {
		return err
	}
	if err := ed25519.VerifyWithOptions(public, sum, release.Verification.Signature, &ed25519.Options{Hash: crypto.SHA512}); err != nil {
		return fmt.Errorf("%w: %v", ErrUnverifiedPayload, err)
	}
	return nil
}

func normalizeAlgo(algo string) string {
	out := make([]byte, 0, len(algo))
	for i := 0; i < len(algo); i++ {
		character := algo[i]
		if character >= 'A' && character <= 'Z' {
			character += 'a' - 'A'
		}
		out = append(out, character)
	}
	return string(out)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
