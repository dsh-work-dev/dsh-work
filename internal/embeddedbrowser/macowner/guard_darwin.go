//go:build darwin && cgo

package macowner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/unix"
)

func fmtKey(pid uint32, started uint64) string { return fmt.Sprintf("%d:%d", pid, started) }

var errCEFBrokerExited = errors.New("MAC_CEF_BROKER_NO_LONGER_LIVE")

type guard struct {
	mu, daemonWrite, nativeWrite                  sync.Mutex
	cfg                                           launchConfig
	key                                           []byte
	state                                         journal
	daemon, native                                net.Conn
	brokerReady                                   chan struct{}
	start                                         chan struct{}
	wake                                          chan struct{}
	stopping, engine, quiescent, released, exited bool
	knownError                                    error
	pendingSnapshot                               *message
	mcpStopAt                                     time.Time
	mcpForceAt                                    time.Time
	brokerDevice, brokerInode                     uint64
}

func (g *guard) wakeup() {
	select {
	case g.wake <- struct{}{}:
	default:
	}
}
func (g *guard) persist() error { return save(g.cfg.Directory, g.key, g.state) }
func (g *guard) notify(kind string) {
	g.mu.Lock()
	data, err := json.Marshal(message{Kind: kind, State: &g.state})
	c := g.daemon
	g.mu.Unlock()
	if c == nil || err != nil {
		return
	}
	var value message
	if json.Unmarshal(data, &value) != nil {
		return
	}
	g.daemonWrite.Lock()
	err = writeFrame(c, value)
	g.daemonWrite.Unlock()
	if err != nil {
		g.recordMCPOwnerEvent("owner-notify-failed")
		c.Close()
		g.requestStop()
	}
}
func (g *guard) nativeCommand(kind string) error {
	g.mu.Lock()
	c := g.native
	g.mu.Unlock()
	if c == nil {
		return nil
	}
	g.nativeWrite.Lock()
	defer g.nativeWrite.Unlock()
	return writeFrame(c, struct {
		Kind string `json:"kind"`
	}{kind})
}
func (g *guard) requestStop() {
	g.mu.Lock()
	g.stopping = true
	g.mu.Unlock()
	if g.cfg.MCP != nil || g.cfg.Restricted != nil {
		g.stopMCP(false)
	} else {
		_ = g.nativeCommand("stop")
	}
	g.wakeup()
}
func (g *guard) force() {
	if g.cfg.MCP != nil || g.cfg.Restricted != nil {
		g.stopMCP(true)
		return
	}
	g.mu.Lock()
	g.stopping = true
	targets := []localipc.ProcessTarget{}
	for _, r := range g.state.Owned {
		if r.Target != nil {
			targets = append(targets, *r.Target)
		}
	}
	g.mu.Unlock()
	for _, t := range targets {
		if err := localipc.SignalTarget(t, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, localipc.ErrTargetStale) {
			g.mu.Lock()
			g.knownError = err
			g.mu.Unlock()
		}
	}
	g.wakeup()
}

