//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/platform/automationpackages"
)

// AutomationPreparer installs only the portable Browser package collection.
// Computer backends retain their unavailable Linux contract.
type AutomationPreparer struct{ packages *automationpackages.Installer }

func NewAutomationPreparer(root string) *AutomationPreparer {
	return &AutomationPreparer{packages: &automationpackages.Installer{
		Root: root, Executor: NewCommandExecutor(NewSystemdAdapter()),
		Replacer: FileReplacer{}, NPMCommand: "npm",
	}}
}
func (p *AutomationPreparer) ConfigureToolchain(resolve func(context.Context) (dshmanager.ResolvedNode, error)) {
	p.packages.Resolve = resolve
}
func (p *AutomationPreparer) Ready(c automation.Capability) bool {
	return c == automation.Browser && p.packages.Ready(c)
}
func (p *AutomationPreparer) Prepare(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	return p.prepare(ctx, c, report, false)
}
func (p *AutomationPreparer) Repair(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	return p.prepare(ctx, c, report, true)
}
func (p *AutomationPreparer) prepare(ctx context.Context, c automation.Capability, report func(automation.Progress), force bool) error {
	if c != automation.Browser {
		return automation.ErrUnavailable
	}
	return p.packages.Prepare(ctx, c, report, force)
}
func (p *AutomationPreparer) Inventory() []automation.Component {
	row := automation.Component{ID: "browser-mcp", Name: "Playwright MCP", Capability: automation.Browser,
		SupportedVersion: automation.MCPVersion, Adapter: "@modelcontextprotocol/sdk " + automation.SDKVersion,
		BackendAvailable: true, State: "missing", Location: automation.PackageRoot(p.packages.Root, automation.Browser)}
	if directory, err := automation.InstalledPackage(p.packages.Root, automation.Browser); err == nil {
		row.Location = directory
		data, err := os.ReadFile(filepath.Join(directory, "node_modules", "@playwright", "mcp", "package.json"))
		var manifest struct{ Version string }
		if err == nil && json.Unmarshal(data, &manifest) == nil {
			row.InstalledVersion = manifest.Version
		}
	}
	if row.InstalledVersion != "" {
		row.State = "installed"
		if row.InstalledVersion != row.SupportedVersion {
			row.State = "update"
		} else if !p.Ready(automation.Browser) {
			row.State = "damaged"
		}
	}
	return []automation.Component{row}
}
