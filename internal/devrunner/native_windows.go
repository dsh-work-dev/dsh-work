//go:build windows

package devrunner

import (
	"context"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/daemon"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/supervisor"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Workspace, Runtime string
	Logger             *log.Logger
}
type owned struct {
	worker                   supervisor.Worker
	readyAt, readyGeneration string
}
type Native struct {
	mu                                 sync.Mutex
	config                             Config
	env                                map[string]string
	tools                              map[string]string
	children                           map[int]*owned // 0=daemon; 1/2=UI
	buildOwner                         supervisor.Worker
	history                            map[int]ProcessState
	abnormal                           error
	build, lastError, active, buildLog string
	readyProbe                         func(context.Context, int) bool
	adapter                            supervisor.Adapter
}

func NewNative(c Config) (*Native, error) {
	root, err := filepath.Abs(c.Workspace)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	c.Workspace = root
	if !filepath.IsAbs(c.Runtime) {
		return nil, errors.New("absolute runtime path required")
	}
	if _, err = os.Stat(c.Runtime); err != nil {
		return nil, err
	}
	module, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil || !strings.HasPrefix(string(module), "module github.com/local/dsh-work\r\n") && !strings.HasPrefix(string(module), "module github.com/local/dsh-work\n") {
		return nil, errors.New("runner must be scoped to the dsh-work workspace")
	}
	state := filepath.Join(root, "bin", "e67-ui-state")
	if os.Getenv("DSH_WORK_EMBEDDED_CHECKPOINT") != "1" || !strings.EqualFold(filepath.Clean(os.Getenv("DSH_WORK_EMBEDDED_ROOT")), state) {
		return nil, errors.New("start the runner using start-embedded-development.ps1 -Runner from an ordinary terminal")
	}
	tools := map[string]string{}
	for _, name := range []string{"go", "node", "wails3", "python"} {
		path, e := exec.LookPath(name)
		if e != nil {
			return nil, e
		}
		tools[name] = path
	}
	shell, err := launcherPowerShell(os.Getenv("DSH_WORK_EMBEDDED_POWERSHELL"))
	if err != nil {
		return nil, err
	}
	tools["powershell"] = shell
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	n := &Native{config: c, env: env, tools: tools, children: map[int]*owned{}, build: "not-built", adapter: win.NewJobObjectAdapter()}
	return n, nil
}
func (n *Native) identity() string { return filepath.Join(n.config.Workspace, "bin", "e67-ui-state") }
func (n *Native) uiIdentity(instance int) string {
	v := n.identity() + "-ui"
	if instance == 2 {
		v += "-embedded-2"
	}
	return v
}
func (n *Native) log(format string, args ...any) {
	if n.config.Logger != nil {
		n.config.Logger.Printf(format, args...)
	}
}
func (n *Native) Execute(ctx context.Context, action string, instance int) (err error) {
	n.mu.Lock()
	n.active = action
	if instance != 0 {
		n.active += " " + strconv.Itoa(instance)
	}
	n.mu.Unlock()
	n.log("command started: %s instance=%d", action, instance)
	defer func() {
		n.mu.Lock()
		n.active = ""
		if err != nil {
			n.lastError = err.Error()
		}
		n.mu.Unlock()
		n.log("command finished: %s instance=%d error=%v", action, instance, err)
	}()
	switch action {
	case "build":
		return n.buildCandidate(ctx)
	case "start":
		return n.startUI(ctx, instance)
	case "stop":
		if instance == 0 {
			return n.stopAll(ctx)
		}
		return n.stopUI(ctx, instance)
	case "restart":
		if err = n.stopUI(ctx, instance); err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		return n.startUI(ctx, instance)
	case "shutdown":
		return n.stopAll(ctx)
	default:
		return errors.New("unsupported fixed operation")
	}
}
func (n *Native) get(instance int) *owned {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.children[instance]
}
func live(w supervisor.Worker) bool {
	if w == nil {
		return false
	}
	select {
	case <-w.Exited():
		return false
	default:
		return true
	}
}
func probe(ctx context.Context, identity, path string, out any) error {
	client := daemon.NewClient(identity)
	defer client.Close()
	return client.JSON(ctx, path, nil, out)
}