func (g *guard) serve(c net.Conn) {
	defer c.Close()
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	var hello message
	if readFrame(c, &hello) != nil {
		return
	}
	if hello.Generation != g.cfg.Plan.GenerationID {
		return
	}
	brokerRegistration := hello.Kind == "broker" && g.cfg.Restricted == nil && g.cfg.MCP == nil
	target, err := localipc.TargetFromConn(c)
	if err != nil {
		if brokerRegistration {
			g.mu.Lock()
			g.recordNativeEventLocked("owner-register-target-unavailable")
			g.mu.Unlock()
		}
		return
	}
	if hello.Kind == "daemon" {
		if target.PID != g.cfg.Host.PID || target.Started != g.cfg.Host.Started || hello.Secret != g.cfg.Secret {
			return
		}
		g.mu.Lock()
		if g.daemon != nil || g.stopping {
			g.mu.Unlock()
			return
		}
		g.daemon = c
		g.mu.Unlock()
		close(g.start)
		c.SetReadDeadline(time.Time{})
		for {
			var command message
			if readFrame(c, &command) != nil {
				g.recordMCPOwnerEvent("owner-daemon-read-closed")
				g.requestStop()
				return
			}
			switch command.Kind {
			case "stop":
				g.recordMCPOwnerEvent("owner-stop-command")
				g.requestStop()
			case "force":
				g.recordMCPOwnerEvent("owner-force-command")
				g.force()
			default:
				g.requestStop()
				return
			}
		}
	}
	if g.cfg.Restricted != nil {
		g.registerRestrictedDSH(c, hello, target)
		return
	}
	if g.cfg.MCP != nil {
		g.registerMCP(c, hello, target)
		return
	}
	select {
	case <-g.brokerReady:
	case <-time.After(3 * time.Second):
		if brokerRegistration {
			g.mu.Lock()
			g.recordNativeEventLocked("owner-register-launch-not-ready")
			g.mu.Unlock()
		}
		return
	}
	p, err := process(target.PID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if err != nil || p.Started != target.Started || p.State != "live" {
		if brokerRegistration {
			g.recordNativeEventLocked("owner-register-process-unavailable")
		}
		return
	}
	if hello.Kind == "broker" {
		if hello.RendererChild != 0 {
			return
		}
		if failure := g.brokerRegistrationFailure(p); failure != "" {
			g.recordNativeEventLocked(failure)
			return
		}
	} else if hello.Kind == "helper" {
		if p.PPID != g.state.Broker.PID {
			return
		}
		parent, e := instance(g.state.Broker)
		if e != nil || parent.State != "live" {
			return
		}
		pathOK := false
		for _, path := range g.cfg.Helpers {
			if path == p.Path {
				pathOK = true
			}
		}
		if !pathOK {
			return
		}
		switch hello.Type {
		case "renderer":
			if hello.RendererChild == 0 || hello.RendererChild > 0x7fffffff || !sameJobFile(p.Path, g.cfg.Renderer) {
				return
			}
		case "gpu-process", "utility", "code-sign-clone-cleanup":
			if hello.RendererChild != 0 {
				return
			}
		default:
			return
		}
	} else {
		return
	}
	g.state.Owned[processKey(p)] = owned{Process: p, Target: &target, Role: hello.Kind + ":" + hello.Type, RendererChild: hello.RendererChild}
	if err = g.persist(); err != nil {
		if brokerRegistration {
			g.recordNativeEventLocked("owner-register-journal-failed")
		}
		g.knownError = err
		g.wakeup()
		return
	}
	if writeFrame(c, struct {
		Kind string `json:"kind"`
	}{"registered"}) != nil {
		if brokerRegistration {
			g.recordNativeEventLocked("owner-register-reply-failed")
		}
		return
	}
	g.wakeup()
}

func (g *guard) brokerRegistrationFailure(p Process) string {
	switch {
	case p.PID != g.state.Broker.PID || p.Started != g.state.Broker.Started:
		return "owner-register-process-mismatch"
	case p.PPID != g.state.Owner.PID:
		return "owner-register-parent-mismatch"
	case p.Path == "":
		return "owner-register-path-unavailable"
	// Chromium's code signing clone hard-links the executable. Darwin can
	// return a clone's path for the same running vnode after a name lookup.
	case !pinnedJobFile(p.Path, g.cfg.Plan.Executable, g.brokerDevice, g.brokerInode):
		return "owner-register-path-mismatch"
	default:
		return ""
	}
}

func (g *guard) startBroker() error {
	if g.cfg.Restricted != nil {
		return g.startRestrictedDSH()
	}
	if g.cfg.MCP != nil {
		return g.startMCPBroker()
	}
	device, inode, err := fileIdentity(g.cfg.Plan.Executable)
	if err != nil {
		return err
	}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return err
	}
	parent, child := os.NewFile(uintptr(pair[0]), "owner-parent"), os.NewFile(uintptr(pair[1]), "owner-child")
	defer parent.Close()
	defer child.Close()
	c, err := net.FileConn(parent)
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.native = c
	g.mu.Unlock()
	cmd := exec.Command(g.cfg.Plan.Executable, g.cfg.Plan.Args...)
	cmd.Dir = g.cfg.Plan.WorkingDirectory
	cmd.Stdout = io.Discard
	cmd.Stderr = &nativeEventWriter{guard: g}
	cmd.Env = []string{}
	for key, value := range g.cfg.Plan.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Env = append(cmd.Env, "DSH_WORK_CEF_OWNER_FD=3", "DSH_WORK_CEF_OWNER_SOCKET="+g.cfg.Socket, "DSH_WORK_CEF_OWNER_GENERATION="+g.cfg.Plan.GenerationID, "DSH_WORK_CEF_OWNER_PID="+strconv.Itoa(os.Getpid()), "DSH_WORK_CEF_OWNER_STARTED="+strconv.FormatUint(g.state.Owner.Started, 10))
	cmd.ExtraFiles = []*os.File{child}
	if err = cmd.Start(); err != nil {
		return err
	}
	p, err := process(uint32(cmd.Process.Pid))
	if err != nil {
		// Do not substitute a PID-only signal for missing instance evidence.
		// Returning exits this LaunchAgent root; launchd owns the inherited group.
		// The dirty journal still refuses a new generation until safe recovery.
		return err
	}
	g.mu.Lock()
	g.state.Broker = p
	g.brokerDevice, g.brokerInode = device, inode
	g.state.Owned[processKey(p)] = owned{Process: p, Role: "broker"}
	err = g.persist()
	g.mu.Unlock()
	close(g.brokerReady)
	go g.readNative(c)
	g.watchProcess(cmd)
	g.notify("started")
	return err
}

