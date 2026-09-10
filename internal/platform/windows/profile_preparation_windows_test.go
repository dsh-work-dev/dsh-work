//go:build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

type preparationCommandRunner struct{ CommandExecutor }

func (r preparationCommandRunner) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshmanager.CommandResult, error) {
	result, err := r.CommandExecutor.Run(ctx, executable, args, env, dir)
	return dshmanager.CommandResult{Stdout: result.Stdout, Stderr: result.Stderr}, err
}

func TestPrepareFirstUseProfileStartsWithValidWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "new-home")
	runtime := filepath.Join(root, "dsh.cmd")
	script := "@echo off\r\n@if not \"%1\"==\"plugin\" exit /b 1\r\n@if not \"%4\"==\"install\" exit /b 2\r\n@mkdir \"%DSH_HOME%\\profiles\\web\"\r\n@echo {}>\"%DSH_HOME%\\profiles\\web\\package.json\"\r\n"
	if err := os.WriteFile(runtime, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	manager, err := dshmanager.New(dshmanager.Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []dshmanager.DataDirectoryInfo{{ID: "home", Path: home, Ownership: dshmanager.DataDirectoryOwnershipDSHWork}},
		Runtimes:        []dshmanager.RuntimeInfo{{ID: "dsh", Version: "1.0.0", Path: runtime}},
		CommandRunner:   preparationCommandRunner{}, PluginCommands: dshadapter.NewPluginCommands(),
		ProfileCatalog: dshadapter.New(nil, dshadapter.SupportedVersion),
	})
	if err != nil {
		t.Fatal(err)
	}
	launch, err := manager.ResolveLaunch(context.Background(), dshmanager.LaunchRequest{RuntimeID: "dsh", Profile: dshmanager.ProfileRef{DataDirectoryID: "home", Name: "web"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("read-only resolution created home: %v", err)
	}
	ctx := dshadapter.WithCommandOutput(context.Background(), func(line string) { t.Log(line) })
	if err := manager.PrepareRunContext(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "profiles", "web", "package.json")); err != nil {
		t.Fatal(err)
	}
}
