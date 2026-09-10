package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

type nodeCheckingPluginRunner struct {
	t         *testing.T
	path      string
	home      string
	calls     int
	failFirst bool
}

func (r *nodeCheckingPluginRunner) Run(_ context.Context, _ string, args []string, env map[string]string, _ string) (CommandResult, error) {
	r.calls++
	if env["PATH"] != r.path || env["DSH_HOME"] != r.home {
		r.t.Errorf("plugin command %v environment = %v; want selected Node PATH %q and home %q", args, env, r.path, r.home)
	}
	if r.failFirst && r.calls == 1 {
		return CommandResult{}, errors.New("ECONNRESET")
	}
	return CommandResult{}, nil
}

func TestPluginOperationsUseCurrentNode(t *testing.T) {
	for _, operation := range []string{"add", "update", "remove", "local", "private", "list", "mirror", "unavailable"} {
		t.Run(operation, func(t *testing.T) {
			_, node := nodeFixture(t)
			root := t.TempDir()
			home := filepath.Join(root, "home")
			profilePath := filepath.Join(home, "profiles", "alpha")
			if err := os.MkdirAll(profilePath, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(`{"dependencies":{"example":"1.0.0"}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if operation == "private" {
				if err := os.WriteFile(filepath.Join(profilePath, ".npmrc"), []byte("registry=https://private.example/\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			runtimePath := filepath.Join(root, "dsh.cmd")
			if err := os.WriteFile(runtimePath, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Dir(node.NodePath)
			runner := &nodeCheckingPluginRunner{t: t, path: path, home: home, failFirst: operation == "mirror"}
			verifier := &recordingRuntimeVerifier{}
			manager, err := New(Config{
				StatePath: filepath.Join(root, "manager.json"), Nodes: []NodeInstallationInfo{node},
				NodeResolver:    &nodeResolverFake{resolved: ResolvedNode{ChildEnvironment: map[string]string{"PATH": path}}},
				Runtimes:        []RuntimeInfo{{ID: "dsh", Version: "1.2.3", Path: runtimePath, ToolchainPath: filepath.Join(root, "old-node")}},
				DataDirectories: []DataDirectoryInfo{{ID: "home", Path: home, Ownership: DataDirectoryOwnershipDSHWork}},
				RuntimeVerifier: verifier, CommandRunner: runner, PluginCommands: testPluginCommands{},
			})
			if err != nil {
				t.Fatal(err)
			}
			current := RunContext{RuntimeID: "dsh", Node: NodeSelection{Kind: NodeSelectionManaged, InstallationID: node.ID}, Profile: ProfileRef{DataDirectoryID: "home", Name: "alpha"}}
			if _, err := manager.CommitCurrent(context.Background(), &current); err != nil {
				t.Fatal(err)
			}
			target := PluginTarget{Profile: current.Profile}
			if operation == "unavailable" {
				manager.config.NodeResolver = nil
				if err := os.Remove(node.NodePath); err != nil {
					t.Fatal(err)
				}
			}
			switch operation {
			case "list":
				_, err = manager.ListPlugins(context.Background(), PluginListRequest{Target: target})
			case "remove":
				_, err = manager.RemovePlugin(context.Background(), PluginRemoveRequest{Target: target, Package: "example"})
			case "update":
				_, err = manager.UpgradePlugin(context.Background(), PluginUpgradeRequest{Target: target, Package: "example"})
			default:
				pkg := "example"
				if operation == "local" {
					pkg = "file:./plugin"
				}
				_, err = manager.InstallPlugin(context.Background(), PluginInstallRequest{Target: target, Package: pkg})
			}
			if operation == "unavailable" {
				assertFailureCode(t, err, lifecycle.ErrorRuntimeInstallUnavailable)
				if runner.calls != 0 {
					t.Fatal("plugin mutation ran with unavailable selected Node")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if operation == "mirror" && runner.calls != 2 {
				t.Fatalf("mirror command count = %d", runner.calls)
			}
			if runner.calls == 0 {
				t.Fatal("no plugin command executed")
			}
			if verifier.env["PATH"] != path {
				t.Errorf("runtime verified with PATH %q, want %q", verifier.env["PATH"], path)
			}
		})
	}
}
