package dshmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

// profileManifestWithBundles is a DSH profile manifest as DSH writes it, with
// an extra key the disable must not disturb.
const profileManifestWithBundles = `{
  "name": "dsh-profile-web",
  "private": true,
  "dependencies": {
    "@acme/widget": "1.0.0",
    "@acme/archive": "2.0.0",
    "left-pad": "1.3.0"
  },
  "dsh": {
    "profile": {
      "bundles": [
        "@deepseek-ai/dsh-web-app",
        "@acme/widget",
        "@acme/archive"
      ],
      "patchReload": true
    }
  }
}
`

func newDisableTestManager(t *testing.T) (*Manager, ResolvedLaunch, *recordingRunner, string) {
	t.Helper()
	root := t.TempDir()
	homePath := filepath.Join(root, "dsh-home")
	profilePath := filepath.Join(homePath, "profiles", "web")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(profileManifestWithBundles), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDirectory := DataDirectoryInfo{ID: "dsh-work", Name: "dsh-work", Path: homePath, Ownership: DataDirectoryOwnershipDSHWork}
	runtime := RuntimeInfo{ID: "dsh-test", Version: "0.1.2-alpha.3", Path: runtimePath}
	runner := &recordingRunner{}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{dataDirectory},
		CommandRunner:   runner,
		PluginCommands:  testPluginCommands{},
		Runtimes:        []RuntimeInfo{runtime},
	})
	if err != nil {
		t.Fatal(err)
	}
	launch := ResolvedLaunch{
		Target:        RunContext{RuntimeID: runtime.ID, Profile: ProfileRef{DataDirectoryID: dataDirectory.ID, Name: "web"}},
		DataDirectory: dataDirectory,
		Runtime:       runtime,
	}
	return manager, launch, runner, profilePath
}

func readBundles(t *testing.T, profilePath string) []string {
	t.Helper()
	manifest, err := readProfileBundleManifest(profilePath)
	if err != nil {
		t.Fatal(err)
	}
	return manifest.Bundles()
}

func TestDisablePluginRemovesOnlyItsBundleAndRecordsIt(t *testing.T) {
	manager, launch, _, profilePath := newDisableTestManager(t)
	result, err := manager.ApplyPluginDisabled(context.Background(), launch, "@acme/widget", true)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(readBundles(t, profilePath), ","); got != "@deepseek-ai/dsh-web-app,@acme/archive" {
		t.Fatalf("bundles = %s", got)
	}
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("manifest is not JSON: %v", err)
	}
	if !strings.Contains(string(data), `"patchReload": true`) || !strings.Contains(string(data), `"@acme/widget": "1.0.0"`) {
		t.Fatalf("disable changed unrelated manifest content:\n%s", data)
	}
	disabled := map[string]bool{}
	for _, plugin := range result.Plugins {
		disabled[plugin.Package] = plugin.Disabled
	}
	if !disabled["@acme/widget"] || disabled["@acme/archive"] {
		t.Fatalf("plugin listing = %#v", result.Plugins)
	}
	state, err := (FileStateStore{}).Load(context.Background(), manager.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || len(state.PluginDisables) != 1 || state.PluginDisables[0].Package != "@acme/widget" || state.PluginDisables[0].BundleIndex != 1 {
		t.Fatalf("persisted disables = %#v", state)
	}
}