func (g *guard) readNative(c net.Conn) {
	for {
		var m message
		if err := readFrame(c, &m); err != nil {
			g.wakeup()
			return
		}
		g.mu.Lock()
		if g.exited {
			g.mu.Unlock()
			return
		}
		switch m.Kind {
		case "initialize":
			if g.stopping {
				g.mu.Unlock()
				_ = g.nativeCommand("stop")
				continue
			}
			g.engine = true
			g.state.Phase = "initializing"
			if err := g.persist(); err != nil {
				g.knownError = err
				g.mu.Unlock()
				_ = g.nativeCommand("stop")
				continue
			}
			g.mu.Unlock()
			_ = g.nativeCommand("initialize")
		case "runtime":
			if m.Version != Version || m.CrashReporting == nil || *m.CrashReporting {
				g.knownError = ErrCleanupPending
				g.mu.Unlock()
				g.requestStop()
				g.notify("pending")
				continue
			}
			g.state.RuntimeVersion = m.Version
			g.state.RuntimeReady = true
			g.state.Phase = "running"
			if err := g.persist(); err != nil {
				g.knownError = err
			}
			g.mu.Unlock()
			g.notify("runtime")
		case "shutdown-start":
			g.state.Phase = "shutdown-started"
			if err := g.persist(); err != nil {
				g.knownError = err
				g.mu.Unlock()
				continue
			}
			g.mu.Unlock()
			_ = g.nativeCommand("shutdown-start")
		case "quiescent":
			g.pendingSnapshot = &m
			g.knownError = g.commitSnapshotLocked()
			g.mu.Unlock()
		default:
			g.knownError = ErrCleanupPending
			g.mu.Unlock()
		}
		g.wakeup()
	}
}

func (g *guard) commitSnapshotLocked() error {
	m := g.pendingSnapshot
	if m == nil || m.Broker.PID != g.state.Broker.PID || m.Broker.Started != g.state.Broker.Started {
		return ErrCleanupPending
	}
	if err := g.admitLocked(m.Group, false); err != nil {
		return err
	}
	if err := g.admitLocked(m.Children, true); err != nil {
		return err
	}
	g.quiescent = true
	g.state.Children = m.Children.Members
	g.state.Phase = "quiescent"
	return g.persist()
}

