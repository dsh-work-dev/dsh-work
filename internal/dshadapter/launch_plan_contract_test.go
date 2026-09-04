package dshadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/workspacecontext"
)

func TestBuildLaunchPlanRequiresAnExplicitBootstrapDirectory(t *testing.T) {
	adapter := New(nil, SupportedVersion)
	_, err := adapter.BuildLaunchPlan(LaunchContext{
		GenerationID:  "generation",
		Runtime:       Runtime{Path: `C:\tools\dsh.cmd`, Version: SupportedVersion},
		DataDirectory: `C:\Users\you\AppData\Local\dsh-work\dsh`,
		Profile:       "web",
		Workspace:     workspacecontext.Context{GenerationID: "generation", State: workspacecontext.StateSelectionRequired},
		Port:          4567,
	})
	if err == nil || !strings.Contains(err.Error(), "bootstrap directory") {
		t.Fatalf("BuildLaunchPlan() error = %v, want explicit bootstrap-directory failure", err)
	}
}

func TestBuildLaunchPlanCarriesAnExplicitWorkspaceContext(t *testing.T) {
	adapter := New(nil, SupportedVersion)
	bootstrap := t.TempDir()
	workspacePath := filepath.Join(t.TempDir(), "project")
	if err := os.Mkdir(workspacePath, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace, err := workspacecontext.NewSelected("generation", "workspace-1", workspacePath, "Project")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := adapter.BuildLaunchPlan(LaunchContext{
		GenerationID:       "generation",
		Runtime:            Runtime{Path: `C:\tools\dsh.cmd`, Version: SupportedVersion},
		BootstrapDirectory: bootstrap,
		DataDirectory:      filepath.Join(t.TempDir(), "dsh-data"),
		Profile:            "web",
		Workspace:          workspace,
		Port:               4567,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorkingDirectory != workspacePath {
		t.Fatalf("working directory = %q, want selected Workspace path %q", plan.WorkingDirectory, workspacePath)
	}
}
