package release

import (
	"strings"
	"testing"
)

func TestParseAcceptsReleaseShapes(t *testing.T) {
	tests := []struct {
		input   string
		base    string
		stage   Stage
		channel Channel
		date    string
		runID   string
		beta    uint64
		build   string
	}{
		{input: "0.3.0", base: "0.3.0", stage: StageNone, channel: ChannelStable},
		{input: "v0.3.0", base: "0.3.0", stage: StageNone, channel: ChannelStable},
		{input: "12.34.56", base: "12.34.56", stage: StageNone, channel: ChannelStable},
		{input: "0.3.0-beta.2", base: "0.3.0", stage: StageBeta, channel: ChannelBeta, beta: 2},
		{input: "0.3.0-beta.10", base: "0.3.0", stage: StageBeta, channel: ChannelBeta, beta: 10},
		{input: "0.3.0-nightly.20261013.123456789", base: "0.3.0", stage: StageNightly, channel: ChannelNightly, date: "20261013", runID: "123456789"},
		{input: "0.1.0-nightly.20261013.1", base: "0.1.0", stage: StageNightly, channel: ChannelNightly, date: "20261013", runID: "1"},
		{input: "0.3.0+build.7", base: "0.3.0", stage: StageNone, channel: ChannelStable, build: "build.7"},
	}

	for _, test := range tests {
		version, err := Parse(test.input)
		if err != nil {
			t.Fatalf("Parse(%q) returned error: %v", test.input, err)
		}
		if got := version.Base(); got != test.base {
			t.Errorf("Parse(%q).Base() = %q, want %q", test.input, got, test.base)
		}
		if got := version.Stage(); got != test.stage {
			t.Errorf("Parse(%q).Stage() = %q, want %q", test.input, got, test.stage)
		}
		if got := version.Channel(); got != test.channel {
			t.Errorf("Parse(%q).Channel() = %q, want %q", test.input, got, test.channel)
		}
		if got := version.NightlyDate(); got != test.date {
			t.Errorf("Parse(%q).NightlyDate() = %q, want %q", test.input, got, test.date)
		}
		if got := version.NightlyRunID(); got != test.runID {
			t.Errorf("Parse(%q).NightlyRunID() = %q, want %q", test.input, got, test.runID)
		}
		if got := version.BetaNumber(); got != test.beta {
			t.Errorf("Parse(%q).BetaNumber() = %d, want %d", test.input, got, test.beta)
		}
		if got := version.BuildMetadata(); got != test.build {
			t.Errorf("Parse(%q).BuildMetadata() = %q, want %q", test.input, got, test.build)
		}
	}
}

func TestParseRejectsInvalidVersions(t *testing.T) {
	inputs := []string{
		"",
		"   ",
		"1",
		"1.2",
		"1.2.3.4",
		"01.2.3",
		"1.02.3",
		"1.2.03",
		"1.2.x",
		"1.2.3-",
		"1.2.3-01",
		"1.2.3-alpha",
		"1.2.3-rc.1",
		"1.2.3-nightly",
		"1.2.3-nightly.20261013",
		"1.2.3-nightly.2026101.7",
		"1.2.3-nightly.20261013.0012",
		"1.2.3-nightly.20261013.abc",
		"1.2.3-beta",
		"1.2.3-beta.1.2",
		"1.2.3-beta.0x1",
		"1.2.3+build..7",
		"1.2.3+build!",
	}
	for _, input := range inputs {
		if version, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) = %q, want an error", input, version.String())
		}
	}
}

func TestParseReleaseRejectsBuildMetadata(t *testing.T) {
	// Precedence ignores build metadata, but a release identity does not get to
	// carry it: the build SHA is recorded next to the version instead.
	for _, input := range []string{"0.3.0+build.7", "0.3.0-nightly.20261013.12+meta"} {
		if version, err := ParseRelease(input); err == nil {
			t.Errorf("ParseRelease(%q) = %q, want an error", input, version.String())
		}
	}
	for _, input := range []string{"0.3.0", "0.3.0-beta.2", "0.3.0-nightly.20261013.123456789"} {
		if _, err := ParseRelease(input); err != nil {
			t.Errorf("ParseRelease(%q) returned error: %v", input, err)
		}
	}
}

