//go:build darwin && cgo

package macowner

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/local/dsh-work/internal/supervisor"
)

const mcpVersion = "mcp:0.0.83/sdk:1.32.2/ws:8.22.1"

//go:embed mcp_bootstrap.mjs
var mcpBootstrap []byte

type mcpConfig struct {
	Node         string `json:"node"`
	NodeDevice   uint64 `json:"nodeDevice"`
	NodeInode    uint64 `json:"nodeInode"`
	Adapter      string `json:"adapter"`
	CLI          string `json:"cli"`
	Dependencies string `json:"dependencies"`
	Bridge       string `json:"bridge"`
}

// MCPAdapter admits only this application's generated browser adapter and the
// pinned MCP CDP-attached child. It is not a default DSH/terminal supervisor.
type MCPAdapter struct {
	publication              Publication
	node, dependencies, root string
}

func NewMCPAdapter(p Publication, node, dependencies, root string) (*MCPAdapter, error) {
	if !p.verified || !filepath.IsAbs(node) || !filepath.IsAbs(dependencies) || !filepath.IsAbs(root) {
		return nil, errors.New("MAC_MCP_CONFIGURATION_REQUIRED")
	}
	node, err := filepath.EvalSymlinks(node)
	if err != nil {
		return nil, err
	}
	dependencies, err = filepath.EvalSymlinks(dependencies)
	if err != nil {
		return nil, err
	}
	for name, want := range map[string]string{"@playwright/mcp": "0.0.83", "@modelcontextprotocol/sdk": "1.32.2", "ws": "8.22.1", "playwright-core": "1.64.0-alpha-1790635538000", "playwright": "1.64.0-alpha-1790635538000"} {
		data, e := os.ReadFile(filepath.Join(dependencies, "node_modules", name, "package.json"))
		if e != nil {
			return nil, e
		}
		var value struct{ Version string }
		if json.Unmarshal(data, &value) != nil || value.Version != want {
			return nil, errors.New("MAC_MCP_PUBLICATION_REJECTED")
		}
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	return &MCPAdapter{publication: p, node: node, dependencies: dependencies, root: root}, nil
}
func (a *MCPAdapter) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	dir, err := filepath.EvalSymlinks(plan.WorkingDirectory)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(a.root, dir)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || !strings.HasPrefix(filepath.Base(dir), "session-") {
		return nil, errors.New("MAC_MCP_RESOURCE_ROOT_REJECTED")
	}
	if len(plan.Args) != 1 || !sameJobFile(plan.Executable, a.node) || !sameJobFile(plan.Args[0], filepath.Join(dir, "adapter.mjs")) || plan.Env["NODE_OPTIONS"] != "" {
		return nil, errors.New("MAC_MCP_LAUNCH_PLAN_REJECTED")
	}
	for name, value := range plan.Env {
		upper := strings.ToUpper(name)
		if value != "" && (strings.HasPrefix(upper, "PLAYWRIGHT_") || upper == "PWDEBUG" || strings.HasPrefix(upper, "PWTEST_")) {
			return nil, errors.New("MAC_MCP_CONFIG_OVERRIDE_REJECTED")
		}
	}
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	// The trusted resource factory wrote this private sidecar. Re-anchor its
	// addon to the actual host bundle, not a tool/workspace module search path.
	sidecar, err := readPrivate(filepath.Join(dir, "native-connection.json"))
	if err != nil {
		return nil, err
	}
	var connection struct {
		NativeBridge string `json:"nativeBridge"`
	}
	hostExe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	expectedBridge := filepath.Join(filepath.Dir(hostExe), "workeripc-mac.node")
	if json.Unmarshal(sidecar, &connection) != nil || !sameJobFile(connection.NativeBridge, expectedBridge) {
		return nil, errors.New("WORKER_IPC_MAC_BRIDGE_MISSING")
	}
	nodeDev, nodeIno, err := fileIdentity(a.node)
	if err != nil {
		return nil, err
	}
	cli, err := filepath.EvalSymlinks(filepath.Join(a.dependencies, "node_modules/@playwright/mcp/cli.js"))
	if err != nil {
		return nil, err
	}
	script, err := filepath.EvalSymlinks(plan.Args[0])
	if err != nil {
		return nil, err
	}
	ownerDir := filepath.Join(dir, ".dsh-mcp-owner")
	if err = privateDir(ownerDir); err != nil {
		return nil, err
	}
	inner := &Adapter{publication: a.publication, profile: dir, directory: ownerDir, mcp: &mcpConfig{Node: a.node, NodeDevice: nodeDev, NodeInode: nodeIno, Adapter: script, CLI: cli, Dependencies: a.dependencies, Bridge: connection.NativeBridge}}
	// The component's durable owner namespace is private and independent of the
	// existing caller's logical session/generation contract.
	plan.GenerationID = "cef-MCP" + rand.Text()
	worker, err := inner.Start(ctx, plan, raw)
	if inner.worker != nil {
		return &mcpWorker{Worker: inner.worker}, err
	}
	return worker, err
}

// Only this component exposes the optional private-connection binding. CEF and
// older supervisors keep their existing public Worker contract unchanged.
type mcpWorker struct{ *Worker }

func (w *mcpWorker) PrivateConnectionProcess() (uint32, uint64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state.Component != "owned-cef-mcp" || !w.state.RuntimeReady {
		return 0, 0
	}
	return w.state.Broker.PID, w.state.Broker.Started
}
