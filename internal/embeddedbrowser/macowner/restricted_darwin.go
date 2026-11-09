//go:build darwin && cgo

package macowner

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/redact"
	"github.com/local/dsh-work/internal/supervisor"
)

const restrictedVersion = "restricted-dsh/node-only-v1"

//go:embed restricted_bootstrap.mjs
var restrictedBootstrap []byte

type restrictedConfig struct {
	Node       string `json:"node"`
	Entry      string `json:"entry"`
	Sandbox    string `json:"sandbox"`
	Bridge     string `json:"bridge"`
	NodeDevice uint64 `json:"nodeDevice"`
	NodeInode  uint64 `json:"nodeInode"`
}

// RestrictedDSHAdapter admits only the pinned DSH CLI and fixed Node probes.
// Kernel deny-fork/exec is installed before any DSH/profile module can load.
// This component grants no CEF, MCP child, arbitrary command, or PTY ability.
type RestrictedDSHAdapter struct {
	mu                                      sync.Mutex
	pending                                 map[*Worker]struct{}
	publication                             Publication
	node, cli, root, sandbox, bridge, probe string
}

func NewRestrictedDSHAdapter(p Publication, node, cli, root string) (*RestrictedDSHAdapter, error) {
	if !p.verified || !filepath.IsAbs(root) {
		return nil, errors.New("MAC_PREVIEW_CONFIGURATION_REQUIRED")
	}
	var err error
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		return nil, err
	}
	cli, err = filepath.EvalSymlinks(cli)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(cli)), "package.json"))
	if err != nil {
		return nil, err
	}
	var manifest struct {
		Name    string
		Version string
	}
	if json.Unmarshal(data, &manifest) != nil || manifest.Name != "@deepseek-ai/dsh" || manifest.Version != "0.2.2-rc.1" || filepath.Base(cli) != "bin.js" {
		return nil, errors.New("MAC_PREVIEW_DSH_PUBLICATION_REJECTED")
	}
	host, err := os.Executable()
	if err != nil {
		return nil, err
	}
	sandbox, bridge := filepath.Join(filepath.Dir(host), "dsh-preview-sandbox.node"), filepath.Join(filepath.Dir(host), "workeripc-mac.node")
	for _, asset := range []string{node, sandbox, bridge} {
		if _, _, err = fileIdentity(asset); err != nil {
			return nil, err
		}
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if err = privateDir(root); err != nil {
		return nil, err
	}
	// Old daemon leases must settle before a fresh execution world is admitted.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "generation-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if err = privateDir(dir); err != nil {
			return nil, err
		}
		key, e := ownerKey(dir)
		if e != nil {
			return nil, e
		}
		old := &Adapter{publication: p, profile: dir, directory: dir}
		if e = old.recoverAttempts(ctx, key); e != nil {
			return nil, e
		}
		if e = recoverGeneration(ctx, dir, key); e != nil {
			return nil, e
		}
	}
	probe := filepath.Join(root, "node-probe.mjs")
	if err = writePrivate(probe, []byte("if(process.argv[2]==='path')console.log(process.execPath);else if(process.argv[2]==='version')console.log(process.version);else throw Error('MAC_PREVIEW_PROBE_REJECTED');\n")); err != nil {
		return nil, err
	}
	return &RestrictedDSHAdapter{publication: p, node: node, cli: cli, root: root, sandbox: sandbox, bridge: bridge, probe: probe}, nil
}
func (a *RestrictedDSHAdapter) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (supervisor.Worker, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// A same-Runtime retry cannot evade an uncertain earlier owner by allocating
	// another generation directory. Known live Workers are not cleanup failures.
	for old := range a.pending {
		if err := old.Close(); err != nil {
			return nil, errors.Join(ErrCleanupPending, err)
		}
		delete(a.pending, old)
	}
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	args := append([]string{}, plan.Args...)
	entry := a.cli
	if sameJobFile(plan.Executable, a.node) {
		if len(args) == 1 && args[0] == "--version" {
			entry = a.probe
			args = []string{"version"}
		} else if len(args) == 2 && args[0] == "-p" && args[1] == "process.execPath" {
			entry = a.probe
			args = []string{"path"}
		} else {
			return nil, errors.New("MAC_PREVIEW_PROCESS_UNAVAILABLE: only the selected DSH runtime is available")
		}
	} else if !sameJobFile(plan.Executable, a.cli) {
		return nil, errors.New("MAC_PREVIEW_PROCESS_UNAVAILABLE: local commands are unavailable in this Mac preview")
	}
	// Never let an option select a different Node entry point, loader, or profile.
	if entry == a.cli && !(len(args) == 1 && args[0] == "--version") {
		if len(args) < 4 || args[0] != "--patch" {
			return nil, errors.New("MAC_PREVIEW_DSH_LAUNCH_REJECTED")
		}
		for _, v := range args {
			if strings.HasPrefix(v, "--import") || strings.HasPrefix(v, "--require") || v == "-r" {
				return nil, errors.New("MAC_PREVIEW_DSH_LAUNCH_REJECTED")
			}
		}
	}
	dev, ino, err := fileIdentity(a.node)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(a.root, "generation-"+rand.Text())
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	if entry == a.cli && !(len(args) == 1 && args[0] == "--version") {
		patch := filepath.Join(dir, "restricted.patch.yml")
		if err = writePrivate(patch, []byte("- id: tool-bash\n  disabled: true\n- id: tool-pwsh\n  disabled: true\n- id: tool-jobs\n  disabled: true\n- id: tool-fs-search\n  disabled: true\n")); err != nil {
			return nil, err
		}
		args = append([]string{"--patch", patch}, args...)
	}
	cfg := &restrictedConfig{Node: a.node, Entry: entry, Sandbox: a.sandbox, Bridge: a.bridge, NodeDevice: dev, NodeInode: ino}
	inner := &Adapter{publication: a.publication, profile: dir, directory: dir, restricted: cfg}
	plan.Executable = a.node
	plan.Args = append([]string{entry}, args...)
	plan.GenerationID = "cef-DSH" + rand.Text()
	w, err := inner.Start(ctx, plan, raw)
	if inner.worker != nil {
		if err != nil {
			if a.pending == nil {
				a.pending = map[*Worker]struct{}{}
			}
			a.pending[inner.worker] = struct{}{}
		}
		return &restrictedWorker{Worker: inner.worker, adapter: a}, err
	}
	return w, err
}

