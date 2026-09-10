//go:build windows

package windows

import (
	"context"
	"errors"
	"testing"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

type toolchainProbeExecutor struct {
	pnpmFails   bool
	nodeVersion string
	calls       []string
}

func (e *toolchainProbeExecutor) Run(_ context.Context, executable string, args []string, _ map[string]string, _ string) (dshadapter.CommandResult, error) {
	e.calls = append(e.calls, executable)
	if len(args) == 2 && args[0] == "-p" && args[1] == "process.execPath" {
		return dshadapter.CommandResult{Stdout: `C:\selected-node\node.exe`}, nil
	}
	switch executable {
	case "node.exe":
		version := e.nodeVersion
		if version == "" {
			version = "v24.20.0"
		}
		return dshadapter.CommandResult{Stdout: version}, nil
	case "pnpm.cmd":
		if e.pnpmFails {
			return dshadapter.CommandResult{}, errors.New("pnpm unavailable")
		}
		return dshadapter.CommandResult{Stdout: "11.19.0"}, nil
	case "npm.cmd":
		return dshadapter.CommandResult{Stdout: "11.19.0"}, nil
	default:
		return dshadapter.CommandResult{}, errors.New("unexpected probe")
	}
}

func toolchainLookup(name string) (string, error) {
	paths := map[string]string{
		"node": "node.exe",
		"pnpm": "pnpm.cmd",
		"npm":  "npm.cmd",
	}
	path, ok := paths[name]
	if !ok {
		return "", errors.New("not found")
	}
	return path, nil
}

func TestSystemToolchainResolverPrefersPnpm(t *testing.T) {
	executor := &toolchainProbeExecutor{}
	resolver := systemToolchainResolver{executor: executor, lookup: toolchainLookup}
	toolchain, err := resolver.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if toolchain.kind != dshmanager.RuntimeToolchainSystemPNPM || toolchain.packageManagerPath != "pnpm.cmd" {
		t.Fatalf("resolved toolchain = %#v", toolchain)
	}
	if len(executor.calls) != 2 {
		t.Fatalf("probe calls = %#v", executor.calls)
	}
}

func TestSystemToolchainResolverFallsBackToNpmWhenPnpmProbeFails(t *testing.T) {
	executor := &toolchainProbeExecutor{pnpmFails: true}
	resolver := systemToolchainResolver{executor: executor, lookup: toolchainLookup}
	toolchain, err := resolver.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if toolchain.kind != dshmanager.RuntimeToolchainSystemNPM || toolchain.packageManagerPath != "npm.cmd" {
		t.Fatalf("resolved toolchain = %#v", toolchain)
	}
	if len(executor.calls) != 3 {
		t.Fatalf("probe calls = %#v", executor.calls)
	}
}

func TestSystemToolchainResolverDoesNotFilterNodeMajorVersions(t *testing.T) {
	executor := &toolchainProbeExecutor{nodeVersion: "v23.0.0"}
	resolver := systemToolchainResolver{executor: executor, lookup: toolchainLookup}
	toolchain, err := resolver.Resolve(context.Background(), nil)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if toolchain.version != "23.0.0" || toolchain.kind != dshmanager.RuntimeToolchainSystemPNPM {
		t.Fatalf("resolved toolchain = %#v", toolchain)
	}
}

func TestRunNodeResolverDoesNotInvokePackageManagerDuringLaunchResolution(t *testing.T) {
	executor := &toolchainProbeExecutor{nodeVersion: "v18.3.0"}
	resolver := RunNodeResolver{executor: executor, lookup: toolchainLookup}
	resolved, err := resolver.Resolve(context.Background(), dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Version != "18.3.0" || resolved.NodePath != `C:\selected-node\node.exe` || len(executor.calls) != 2 || executor.calls[0] != "node.exe" {
		t.Fatalf("launch resolution = %#v calls=%#v", resolved, executor.calls)
	}
}
