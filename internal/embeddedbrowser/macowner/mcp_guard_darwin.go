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
		Bridge       string `json:"bridge"`
		Socket       string `json:"socket"`
		Generation   string `json:"generation"`
		OwnerPID     uint32 `json:"ownerPID"`
		OwnerStarted string `json:"ownerStarted"`
	}{cfg.Adapter, cfg.CLI, cfg.Dependencies, cfg.Bridge, g.cfg.Socket, g.cfg.Plan.GenerationID, g.state.Owner.PID, strconv.FormatUint(g.state.Owner.Started, 10)})
	if err != nil {
		return err
	}
	if err = writePrivate(config, body); err != nil {
		return err
	}
	cmd := exec.Command(g.cfg.Plan.Executable, g.cfg.Plan.Args...)
	cmd.Dir = g.cfg.Plan.WorkingDirectory
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
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
	g.state.Owned[processKey(p)] = owned{Process: p, Role: "mcp-adapter-preentry"}
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
	select {
	case <-g.brokerReady:
	case <-time.After(3 * time.Second):
		return
	}
	p, err := process(target.PID)
	cfg := g.cfg.MCP
	if err != nil || p.Started != target.Started || p.State != "live" || !pinnedJobFile(p.Path, cfg.Node, cfg.NodeDevice, cfg.NodeInode) {
		return
	}
	g.mu.Lock()
	root, err := instance(g.state.Broker)
	if err != nil || root.State != "live" || g.stopping || p.PGID != g.state.Owner.PGID {
		g.mu.Unlock()
		return
	}
	role := ""
	isRoot := p.PID == root.PID && p.Started == root.Started && p.PPID == g.state.Owner.PID
	if isRoot {
		role = "mcp-adapter"
	} else if p.PPID == root.PID {
		parent, ok := g.state.Owned[processKey(root)]
		if ok && parent.Target != nil {
			role = "mcp-stdio-child"
		}
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
		g.state.RuntimeVersion = mcpVersion
		g.state.Phase = "mcp-running"
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
		if _, ok := g.state.Owned[processKey(p)]; ok {
			continue
		}
		// The fixed SDK child is non-detached and registers before its entry.
		// Keep its real pre-registration instance too; map-empty never means empty.
		if p.PGID != g.state.Owner.PGID || p.PPID != root.PID || !pinnedJobFile(p.Path, g.cfg.MCP.Node, g.cfg.MCP.NodeDevice, g.cfg.MCP.NodeInode) {
			return ErrCleanupPending
		}
		g.state.Owned[processKey(p)] = owned{Process: p, Role: "mcp-stdio-preentry"}
	}
	return nil
}