func TestParseRejectsOutOfRangeNumbers(t *testing.T) {
	huge := strings.Repeat("9", 20)
	for _, input := range []string{huge + ".1.1", "1." + huge + ".1", "1.1.1-beta." + huge} {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) accepted an out-of-range numeric identifier", input)
		}
	}
}

func TestCompare(t *testing.T) {
	// SemVer precedence, in the specification's order. Note that for one base
	// version a beta build sorts *below* a nightly build, because "beta" <
	// "nightly" in ASCII order; the channels are still distinct, and the
	// intended channel order is release.Channels, not Compare.
	ordered := []string{
		"0.1.0-beta.1",
		"0.1.0-beta.2",
		"0.1.0-beta.10",
		"0.1.0-nightly.20261012.1",
		"0.1.0-nightly.20261013.1",
		"0.1.0-nightly.20261013.2",
		"0.1.0-nightly.20261013.10",
		"0.1.0-nightly.20261014.1",
		"0.1.0",
		"0.2.0-beta.1",
		"0.2.0-nightly.20261013.1",
		"0.2.0",
		"0.10.0",
		"1.0.0",
	}
	for i := 0; i < len(ordered)-1; i++ {
		lower := MustParse(ordered[i])
		higher := MustParse(ordered[i+1])
		if !IsNewer(higher, lower) {
			t.Errorf("IsNewer(%q, %q) = false, want true", ordered[i+1], ordered[i])
		}
		if IsNewer(lower, higher) {
			t.Errorf("IsNewer(%q, %q) = true, want false", ordered[i], ordered[i+1])
		}
	}
}

func TestCompareCrossChannelSameBase(t *testing.T) {
	// The nightly of a base is newer than a beta of the same base under SemVer
	// precedence, even though beta is published before nightly. Neither may be
	// newer than the other's channel; routing always uses Channel.
	beta := MustParse("0.3.0-beta.2")
	nightly := MustParse("0.3.0-nightly.20261013.123456789")
	stable := MustParse("0.3.0")

	if !IsNewer(nightly, beta) {
		t.Fatalf("IsNewer(%q, %q) = false, want true for SemVer precedence", nightly.String(), beta.String())
	}
	if beta.Channel() != ChannelBeta || nightly.Channel() != ChannelNightly || stable.Channel() != ChannelStable {
		t.Fatalf("channel must come from the version shape, not from precedence")
	}
	if !IsNewer(stable, nightly) || !IsNewer(stable, beta) {
		t.Fatalf("the stable release of a base must outrank its pre-releases")
	}
}

func TestCompareNightlySameDayRunIDsAscend(t *testing.T) {
	// Two different runs on the same UTC day must produce ascending SemVer
	// versions, whatever the digit count, and the same run must reproduce the
	// identical version.
	first := MustParse("0.3.0-nightly.20261013.999999999")
	second := MustParse("0.3.0-nightly.20261013.1000000000")
	if !IsNewer(second, first) {
		t.Fatalf("a longer run id must win: %q vs %q", second.String(), first.String())
	}
	if !Equal(MustParse("0.3.0-nightly.20261013.1000000000"), second) {
		t.Fatalf("the same date and run id must reproduce the same version")
	}
}

func TestCompareIgnoresBuildMetadata(t *testing.T) {
	withMetadata := MustParse("0.3.0+build.7")
	without := MustParse("0.3.0")
	if !Equal(withMetadata, without) {
		t.Fatalf("build metadata changed precedence: %q vs %q", withMetadata.String(), without.String())
	}
}

func TestCompareNightlyRunIDAcrossDigitCounts(t *testing.T) {
	lower := MustParse("0.3.0-nightly.20261013.999999999")
	higher := MustParse("0.3.0-nightly.20261013.1000000000")
	if !IsNewer(higher, lower) {
		t.Fatalf("a longer run id must win: %q vs %q", higher.String(), lower.String())
	}
}

