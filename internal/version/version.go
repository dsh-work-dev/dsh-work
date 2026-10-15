// Package version carries the application version and release identity that the
// release pipeline substitutes into a build.
package version

import (
	"encoding/base64"
	"strings"
)

// Value is replaced by the Windows release task through -ldflags. Development
// builds keep the explicit fallback so the CLI remains useful without a
// packaging step.
var Value = "development"

// BuildChannel records which release channel produced this executable. It is
// substituted by the release pipeline; an empty value means the build did not
// come from the pipeline.
var BuildChannel = ""

// DistributionFeed is the update feed root this build subscribes to, and
// DistributionPublicKey is the raw Ed25519 public key the build trusts. Both
// are substituted by the release pipeline.
//
// They are compile-time values on purpose. A released build must not be
// redirectable to another feed or another trust root by an environment
// variable, so the development overrides are resolved in the desktop updater
// and are honoured only while Value is the development marker.
var (
	DistributionFeed      = ""
	DistributionPublicKey = ""
)

// ReleaseVersion reports the semantic version this executable was built as, or
// the development marker when it was not produced by the release pipeline.
func ReleaseVersion() string {
	return String()
}

// String returns the application version reported by this executable.
func String() string {
	if Value == "" {
		return "development"
	}
	return Value
}

// Channel returns the release channel this executable was built for. The
// channel identifies where the build came from, not what the user subscribes
// to; an empty result means the build has no channel identity.
func Channel() string {
	return strings.ToLower(strings.TrimSpace(BuildChannel))
}

// Feed returns the update feed root compiled into this build.
func Feed() string {
	return strings.TrimSpace(DistributionFeed)
}

// PublicKey returns the raw Ed25519 public key compiled into this build. The
// pipeline substitutes it as base64 text, so it is decoded here; a build whose
// key cannot be decoded has no usable trust root and reports none.
func PublicKey() []byte {
	encoded := strings.TrimSpace(DistributionPublicKey)
	if encoded == "" {
		return nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(encoded); err == nil {
		return decoded
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(encoded); err == nil {
		return decoded
	}
	return nil
}

// IsDevelopment reports whether this executable was produced by the release
// pipeline. Only a development build may weaken a production check or accept
// an override of the feed or the trust root.
func IsDevelopment() bool {
	return String() == "development"
}
