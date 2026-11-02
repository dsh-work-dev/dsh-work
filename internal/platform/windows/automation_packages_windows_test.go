//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"os"
	"path/filepath"
	"testing"
)

type automationInstallCommand struct {
	t         *testing.T
	node, cli string
	fail      bool
	calls     int
}

func (f *automationInstallCommand) Run(_ context.Context, command string, args []string, env map[string]string, dir string) (dshadapter.CommandResult, error) {
	f.calls++
	if command != f.node || len(args) < 2 || args[0] != f.cli || args[1] != "ci" || env["PATH"] != "selected-node-only" {
		f.t.Fatalf("did not use selected toolchain: %s %v %v", command, args, env)
	}
	if f.fail {
		return dshadapter.CommandResult{}, errors.New("simulated download failure")
	}
	for name, version := range map[string]string{"@playwright/mcp": automation.MCPVersion, "@modelcontextprotocol/sdk": automation.SDKVersion} {
		p := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		os.MkdirAll(p, 0700)
		b, _ := json.Marshal(map[string]string{"version": version})
		os.WriteFile(filepath.Join(p, "package.json"), b, 0600)
	}
	return dshadapter.CommandResult{}, nil
}
func TestAutomationUsesSelectedNodeAndKeepsPublishedSetsOnRepairFailure(t *testing.T) {
	root := t.TempDir()
	node := filepath.Join(root, "managed", "node.exe")
	cli := filepath.Join(filepath.Dir(node), "node_modules", "npm", "bin", "npm-cli.js")
	os.MkdirAll(filepath.Dir(cli), 0700)
	os.WriteFile(cli, []byte("fixture"), 0600)
	f := &automationInstallCommand{t: t, node: node, cli: cli}
	p := NewAutomationPreparer(filepath.Join(root, "cache"))
	p.executor = f
	p.ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error) {
		return dshmanager.ResolvedNode{NodePath: node, ChildEnvironment: map[string]string{"PATH": "selected-node-only"}}, nil
	})
	report := func(automation.Progress) {}
	if e := p.Prepare(context.Background(), automation.Browser, report); e != nil {
		t.Fatal(e)
	}
	first, e := automation.PublishedPackage(p.root, automation.Browser)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Repair(context.Background(), automation.Browser, report); e != nil {
		t.Fatal(e)
	}
	second, _ := automation.PublishedPackage(p.root, automation.Browser)
	if first == second {
		t.Fatal("repair modified old published directory")
	}
	if _, e = os.Stat(first); e != nil {
		t.Fatal("old set lost", e)
	}
	f.fail = true
	if e = p.Repair(context.Background(), automation.Browser, report); e == nil {
		t.Fatal("failed download published")
	}
	after, _ := automation.PublishedPackage(p.root, automation.Browser)
	if after != second {
		t.Fatal("failure replaced current set")
	}
	calls := f.calls
	p.ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error) {
		return dshmanager.ResolvedNode{}, errors.New("offline/no toolchain")
	})
	if e = p.Prepare(context.Background(), automation.Browser, report); e != nil || calls != f.calls {
		t.Fatal("valid offline set not reused", e)
	}
}
