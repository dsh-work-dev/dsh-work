//go:build darwin && arm64 && cgo && e67nativefixture

package mac

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/platform/automationpackages"
)

func TestLiveMacLockedAutomationPublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	preparer := NewAutomationPreparer(root)
	preparer.ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error) {
		return dshmanager.ResolvedNode{NodePath: node, NPMPath: npm, ChildEnvironment: macNodeEnvironment(filepath.Dir(node))}, nil
	})
	var lastStep string
	report := func(p automation.Progress) {
		if p.Step != lastStep {
			t.Log(p.Step)
			lastStep = p.Step
		}
	}
	pathFor := func(capability automation.Capability) string {
		t.Helper()
		path, err := automation.PublishedPackage(root, capability)
		if err != nil {
			t.Fatal(err)
		}
		return path
	}

	if err := preparer.Prepare(ctx, automation.Browser, report); err != nil {
		t.Fatalf("install locked browser component: %v", errors.Unwrap(err))
	}
	browser := pathFor(automation.Browser)
	if !preparer.Ready(automation.Browser) {
		t.Fatal("browser component was not recognized after installation")
	}
	if err := preparer.Repair(ctx, automation.Browser, report); err != nil {
		t.Fatalf("repair locked browser component: %v", errors.Unwrap(err))
	}
	if pathFor(automation.Browser) == browser {
		t.Fatal("same-version repair reused a published tree")
	}
	if err := automationpackages.Validate(browser, automation.Browser); err != nil {
		t.Fatal(err)
	}

	if err := preparer.Prepare(ctx, automation.Computer, report); err != nil {
		t.Fatalf("install locked Cua Driver component: %v", errors.Unwrap(err))
	}
	cua := pathFor(automation.Computer)
	cuaExecutable := filepath.Join(cua, macCuaAppName, "Contents", "MacOS", "cua-driver")
	versionCommand := exec.CommandContext(ctx, cuaExecutable, "--version")
	versionCommand.Dir = t.TempDir()
	if output, err := versionCommand.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "cua-driver "+automation.DriverVersion {
		t.Fatalf("published Cua Driver version: %s %v", output, err)
	}

	if err := preparer.Prepare(ctx, automation.AUV, report); err != nil {
		t.Fatalf("install locked AUV component: %v", errors.Unwrap(err))
	}
	auv := pathFor(automation.AUV)
	rows := preparer.Inventory()
	if !preparer.Ready(automation.Computer) || !preparer.Ready(automation.AUV) || len(rows) != 3 ||
		rows[1].InstalledVersion != automation.DriverVersion || rows[1].BackendAvailable ||
		rows[2].InstalledVersion != automation.AUVVersion || !rows[2].BackendAvailable {
		t.Fatalf("published computer engine inventory: %+v", rows)
	}
	driver, err := automation.AUVExecutable(auv)
	if err != nil {
		t.Fatal(err)
	}
	source, err := filepath.Abs("../../automation/computerruntime/auv.mjs")
	if err != nil {
		t.Fatal(err)
	}
	const probe = `
		import { pathToFileURL } from 'node:url';
		const [source, dependencies, driver] = process.argv.slice(1);
		const { createAuvBackend } = await import(pathToFileURL(source));
		const backend = await createAuvBackend({dependencies, driver});
		await backend.close();
	`
	child := exec.CommandContext(ctx, node, "--input-type=module", "--eval", probe, source, auv, driver)
	child.Dir = t.TempDir()
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("launch and close published AUV: %v %s", err, output)
	}

	// A staged verification failure must leave the complete revision selected.
	preparer.packages.Verify = func(context.Context, string, automation.Capability) error {
		return errors.New("fixture publisher refusal")
	}
	if err := preparer.Repair(ctx, automation.AUV, report); automation.FailureCode(err) != "verify" {
		t.Fatalf("publisher refusal was not reported: %v", err)
	}
	if pathFor(automation.AUV) != auv {
		t.Fatal("failed repair changed the AUV publication")
	}
}
