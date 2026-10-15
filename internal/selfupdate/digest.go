// Package selfupdate turns one release from the update feed into a verified
// local installer payload.
//
// The desktop release unit is the signed per-user installer, not a single
// executable: an update must replace the whole dsh-work release unit
// (dsh-work.exe and dsh-work-cli.exe) through the existing maintenance
// boundary and installer, so GUI and CLI can never drift apart.
//
// A build that names a publisher trusts an installer by its Authenticode
// signature, which must chain to a trusted root and name that publisher. A
// build without one (released unsigned through GitHub) trusts the HTTPS feed
// compiled into it. Either way the feed's SHA-512 digest is checked so a
// truncated or corrupted download is refused, and the version identity embedded
// in the payload must match the release the feed advertised before any
// installer is started. The upstream Wails
// verifier accepts a release with no digest at all, so the digest requirement
// is enforced here.
package selfupdate

import (
	"crypto/sha512"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	wailsupdater "github.com/wailsapp/wails/v3/pkg/updater"
)

var (
	// ErrUnverifiedPayload reports a payload whose bytes or signature do not
	// match the release the feed advertised and the publisher this build trusts.
	ErrUnverifiedPayload = errors.New("update payload did not verify")
	// ErrUnsupportedDigest reports a release whose digest is not SHA-512.
	ErrUnsupportedDigest = errors.New("update payload does not use the supported digest algorithm")
	// ErrUnverifiableRelease reports a release the updater refuses to stage.
	ErrUnverifiableRelease = errors.New("update release carries no verifiable identity")
)

// ProductionDigestAlgo is the digest algorithm a release must advertise. It is
// what `wails3 updater manifest` writes for every artifact.
const ProductionDigestAlgo = "sha512"

// FeedVerification describes the digest a feed manifest advertises for one
// artifact. It is copied out of the provider release so the decision about
// whether a payload may be staged can be exercised without the Wails types.
type FeedVerification struct {
	DigestAlgo string
	Digest     []byte
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
			DigestAlgo: release.Verification.DigestAlgo,
			Digest:     release.Verification.Digest,
		}
	}
	return mapped
}

// RequireDigest reports whether a release carries the SHA-512 digest the
// updater checks the downloaded bytes against.
func RequireDigest(release *FeedRelease) error {
	if release == nil || release.Verification == nil {
		return fmt.Errorf("%w: the feed manifest carries no verification block", ErrUnverifiableRelease)
	}
	verification := release.Verification
	if len(verification.Digest) == 0 {
		return fmt.Errorf("%w: the feed manifest carries no digest", ErrUnverifiableRelease)
	}
	if algo := strings.ToLower(strings.TrimSpace(verification.DigestAlgo)); algo != "" && algo != ProductionDigestAlgo {
		return fmt.Errorf("%w: %q", ErrUnsupportedDigest, verification.DigestAlgo)
	}
	return nil
}

// VerifyDigestFile checks a payload on disk against the digest the feed
// advertised, without loading it into memory.
func VerifyDigestFile(path string, release *FeedRelease) error {
	if err := RequireDigest(release); err != nil {
		return err
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
	if !equalBytes(hasher.Sum(nil), release.Verification.Digest) {
		return fmt.Errorf("%w: digest mismatch", ErrUnverifiedPayload)
	}
	return nil
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