func (g *guard) admitLocked(c Census, children bool) error {
	if g.cfg.Restricted != nil {
		return g.admitRestrictedGroupLocked(c)
	}
	if g.cfg.MCP != nil {
		return g.admitMCPGroupLocked(c)
	}
	if c.Error != 0 || c.Truncated {
		return ErrCleanupPending
	}
	root, err := instance(g.state.Broker)
	if err != nil {
		return err
	}
	switch root.State {
	case "gone", "reused", "zombie":
		return errCEFBrokerExited
	case "live":
	default:
		return ErrCleanupPending
	}
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
		if p.PPID != root.PID || (!children && p.PGID != g.state.Owner.PGID) {
			return ErrCleanupPending
		}
		actual, e := instance(p)
		if e != nil {
			return e
		}
		if actual.State != "gone" && actual.State != "reused" && actual.PPID != root.PID {
			return ErrCleanupPending
		}
		g.state.Owned[processKey(p)] = owned{Process: p, Role: "kernel-child"}
	}
	return nil
}

func (g *guard) beforeRootExit() (bool, error) {
	for _, r := range g.state.Owned {
		if r.Process.PID == g.state.Broker.PID && r.Process.Started == g.state.Broker.Started {
			continue
		}
		p, e := instance(r.Process)
		if e != nil {
			return false, e
		}
		if p.State == "gone" || p.State == "reused" {
			continue
		}
		if r.Role == "helper:code-sign-clone-cleanup" && r.Target != nil && r.Process.PGID != g.state.Owner.PGID {
			matched := false
			for _, child := range g.state.Children {
				if child.PID == r.Process.PID && child.Started == r.Process.Started && child.PPID == g.state.Broker.PID {
					matched = true
				}
			}
			if matched {
				continue
			}
			return false, ErrCleanupPending
		}
		return false, nil
	}
	c, e := group(g.state.Owner.PGID)
	if e != nil {
		return false, e
	}
	for _, p := range c.Members {
		if p.State == "gone" {
			continue
		}
		if p.PID != g.state.Owner.PID && p.PID != g.state.Broker.PID {
			return false, nil
		}
	}
	return true, nil
}

func empty(state journal, excludeOwner bool) (bool, error) {
	groups := map[uint32]bool{state.Owner.PGID: true}
	for _, r := range state.Owned {
		p, e := instance(r.Process)
		if e != nil {
			return false, e
		}
		if p.State != "gone" && p.State != "reused" {
			return false, nil
		}
		groups[r.Process.PGID] = true
	}
	for id := range groups {
		if id == 0 {
			return false, ErrCleanupPending
		}
		c, e := group(id)
		if e != nil {
			return false, e
		}
		for _, p := range c.Members {
			if p.State == "gone" {
				continue
			}
			if excludeOwner && p.PID == state.Owner.PID && p.Started == state.Owner.Started {
				continue
			}
			return false, nil
		}
	}
	return true, nil
}

