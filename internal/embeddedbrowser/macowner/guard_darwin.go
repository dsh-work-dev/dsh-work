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
	_ = g.nativeCommand("stop")
	g.wakeup()
}
func (g *guard) force() {
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
	target, err := localipc.TargetFromConn(c)
	if err != nil {
		return
	}
	if hello.Generation != g.cfg.Plan.GenerationID {
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
				g.requestStop()
				return
			}
			switch command.Kind {
			case "stop":
				g.requestStop()
			case "force":
				g.force()
			default:
				g.requestStop()
				return
			}
		}
	}
	select {
	case <-g.brokerReady:
	case <-time.After(3 * time.Second):
		return
	}
	p, err := process(target.PID)
	if err != nil || p.Started != target.Started || p.State != "live" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if hello.Kind == "broker" {
		if p.PID != g.state.Broker.PID || p.Started != g.state.Broker.Started || p.PPID != g.state.Owner.PID || p.Path != g.cfg.Plan.Executable {
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
		case "renderer", "gpu-process", "utility", "code-sign-clone-cleanup":
		default:
			return
		}
	} else {
		return
	}
	g.state.Owned[processKey(p)] = owned{Process: p, Target: &target, Role: hello.Kind + ":" + hello.Type}
	if err = g.persist(); err != nil {
		g.knownError = err
		g.wakeup()
		return
	}
	if writeFrame(c, struct {
		Kind string `json:"kind"`
	}{"registered"}) != nil {
		return
	}
	g.wakeup()
}

func (g *guard) startBroker() error {
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
	g.state.Owned[processKey(p)] = owned{Process: p, Role: "broker"}
	err = g.persist()
	g.mu.Unlock()
	close(g.brokerReady)
	go g.readNative(c)
	go func() {
		e := cmd.Wait()
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
		_ = g.persist()
		g.mu.Unlock()
		g.notify("exited")
		g.wakeup()
	}()
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
	if c.Error != 0 || c.Truncated {
		return ErrCleanupPending
	}
	root, err := instance(g.state.Broker)
	if err != nil || root.State != "live" {
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
	fd, err := unix.Open(filepath.Join(cfg.Profile, ".dsh-cef-profile.lock"), unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
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
	for {
		select {
		case <-ticker.C:
		case <-g.wake:
		case <-ctx.Done():
			return ctx.Err()
		}
		g.mu.Lock()
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
		if g.engine && !g.quiescent && !g.exited {
			before := len(g.state.Owned)
			c, e := group(g.state.Owner.PGID)
			if e == nil {
				e = g.admitLocked(c, false)
			}
			if e == nil && before != len(g.state.Owned) {
				e = g.persist()
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
		if g.exited && (g.quiescent || !g.engine) {
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
		if g.exited && g.engine && !g.quiescent {
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
func (w *nativeEventWriter) record(line string) {
	name := ""
	switch line {
	case "DSH_CEF_EVENT shutdown-enter":
		name = "shutdown-enter"
	case "DSH_CEF_EVENT before-close":
		name = "before-close"
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
	if len(w.guard.state.NativeEvents) == 64 {
		w.guard.state.NativeEventsTruncated = true
		return
	}
	w.guard.state.NativeEvents = append(w.guard.state.NativeEvents, nativeEvent{Name: name, At: time.Now().UTC()})
}
