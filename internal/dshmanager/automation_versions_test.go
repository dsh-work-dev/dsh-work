package dshmanager

import (
	"context"
	"encoding/json"
	"github.com/local/dsh-work/internal/automation"
	"testing"
)

type automationVersionsAdapter struct {
	ProfileVersionAdapter
	PluginCommandBuilder
}

func (automationVersionsAdapter) CaptureVersions(context.Context, string) (ProfileVersionInput, error) {
	return ProfileVersionInput{Lock: "fixture-lock"}, nil
}
func TestAutomationVersionPointScopeAndLegacyDigest(t *testing.T) {
	m := newTestManager(t)
	m.config.PluginCommands = automationVersionsAdapter{}
	m.config.AutomationVersions = automation.CurrentVersionInputs
	launch := appProfileLaunch(m.config.DataDirectories[0])
	launch.Runtime.Version = "0.2.1-rc.5"
	point, e := m.readVersionPoint(context.Background(), launch)
	if e != nil {
		t.Fatal(e)
	}
	if point.Input.Automation == nil || point.Input.Automation.Validate() != nil {
		t.Fatal("automation versions absent from owned profile")
	}
	changed := *point
	input := *point.Input.Automation
	input.DriverSHA256 = "changed"
	changed.Input.Automation = &input
	finishVersionPoint(&changed)
	if changed.Digest == point.Digest {
		t.Fatal("automation versions excluded from digest")
	}
	data, e := json.Marshal(cloneVersionRecovery(&VersionRecoveryState{Points: []storedRestorePoint{*point}}))
	if e != nil || len(data) == 0 {
		t.Fatal("version inputs do not survive persistence", e)
	}
	launch.Target.Profile.Name = "web"
	legacy, e := m.readVersionPoint(context.Background(), launch)
	if e != nil || legacy.Input.Automation != nil {
		t.Fatal("automation metadata affected another profile", e)
	}
	// Absent automation fields retain the pre-existing wire/digest shape.
	data, _ = json.Marshal(legacy.Input)
	var raw map[string]any
	json.Unmarshal(data, &raw)
	if _, ok := raw["automation"]; ok {
		t.Fatal("nil automation changed old records")
	}
	launch.Target.Profile.Name = AppProfileName
	launch.DataDirectory.Ownership = DataDirectoryOwnershipUser
	p, e := m.readVersionPoint(context.Background(), launch)
	if e != nil || p.Input.Automation != nil {
		t.Fatal("user-owned directory received host dependencies", e)
	}
}

func TestLegacyRestoreComparisonDoesNotRequireNewHostMetadata(t *testing.T) {
	expected := &storedRestorePoint{RestorePoint: RestorePoint{DSHVersion: "0.2.1-rc.5", NodeVersion: "24.2.0"}, Input: ProfileVersionInput{Lock: "frozen"}}
	finishVersionPoint(expected)
	actual := *expected
	v := automation.CurrentVersionInputs()
	actual.Input.Automation = &v
	finishVersionPoint(&actual)
	if !matchesRestoredVersions(expected, &actual) {
		t.Fatal("legacy recovery required a newly introduced host field")
	}
	actual.Input.Lock = "different"
	finishVersionPoint(&actual)
	if matchesRestoredVersions(expected, &actual) {
		t.Fatal("legacy compatibility hid a real dependency change")
	}
}

func TestSetupOnlyHostRecoverySourceIsNotSilentlyUpgraded(t *testing.T) {
	input := automation.CurrentVersionInputs()
	if input.ExtensionSource == "not-integrated" {
		t.Fatal("formal extension source absent")
	}
	input.ExtensionSource = "not-integrated"
	if input.Validate() == nil {
		t.Fatal("step-3 recovery point silently changed to current extension contract")
	}
}
