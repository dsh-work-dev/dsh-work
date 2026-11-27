//go:build darwin && arm64

package dshmanager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/automation"
)

func TestMacPreviewRestoreRetainsEarlierUnavailableEngineMetadata(t *testing.T) {
	current := automation.CurrentVersionInputs()
	earlierLock, err := os.ReadFile(filepath.Join("..", "automation", "assets", "computer-auv", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	earlier := current
	earlier.AUVLock = string(earlierLock)
	earlier.DriverArchive, earlier.DriverSHA256 = automation.DriverArchive, automation.DriverSHA256
	if earlier == current || earlier.Validate() != nil {
		t.Fatal("earlier preview record was not recognized as a supported host input")
	}

	expected := &storedRestorePoint{RestorePoint: RestorePoint{DSHVersion: "0.2.2-rc.1", NodeVersion: "24.2.0"},
		Input: ProfileVersionInput{Lock: "recorded profile lock", Automation: &earlier}}
	actual := *expected
	actual.Input.Automation = &current
	finishVersionPoint(expected)
	finishVersionPoint(&actual)
	if expected.Digest == actual.Digest || !matchesRestoredVersions(expected, &actual) {
		t.Fatal("earlier Mac preview record was not compared in its captured shape")
	}

	actual.Input.Lock = "changed profile lock"
	finishVersionPoint(&actual)
	if matchesRestoredVersions(expected, &actual) {
		t.Fatal("preview host compatibility hid a changed profile lock")
	}
	earlier.BrowserLock = "unrecognized browser lock"
	if earlier.Validate() == nil {
		t.Fatal("preview host compatibility accepted unrelated dependency changes")
	}
}
