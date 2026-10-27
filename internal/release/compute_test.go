package release

import (
	"strings"
	"testing"
)

func TestComputeStable(t *testing.T) {
	release, err := Compute(ReleaseOptions{Channel: ChannelStable, Base: "0.3.0"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if release.Version.String() != "0.3.0" {
		t.Errorf("Version = %q, want 0.3.0", release.Version.String())
	}
	if release.Base != "0.3.0" {
		t.Errorf("Base = %q, want 0.3.0", release.Base)
	}
	if release.Channel != ChannelStable {
		t.Errorf("Channel = %q, want stable", release.Channel)
	}
	if release.FileVersion != "0.3.0.0" {
		t.Errorf("FileVersion = %q, want 0.3.0.0", release.FileVersion)
	}
	if release.Date != "" || release.RunID != "" {
		t.Errorf("a stable release must not carry nightly identity: date=%q runID=%q", release.Date, release.RunID)
	}
}

func TestComputeStableAcceptsMatchingExplicitVersion(t *testing.T) {
	release, err := Compute(ReleaseOptions{Channel: ChannelStable, Base: "0.3.0", Version: "v0.3.0"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if release.Version.String() != "0.3.0" {
		t.Errorf("Version = %q, want 0.3.0", release.Version.String())
	}
}

func TestComputeBeta(t *testing.T) {
	release, err := Compute(ReleaseOptions{Channel: ChannelBeta, Base: "0.3.0", Version: "v0.3.0-beta.2"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if release.Version.String() != "0.3.0-beta.2" {
		t.Errorf("Version = %q, want 0.3.0-beta.2", release.Version.String())
	}
	if release.Version.BetaNumber() != 2 {
		t.Errorf("BetaNumber = %d, want 2", release.Version.BetaNumber())
	}
	if release.FileVersion != "0.3.0.0" {
		t.Errorf("FileVersion = %q, want 0.3.0.0", release.FileVersion)
	}
	if release.Version.Channel() != ChannelBeta {
		t.Errorf("version channel = %q, want beta", release.Version.Channel())
	}
}

func TestComputeNightly(t *testing.T) {
	release, err := Compute(ReleaseOptions{
		Channel:   ChannelNightly,
		Base:      "0.3.0",
		Date:      "2026-10-13",
		RunID:     "123456789",
		SourceSHA: "0123456789abcdef0123456789abcdef01234567",
	})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if release.Version.String() != "0.3.0-nightly.20261013.123456789" {
		t.Errorf("Version = %q, want 0.3.0-nightly.20261013.123456789", release.Version.String())
	}
	if release.Date != "20261013" {
		t.Errorf("Date = %q, want 20261013", release.Date)
	}
	if release.RunID != "123456789" {
		t.Errorf("RunID = %q, want 123456789", release.RunID)
	}
	if release.SourceSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("SourceSHA = %q, want the build SHA", release.SourceSHA)
	}
	if strings.Contains(release.Version.String(), release.SourceSHA) {
		t.Errorf("the source SHA must not leak into the version: %q", release.Version.String())
	}
	if release.FileVersion != "0.3.0.0" {
		t.Errorf("FileVersion = %q, want 0.3.0.0", release.FileVersion)
	}
}

func TestComputeNightlyAcceptsCompactDate(t *testing.T) {
	release, err := Compute(ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "20261013", RunID: "7"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if release.Version.String() != "0.3.0-nightly.20261013.7" {
		t.Errorf("Version = %q, want 0.3.0-nightly.20261013.7", release.Version.String())
	}
}

func TestComputeNightlyRerunIsIdentical(t *testing.T) {
	// run_attempt is not an input: retrying the same workflow run must
	// reproduce the same identity byte for byte.
	options := ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10-13", RunID: "123456789"}
	first, err := Compute(options)
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	second, err := Compute(options)
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if first.Version.String() != second.Version.String() {
		t.Fatalf("a retry changed the version: %q vs %q", first.Version.String(), second.Version.String())
	}
}

func TestComputeNightlyRunIDsAscendOnOneDay(t *testing.T) {
	options := ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10-13"}
	first, err := Compute(ReleaseOptions{Channel: options.Channel, Base: options.Base, Date: options.Date, RunID: "999999999"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	second, err := Compute(ReleaseOptions{Channel: options.Channel, Base: options.Base, Date: options.Date, RunID: "1000000000"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if !IsNewer(second.Version, first.Version) {
		t.Fatalf("a later run on the same day must be newer: %q vs %q", second.Version.String(), first.Version.String())
	}
}

func TestComputeNightlyBaseFollowsLastStable(t *testing.T) {
	// A config base that is already published as stable moves to the next
	// patch, so a stable release needs no immediate config change.
	for _, base := range []string{"0.3.0", "0.2.0"} {
		release, err := Compute(ReleaseOptions{
			Channel: ChannelNightly, Base: base, Date: "2026-10-13", RunID: "1", LastStableVersion: "0.3.0",
		})
		if err != nil {
			t.Fatalf("base %s: Compute returned error: %v", base, err)
		}
		if release.Version.String() != "0.3.1-nightly.20261013.1" || release.Base != "0.3.1" {
			t.Errorf("base %s: version = %q, base = %q, want 0.3.1-nightly.20261013.1 on 0.3.1", base, release.Version.String(), release.Base)
		}
		if !IsNewer(release.Version, MustParse("0.3.0")) {
			t.Errorf("base %s: %q must sort above the published stable 0.3.0", base, release.Version.String())
		}
	}
	// A config base raised past the stable line wins.
	release, err := Compute(ReleaseOptions{
		Channel: ChannelNightly, Base: "0.4.0", Date: "2026-10-13", RunID: "1", LastStableVersion: "0.3.0",
	})
	if err != nil {
		t.Fatalf("a nightly above the published stable base must be accepted: %v", err)
	}
	if release.Version.String() != "0.4.0-nightly.20261013.1" {
		t.Errorf("version = %q, want the config base 0.4.0", release.Version.String())
	}
	if release.LastStableBase != "0.3.0" {
		t.Errorf("LastStableBase = %q, want 0.3.0", release.LastStableBase)
	}
}

func TestComputeRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		options ReleaseOptions
	}{
		{name: "unknown channel", options: ReleaseOptions{Channel: "canary", Base: "0.3.0"}},
		{name: "empty base", options: ReleaseOptions{Channel: ChannelStable}},
		{name: "base with prerelease", options: ReleaseOptions{Channel: ChannelStable, Base: "0.3.0-beta.1"}},
		{name: "base with leading zero", options: ReleaseOptions{Channel: ChannelStable, Base: "01.3.0"}},
		{name: "base with build metadata", options: ReleaseOptions{Channel: ChannelStable, Base: "0.3.0+meta"}},
		{name: "stable with prerelease version", options: ReleaseOptions{Channel: ChannelStable, Base: "0.3.0", Version: "0.3.0-beta.1"}},
		{name: "stable version on another base", options: ReleaseOptions{Channel: ChannelStable, Base: "0.3.0", Version: "0.4.0"}},
		{name: "beta without version", options: ReleaseOptions{Channel: ChannelBeta, Base: "0.3.0"}},
		{name: "beta with stable version", options: ReleaseOptions{Channel: ChannelBeta, Base: "0.3.0", Version: "0.3.0"}},
		{name: "beta on another base", options: ReleaseOptions{Channel: ChannelBeta, Base: "0.3.0", Version: "0.4.0-beta.1"}},
		{name: "beta with leading zero", options: ReleaseOptions{Channel: ChannelBeta, Base: "0.3.0", Version: "0.3.0-beta.01"}},
		{name: "beta with build metadata", options: ReleaseOptions{Channel: ChannelBeta, Base: "0.3.0", Version: "0.3.0-beta.1+sha"}},
		{name: "nightly without date", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", RunID: "1"}},
		{name: "nightly with impossible date", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-02-30", RunID: "1"}},
		{name: "nightly with short date", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10", RunID: "1"}},
		{name: "nightly without run id", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10-13"}},
		{name: "nightly with zero run id", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10-13", RunID: "0"}},
		{name: "nightly with non-numeric run id", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10-13", RunID: "run-7"}},
		{name: "nightly with explicit version", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.3.0", Date: "2026-10-13", RunID: "1", Version: "0.3.0-nightly.20261013.1"}},
		{name: "last stable with prerelease", options: ReleaseOptions{Channel: ChannelNightly, Base: "0.4.0", Date: "2026-10-13", RunID: "1", LastStableVersion: "0.3.0-beta.1"}},
		{name: "numeric field overflow", options: ReleaseOptions{Channel: ChannelStable, Base: "65536.0.0"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, err := Compute(test.options)
			if err == nil {
				t.Fatalf("Compute accepted %+v and produced %q", test.options, release.Version.String())
			}
			if !InputError(err) {
				t.Errorf("Compute error is not an input error: %v", err)
			}
		})
	}
}

func TestComputeStableAcceptsMaximumFields(t *testing.T) {
	release, err := Compute(ReleaseOptions{Channel: ChannelStable, Base: "65535.65535.65535"})
	if err != nil {
		t.Fatalf("Compute returned error: %v", err)
	}
	if release.FileVersion != "65535.65535.65535.0" {
		t.Errorf("FileVersion = %q, want 65535.65535.65535.0", release.FileVersion)
	}
}
