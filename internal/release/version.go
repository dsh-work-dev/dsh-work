// Package release owns the identity of one published dsh-work release: the
// semantic version string that is shown to users, the base version the
// Windows file metadata is derived from, the release channel a build belongs
// to, and the extra identity a nightly build carries.
//
// Channel is explicit metadata, never derived from version precedence. For one
// base version SemVer places nightly above beta, because the identifiers
// compare in ASCII order and "beta" < "nightly". A nightly build therefore
// looks newer than a beta build of the same base, and channel priority can
// never come from Compare. Channel membership is read from Channel, and the
// intended publication order is read from Channels.
package release

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Channel is the release channel a build belongs to or a user subscribes to.
type Channel string

const (
	ChannelStable  Channel = "stable"
	ChannelBeta    Channel = "beta"
	ChannelNightly Channel = "nightly"
)

// Channels lists every supported channel in publication order.
var Channels = []Channel{ChannelStable, ChannelBeta, ChannelNightly}

// DefaultChannel is the channel a fresh installation subscribes to.
const DefaultChannel = ChannelStable

// Valid reports whether the channel is one this build understands.
func (c Channel) Valid() bool {
	switch c {
	case ChannelStable, ChannelBeta, ChannelNightly:
		return true
	}
	return false
}

// ParseChannel maps a persisted or user-supplied value onto a channel. An
// empty value is the default channel; anything else must be an exact match.
func ParseChannel(value string) (Channel, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return DefaultChannel, nil
	}
	channel := Channel(trimmed)
	if !channel.Valid() {
		return "", fmt.Errorf("unknown release channel %q", trimmed)
	}
	return channel, nil
}

// Stage is the pre-release stage of a version. A release version has no stage.
type Stage string

const (
	StageNone    Stage = ""
	StageBeta    Stage = "beta"
	StageNightly Stage = "nightly"
)

// maxNumericIdentifier bounds a numeric component so it stays representable as
// a uint64. SemVer itself has no upper limit; this one keeps arithmetic safe.
const maxNumericIdentifier = uint64(1<<63 - 1)

var (
	identifierPattern = regexp.MustCompile(`^[0-9A-Za-z-]+$`)
	runIDPattern      = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// Version is a parsed release version.
type Version struct {
	raw         string
	major       uint64
	minor       uint64
	patch       uint64
	stage       Stage
	stageNumber uint64
	date        string
	runID       string
	build       string
}

// Parse validates value against SemVer 2.0.0 and the dsh-work release shape.
//
// Accepted shapes:
//
//	X.Y.Z                       release (stable)
//	X.Y.Z-beta.N                beta
//	X.Y.Z-nightly.YYYYMMDD.RUN  nightly, RUN is the decimal workflow run id
//
// Numeric identifiers must not carry leading zeros, identifiers must be
// non-empty and limited to [0-9A-Za-z-], and build metadata ("+...") is
// accepted but ignored for precedence.
func Parse(value string) (Version, error) {
	version, err := parse(value)
	if err != nil {
		return Version{}, err
	}
	return version, nil
}

// ParseRelease validates a version that is about to be handed to a build.
//
// It applies everything Parse does and additionally rejects build metadata. A
// published release carries its identity in the version and its build identity
// in a separately recorded source SHA, so "0.3.0-nightly.20261013.12+meta" is
// rejected rather than silently truncated into a version that no longer matches
// the artifact name.
func ParseRelease(value string) (Version, error) {
	version, err := parse(value)
	if err != nil {
		return Version{}, err
	}
	if version.build != "" {
		return Version{}, fmt.Errorf("release version %q must not carry build metadata", value)
	}
	return version, nil
}

func parse(value string) (Version, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return Version{}, errors.New("release version is empty")
	}
	raw := strings.TrimPrefix(strings.TrimPrefix(trimmed, "v"), "V")

	core := raw
	build := ""
	if index := strings.IndexByte(core, '+'); index >= 0 {
		build = core[index+1:]
		core = core[:index]
		if err := validIdentifiers(build, false); err != nil {
			return Version{}, fmt.Errorf("release version %q: build metadata %w", value, err)
		}
		if build == "" {
			return Version{}, fmt.Errorf("release version %q: build metadata is empty", value)
		}
	}

	pre := ""
	if index := strings.IndexByte(core, '-'); index >= 0 {
		pre = core[index+1:]
		core = core[:index]
		if pre == "" {
			return Version{}, fmt.Errorf("release version %q: pre-release part is empty", value)
		}
	}

	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return Version{}, fmt.Errorf("release version %q must be X.Y.Z", value)
	}
	numbers := make([]uint64, 3)
	for i, field := range fields {
		number, err := parseNumericIdentifier(field)
		if err != nil {
			return Version{}, fmt.Errorf("release version %q: %w", value, err)
		}
		numbers[i] = number
	}

	version := Version{
		raw:   raw,
		major: numbers[0],
		minor: numbers[1],
		patch: numbers[2],
		build: build,
	}
	if pre == "" {
		return version, nil
	}

	parts := strings.Split(pre, ".")
	if err := validIdentifiers(pre, true); err != nil {
		return Version{}, fmt.Errorf("release version %q: pre-release %w", value, err)
	}
	switch Stage(parts[0]) {
	case StageBeta:
		if len(parts) != 2 {
			return Version{}, fmt.Errorf("release version %q: beta pre-release must be beta.N", value)
		}
		number, err := parseNumericIdentifier(parts[1])
		if err != nil {
			return Version{}, fmt.Errorf("release version %q: beta pre-release %w", value, err)
		}
		version.stage = StageBeta
		version.stageNumber = number
	case StageNightly:
		if len(parts) != 3 {
			return Version{}, fmt.Errorf("release version %q: nightly pre-release must be nightly.YYYYMMDD.RUNID", value)
		}
		date := parts[1]
		if len(date) != 8 {
			return Version{}, fmt.Errorf("release version %q: nightly date %q must be YYYYMMDD in UTC", value, date)
		}
		if _, err := parseNumericIdentifier(date); err != nil {
			return Version{}, fmt.Errorf("release version %q: nightly date %q must be YYYYMMDD in UTC", value, date)
		}
		if !runIDPattern.MatchString(parts[2]) {
			return Version{}, fmt.Errorf("release version %q: nightly run id %q must be a decimal workflow run id", value, parts[2])
		}
		version.stage = StageNightly
		version.date = date
		version.runID = parts[2]
	default:
		return Version{}, fmt.Errorf("release version %q: unsupported pre-release stage %q", value, parts[0])
	}
	return version, nil
}

