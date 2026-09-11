package dshadapter

import (
	"errors"
	"github.com/local/dsh-work/internal/workspacecontext"
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeModeLaunchOmitsUserDataAndExtensionPatches(t *testing.T) {
	root := t.TempDir()
	a := New(nil, SupportedVersion)
	a.SetUserDataDirectory(filepath.Join(root, "original-data"))
	a.SetLaunchPatch(func(string) (string, error) { return "", errors.New("broken host extension") })
	plan, err := a.BuildLaunchPlan(LaunchContext{SafeMode: true, GenerationID: "safe-generation", Runtime: Runtime{Path: filepath.Join(root, "dsh.cmd"), Version: SupportedVersion}, BootstrapDirectory: filepath.Join(root, "bootstrap"), DataDirectory: filepath.Join(root, "rescue"), Profile: "web", Workspace: workspacecontext.Context{GenerationID: "safe-generation", State: workspacecontext.StateSelectionRequired}, HostPatch: "host.patch.json"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.Join(plan.Args, " "), "--patch") != 1 || plan.Args[1] != "host.patch.json" || plan.Env["DSH_HOME"] != filepath.Join(root, "rescue") {
		t.Fatalf("unsafe launch = %#v", plan)
	}
}
