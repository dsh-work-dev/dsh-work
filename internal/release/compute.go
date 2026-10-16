package release

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// errInvalidInput marks a rejection that is caused by the caller's input rather
// than by a broken environment, so a command can print the reason on its own
// line instead of a wrapped diagnostic.
var errInvalidInput = errors.New("invalid release input")

// InputError reports whether err was caused by invalid caller input.
func InputError(err error) bool { return errors.Is(err, errInvalidInput) }

// ReleaseOptions carries everything that identifies one release attempt. The
// base version is always the build/config.yml value; Version, Date and RunID
// are the explicit inputs a preflight passes in.
type ReleaseOptions struct {
	Channel Channel

	// Version is the full release version. A beta tag's version is passed in
	// unchanged ("0.3.0-beta.2"); a base-only value ("0.3.0") is accepted for
	// the stable channel.
	Version string
	// Base is the X.Y.Z base from build/config.yml. It must match the base of
	// Version and must not carry a pre-release part itself.
	Base string
	// Date is the UTC date of the workflow run, as YYYY-MM-DD or YYYYMMDD. It
	// is part of a nightly version and is ignored by other channels.
	Date string
	// RunID is the decimal GitHub workflow run id. It is part of a nightly
	// version and is ignored by other channels. run_attempt is deliberately not
	// an input: a retry of the same run reproduces the same version.
	RunID string
	// SourceSHA is the build commit, recorded next to the version and never
	// inside it. Build metadata is ignored for precedence, so the SHA must not
	// be smuggled into the version.
	SourceSHA string
	// LastStableVersion is the highest stable version published for this
	// repository, used to prove that a nightly base moved forward. Empty means
	// no stable release exists yet.
	LastStableVersion string
}

// Release is the identity computed once by the preflight. Every consumer - the
// build scripts, the installer name, the manifest, About and the release notes
// - uses this one value.
type Release struct {
	Version        Version
	Channel        Channel
	Base           string
	SourceSHA      string
	Date           string
	RunID          string
	FileVersion    string
	LastStableBase string
}

// Compute validates the inputs and returns the release identity.
//
// The rules are the ones a release is allowed to break:
//
//   - the base in build/config.yml must be X.Y.Z and must match the base of the
//     requested version, so a tag cannot ship a version other than the one the
//     build is rooted at;
//   - a stable release has no pre-release part;
//   - a beta version is exactly X.Y.Z-beta.N with the config base;
//   - a nightly version is derived from the base and the UTC date and run id of
//     the run, and its base must be strictly higher than the last published
//     stable base, which is why a stable release is followed by raising the
//     config base;
//   - numeric components must fit the Windows file-version fields.
func Compute(options ReleaseOptions) (Release, error) {
	if !options.Channel.Valid() {
		return Release{}, invalidf("unknown release channel %q", options.Channel)
	}

	base, err := Parse(options.Base)
	if err != nil {
		return Release{}, invalidf("base version: %v", err)
	}
	if base.IsPrerelease() {
		return Release{}, invalidf("base version %q must be X.Y.Z without a pre-release part", options.Base)
	}
	if base.BuildMetadata() != "" {
		return Release{}, invalidf("base version %q must not carry build metadata", options.Base)
	}

	release := Release{
		Channel:   options.Channel,
		Base:      base.Base(),
		SourceSHA: strings.TrimSpace(options.SourceSHA),
	}

	switch options.Channel {
	case ChannelStable:
		version := base
		if strings.TrimSpace(options.Version) != "" {
			parsed, err := ParseRelease(options.Version)
			if err != nil {
				return Release{}, invalidf("%v", err)
			}
			if parsed.IsPrerelease() {
				return Release{}, invalidf("stable release %q must not carry a pre-release part", options.Version)
			}
			if !SameBase(parsed, base) {
				return Release{}, invalidf("stable release %q does not match the config base %q", options.Version, base.String())
			}
			version = parsed
		}
		release.Version = version

	case ChannelBeta:
		trimmed := strings.TrimSpace(options.Version)
		if trimmed == "" {
			return Release{}, invalidf("a beta release needs an explicit version such as v%s-beta.1", base.Base())
		}
		version, err := ParseRelease(trimmed)
		if err != nil {
			return Release{}, invalidf("%v", err)
		}
		if version.Stage() != StageBeta {
			return Release{}, invalidf("beta release %q must be X.Y.Z-beta.N", trimmed)
		}
		if !SameBase(version, base) {
			return Release{}, invalidf("beta release %q does not match the config base %q", trimmed, base.Base())
		}
		release.Version = version

	case ChannelNightly:
		date, err := normaliseDate(options.Date)
		if err != nil {
			return Release{}, err
		}
		runID := strings.TrimSpace(options.RunID)
		if !runIDPattern.MatchString(runID) {
			return Release{}, invalidf("nightly run id %q must be the decimal workflow run id", options.RunID)
		}
		if strings.TrimSpace(options.Version) != "" {
			return Release{}, invalidf("a nightly version is derived from the base, date and run id; dropping the explicit version %q", options.Version)
		}
		if previous := strings.TrimSpace(options.LastStableVersion); previous != "" {
			lastStable, err := Parse(previous)
			if err != nil {
				return Release{}, invalidf("last stable version: %v", err)
			}
			if lastStable.IsPrerelease() {
				return Release{}, invalidf("last stable version %q must be a plain X.Y.Z release", previous)
			}
			if !BaseSupersedes(base, lastStable) {
				return Release{}, invalidf("nightly base %s must be higher than the last published stable base %s; raise info.version in build/config.yml",
					base.Base(), lastStable.Base())
			}
			release.LastStableBase = lastStable.Base()
		}
		release.Version = MustParse(fmt.Sprintf("%s-nightly.%s.%s", base.Base(), date, runID))
		release.Date = date
		release.RunID = runID
	}

	fileVersion, err := release.Version.WindowsFileVersion()
	if err != nil {
		return Release{}, invalidf("%v", err)
	}
	release.FileVersion = fileVersion
	return release, nil
}

// normaliseDate accepts the YYYY-MM-DD form a workflow exposes and the
// YYYYMMDD form a version carries, and returns the UTC calendar date in the
// YYYYMMDD form. It rejects a date that is not a real calendar day.
func normaliseDate(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", invalidf("a nightly release needs the UTC date of the workflow run as YYYY-MM-DD")
	}
	compact := strings.ReplaceAll(trimmed, "-", "")
	if len(compact) != 8 {
		return "", invalidf("nightly date %q must be YYYY-MM-DD in UTC", value)
	}
	parsed, err := time.ParseInLocation(time.DateOnly, compact[:4]+"-"+compact[4:6]+"-"+compact[6:], time.UTC)
	if err != nil || parsed.Format("20060102") != compact {
		return "", invalidf("nightly date %q is not a real UTC calendar date", value)
	}
	return compact, nil
}

// invalidf builds a rejection that InputError recognises.
func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errInvalidInput, fmt.Sprintf(format, args...))
}