type expectedUIOwner struct {
	PID     uint32
	Started uint64
}

func ownedClient(identity string, p *owned) *daemon.Client {
	if p == nil {
		return daemon.NewProcessClient(identity, daemon.ProcessPeer{})
	}
	d := p.worker.Diagnostics()
	return daemon.NewProcessClient(identity, daemon.ProcessPeer{PID: uint32(d.PID), Started: d.ProcessStarted})
}
func ownedProbe(ctx context.Context, identity, path string, p *owned, out any) error {
	client := ownedClient(identity, p)
	defer client.Close()
	return client.JSON(ctx, path, nil, out)
}
func absent(err error) bool { return errors.Is(err, os.ErrNotExist) }
func (n *Native) foreignAbsent(ctx context.Context) error {
	for _, entry := range []struct{ identity, path string }{{n.identity(), "/snapshot"}, {n.uiIdentity(1), "/status"}, {n.uiIdentity(2), "/status"}} {
		bounded, stop := context.WithTimeout(ctx, time.Second)
		var state struct{ PID int }
		err := probe(bounded, entry.identity, entry.path, &state)
		stop()
		if err == nil {
			return fmt.Errorf("candidate process PID %d is already running; runner will not adopt or stop it", state.PID)
		}
		if !absent(err) {
			return fmt.Errorf("cannot establish candidate endpoint is absent: %w", err)
		}
	}
	return nil
}
func (n *Native) launch(ctx context.Context, role int, exe string, args []string) (supervisor.Worker, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env := map[string]string{}
	for k, v := range n.env {
		env[k] = v
	}
	env["DSH_WORK_EXECUTABLE"] = n.config.Runtime
	env["DSH_WORK_EMBEDDED_UI_INSTANCE"] = "1"
	env["DSH_WORK_WEBVIEW_DEBUG_PORT"] = "9333"
	if role == 2 {
		env["DSH_WORK_EMBEDDED_UI_INSTANCE"] = "2"
		env["DSH_WORK_WEBVIEW_DEBUG_PORT"] = "9343"
	}
	w, err := n.adapter.Start(ctx, supervisor.LaunchPlan{GenerationID: fmt.Sprintf("e67-runner-%d-%d", role, time.Now().UnixNano()), Executable: exe, Args: args, Env: env, WorkingDirectory: n.config.Workspace, ExpectedOrigin: "http://127.0.0.1:1"}, nil)
	if err != nil {
		return nil, err
	}
	go func() {
		for event := range w.Events() {
			n.log("child role=%d %s: %s", role, event.Stream, event.Text)
		}
	}()
	n.log("child started role=%d pid=%d created=%d", role, w.Diagnostics().PID, w.Diagnostics().ProcessStarted)
	return w, nil
}
func (n *Native) reap(ctx context.Context, instance int) error {
	p := n.get(instance)
	if p == nil {
		return nil
	}
	if live(p.worker) {
		return errors.New("owned process is still live")
	}
	if err := p.worker.WaitEmpty(ctx); err != nil {
		return fmt.Errorf("owned tree has not joined; owner retained: %w", err)
	}
	if err := p.worker.Close(); err != nil {
		return fmt.Errorf("owned supervisor close failed; owner retained: %w", err)
	}
	d := p.worker.Diagnostics()
	exit := p.worker.ExitResult()
	state := ProcessState{PID: d.PID, Started: d.ProcessStarted, TreeOwned: false, ExitCode: &exit.Code}
	var abnormal error
	if !exit.Started || exit.Code != 0 || exit.Err != "" {
		abnormal = fmt.Errorf("owned process role %d exited abnormally: %+v", instance, exit)
	}
	n.mu.Lock()
	if n.history == nil {
		n.history = map[int]ProcessState{}
	}
	n.history[instance] = state
	if n.children[instance] == p {
		delete(n.children, instance)
	}
	n.abnormal = errors.Join(n.abnormal, abnormal)
	n.mu.Unlock()
	return abnormal
}
func (n *Native) buildCandidate(ctx context.Context) error {
	for _, i := range []int{1, 2, 0} {
		if p := n.get(i); p != nil {
			if live(p.worker) {
				return errors.New("stop all managed UIs and daemon before building")
			}
			if err := n.reap(ctx, i); err != nil {
				return err
			}
		}
	}
	if err := n.foreignAbsent(ctx); err != nil {
		return err
	}
	n.mu.Lock()
	old := n.buildOwner
	n.mu.Unlock()
	if old != nil {
		if live(old) {
			return errors.New("previous build cleanup still owns a live process; inspect status")
		}
		if err := n.reapBuild(ctx); err != nil {
			return err
		}
	}
	n.mu.Lock()
	n.build = "building"
	n.buildLog = filepath.Join(n.config.Workspace, "bin", "e67-dev-runner", "runner.log")
	n.mu.Unlock()
	commands := []struct {
		exe  string
		args []string
	}{
		// Reusable MSBuild nodes outlive a successful root process, keeping its
		// supervised Job nonempty and preventing the next build stage.
		{n.tools["python"], []string{"-m", "cmake", "--build", "bin/embedded-cef-vs2022", "--config", "Release", "--target", "dsh_cef_broker", "--parallel", "8", "--", "/nodeReuse:false"}},
		{n.tools["powershell"], []string{"-NoProfile", "-File", filepath.Join(n.config.Workspace, "scripts", "start-embedded-development.ps1"), "-BuildOnly", "-Runtime", n.config.Runtime}},
	}
	for _, command := range commands {
		if err := ctx.Err(); err != nil {
			n.setBuildFailure()
			return err
		}
		w, err := n.launch(ctx, -1, command.exe, command.args)
		if err != nil {
			n.setBuildFailure()
			return err
		}
		n.mu.Lock()
		n.buildOwner = w
		n.mu.Unlock()
		select {
		case <-w.Exited():
		case <-ctx.Done():
			force, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err = errors.Join(ctx.Err(), w.ForceStop(force))
			stop()
		}
		// Preserve the root result before stopping any surviving descendants.
		exit := w.ExitResult()
		cleanup, stop := context.WithTimeout(context.Background(), 8*time.Second)
		joinErr := w.WaitEmpty(cleanup)
		stop()
		if errors.Is(joinErr, context.DeadlineExceeded) && !live(w) {
			d := w.Diagnostics()
			n.log("build root exited pid=%d exit=%d; stopping remaining owned Job processes active=%d", d.PID, exit.Code, d.ActiveProcesses)
			force, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			joinErr = w.ForceStop(force)
			cancel()
			if joinErr == nil {
				joined, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				joinErr = w.WaitEmpty(joined)
				cancel()
			}
		}
		if joinErr == nil {
			joinErr = w.Close()
		}
		if joinErr == nil {
			n.mu.Lock()
			n.buildOwner = nil
			n.mu.Unlock()
		}
		if !exit.Started {
			exit = w.ExitResult()
		}
		if err != nil || joinErr != nil || !exit.Started || exit.Code != 0 || exit.Err != "" {
			n.setBuildFailure()
			return errors.Join(err, joinErr, fmt.Errorf("build failed: exit=%d error=%s; see %s", exit.Code, exit.Err, n.buildLog))
		}
	}
	n.mu.Lock()
	n.build = "complete"
	n.mu.Unlock()
	return nil
}
func (n *Native) setBuildFailure() { n.mu.Lock(); n.build = "failed"; n.mu.Unlock() }
func (n *Native) startDaemon(ctx context.Context) error {
	if p := n.get(0); p != nil {
		if !live(p.worker) {
			if err := n.reap(ctx, 0); err != nil {
				return err
			}
		} else {
			return n.waitDaemon(ctx, p)
		}
	}
	// Never let ensureDaemon adopt an already-running candidate owned elsewhere.
	bounded, stop := context.WithTimeout(ctx, time.Second)
	var existing daemon.Snapshot
	err := probe(bounded, n.identity(), "/snapshot", &existing)
	stop()
	if err == nil {
		return fmt.Errorf("candidate daemon PID %d is not runner-owned", existing.PID)
	}
	if !absent(err) {
		return err
	}
	w, err := n.launch(ctx, 0, filepath.Join(n.config.Workspace, "bin", "dsh-work.exe"), []string{"--daemon"})
	if err != nil {
		return err
	}
	n.mu.Lock()
	n.children[0] = &owned{worker: w}
	n.mu.Unlock()
	return n.waitDaemon(ctx, n.get(0))
}
func (n *Native) waitDaemon(ctx context.Context, p *owned) error {
	err := n.wait(ctx, func() bool {
		if !live(p.worker) {
			return true
		}
		var state daemon.Snapshot
		bounded, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		return ownedProbe(bounded, n.identity(), "/snapshot", p, &state) == nil && state.PID == int(p.worker.Diagnostics().PID)
	})
	if err != nil {
		return err
	}
	if !live(p.worker) {
		return fmt.Errorf("owned daemon exited during startup: %+v", p.worker.ExitResult())
	}
	return nil
}