// MustParse is Parse for tests and constant input.
func MustParse(value string) Version {
	version, err := Parse(value)
	if err != nil {
		panic(err)
	}
	return version
}

// String returns the version without a leading "v".
func (v Version) String() string { return v.raw }

// canonical renders the precedence-relevant part the semver package expects: a
// leading "v", and no build metadata.
func (v Version) canonical() string {
	if v.build == "" {
		return "v" + v.raw
	}
	return "v" + strings.TrimSuffix(v.raw, "+"+v.build)
}

// IsZero reports whether the version carries nothing.
func (v Version) IsZero() bool { return v.raw == "" }

// Major, Minor and Patch are the numeric components of the base version.
func (v Version) Major() uint64 { return v.major }
func (v Version) Minor() uint64 { return v.minor }
func (v Version) Patch() uint64 { return v.patch }

// Base returns the X.Y.Z base without any pre-release or build metadata.
func (v Version) Base() string {
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

// Stage returns the pre-release stage; StageNone for a release version.
func (v Version) Stage() Stage { return v.stage }

// BetaNumber returns N for a beta version and 0 otherwise.
func (v Version) BetaNumber() uint64 { return v.stageNumber }

// NightlyDate returns the UTC YYYYMMDD component of a nightly version.
func (v Version) NightlyDate() string { return v.date }

// NightlyRunID returns the workflow run id component of a nightly version.
func (v Version) NightlyRunID() string { return v.runID }

// BuildMetadata returns the "+..." section without the leading plus sign.
func (v Version) BuildMetadata() string { return v.build }

// IsPrerelease reports whether the version carries a pre-release stage.
func (v Version) IsPrerelease() bool { return v.stage != StageNone }

// Channel reports the release channel this version shape belongs to.
func (v Version) Channel() Channel {
	switch v.stage {
	case StageBeta:
		return ChannelBeta
	case StageNightly:
		return ChannelNightly
	default:
		return ChannelStable
	}
}

// ValidateBuildVersion rejects a version that a release build may not carry.
// Local builds report "development" and are allowed; anything else must parse
// as a release version.
func ValidateBuildVersion(value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if trimmed == DevelopmentVersion {
		return nil
	}
	if _, err := ParseRelease(trimmed); err != nil {
		return err
	}
	return nil
}

// DevelopmentVersion is the version reported by a build that was not produced
// by the release pipeline.
const DevelopmentVersion = "development"

func parseNumericIdentifier(value string) (uint64, error) {
	if value == "" {
		return 0, errors.New("numeric identifier is empty")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("identifier %q is not numeric", value)
		}
	}
	if len(value) > 1 && value[0] == '0' {
		return 0, fmt.Errorf("numeric identifier %q has a leading zero", value)
	}
	number, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("numeric identifier %q is out of range", value)
	}
	if number > maxNumericIdentifier {
		return 0, fmt.Errorf("numeric identifier %q is out of range", value)
	}
	return number, nil
}

// validIdentifiers checks every dot-separated identifier. When numeric is
// true, numeric identifiers must not carry leading zeros.
func validIdentifiers(value string, numeric bool) error {
	if value == "" {
		return errors.New("is empty")
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return errors.New("has an empty identifier")
		}
		if !identifierPattern.MatchString(identifier) {
			return fmt.Errorf("identifier %q has characters outside [0-9A-Za-z-]", identifier)
		}
		if numeric && isNumeric(identifier) {
			if _, err := parseNumericIdentifier(identifier); err != nil {
				return err
			}
		}
	}
	return nil
}

func isNumeric(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return value != ""
}
