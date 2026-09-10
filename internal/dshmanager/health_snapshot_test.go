package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthySnapshotSurvivesReloadAndDependencyMutation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	profile := filepath.Join(home, "profiles", "alpha")
	runtimePath := filepath.Join(root, "installation", "node_modules", ".bin", "dsh.cmd")
	nodePath := filepath.Join(root, "node", "node.exe")
	write := func(path, value string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path, want string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
		}
	}
	write(runtimePath, "healthy dsh")
	write(filepath.Join(root, "installation", "node_modules", "dependency", "native.node"), "healthy runtime native dependency")
	write(nodePath, "healthy node")
	write(filepath.Join(profile, "package.json"), `{"dependencies":{"plugin":"1.0.0"}}`)
	write(filepath.Join(profile, "node_modules", "plugin", "native.node"), "healthy plugin native dependency")
	write(filepath.Join(home, "settings.yaml"), "healthy settings")
	target := RunContext{RuntimeID: "dsh", Node: NodeSelection{Kind: NodeSelectionSystem}, Profile: ProfileRef{DataDirectoryID: "home", Name: "alpha"}}
	config := Config{StatePath: filepath.Join(root, "manager.json"), Runtimes: []RuntimeInfo{{ID: "dsh", Path: runtimePath, Version: "1.2.3"}}, DataDirectories: []DataDirectoryInfo{{ID: "home", Path: home, Ownership: DataDirectoryOwnershipDSHWork}}, DefaultRunContext: target}
	manager, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	launch, err := manager.ResolveLaunch(ctx, LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		t.Fatal(err)
	}
	launch.Node.NodePath = nodePath
	if _, err := manager.CommitHealthy(ctx, launch); err != nil {
		t.Fatal(err)
	}
	write(runtimePath, "broken dsh")
	write(nodePath, "changed system node")
	write(filepath.Join(profile, "node_modules", "plugin", "native.node"), "broken plugin")
	write(filepath.Join(profile, "node_modules", "bad", "index.js"), "candidate only")
	write(filepath.Join(home, "settings.yaml"), "broken settings")
	for _, name := range []string{"sessions/chat.jsonl", "storages/workspaces.json", "worktrees/project/file.txt", "attachments/upload.txt", "profiles/alpha/sessions/chat.jsonl"} {
		write(filepath.Join(home, name), "new user data")
	}
	reloaded, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reloaded.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Current != nil || snapshot.KnownGood == nil || *snapshot.KnownGood != target {
		t.Fatalf("reloaded state = %#v", snapshot)
	}
	restored, err := reloaded.RestoreHealthy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	read(restored.Runtime.Path, "healthy dsh")
	read(filepath.Join(filepath.Dir(filepath.Dir(restored.Runtime.Path)), "dependency", "native.node"), "healthy runtime native dependency")
	read(restored.Node.NodePath, "healthy node")
	read(filepath.Join(profile, "node_modules", "plugin", "native.node"), "healthy plugin native dependency")
	read(filepath.Join(home, "settings.yaml"), "healthy settings")
	if _, err := os.Stat(filepath.Join(profile, "node_modules", "bad")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("candidate dependency survived: %v", err)
	}
	for _, name := range []string{"sessions/chat.jsonl", "storages/workspaces.json", "worktrees/project/file.txt", "attachments/upload.txt", "profiles/alpha/sessions/chat.jsonl"} {
		read(filepath.Join(home, name), "new user data")
	}
	// Simulate a process exit part-way through restoration. The next Manager
	// replays the durable pending recovery before trying a normal launch.
	write(filepath.Join(profile, "package.json"), "partial restore")
	reloaded, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := reloaded.ResumeRecovery(ctx)
	if err != nil || resumed == nil {
		t.Fatalf("resume = %#v, %v", resumed, err)
	}
	read(filepath.Join(profile, "package.json"), `{"dependencies":{"plugin":"1.0.0"}}`)
	if _, err := reloaded.CommitHealthy(ctx, *resumed); err != nil {
		t.Fatal(err)
	}
	reloaded, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := reloaded.ResumeRecovery(ctx); err != nil || resumed != nil {
		t.Fatalf("committed restore still pending: %#v, %v", resumed, err)
	}
}

func TestHealthDependencyLinksDoNotAliasOriginal(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	packagePath := filepath.Join(source, ".pnpm", "plugin", "node_modules", "plugin")
	if err := os.MkdirAll(packagePath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "native.node"), []byte("healthy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(packagePath, filepath.Join(source, "plugin")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	destination := filepath.Join(root, "snapshot")
	if err := copyHealthTree(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packagePath, "native.node"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(destination, "plugin", "native.node"))
	if err != nil || string(data) != "healthy" {
		t.Fatalf("snapshot aliases original: %q, %v", data, err)
	}
}

func TestStartupCanCommitWithoutCopyingSnapshot(t *testing.T) {
	ctx := context.Background()
	manager := newTestManager(t)
	manager.config.DisableHealthSnapshots = true
	target := *manager.configured
	launch, err := manager.ResolveLaunch(ctx, LaunchRequest{RuntimeID: target.RuntimeID, Node: target.Node, Profile: target.Profile})
	if err != nil {
		t.Fatal(err)
	}
	// A nonexistent copy source must not prevent publishing a validated launch
	// while snapshot integration is disabled.
	launch.Node.NodePath = filepath.Join(t.TempDir(), "not-a-copy-source", "node.exe")
	if _, err := manager.CommitHealthy(ctx, launch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(manager.config.StatePath), "healthy-snapshots")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot directory created: %v", err)
	}
	reloaded, err := New(manager.config)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reloaded.Snapshot(ctx)
	if err != nil || snapshot.Configured == nil || *snapshot.Configured != launch.Target {
		t.Fatalf("selected environment not persisted: %#v, %v", snapshot.Configured, err)
	}
}