// RunGuard is a separate LaunchAgent root. It remains alive when its daemon
// connection dies and releases the profile only after the complete CEF gate.
func RunGuard(configPath string) error {
	data, err := readPrivate(configPath)
	if err != nil {
		return err
	}
	var cfg launchConfig
	if err = json.Unmarshal(data, &cfg); err != nil {
		return err
	}
	if err = privateDir(cfg.Directory); err != nil {
		return err
	}
	if err = os.Remove(configPath); err != nil {
		return err
	}
	key, err := ownerKey(cfg.Directory)
	if err != nil {
		return err
	}
	leaseName := ".dsh-cef-profile.lock"
	if cfg.Restricted != nil {
		leaseName = ".dsh-restricted-component.lock"
	}
	if cfg.MCP != nil {
		leaseName = ".dsh-mcp-component.lock"
		if cfg.MCP.AUVDriver != "" {
			leaseName = ".dsh-auv-component.lock"
		}
	}
	fd, err := unix.Open(filepath.Join(cfg.Profile, leaseName), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	if old, e := load(cfg.Directory, key); e == nil && old.Phase != "empty" {
		return ErrCleanupPending
	} else if e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	self, err := process(uint32(os.Getpid()))
	if err != nil || self.PGID != self.PID {
		return errors.Join(ErrCleanupPending, err)
	}
	g := &guard{cfg: cfg, key: key, state: journal{Generation: cfg.Plan.GenerationID, Job: cfg.Job, Owner: self, Phase: "starting", Owned: map[string]owned{}}, brokerReady: make(chan struct{}), start: make(chan struct{}), wake: make(chan struct{}, 1)}
	if cfg.Restricted != nil {
		g.state.Component = "restricted-dsh"
	}
	if cfg.MCP != nil {
		g.state.Component = "owned-cef-mcp"
		if cfg.MCP.AUVDriver != "" {
			g.state.Component = "owned-auv"
		}
	}
	if err = g.persist(); err != nil {
		return err
	}
	if err = setCurrent(cfg.Directory, key, cfg.Plan.GenerationID); err != nil {
		return err
	}
	l, err := localipc.Listen(cfg.Socket, false)
	if err != nil {
		return err
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		limit := make(chan struct{}, 16)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			select {
			case limit <- struct{}{}:
				go func() { defer func() { <-limit }(); g.serve(c) }()
			default:
				c.Close()
			}
		}
	}()
	select {
	case <-g.start:
	case <-time.After(10 * time.Second):
		return errors.New("MAC_CEF_OWNER_START_TIMEOUT")
	}
	if err = g.startBroker(); err != nil {
		g.mu.Lock()
		g.knownError = err
		g.mu.Unlock()
		g.notify("pending")
		return errors.Join(ErrCleanupPending, err)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	pendingNotified := false
	crashForced := false
	for {
		select {
		case <-ticker.C:
		case <-g.wake:
		case <-ctx.Done():
			return ctx.Err()
		}
		g.mu.Lock()
		stopGraceExpired := !g.mcpStopAt.IsZero() && time.Since(g.mcpStopAt) > 3*time.Second
		if cfg.Restricted != nil && stopGraceExpired && g.mcpForceAt.IsZero() {
			// A registered Worker can ignore SIGTERM. Force the pinned process
			// while the guard still owns the group, then let its waiter reap it.
			g.mu.Unlock()
			g.stopMCP(true)
			continue
		}
		awaitingRestrictedExit := cfg.Restricted != nil && !g.mcpForceAt.IsZero() && time.Since(g.mcpForceAt) < time.Second
		if (cfg.MCP != nil || cfg.Restricted != nil) && stopGraceExpired && !awaitingRestrictedExit {
			// The LaunchAgent still holds the original PGID here. Returning
			// lets launchd finish its owned inherited group, including a child
			// stopped before its mandatory registration. Recovery still needs
			// actual instance/group empty; this is not a completion record.
			g.state.Phase = "mcp-retiring"
			if cfg.MCP != nil && cfg.MCP.AUVDriver != "" {
				g.state.Phase = "auv-retiring"
			}
			if cfg.Restricted != nil {
				g.state.Phase = "restricted-retiring"
			}
			err := g.persist()
			g.mu.Unlock()
			return errors.Join(ErrCleanupPending, err)
		}
		if g.pendingSnapshot != nil && g.knownError != nil {
			if g.quiescent {
				g.knownError = g.persist()
			} else if !g.exited {
				g.knownError = g.commitSnapshotLocked()
			}
		}
		if g.knownError != nil {
			g.mu.Unlock()
			if !pendingNotified {
				g.notify("pending")
				pendingNotified = true
			}
			continue
		}
		pendingNotified = false
		if (g.engine || cfg.MCP != nil || cfg.Restricted != nil) && !g.quiescent && !g.exited {
			before := len(g.state.Owned)
			c, e := group(g.state.Owner.PGID)
			if e == nil {
				e = g.admitLocked(c, false)
			}
			if e == nil && before != len(g.state.Owned) {
				e = g.persist()
			}
			if errors.Is(e, errCEFBrokerExited) {
				// The kernel can report death before cmd.Wait publishes it.
				g.mu.Unlock()
				continue
			}
			if e != nil {
				g.knownError = e
				g.mu.Unlock()
				g.requestStop()
				g.notify("pending")
				continue
			}
		}
		if g.quiescent && !g.released && !g.exited {
			ready, e := g.beforeRootExit()
			if e != nil {
				g.knownError = e
				g.mu.Unlock()
				g.notify("pending")
				continue
			}
			if ready {
				g.released = true
				g.mu.Unlock()
				if e = g.nativeCommand("release"); e != nil {
					g.mu.Lock()
					g.knownError = e
					g.mu.Unlock()
				}
				continue
			}
		}
		if g.state.Phase == "broker-exited" && !crashForced {
			crashForced = true
			g.mu.Unlock()
			g.force()
			continue
		}
		if g.exited && (g.quiescent || !g.engine || g.state.Phase == "broker-exited") {
			ready, e := empty(g.state, true)
			if e != nil {
				g.knownError = e
				g.mu.Unlock()
				g.notify("pending")
				continue
			}
			if ready {
				g.state.Phase = "empty"
				e = g.persist()
				g.mu.Unlock()
				if e != nil {
					return errors.Join(ErrCleanupPending, e)
				}
				g.notify("empty")
				if g.native != nil {
					g.native.Close()
				}
				if g.daemon != nil {
					g.daemon.Close()
				}
				return nil
			}
		}
		if g.exited && g.engine && !g.quiescent && g.state.Phase != "broker-exited" {
			g.knownError = ErrCleanupPending
			g.mu.Unlock()
			g.notify("pending")
			continue
		}
		g.mu.Unlock()
	}
}

// Drain all stderr without retaining CEF/error/page text. Only our fixed native
// lifecycle markers become uploadable measurements; oversized lines are dropped.
type nativeEventWriter struct {
	guard     *guard
	line      []byte
	oversized bool
}

func (w *nativeEventWriter) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' {
			if !w.oversized {
				w.record(string(w.line))
			}
			w.line = w.line[:0]
			w.oversized = false
		} else if len(w.line) < 127 && !w.oversized {
			w.line = append(w.line, b)
		} else {
			w.oversized = true
		}
	}
	return len(data), nil
}
func (g *guard) recordMCPOwnerEvent(name string) {
	if g.cfg.MCP != nil && g.cfg.Plan.Env["DSH_WORK_BROWSER_DIAGNOSTICS"] == "1" {
		(&nativeEventWriter{guard: g}).record("DSH_MCP_EVENT " + name)
	}
}

