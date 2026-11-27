//go:build darwin && cgo

package mac

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

func TestLiveMacProfileVersionApproval(t *testing.T) {
	if os.Getenv("DSH_WORK_TEST_LIVE_MAC_DSH") != "1" {
		t.Skip("requires the signed Mac CEF fixture and pinned DSH")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", root)
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
	t.Setenv("DSH_HOME", root)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	commands := dshadapter.NewPluginCommands()
	args, err := commands.AllowVersion("web", "is-number@7.0.0", "0.2.2-rc.1")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DSH_HOME": root}
	profile := filepath.Join(root, "profiles", "web")
	compatibility := filepath.Join(profile, "compatibility.json")
	for _, attempt := range []struct {
		args []string
		env  map[string]string
		dir  string
	}{
		{args[:len(args)-1], env, root},
		{args, nil, root},
		{args, env, t.TempDir()},
		{[]string{"plugin", "--profile", "web", "install", "--ignore-scripts=false"}, env, root},
	} {
		if _, err := runtime.Run(ctx, runtime.cli, attempt.args, attempt.env, attempt.dir); err == nil {
			t.Fatalf("unapproved invocation succeeded: %v", attempt.args)
		}
	}
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("rejected invocation changed the profile: %v", err)
	}
	result, err := runtime.Run(ctx, runtime.cli, args, env, root)
	if err != nil {
		t.Fatalf("approval failed: %v; stdout=%q, stderr=%q", err, result.Stdout, result.Stderr)
	}
	body, err := os.ReadFile(compatibility)
	if err != nil {
		t.Fatal(err)
	}
	var exemptions map[string][]string
	if err = json.Unmarshal(body, &exemptions); err != nil || len(exemptions) != 1 {
		t.Fatalf("unexpected approval record: %s (%v)", body, err)
	}
	if versions := exemptions["is-number@7.0.0"]; len(versions) != 1 || versions[0] != "0.2.2-rc.1" {
		t.Fatalf("incorrect approval scope: %s", body)
	}
	stale, err := commands.AllowVersion("web", "is-number@6.0.0", "0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	result, err = runtime.Run(ctx, runtime.cli, stale, env, root)
	if err == nil || !strings.Contains(result.Stderr, "Cannot approve DSH 0.2.1") {
		t.Fatalf("stale approval was not refused by the pinned DSH CLI: %v; stderr=%q", err, result.Stderr)
	}
	after, err := os.ReadFile(compatibility)
	if err != nil || string(after) != string(body) {
		t.Fatalf("refused approval changed the record: %s (%v)", after, err)
	}
}

func TestLiveManagedMacDSHInstall(t *testing.T) {
	if os.Getenv("DSH_WORK_TEST_LIVE_MAC_DSH") != "1" {
		t.Skip("requires the signed Mac CEF fixture, pinned DSH and npm registry access")
	}
	const want = "0.2.2-rc.1"
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", root)
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runtime, err := NewRuntime()
	if err != nil {
		t.Fatal(err)
	}
	installer := runtime.DSHInstaller(root).(macDSHInstaller)
	var candidate dshmanager.RuntimeInfo
	if fixture := os.Getenv("DSH_WORK_TEST_LOCAL_DSH_TREE"); fixture != "" {
		if !filepath.IsAbs(fixture) {
			t.Fatal("expected an absolute pinned DSH package tree")
		}
		stage := filepath.Join(root, "staged")
		if err = os.Mkdir(stage, 0700); err != nil {
			t.Fatal(err)
		}
		if err = exec.CommandContext(ctx, "/bin/cp", "-cR", filepath.Join(fixture, "node_modules"), stage).Run(); err != nil {
			t.Fatal(err)
		}
		id := "dsh-" + want
		original := filepath.Join(root, id)
		if err = installer.publish(ctx, runtime.node, want, stage, original); err != nil {
			t.Fatal(err)
		}
		candidate = dshmanager.RuntimeInfo{ID: id, Version: want, Source: dshmanager.RuntimeSourceManaged, Path: macDSHEntry(original)}
		// A restore publishes another physical tree and retains the original.
		revisions, err := macStoreDirectory(root, filepath.Join(id, "revisions"), true)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.Mkdir(stage, 0700); err != nil {
			t.Fatal(err)
		}
		if err = exec.CommandContext(ctx, "/bin/cp", "-cR", filepath.Join(fixture, "node_modules"), stage).Run(); err != nil {
			t.Fatal(err)
		}
		revision := filepath.Join(revisions, "generation-"+rand.Text())
		if err = installer.publish(ctx, runtime.node, want, stage, revision); err != nil {
			t.Fatal(err)
		}
		for _, directory := range []string{original, revision} {
			if _, err = runtime.dshForExecutable(macDSHEntry(directory)); err != nil {
				t.Fatal(err)
			}
		}
	} else {
		candidate, err = installer.Install(ctx, want)
		if err != nil {
			t.Fatal(err)
		}
	}
	result, err := runtime.Run(ctx, candidate.Path, []string{"--version"}, nil, root)
	if err != nil || dshadapter.ParseVersion(result.Stdout) != want {
		t.Fatalf("published DSH version=%q error=%v", strings.TrimSpace(result.Stdout), err)
	}
	if err = installer.Remove(ctx, candidate); err != nil {
		t.Fatal(err)
	}
}