type restrictedWorker struct {
	*Worker
	adapter *RestrictedDSHAdapter
}

func (w *restrictedWorker) recordLocked(err error, retired bool) error {
	if err != nil {
		if w.adapter.pending == nil {
			w.adapter.pending = map[*Worker]struct{}{}
		}
		w.adapter.pending[w.Worker] = struct{}{}
	} else if retired {
		delete(w.adapter.pending, w.Worker)
	}
	return err
}
func (w *restrictedWorker) RequestStop(ctx context.Context) error {
	w.adapter.mu.Lock()
	defer w.adapter.mu.Unlock()
	return w.recordLocked(w.Worker.RequestStop(ctx), false)
}
func (w *restrictedWorker) ForceStop(ctx context.Context) error {
	w.adapter.mu.Lock()
	defer w.adapter.mu.Unlock()
	return w.recordLocked(w.Worker.ForceStop(ctx), false)
}
func (w *restrictedWorker) WaitEmpty(ctx context.Context) error {
	w.adapter.mu.Lock()
	defer w.adapter.mu.Unlock()
	return w.recordLocked(w.Worker.WaitEmpty(ctx), false)
}
func (w *restrictedWorker) Close() error {
	w.adapter.mu.Lock()
	defer w.adapter.mu.Unlock()
	err := w.Worker.Close()
	return w.recordLocked(err, err == nil)
}

