//go:build darwin && cgo

package macowner

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

func (g *guard) startMCPBroker() error {
	cfg := g.cfg.MCP
	// No other entry point or interpreter can use this component's process-group
	// argument. The stdio child's mandatory --import executes before its CLI.
	if !sameJobFile(g.cfg.Plan.Executable, cfg.Node) || len(g.cfg.Plan.Args) != 1 || !sameJobFile(g.cfg.Plan.Args[0], cfg.Adapter) || !pinnedJobFile(cfg.Node, cfg.Node, cfg.NodeDevice, cfg.NodeInode) {
		return errors.New("MAC_MCP_COMPONENT_REJECTED")
	}
	if cfg.AUVDriver != "" && !pinnedJobFile(cfg.AUVDriver, cfg.AUVDriver, cfg.AUVDevice, cfg.AUVInode) {
		return errors.New("MAC_AUV_COMPONENT_REJECTED")
	}
	run := filepath.Dir(g.cfg.Socket)
	module := filepath.Join(run, "mcp-bootstrap.mjs")
	config := filepath.Join(run, "mcp-bootstrap.json")
	if err := writePrivate(module, mcpBootstrap); err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Adapter      string `json:"adapter"`
		CLI          string `json:"cli"`
		Dependencies string `json:"dependencies"`
		AUVDriver    string `json:"auvDriver,omitempty"`
		Bridge       string `json:"bridge"`
		Socket       string `json:"socket"`
		Generation   string `json:"generation"`
		OwnerPID     uint32 `json:"ownerPID"`
		OwnerStarted string `json:"ownerStarted"`
	}{cfg.Adapter, cfg.CLI, cfg.Dependencies, cfg.AUVDriver, cfg.Bridge, g.cfg.Socket, g.cfg.Plan.GenerationID, g.state.Owner.PID, strconv.FormatUint(g.state.Owner.Started, 10)})
	if err != nil {
		return err
	}
	if err = writePrivate(config, body); err != nil {
		return err
	}
	cmd := exec.Command(g.cfg.Plan.Executable, g.cfg.Plan.Args...)
	cmd.Dir = g.cfg.Plan.WorkingDirectory
	cmd.Stdout = io.Discard
	cmd.Stderr = &nativeEventWriter{guard: g}
	for key, value := range g.cfg.Plan.Env {
		if key != "NODE_OPTIONS" && key != "DSH_WORK_MCP_OWNER_BOOTSTRAP" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	assetURL := (&url.URL{Scheme: "file", Path: module}).String()
	cmd.Env = append(cmd.Env, "NODE_OPTIONS=--import="+assetURL, "DSH_WORK_MCP_OWNER_BOOTSTRAP="+config)
	if err = cmd.Start(); err != nil {
		return err
	}
	p, err := process(uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.state.Broker = p
	role := "mcp-adapter-preentry"
	if cfg.AUVDriver != "" {
		role = "auv-adapter-preentry"
	}
	g.state.Owned[processKey(p)] = owned{Process: p, Role: role}
	err = g.persist()
	g.mu.Unlock()
	close(g.brokerReady)
	g.watchProcess(cmd)
	return err
}
func (g *guard) registerMCP(c net.Conn, hello message, target localipc.ProcessTarget) {
	if hello.Kind != "mcp" {
		return
	}
	cfg := g.cfg.MCP
	version := mcpVersion
	if cfg.AUVDriver != "" {
		version = auvOwnerVersion
	}
	g.registerNode(c, hello, target, cfg.Node, cfg.NodeDevice, cfg.NodeInode, cfg.AUVDriver != "", version)
}
func (g *guard) registerNode(c net.Conn, hello message, target localipc.ProcessTarget, node string, device, inode uint64, rootOnly bool, version string) {
	select {
	case <-g.brokerReady:
	case <-time.After(3 * time.Second):
		return
	}
	p, err := process(target.PID)
	if err != nil || p.Started != target.Started || p.State != "live" || !pinnedJobFile(p.Path, node, device, inode) {
		return
	}
	g.mu.Lock()
	root, err := instance(g.state.Broker)
	if err != nil || root.State != "live" || g.stopping || p.PGID != g.state.Owner.PGID {
		g.mu.Unlock()
		return
	}
	role := ""
	restricted := version == restrictedVersion || version == profileMaintenanceVersion
	isRoot := p.PID == root.PID && p.Started == root.Started && p.PPID == g.state.Owner.PID
	if isRoot {
		role = "mcp-adapter"
	} else if !rootOnly && p.PPID == root.PID {
		parent, ok := g.state.Owned[processKey(root)]
		if ok && parent.Target != nil {
			role = "mcp-stdio-child"
		}
	}
	if isRoot && restricted {
		role = "restricted-dsh"
	} else if isRoot && version == auvOwnerVersion {
		role = "auv-adapter"
	} else if !isRoot && role != "" && restricted {
		role = "profile-pnpm"
	}
	if role == "" {
		g.mu.Unlock()
		return
	}
	if previous, ok := g.state.Owned[processKey(p)]; ok && previous.Target != nil {
		g.mu.Unlock()
		return
	}
	g.state.Owned[processKey(p)] = owned{Process: p, Target: &target, Role: role}
	if isRoot {
		g.state.RuntimeReady = true
		g.state.RuntimeVersion = version
		g.state.Phase = "mcp-running"
		if version == auvOwnerVersion {
			g.state.Phase = "auv-running"
		} else if restricted {
			g.state.Phase = "restricted-running"
		}
	}
	err = g.persist()
	if err != nil {
		g.knownError = err
	}
	g.mu.Unlock()
	if err != nil {
		return
	}
	if writeFrame(c, message{Kind: "registered"}) != nil {
		return
	}
	if isRoot {
		g.notify("runtime")
	} else {
		g.notify("member")
	}
	g.wakeup()
}
func (g *guard) stopMCP(force bool) {
	g.mu.Lock()
	g.stopping = true
	g.state.StopRequested = true
	g.state.ForceRequested = g.state.ForceRequested || force
	if g.mcpStopAt.IsZero() {
		g.mcpStopAt = time.Now()
	}
	if force && g.mcpForceAt.IsZero() {
		g.mcpForceAt = time.Now()
	}
	targets := []localipc.ProcessTarget{}
	for _, r := range g.state.Owned {
		if r.Target != nil {
			targets = append(targets, *r.Target)
		}
	}
	g.mu.Unlock()
	signal := syscall.SIGTERM
	if force {
		signal = syscall.SIGKILL
	}
	for _, target := range targets {
		if err := localipc.SignalTarget(target, signal); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, localipc.ErrTargetStale) {
			g.mu.Lock()
			g.knownError = err
			g.mu.Unlock()
		}
	}
	g.wakeup()
}
func (g *guard) admitMCPGroupLocked(c Census) error {
	if c.Error != 0 || c.Truncated {
		return ErrCleanupPending
	}
	root := g.state.Broker
	cfg := g.cfg.MCP
	for _, p := range c.Members {
		if p.State == "gone" {
			continue
		}
		if p.Error != 0 || p.Started == 0 || p.State == "unknown" {
			return ErrCleanupPending
		}
		if p.PID == g.state.Owner.PID && p.Started == g.state.Owner.Started {
			continue
		}
		if record, ok := g.state.Owned[processKey(p)]; ok {
			if p.State == "live" && (record.Role == "auv-native" || record.Role == "auv-native-child") &&
				!pinnedJobFile(p.Path, cfg.AUVDriver, cfg.AUVDevice, cfg.AUVInode) {
				return ErrCleanupPending
			}
			continue
		}
		// The fixed SDK child is non-detached and registers before its entry.
		// Keep its real pre-registration instance too; map-empty never means empty.
		path, device, inode, role := cfg.Node, cfg.NodeDevice, cfg.NodeInode, "mcp-stdio-preentry"
		if cfg.AUVDriver != "" {
			path, device, inode, role = cfg.AUVDriver, cfg.AUVDevice, cfg.AUVInode, "auv-native"
		}
		if p.PGID != g.state.Owner.PGID || !pinnedJobFile(p.Path, path, device, inode) {
			return ErrCleanupPending
		}
		if p.PPID != root.PID {
			// The AUV daemon starts its local runner with the same pinned
			// executable. Admit only that immediate worker generation.
			if cfg.AUVDriver == "" || !auvRunnerParent(p, root, cfg) {
				return ErrCleanupPending
			}
			role = "auv-native-child"
		}
		g.state.Owned[processKey(p)] = owned{Process: p, Role: role}
	}
	return nil
}

func auvRunnerParent(child, root Process, cfg *mcpConfig) bool {
	parent, err := process(child.PPID)
	return err == nil && parent.State == "live" && parent.PGID == child.PGID &&
		parent.PPID == root.PID && parent.Started >= root.Started && parent.Started <= child.Started &&
		pinnedJobFile(parent.Path, cfg.AUVDriver, cfg.AUVDevice, cfg.AUVInode)
}
