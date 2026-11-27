//go:build linux

package linux

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshmanager"
)

func TestAutomationPreparerKeepsComputerUnavailable(t *testing.T) {
	p := NewAutomationPreparer(t.TempDir())
	for _, c := range []automation.Capability{automation.Computer, automation.AUV} {
		if p.Ready(c) || !errors.Is(p.Prepare(context.Background(), c, func(automation.Progress) {}), automation.ErrUnavailable) || !errors.Is(p.Repair(context.Background(), c, func(automation.Progress) {}), automation.ErrUnavailable) {
			t.Fatalf("non-Browser capability admitted: %s", c)
		}
	}
	rows := p.Inventory()
	if len(rows) != 1 || rows[0].Capability != automation.Browser || rows[0].State != "missing" {
		t.Fatal("incorrect component inventory", rows)
	}
}

// Runs the actual product preparer through the qualified systemd command owner,
// using official locked npm input and a fresh application artifact root.
func TestLiveLinuxBrowserPreparation(t *testing.T) {
	if os.Getenv("DSH_WORK_BROWSER_PREPARE_TEST") != "1" {
		t.Skip("explicit official Browser package installation")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	p := NewAutomationPreparer(t.TempDir())
	resolver := NewSystemNodeResolver(NewCommandExecutor(NewSystemdAdapter()))
	p.ConfigureToolchain(func(ctx context.Context) (dshmanager.ResolvedNode, error) {
		return resolver.Resolve(ctx, dshmanager.NodeSelection{Kind: dshmanager.NodeSelectionSystem}, nil)
	})
	if err := p.Prepare(ctx, automation.Browser, func(automation.Progress) {}); err != nil {
		t.Fatal(err)
	}
	first, err := automation.PublishedPackage(p.packages.Root, automation.Browser)
	if err != nil || !p.Ready(automation.Browser) {
		t.Fatal("official publication not prepared", first, err)
	}
	t.Logf("official Browser prepared: MCP=%s SDK=%s ws=%s", automation.MCPVersion, automation.SDKVersion, automation.BrowserWebSocketVersion)
	p.ConfigureToolchain(func(context.Context) (dshmanager.ResolvedNode, error) {
		return dshmanager.ResolvedNode{}, errors.New("controlled unavailable toolchain")
	})
	if err := p.Prepare(ctx, automation.Browser, func(automation.Progress) {}); err != nil {
		t.Fatal("prepared publication was not reusable offline", err)
	}
	if err := p.Repair(ctx, automation.Browser, func(automation.Progress) {}); err == nil {
		t.Fatal("failed toolchain repair falsely succeeded")
	}
	after, err := automation.PublishedPackage(p.packages.Root, automation.Browser)
	if err != nil || after != first || !p.Ready(automation.Browser) {
		t.Fatal("failed repair changed the prepared publication", after, err)
	}
}
