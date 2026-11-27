//go:build darwin && cgo

package mac

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/platform/automationpackages"
)

type AutomationPreparer struct{ packages automationpackages.Installer }

func NewAutomationPreparer(root string) *AutomationPreparer {
	return &AutomationPreparer{packages: automationpackages.Installer{
		Root: root, Executor: macAutomationNPM{}, Replacer: FileReplacer{},
		NPMCommand: "npm", Stage: stageMacAutomationPackage, Verify: verifyMacAutomationPackage,
	}}
}

func (p *AutomationPreparer) ConfigureToolchain(resolve func(context.Context) (dshmanager.ResolvedNode, error)) {
	p.packages.Resolve = resolve
}

func (p *AutomationPreparer) Ready(c automation.Capability) bool {
	if !macAutomationPackageSupported(c) || !p.packages.Ready(c) {
		return false
	}
	if c != automation.Computer {
		return true
	}
	root, err := automation.PublishedPackage(p.packages.Root, c)
	return err == nil && macCuaPackageInstalled(root)
}

func macAutomationPackageSupported(c automation.Capability) bool {
	return c == automation.Browser || runtime.GOARCH == "arm64" && (c == automation.AUV || c == automation.Computer)
}

func (p *AutomationPreparer) Prepare(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	if !macAutomationPackageSupported(c) {
		return automation.ErrUnavailable
	}
	return p.packages.Prepare(ctx, c, report, false)
}

func (p *AutomationPreparer) Repair(ctx context.Context, c automation.Capability, report func(automation.Progress)) error {
	if !macAutomationPackageSupported(c) {
		return automation.ErrUnavailable
	}
	return p.packages.Prepare(ctx, c, report, true)
}

func (p *AutomationPreparer) Inventory() []automation.Component {
	rows := []automation.Component{
		{ID: "browser-mcp", Name: "Playwright MCP", Capability: automation.Browser,
			SupportedVersion: automation.MCPVersion, Adapter: "@modelcontextprotocol/sdk " + automation.SDKVersion, BackendAvailable: true},
	}
	if runtime.GOARCH == "arm64" {
		rows = append(rows,
			automation.Component{ID: "cua-driver", Name: "Cua Driver", Capability: automation.Computer,
				SupportedVersion: automation.DriverVersion, Adapter: "@modelcontextprotocol/client 2.2.0"},
			automation.Component{ID: "auv", Name: "AUV", Capability: automation.AUV,
				SupportedVersion: automation.AUVVersion, Adapter: "@auv-js/sdk " + automation.AUVVersion, BackendAvailable: true})
	}
	for n := range rows {
		row := &rows[n]
		row.State = "missing"
		row.Location = automation.PackageRoot(p.packages.Root, row.Capability)
		if directory, err := automation.PublishedPackage(p.packages.Root, row.Capability); err == nil {
			row.Location = directory
			if p.Ready(row.Capability) {
				row.InstalledVersion = row.SupportedVersion
				row.State = "installed"
			} else {
				row.State = "damaged"
			}
		}
	}
	return rows
}

type macAutomationNPM struct{}

func (macAutomationNPM) Run(ctx context.Context, executable string, args []string, env map[string]string, directory string) (dshadapter.CommandResult, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(directory) {
		return dshadapter.CommandResult{}, errors.New("absolute npm executable and installation directory required")
	}
	lifecycle := []string{"ci", "--ignore-scripts", "--no-audit", "--no-fund"}
	invocation := args
	if len(args) == len(lifecycle)+1 && filepath.IsAbs(args[0]) && filepath.Base(args[0]) == "npm-cli.js" {
		invocation = args[1:]
	}
	if !slices.Equal(invocation, lifecycle) {
		return dshadapter.CommandResult{}, errors.New("unrecognized locked npm installation")
	}
	nodeBin, _, _ := strings.Cut(env["PATH"], string(filepath.ListSeparator))
	if !filepath.IsAbs(nodeBin) {
		nodeBin = filepath.Dir(executable)
	}
	return runMacNPM(ctx, executable, append(append([]string{}, args...), "--registry", "https://registry.npmjs.org/"), directory, nodeBin)
}

func verifyMacAutomationPackage(ctx context.Context, root string, c automation.Capability) error {
	if c == automation.Computer {
		return verifyMacCuaPackage(ctx, root)
	}
	if c != automation.AUV {
		return nil
	}
	driver, err := automation.AUVExecutable(root)
	if err != nil {
		return err
	}
	// Use the designated identity of the official pinned CLI as well as the
	// archive integrity verified by npm ci.
	bounded, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := exec.CommandContext(bounded, "/usr/bin/codesign", "--verify", "--strict", "-R="+automation.MacAUVCodeRequirement, driver).Run(); err != nil {
		return errors.Join(errors.New("Mac AUV publisher signature rejected"), err)
	}
	return nil
}
