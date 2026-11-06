//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"golang.org/x/mod/semver"
)

func packageVersion(root, name string) string {
	if root == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(root, "node_modules", filepath.FromSlash(name), "package.json"))
	if err != nil {
		return ""
	}
	var p struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &p) != nil {
		return ""
	}
	return p.Version
}

func (i *AutomationPreparer) driverRoot() string {
	return filepath.Join(i.root, "automation", "cua-driver", automation.DriverVersion, "windows-amd64")
}

func (i *AutomationPreparer) driverReady() bool {
	root := i.driverRoot()
	b, err := os.ReadFile(filepath.Join(root, "release.json"))
	if err != nil || string(b) != automation.DriverSHA256 {
		return false
	}
	for _, name := range []string{"cua-driver.exe", "cua-driver-uia.exe", "cua-cursor-theme.exe"} {
		if info, err := os.Stat(filepath.Join(root, name)); err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// Inventory reads installed manifests and the executable's own version. The
// executable probe is cached by file modification time and never starts a service.
func (i *AutomationPreparer) Inventory() []automation.Component {
	i.inventoryMu.Lock()
	defer i.inventoryMu.Unlock()
	rows := []automation.Component{
		{ID: "browser-mcp", Name: "Playwright MCP", Capability: automation.Browser, SupportedVersion: automation.MCPVersion, Adapter: "@modelcontextprotocol/sdk " + automation.SDKVersion},
		{ID: "cua-driver", Name: "Cua Driver", Capability: automation.Computer, SupportedVersion: automation.DriverVersion, Adapter: "@modelcontextprotocol/client 2.2.0"},
		{ID: "auv", Name: "AUV", Capability: automation.AUV, SupportedVersion: automation.AUVVersion, Adapter: "@auv-js/sdk " + automation.AUVVersion},
	}
	for n := range rows {
		r := &rows[n]
		r.State = "missing"
		dir, err := automation.InstalledPackage(i.root, r.Capability)
		if err != nil {
			r.Location = automation.PackageRoot(i.root, r.Capability)
		} else {
			r.Location = dir
		}
		switch r.ID {
		case "browser-mcp":
			r.InstalledVersion = packageVersion(dir, "@playwright/mcp")
			r.BackendAvailable = true
		case "auv":
			r.BackendAvailable = true
			r.InstalledVersion = packageVersion(dir, "@auv-js/cli-win32-x64-msvc")
		case "cua-driver":
			r.BackendAvailable = true
			r.Location = i.driverRoot()
			binary := filepath.Join(r.Location, "cua-driver.exe")
			if _, err := os.Stat(binary); os.IsNotExist(err) {
				// An application upgrade can change the supported directory. Keep
				// the previous managed binary visible in read-only inventory.
				candidates, _ := filepath.Glob(filepath.Join(i.root, "automation", "cua-driver", "*", "windows-amd64", "cua-driver.exe"))
				best := ""
				for _, candidate := range candidates {
					version := filepath.Base(filepath.Dir(filepath.Dir(candidate)))
					if semver.IsValid("v"+version) && (best == "" || semver.Compare("v"+version, "v"+best) > 0) {
						best = version
						binary = candidate
					}
				}
				if best != "" {
					r.Location = filepath.Dir(binary)
				}
			}
			if info, e := os.Stat(binary); e == nil && !info.IsDir() {
				if binary != i.driverProbePath || !info.ModTime().Equal(i.driverProbeTime) {
					ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
					cmd := exec.CommandContext(ctx, binary, "--version")
					cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
					out, err := cmd.Output()
					cancel()
					i.driverProbeVersion = ""
					if err == nil {
						for _, field := range strings.Fields(string(out)) {
							field = strings.TrimPrefix(field, "v")
							if semver.IsValid("v" + field) {
								i.driverProbeVersion = field
								break
							}
						}
					}
					i.driverProbeTime = info.ModTime()
					i.driverProbePath = binary
				}
				r.InstalledVersion = i.driverProbeVersion
			}
		}
		if r.InstalledVersion != "" {
			r.State = "installed"
			if r.InstalledVersion != r.SupportedVersion {
				r.State = "update"
			}
		}
		if r.InstalledVersion == r.SupportedVersion && !i.Ready(r.Capability) {
			r.State = "damaged"
		}
	}
	return rows
}
