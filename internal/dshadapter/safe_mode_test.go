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
	plan, err := a.BuildLaunchPlan(LaunchContext{SafeMode: true, GenerationID: "safe-generation", Runtime: Runtime{Path: filepath.Join(root, "dsh.cmd"), Version: SupportedVersion}, BootstrapDirectory: filepath.Join(root, "bootstrap"), DataDirectory: filepath.Join(root, "rescue"), Profile: "web", Workspace: workspacecontext.Context{GenerationID: "safe-generation", State: workspacecontext.StateSelectionRequired}, Port: 3080})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(plan.Args, " "), "--patch") || plan.Env["DSH_HOME"] != filepath.Join(root, "rescue") {
		t.Fatalf("unsafe launch = %#v", plan)
	}
}
