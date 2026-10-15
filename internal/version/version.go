// Package version carries the application version and release identity that the
// release pipeline substitutes into a build.
package version

import "strings"

// Value is replaced by the Windows release task through -ldflags. Development
// builds keep the explicit fallback so the CLI remains useful without a
// packaging step.
var Value = "development"

// BuildChannel records which release channel produced this executable. It is
// substituted by the release pipeline; an empty value means the build did not
// come from the pipeline.
var BuildChannel = ""

// DistributionFeed is the update feed root this build subscribes to, and
// DistributionPublisher is the Authenticode publisher name an update installer
// must be signed by. Both are substituted by the release pipeline.
//
// They are compile-time values on purpose. A released build must not be
// redirectable to another feed or another publisher by an environment
// variable, so the development feed override is resolved in the desktop
// updater and is honoured only while Value is the development marker.
var (
	DistributionFeed      = ""
	DistributionPublisher = ""
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

// Publisher returns the Authenticode publisher name compiled into this build.
func Publisher() string {
	return strings.TrimSpace(DistributionPublisher)
}

// IsDevelopment reports whether this executable was produced by the release
// pipeline. Only a development build may weaken a production check or accept
// an override of the feed.
func IsDevelopment() bool {
	return String() == "development"
}
