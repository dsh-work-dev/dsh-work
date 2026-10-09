package dshadapter

import (
	"github.com/local/dsh-work/internal/workspacecontext"
	"path/filepath"
	"strings"
	"testing"
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
		CoreOverlay: true, UserDataOverlay: false, GenerationID: "safe-generation",
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