func (w *nativeEventWriter) record(line string) {
	name := ""
	switch line {
	case "DSH_MCP_EVENT owner-notify-failed":
		name = "mcp-owner-notify-failed"
	case "DSH_MCP_EVENT owner-daemon-read-closed":
		name = "mcp-owner-daemon-read-closed"
	case "DSH_MCP_EVENT owner-stop-command":
		name = "mcp-owner-stop-command"
	case "DSH_MCP_EVENT owner-force-command":
		name = "mcp-owner-force-command"
	case "DSH_MCP_EVENT entry":
		name = "mcp-entry"
	case "DSH_MCP_EVENT private-connected":
		name = "mcp-private-connected"
	case "DSH_MCP_EVENT hmac-authenticated":
		name = "mcp-hmac-authenticated"
	case "DSH_MCP_EVENT init":
		name = "mcp-init"
	case "DSH_MCP_EVENT adapter-ready":
		name = "mcp-adapter-ready"
	case "DSH_MCP_EVENT route-ready":
		name = "mcp-route-ready"
	case "DSH_MCP_EVENT sdk-connect-start":
		name = "mcp-sdk-connect-start"
	case "DSH_MCP_EVENT sdk-connected":
		name = "mcp-sdk-connected"
	case "DSH_MCP_EVENT tools-ready":
		name = "mcp-tools-ready"
	case "DSH_MCP_EVENT call-start":
		name = "mcp-call-start"
	case "DSH_MCP_EVENT tab-list-start":
		name = "mcp-tab-list-start"
	case "DSH_MCP_EVENT tab-list-returned":
		name = "mcp-tab-list-returned"
	case "DSH_MCP_EVENT tab-select-returned":
		name = "mcp-tab-select-returned"
	case "DSH_MCP_EVENT private-destroy":
		name = "mcp-private-destroy"
	case "DSH_MCP_EVENT call-dispatch":
		name = "mcp-call-dispatch"
	case "DSH_MCP_EVENT call-result":
		name = "mcp-call-result"
	case "DSH_MCP_EVENT call-error":
		name = "mcp-call-error"
	case "DSH_MCP_EVENT inspect-start":
		name = "mcp-inspect-start"
	case "DSH_MCP_EVENT inspect-complete":
		name = "mcp-inspect-complete"
	case "DSH_MCP_EVENT inspect-error":
		name = "mcp-inspect-error"
	case "DSH_MCP_EVENT private-end":
		name = "mcp-private-end"
	case "DSH_MCP_EVENT private-finish":
		name = "mcp-private-finish"
	case "DSH_MCP_EVENT cdp-upgrade":
		name = "mcp-cdp-upgrade"
	case "DSH_MCP_EVENT cdp-connected":
		name = "mcp-cdp-connected"
	case "DSH_MCP_EVENT uncaught-exception":
		name = "mcp-uncaught-exception"
	case "DSH_MCP_EVENT unhandled-rejection":
		name = "mcp-unhandled-rejection"
	case "DSH_MCP_EVENT process-exit-zero":
		name = "mcp-process-exit-zero"
	case "DSH_MCP_EVENT process-exit-nonzero":
		name = "mcp-process-exit-nonzero"
	case "DSH_MCP_EVENT stop-private-error":
		name = "mcp-stop-private-error"
	case "DSH_MCP_EVENT stop-private-close":
		name = "mcp-stop-private-close"
	case "DSH_MCP_EVENT stop-private-timeout":
		name = "mcp-stop-private-timeout"
	case "DSH_MCP_EVENT stop-hmac-rejected":
		name = "mcp-stop-hmac-rejected"
	case "DSH_MCP_EVENT stop-frame-rejected":
		name = "mcp-stop-frame-rejected"
	case "DSH_MCP_EVENT stop-protocol-error":
		name = "mcp-stop-protocol-error"
	case "DSH_MCP_EVENT stop-cdp-error":
		name = "mcp-stop-cdp-error"
	case "DSH_MCP_EVENT stop-cdp-close":
		name = "mcp-stop-cdp-close"
	case "DSH_MCP_EVENT stop-wire-timeout":
		name = "mcp-stop-wire-timeout"
	case "DSH_MCP_EVENT stop-call-fatal":
		name = "mcp-stop-call-fatal"
	case "DSH_CEF_EVENT shutdown-enter":
		name = "shutdown-enter"
	case "DSH_CEF_EVENT startup-owner-carrier-failed":
		name = "startup-owner-carrier-failed"
	case "DSH_CEF_EVENT startup-owner-registration-failed":
		name = "startup-owner-registration-failed"
	case "DSH_CEF_EVENT startup-owner-registration-identity-failed":
		name = "startup-owner-registration-identity-failed"
	case "DSH_CEF_EVENT startup-owner-registration-write-failed":
		name = "startup-owner-registration-write-failed"
	case "DSH_CEF_EVENT startup-owner-registration-reply-failed":
		name = "startup-owner-registration-reply-failed"
	case "DSH_CEF_EVENT startup-owner-descriptor-failed":
		name = "startup-owner-descriptor-failed"
	case "DSH_CEF_EVENT startup-host-carrier-failed":
		name = "startup-host-carrier-failed"
	case "DSH_CEF_EVENT startup-host-hmac-failed":
		name = "startup-host-hmac-failed"
	case "DSH_CEF_EVENT startup-owner-initialize-failed":
		name = "startup-owner-initialize-failed"
	case "DSH_CEF_EVENT download-writer-admission-failed":
		name = "download-writer-admission-failed"
	case "DSH_CEF_EVENT before-close":
		name = "before-close"
	case "DSH_CEF_EVENT before-close-tab-erased":
		name = "before-close-tab-erased"
	case "DSH_CEF_EVENT before-close-return":
		name = "before-close-return"
	case "DSH_CEF_EVENT frame-destroyed-enter":
		name = "frame-destroyed-enter"
	case "DSH_CEF_EVENT frame-destroyed-return":
		name = "frame-destroyed-return"
	case "DSH_CEF_EVENT loop-returned":
		name = "loop-returned"
	case "DSH_CEF_EVENT owner-stop":
		name = "owner-stop"
	case "DSH_CEF_EVENT owner-read-failed":
		name = "owner-read-failed"
	case "DSH_CEF_EVENT monitor-join-start":
		name = "monitor-join-start"
	case "DSH_CEF_EVENT monitor-joined":
		name = "monitor-joined"
	case "DSH_CEF_EVENT shutdown-request":
		name = "shutdown-request"
	case "DSH_CEF_EVENT shutdown-ack":
		name = "shutdown-ack"
	case "DSH_CEF_EVENT shutdown-ack-failed":
		name = "shutdown-ack-failed"
	case "DSH_CEF_EVENT shutdown-returned":
		name = "shutdown-returned"
	case "DSH_CEF_EVENT quiescent-sent":
		name = "quiescent-sent"
	case "DSH_CEF_EVENT quiescent-failed":
		name = "quiescent-failed"
	case "DSH_CEF_EVENT release-received":
		name = "release-received"
	case "DSH_CEF_EVENT release-failed":
		name = "release-failed"
	default:
		return
	}
	w.guard.mu.Lock()
	defer w.guard.mu.Unlock()
	w.guard.recordNativeEventLocked(name)
}

