package dshadapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/workspacecontext"
)

func TestSafeModeLaunchRetainsCoreOverlayAndOmitsUserData(t *testing.T) {
	root := t.TempDir()
	a := New(nil, SupportedVersion)
	a.SetUserDataDirectory(filepath.Join(root, "original-data"))
	corePatch := filepath.Join(root, "core-plugins.patch.json")
	a.SetCoreOverlayPatch(func(generation string) (string, error) {
		if generation != "safe-generation" {
			t.Fatalf("core overlay prepared for generation %q", generation)
		}
		return corePatch, nil
	})
	plan, err := a.BuildLaunchPlan(LaunchContext{
		UserDataOverlay: false, GenerationID: "safe-generation",
		Runtime:            Runtime{Path: filepath.Join(root, "dsh.cmd"), Version: SupportedVersion},
		BootstrapDirectory: filepath.Join(root, "bootstrap"),
		DataDirectory:      filepath.Join(root, "rescue"), Profile: "web",
		Workspace: workspacecontext.Context{GenerationID: "safe-generation", State: workspacecontext.StateSelectionRequired},
		HostPatch: "host.patch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(plan.Args, " ")
	if strings.Count(args, "--patch") != 2 || !strings.Contains(args, corePatch) || !strings.Contains(args, "host.patch.json") || strings.Contains(args, "original-data") {
		t.Fatalf("safe-mode overlays = %#v", plan.Args)
	}
}

func TestSafeModeWithDataLaunchSharesOnlyConfiguredUserDataRoots(t *testing.T) {
	root := t.TempDir()
	sharedData := filepath.Join(root, "user-data")
	rescueHome := filepath.Join(root, "safe-mode", "session-1")
	a := New(nil, SupportedVersion)
	a.SetUserDataDirectory(sharedData)
	plan, err := a.BuildLaunchPlan(LaunchContext{
		UserDataOverlay: true, GenerationID: "safe-generation",
		Runtime:            Runtime{Path: filepath.Join(root, "dsh.cmd"), Version: SupportedVersion},
		BootstrapDirectory: filepath.Join(root, "bootstrap"),
		DataDirectory:      rescueHome, Profile: "web",
		Workspace: workspacecontext.Context{GenerationID: "safe-generation", State: workspacecontext.StateSelectionRequired},
		HostPatch: "host.patch.json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Env["DSH_HOME"] != rescueHome {
		t.Fatalf("safe DSH_HOME = %q, want isolated home %q", plan.Env["DSH_HOME"], rescueHome)
	}
	var patch string
	for _, arg := range plan.Args {
		if strings.HasSuffix(arg, "user-data.patch.json") {
			patch = arg
			break
		}
	}
	if patch == "" {
		t.Fatalf("user-data overlay patch not found in %#v", plan.Args)
	}
	data, err := os.ReadFile(patch)
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		ID     string            `json:"id"`
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"session-persistence-jsonl": filepath.Join(sharedData, "sessions"),
		"storage-json":              filepath.Join(sharedData, "storages"),
		"attachment-local":          sharedData,
		"settings":                  filepath.Join(sharedData, "settings.yaml"),
		"credentials":               filepath.Join(sharedData, ".credentials.yaml"),
	}
	if len(rows) != len(want) {
		t.Fatalf("user-data patch contains %d entries, want %d: %#v", len(rows), len(want), rows)
	}
	for _, row := range rows {
		path := row.Config["root"]
		if path == "" {
			path = row.Config["dshHome"]
		}
		if path == "" {
			path = row.Config["path"]
		}
		if want[row.ID] != path {
			t.Errorf("%s path = %q, want shared path %q", row.ID, path, want[row.ID])
		}
		if strings.Contains(path, rescueHome) {
			t.Errorf("%s path unexpectedly points inside temporary safe home: %q", row.ID, path)
		}
	}
}