func TestChannelIsNotDerivedFromPrecedence(t *testing.T) {
	// Within one base version SemVer places nightly above beta and stable above
	// both, which is not the order the channels are published in. A beta build
	// must still belong to the beta channel, a nightly to the nightly channel,
	// and switching to a channel must never be decided by Compare.
	beta := MustParse("0.3.0-beta.9")
	nightly := MustParse("0.3.0-nightly.20261013.1")
	stable := MustParse("0.3.0")

	if beta.Channel() != ChannelBeta {
		t.Fatalf("beta channel = %q", beta.Channel())
	}
	if nightly.Channel() != ChannelNightly {
		t.Fatalf("nightly channel = %q", nightly.Channel())
	}
	if stable.Channel() != ChannelStable {
		t.Fatalf("stable channel = %q", stable.Channel())
	}
	if !IsNewer(nightly, beta) || !IsNewer(stable, nightly) {
		t.Fatalf("unexpected SemVer precedence for one base: nightly must outrank beta, stable must outrank both")
	}
}

func TestParseChannel(t *testing.T) {
	tests := map[string]Channel{"": ChannelStable, "stable": ChannelStable, "beta": ChannelBeta, "nightly": ChannelNightly, " nightly ": ChannelNightly}
	for input, want := range tests {
		got, err := ParseChannel(input)
		if err != nil {
			t.Fatalf("ParseChannel(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Errorf("ParseChannel(%q) = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"canary", "Stable", "night"} {
		if _, err := ParseChannel(input); err == nil {
			t.Errorf("ParseChannel(%q) accepted an unknown channel", input)
		}
	}
}

func TestValidateBuildVersion(t *testing.T) {
	for _, input := range []string{"", "development", "0.3.0", "0.3.0-beta.1", "0.3.0-nightly.20261013.7"} {
		if err := ValidateBuildVersion(input); err != nil {
			t.Errorf("ValidateBuildVersion(%q) returned error: %v", input, err)
		}
	}
	for _, input := range []string{"dev", "1.2", "0.3.0-nightly.20261013"} {
		if err := ValidateBuildVersion(input); err == nil {
			t.Errorf("ValidateBuildVersion(%q) accepted an invalid build version", input)
		}
	}
}

func TestWindowsFileVersion(t *testing.T) {
	tests := map[string]string{
		"0.3.0":                            "0.3.0.0",
		"0.3.0-beta.2":                     "0.3.0.0",
		"0.3.0-nightly.20261013.123456789": "0.3.0.0",
		"65535.65535.65535":                "65535.65535.65535.0",
	}
	for input, want := range tests {
		got, err := MustParse(input).WindowsFileVersion()
		if err != nil {
			t.Fatalf("WindowsFileVersion(%q) returned error: %v", input, err)
		}
		if got != want {
			t.Errorf("WindowsFileVersion(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWindowsFileVersionRejectsOverflow(t *testing.T) {
	if _, err := MustParse("65536.0.0").WindowsFileVersion(); err == nil {
		t.Fatal("65536.0.0 must not map onto a 16-bit file version field")
	}
	if _, err := MustParse("0.0.70000").WindowsFileVersion(); err == nil {
		t.Fatal("0.0.70000 must not map onto a 16-bit file version field")
	}
}

func TestBaseWindowsFileVersion(t *testing.T) {
	got, err := BaseWindowsFileVersion("0.3.0")
	if err != nil {
		t.Fatalf("BaseWindowsFileVersion returned error: %v", err)
	}
	if got != "0.3.0.0" {
		t.Fatalf("BaseWindowsFileVersion(0.3.0) = %q, want 0.3.0.0", got)
	}
	if _, err := BaseWindowsFileVersion("0.3.0-beta.1"); err == nil {
		t.Fatal("a prerelease base must be rejected")
	}
}