func (g *guard) recordNativeEventLocked(name string) {
	if len(g.state.NativeEvents) == 64 {
		g.state.NativeEventsTruncated = true
		return
	}
	g.state.NativeEvents = append(g.state.NativeEvents, nativeEvent{Name: name, At: time.Now().UTC()})
}

func (g *guard) watchProcess(cmd *exec.Cmd) {
	go func() {
		e := cmd.Wait()
		if stdout, ok := cmd.Stdout.(*restrictedOutput); ok {
			stdout.flush()
		}
		if stderr, ok := cmd.Stderr.(*restrictedOutput); ok {
			stderr.flush()
		}
		g.mu.Lock()
		g.exited = true
		g.state.Exit = supervisor.ExitResult{Started: true, Code: cmd.ProcessState.ExitCode()}
		if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			g.state.ExitSignal = int(status.Signal())
			g.state.ExitCoreDump = status.CoreDump()
		}
		if e != nil {
			g.state.Exit.Err = "MAC_CEF_BROKER_EXIT_FAILED"
		}
		if g.state.Component == "" && g.engine && !g.quiescent &&
			(g.knownError == nil || errors.Is(g.knownError, errCEFBrokerExited)) {
			// A helper must register durably before it can load CEF. Keep its
			// recorded instance and group in the empty check, alongside the
			// LaunchAgent's group even if a child never registered.
			g.knownError = nil
			g.state.Phase = "broker-exited"
			if g.state.Exit.Err == "" {
				g.state.Exit.Err = "MAC_CEF_BROKER_EXIT_WITHOUT_QUIESCENCE"
			}
		}
		if err := g.persist(); err != nil && g.state.Phase == "broker-exited" {
			g.knownError = err
		}
		g.mu.Unlock()
		g.notify("exited")
		g.wakeup()
	}()
}