func TestEnablePluginRestoresItsLayerPosition(t *testing.T) {
	manager, launch, _, profilePath := newDisableTestManager(t)
	if _, err := manager.ApplyPluginDisabled(context.Background(), launch, "@acme/widget", true); err != nil {
		t.Fatal(err)
	}
	result, err := manager.ApplyPluginDisabled(context.Background(), launch, "@acme/widget", false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != profileManifestWithBundles {
		t.Fatalf("disable then enable did not restore the manifest:\n%s", data)
	}
	for _, plugin := range result.Plugins {
		if plugin.Disabled {
			t.Fatalf("plugin %s is still reported disabled", plugin.Package)
		}
	}
	if len(manager.pluginDisablesFor(launch.Target.Profile)) != 0 {
		t.Fatal("enable left the disable record behind")
	}
}

func TestEnforcePluginDisablesUndoesDSHReconciliation(t *testing.T) {
	manager, launch, _, profilePath := newDisableTestManager(t)
	if _, err := manager.ApplyPluginDisabled(context.Background(), launch, "@acme/archive", true); err != nil {
		t.Fatal(err)
	}
	// Any `dsh plugin` command re-adds every installed plugin layer.
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(profileManifestWithBundles), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.EnforcePluginDisables(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(readBundles(t, profilePath), ","); got != "@deepseek-ai/dsh-web-app,@acme/widget" {
		t.Fatalf("bundles after enforcement = %s", got)
	}
}

func TestDisablePluginRefusesCoreMissingAndPlainPackages(t *testing.T) {
	manager, launch, _, profilePath := newDisableTestManager(t)
	for name, testCase := range map[string]struct {
		pkg  string
		code lifecycle.ErrorCode
	}{
		"core package":       {pkg: "@deepseek-ai/dsh-web-app", code: lifecycle.ErrorPluginProtected},
		"not installed":      {pkg: "@acme/missing", code: lifecycle.ErrorPluginNotInstalled},
		"plain dependency":   {pkg: "left-pad", code: lifecycle.ErrorPluginDisableFailed},
		"path-like package":  {pkg: "../escape", code: lifecycle.ErrorPluginSpecInvalid},
		"option-like string": {pkg: "--registry", code: lifecycle.ErrorPluginSpecInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := manager.ApplyPluginDisabled(context.Background(), launch, testCase.pkg, true)
			assertFailureCode(t, err, testCase.code)
		})
	}
	if got := strings.Join(readBundles(t, profilePath), ","); got != "@deepseek-ai/dsh-web-app,@acme/widget,@acme/archive" {
		t.Fatalf("refused disables changed bundles: %s", got)
	}
}

func TestApplyPluginDisabledRefusesWhileARunContextIsCurrent(t *testing.T) {
	manager, launch, _, profilePath := newDisableTestManager(t)
	current := launch.Target
	if _, err := manager.CommitCurrent(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ApplyPluginDisabled(context.Background(), launch, "@acme/widget", true); err == nil {
		t.Fatal("disable succeeded while a Run context was current")
	}
	if got := len(readBundles(t, profilePath)); got != 3 {
		t.Fatalf("bundles changed while current: %d", got)
	}
}

func TestRemovingADisabledPluginForgetsItsRecord(t *testing.T) {
	manager, launch, runner, _ := newDisableTestManager(t)
	if _, err := manager.ApplyPluginDisabled(context.Background(), launch, "@acme/widget", true); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ApplyPlugin(context.Background(), launch, "@acme/widget", "remove"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.args, " "); got != "plugin --profile web remove @acme/widget" {
		t.Fatalf("remove args = %q", got)
	}
	if len(manager.pluginDisablesFor(launch.Target.Profile)) != 0 {
		t.Fatal("removed plugin is still recorded as disabled")
	}
}

func TestPluginDisableRecordsAreValidated(t *testing.T) {
	valid := PluginDisableRecord{Profile: ProfileRef{DataDirectoryID: "dsh-work", Name: "web"}, Package: "@acme/widget", BundleIndex: 1, DisabledAt: "2026-09-18T00:00:00Z"}
	if err := validatePluginDisableRecords([]PluginDisableRecord{valid}); err != nil {
		t.Fatal(err)
	}
	core := valid
	core.Package = "@deepseek-ai/dsh-web-app"
	negative := valid
	negative.BundleIndex = -1
	for name, records := range map[string][]PluginDisableRecord{
		"duplicate":      {valid, valid},
		"core package":   {core},
		"negative index": {negative},
	} {
		if err := validatePluginDisableRecords(records); err == nil {
			t.Errorf("%s: invalid records were accepted", name)
		}
	}
}