func (n *Native) startUI(ctx context.Context, instance int) error {
	n.mu.Lock()
	built := n.build == "complete"
	n.mu.Unlock()
	if !built {
		return errors.New("run build successfully before starting UIs")
	}
	if p := n.get(instance); p != nil {
		if live(p.worker) {
			return errors.New("UI is already running")
		}
		if err := n.reap(ctx, instance); err != nil {
			return err
		}
	}
	bounded, stop := context.WithTimeout(ctx, time.Second)
	var other struct{ PID int }
	err := probe(bounded, n.uiIdentity(instance), "/status", &other)
	stop()
	if err == nil {
		return fmt.Errorf("UI PID %d is not runner-owned", other.PID)
	}
	if !absent(err) {
		return err
	}
	if n.readyProbe == nil {
		port := "9333"
		if instance == 2 {
			port = "9343"
		}
		if connection, e := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond); n.readyProbe == nil && e == nil {
			connection.Close()
			return fmt.Errorf("debug port %s already has an unowned listener", port)
		}
	}
	if err = n.startDaemon(ctx); err != nil {
		return err
	}
	if p := n.get(0); p == nil || !live(p.worker) {
		return errors.New("owned daemon exited during startup")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	w, err := n.launch(ctx, instance, filepath.Join(n.config.Workspace, "bin", "dsh-work.exe"), nil)
	if err != nil {
		return err
	}
	p := &owned{worker: w}
	n.mu.Lock()
	n.children[instance] = p
	n.mu.Unlock()
	err = n.wait(ctx, func() bool {
		if !live(w) {
			return true
		}
		return n.observeReady(ctx, instance, p)
	})
	if err != nil {
		return fmt.Errorf("UI startup incomplete; process owner retained: %w", err)
	}
	if !live(w) {
		return fmt.Errorf("UI exited during startup: %+v", w.ExitResult())
	}
	return nil
}
func (n *Native) observeReady(ctx context.Context, instance int, p *owned) bool {
	var ui struct{ PID int }
	bounded, stop := context.WithTimeout(ctx, time.Second)
	err := ownedProbe(bounded, n.uiIdentity(instance), "/status", p, &ui)
	stop()
	if err != nil || ui.PID != int(p.worker.Diagnostics().PID) {
		return false
	}
	var state daemon.Snapshot
	bounded, stop = context.WithTimeout(ctx, time.Second)
	err = ownedProbe(bounded, n.identity(), "/snapshot", n.get(0), &state)
	stop()
	if err != nil || state.Status.State != "Ready" {
		return false
	}
	if n.readyProbe != nil {
		if !n.readyProbe(ctx, instance) {
			return false
		}
		n.mu.Lock()
		p.readyAt = time.Now().UTC().Format(time.RFC3339Nano)
		p.readyGeneration = state.Status.GenerationID
		n.mu.Unlock()
		return true
	}
	port := "9333"
	if instance == 2 {
		port = "9343"
	}
	bounded, stop = context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	if !webviewReady(bounded, port) {
		return false
	}
	n.mu.Lock()
	p.readyAt = time.Now().UTC().Format(time.RFC3339Nano)
	p.readyGeneration = state.Status.GenerationID
	n.mu.Unlock()
	return true
}
func (n *Native) wait(ctx context.Context, ready func() bool) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if ready() {
			return nil
		}
		timer := time.NewTimer(300 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func (n *Native) stopUI(ctx context.Context, instance int) error {
	p := n.get(instance)
	if p == nil {
		return nil
	}
	if !live(p.worker) {
		return n.reap(ctx, instance)
	}
	bounded, stop := context.WithTimeout(ctx, 3*time.Second)
	var status struct{ PID int }
	err := ownedProbe(bounded, n.uiIdentity(instance), "/status", p, &status)
	stop()
	if err != nil || status.PID != int(p.worker.Diagnostics().PID) {
		return fmt.Errorf("owned UI identity cannot be confirmed; no stop sent: %w", err)
	}
	if err = ownedProbe(ctx, n.uiIdentity(instance), "/stop", p, nil); err != nil {
		return err
	}
	select {
	case <-p.worker.Exited():
	case <-ctx.Done():
		return fmt.Errorf("normal UI stop incomplete; owner retained: %w", ctx.Err())
	}
	if err = n.reap(ctx, instance); err != nil {
		return err
	}
	exit := p.worker.ExitResult()
	if exit.Code != 0 || exit.Err != "" {
		return fmt.Errorf("UI exited abnormally: %+v", exit)
	}
	return nil
}
func (n *Native) stopAll(ctx context.Context) error {
	if err := n.reapBuild(ctx); err != nil {
		return err
	}
	p := n.get(0)
	if p != nil && live(p.worker) {
		var state daemon.Snapshot
		if err := ownedProbe(ctx, n.identity(), "/snapshot", p, &state); err != nil || state.PID != int(p.worker.Diagnostics().PID) {
			return errors.New("owned daemon identity cannot be confirmed; no quit sent")
		}
		owners := []expectedUIOwner{}
		for _, i := range []int{1, 2} {
			child := n.get(i)
			if child != nil && live(child.worker) {
				var ui struct{ PID int }
				if err := ownedProbe(ctx, n.uiIdentity(i), "/status", child, &ui); err != nil {
					return fmt.Errorf("UI endpoint ownership cannot be confirmed: %w", err)
				}
				d := child.worker.Diagnostics()
				owners = append(owners, expectedUIOwner{PID: uint32(d.PID), Started: d.ProcessStarted})
			} else {
				bounded, cancel := context.WithTimeout(ctx, time.Second)
				var ui struct{ PID int }
				err := probe(bounded, n.uiIdentity(i), "/status", &ui)
				cancel()
				if err == nil || !absent(err) {
					return errors.New("unowned candidate UI endpoint exists; no global quit sent")
				}
			}
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("bounded shutdown context required")
		}
		client := ownedClient(n.identity(), p)
		err := client.Call(ctx, "EmbeddedBrowserService", "QuitOwned", "workspace", []any{owners, deadline.UnixMilli()}, nil)
		client.Close()
		if err != nil {
			return err
		}
	} else {
		for _, i := range []int{1, 2} {
			if child := n.get(i); child != nil && live(child.worker) {
				if err := n.stopUI(ctx, i); err != nil {
					return err
				}
			}
		}
	}
	for _, i := range []int{1, 2, 0} {
		if child := n.get(i); child != nil {
			select {
			case <-child.worker.Exited():
			case <-ctx.Done():
				return fmt.Errorf("normal shutdown incomplete; owners retained: %w", ctx.Err())
			}
			if err := n.reap(ctx, i); err != nil {
				return err
			}
			exit := child.worker.ExitResult()
			if exit.Code != 0 || exit.Err != "" {
				return fmt.Errorf("owned process role %d exited abnormally: %+v", i, exit)
			}
		}
	}
	n.mu.Lock()
	history := n.abnormal
	n.mu.Unlock()
	if history != nil {
		return fmt.Errorf("RUNNER_CLEANUP_COMPLETED_ABNORMALLY: %w", history)
	}
	return nil
}
func (n *Native) Status(ctx context.Context) Status {
	n.mu.Lock()
	s := Status{PID: os.Getpid(), Workspace: n.config.Workspace, Tools: n.tools, Build: n.build, BuildLog: n.buildLog, Active: n.active, LastError: n.lastError, UIs: map[int]ProcessState{}}
	for i, state := range n.history {
		if i == 0 {
			s.Daemon = state
		} else {
			s.UIs[i] = state
		}
	}
	children := map[int]*owned{}
	for i, p := range n.children {
		children[i] = &owned{worker: p.worker, readyAt: p.readyAt, readyGeneration: p.readyGeneration}
	}
	n.mu.Unlock()
	var daemonState daemon.Snapshot
	daemonOK := false
	if p := children[0]; p != nil && live(p.worker) {
		bounded, stop := context.WithTimeout(ctx, time.Second)
		err := ownedProbe(bounded, n.identity(), "/snapshot", p, &daemonState)
		stop()
		daemonOK = err == nil && daemonState.PID == int(p.worker.Diagnostics().PID)
	}
	for i, p := range children {
		d := p.worker.Diagnostics()
		state := ProcessState{PID: d.PID, Started: d.ProcessStarted, Alive: live(p.worker), TreeOwned: true, ActiveProcesses: d.ActiveProcesses, ReadyAt: p.readyAt}
		if !state.Alive {
			exit := p.worker.ExitResult()
			if exit.Started {
				state.ExitCode = &exit.Code
			}
		}
		if i == 0 {
			state.IPCReady = daemonOK
			state.Ready = daemonOK && daemonState.Status.State == "Ready"
			s.Daemon = state
		} else {
			var ui struct{ PID int }
			if state.Alive {
				bounded, stop := context.WithTimeout(ctx, time.Second)
				err := ownedProbe(bounded, n.uiIdentity(i), "/status", p, &ui)
				stop()
				state.IPCReady = err == nil && ui.PID == int(d.PID)
			}
			state.Ready = state.IPCReady && daemonOK && daemonState.Status.State == "Ready" && n.observeReady(ctx, i, p)
			state.ReadyAt = p.readyAt
			s.UIs[i] = state
		}
	}
	return s
}

func (n *Native) reapBuild(ctx context.Context) error {
	n.mu.Lock()
	w := n.buildOwner
	n.mu.Unlock()
	if w == nil {
		return nil
	}
	if live(w) {
		return errors.New("build is still active; cleanup owner retained")
	}
	// A failed earlier join can retain reusable build-tool descendants after
	// the root exits. Stop only this owned Job, then prove it empty before
	// releasing ownership; a root exit alone is not cleanup completion.
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := w.ForceStop(ctx); err != nil {
		return fmt.Errorf("build tree cleanup stop failed; owner retained: %w", err)
	}
	if err := w.WaitEmpty(ctx); err != nil {
		return fmt.Errorf("build tree cleanup unfinished: %w", err)
	}
	if err := w.Close(); err != nil {
		return err
	}
	n.mu.Lock()
	if n.buildOwner == w {
		n.buildOwner = nil
	}
	n.mu.Unlock()
	return nil
}

func (n *Native) CleanupComplete() bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.children) == 0 && n.buildOwner == nil
}

// The launcher supplies its actual process image, not a PATH alias. Preserve
// that interpreter's normal execution-policy behavior in build subprocesses.
func launcherPowerShell(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("launcher PowerShell absolute executable required; restart using -Runner")
	}
	name := filepath.Base(path)
	if !strings.EqualFold(name, "pwsh.exe") && !strings.EqualFold(name, "powershell.exe") {
		return "", errors.New("launcher PowerShell executable required")
	}
	resolved, err := exec.LookPath(path)
	if err != nil {
		return "", err
	}
	return resolved, nil
}
