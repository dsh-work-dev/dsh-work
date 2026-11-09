//go:build darwin && cgo

package mac

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/platform/automationpackages"
)

// FixedBrowserPreparer consumes the already-published application component.
// Package acquisition is outside the restricted preview's execution boundary.
type FixedBrowserPreparer struct{ Root string }

func (p FixedBrowserPreparer) verify(c automation.Capability) error {
	if c != automation.Browser {
		return automation.ErrUnavailable
	}
	directory, err := automation.PublishedPackage(p.Root, c)
	if err != nil {
		return errors.New("The browser component must be installed before starting this Mac preview.")
	}
	return automationpackages.Validate(directory, c)
}
func (p FixedBrowserPreparer) Ready(c automation.Capability) bool { return p.verify(c) == nil }
func (p FixedBrowserPreparer) Prepare(ctx context.Context, c automation.Capability, _ func(automation.Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return p.verify(c)
}
func (p FixedBrowserPreparer) Inventory() []automation.Component {
	row := automation.Component{ID: "browser-mcp", Name: "Playwright MCP", Capability: automation.Browser, SupportedVersion: automation.MCPVersion, Adapter: "@modelcontextprotocol/sdk " + automation.SDKVersion, BackendAvailable: true, State: "missing", Location: automation.PackageRoot(p.Root, automation.Browser)}
	if directory, err := automation.PublishedPackage(p.Root, automation.Browser); err == nil {
		row.Location = directory
		if p.Ready(automation.Browser) {
			row.InstalledVersion = automation.MCPVersion
			row.State = "installed"
		} else {
			row.State = "damaged"
		}
	}
	return []automation.Component{row}
}