func (g *guard) startRestrictedDSH() error {
	cfg := g.cfg.Restricted
	if cfg == nil || !pinnedJobFile(g.cfg.Plan.Executable, cfg.Node, cfg.NodeDevice, cfg.NodeInode) || len(g.cfg.Plan.Args) == 0 || !sameJobFile(g.cfg.Plan.Args[0], cfg.Entry) {
		return errors.New("MAC_PREVIEW_COMPONENT_REJECTED")
	}
	run := filepath.Dir(g.cfg.Socket)
	module, config := filepath.Join(run, "restricted-bootstrap.mjs"), filepath.Join(run, "restricted-bootstrap.json")
	if err := writePrivate(module, restrictedBootstrap); err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Entry        string `json:"entry"`
		Sandbox      string `json:"sandbox"`
		Bridge       string `json:"bridge"`
		Socket       string `json:"socket"`
		Generation   string `json:"generation"`
		OwnerPID     uint32 `json:"ownerPID"`
		OwnerStarted string `json:"ownerStarted"`
	}{cfg.Entry, cfg.Sandbox, cfg.Bridge, g.cfg.Socket, g.cfg.Plan.GenerationID, g.state.Owner.PID, strconv.FormatUint(g.state.Owner.Started, 10)})
	if err != nil {
		return err
	}
	if err = writePrivate(config, body); err != nil {
		return err
	}
	cmd := exec.Command(cfg.Node, append([]string{"--import", (&url.URL{Scheme: "file", Path: module}).String()}, g.cfg.Plan.Args...)...)
	cmd.Dir = g.cfg.Plan.WorkingDirectory
	for key, value := range g.cfg.Plan.Env {
		if !strings.HasPrefix(key, "DYLD_") && key != "NODE_OPTIONS" && key != "NODE_REPL_EXTERNAL_MODULE" && key != "DSH_WORK_RESTRICTED_BOOTSTRAP" {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "DSH_WORK_RESTRICTED_BOOTSTRAP="+config)
	cmd.Stdout = &restrictedOutput{g: g, stream: supervisor.StreamStdout}
	cmd.Stderr = &restrictedOutput{g: g, stream: supervisor.StreamStderr}
	if err = cmd.Start(); err != nil {
		return err
	}
	p, err := process(uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	g.mu.Lock()
	g.state.Broker = p
	g.state.Owned[processKey(p)] = owned{Process: p, Role: "restricted-dsh-preentry"}
	err = g.persist()
	g.mu.Unlock()
	close(g.brokerReady)
	g.watchProcess(cmd)
	return err
}
func (g *guard) registerRestrictedDSH(c net.Conn, hello message, target localipc.ProcessTarget) {
	if hello.Kind != "restricted-dsh" || hello.Version != restrictedVersion {
		return
	}
	g.registerNode(c, hello, target, g.cfg.Restricted.Node, g.cfg.Restricted.NodeDevice, g.cfg.Restricted.NodeInode, true, restrictedVersion)
}
func (g *guard) admitRestrictedGroupLocked(c Census) error {
	if c.Error != 0 || c.Truncated {
		return ErrCleanupPending
	}
	for _, p := range c.Members {
		if p.State == "gone" {
			continue
		}
		if p.Error != 0 || p.Started == 0 {
			return ErrCleanupPending
		}
		if p.PID == g.state.Owner.PID && p.Started == g.state.Owner.Started {
			continue
		}
		if p.PID != g.state.Broker.PID || p.Started != g.state.Broker.Started {
			return ErrCleanupPending
		}
	}
	return nil
}

// Bounded transient process output stays on the authenticated owner connection;
// only redacted tails/events are retained by the consumer. It is not a command.
type restrictedOutput struct {
	g         *guard
	stream    supervisor.OutputStream
	line      []byte
	oversized bool
}

func (w *restrictedOutput) Write(data []byte) (int, error) {
	for _, b := range data {
		if b == '\n' {
			if !w.oversized {
				w.emit(string(w.line))
			}
			w.line = w.line[:0]
			w.oversized = false
		} else if len(w.line) < 65536 && !w.oversized {
			w.line = append(w.line, b)
		} else {
			w.line = w.line[:0]
			w.oversized = true
		}
	}
	return len(data), nil
}
func (w *restrictedOutput) emit(line string) {
	w.g.mu.Lock()
	c := w.g.daemon
	generation := w.g.state.Generation
	w.g.mu.Unlock()
	if c == nil {
		return
	}
	w.g.daemonWrite.Lock()
	err := writeFrame(c, message{Kind: "output", Generation: generation, Output: &supervisor.OutputEvent{Stream: w.stream, Text: line}})
	w.g.daemonWrite.Unlock()
	if err != nil {
		c.Close()
		w.g.requestStop()
	}
}
func (w *Worker) acceptRestrictedOutput(m message) bool {
	if m.Kind != "output" {
		return false
	}
	w.mu.Lock()
	generation, component := w.state.Generation, w.state.Component
	w.mu.Unlock()
	if component != "restricted-dsh" || m.Generation != generation || m.Output == nil || len(m.Output.Text) > 65536 || (m.Output.Stream != supervisor.StreamStdout && m.Output.Stream != supervisor.StreamStderr) {
		return false
	}
	if w.raw != nil {
		w.raw(m.Output.Stream, m.Output.Text)
	}
	event := supervisor.OutputEvent{Stream: m.Output.Stream, Text: redact.Secrets(m.Output.Text)}
	w.mu.Lock()
	tail := &w.stdout
	if event.Stream == supervisor.StreamStderr {
		tail = &w.stderr
	}
	*tail += event.Text + "\n"
	if len(*tail) > 8192 {
		*tail = (*tail)[len(*tail)-8192:]
	}
	w.mu.Unlock()
	select {
	case w.events <- event:
	default:
	}
	return true
}
