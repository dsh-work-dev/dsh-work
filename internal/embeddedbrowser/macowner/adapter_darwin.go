//go:build darwin && cgo

package macowner

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
	"golang.org/x/sys/unix"
)

const Version = "156.0.15+gcf671c0+chromium-156.0.8078.80"

type Publication struct {
	Kind       string   `json:"kind"`
	Version    string   `json:"version"`
	App        string   `json:"app"`
	Executable string   `json:"executable"`
	Owner      string   `json:"owner"`
	Helpers    []string `json:"helpers"`
	Sandbox    bool     `json:"sandbox"`
	verified   bool
}

// OpenPublication accepts the installed component selected by trusted app
// configuration, never a page/tool/DSH-supplied executable or addon path.
func OpenPublication(ctx context.Context, path string) (Publication, error) {
	var p Publication
	version, err := unix.Sysctl("kern.osproductversion")
	major, _ := strconv.Atoi(strings.Split(version, ".")[0])
	if err != nil || runtime.GOARCH != "arm64" || major < 15 {
		return p, errors.New("MAC_CEF_PLATFORM_UNAVAILABLE")
	}
	if !filepath.IsAbs(path) {
		return p, errors.New("absolute CEF publication required")
	}
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil || len(data) > 65536 {
		return p, errors.New("CEF publication manifest rejected")
	}
	if err = json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if p.Kind != "dsh-owned-cef-mac" || p.Version != Version || !p.Sandbox || len(p.Helpers) == 0 {
		return p, errors.New("CEF publication identity rejected")
	}
	if p.Executable != filepath.Join(p.App, "Contents", "MacOS", "cefsimple") || p.Owner != filepath.Join(p.App, "Contents", "MacOS", "dsh-cef-owner") {
		return p, errors.New("CEF publication entry points rejected")
	}
	for _, exe := range append([]string{p.Executable, p.Owner}, p.Helpers...) {
		if !filepath.IsAbs(exe) || !strings.HasPrefix(exe, p.App+string(os.PathSeparator)) {
			return p, errors.New("CEF component path outside publication")
		}
		info, e := os.Stat(exe)
		if e != nil || !info.Mode().IsRegular() {
			return p, errors.New("CEF component asset missing")
		}
	}
	check, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err = exec.CommandContext(check, "/usr/bin/codesign", "--verify", "--deep", "--strict", p.App).Run(); err != nil {
		return p, errors.New("CEF publication signature rejected")
	}
	p.verified = true
	return p, nil
}

type Adapter struct {
	mu                 sync.Mutex
	publication        Publication
	profile, directory string
	worker             *Worker
	mcp                *mcpConfig
}

// New is per-profile and CEF-specific. It deliberately does not become the
// application's default Supervisor Adapter for ordinary DSH Workers.
func New(p Publication, profile string) (*Adapter, error) {
	if !p.verified || p.Kind != "dsh-owned-cef-mac" || p.Version != Version || !filepath.IsAbs(profile) {
		return nil, errors.New("owned CEF configuration required")
	}
	if err := os.MkdirAll(profile, 0700); err != nil {
		return nil, err
	}
	actual, err := filepath.EvalSymlinks(profile)
	if err != nil {
		return nil, err
	}
	if err = privateDir(actual); err != nil {
		return nil, err
	}
	dir := filepath.Join(actual, ".dsh-cef-owner")
	if err = privateDir(dir); err != nil {
		return nil, err
	}
	return &Adapter{publication: p, profile: actual, directory: dir}, nil
}

func escaped(text string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(text))
	return b.String()
}
func service(job string) string { return fmt.Sprintf("gui/%d/%s", os.Geteuid(), job) }
func launchctl(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, "/bin/launchctl", args...).Output()
}

var jobPID = regexp.MustCompile(`(?m)^\s*pid = ([0-9]+)\s*$`)

func freshRecoveryState(dir string, key []byte, original journal) (journal, error) {
	latest, err := loadGeneration(dir, key, original.Generation)
	if err != nil {
		return journal{}, errors.Join(ErrCleanupPending, err)
	}
	if latest.Job != original.Job || latest.Component != original.Component || latest.Owner.PID != original.Owner.PID || latest.Owner.Started != original.Owner.Started || (original.Broker.PID != 0 && (latest.Broker.PID != original.Broker.PID || latest.Broker.Started != original.Broker.Started)) {
		return journal{}, ErrCleanupPending
	}
	return latest, nil
}
func recoverState(ctx context.Context, dir string, key []byte, original journal) error {
	for {
		// A guard can publish the real exit/expanded inventory while recovery waits
		// for its death. Never replace that terminal record with our older snapshot.
		s, err := freshRecoveryState(dir, key, original)
		if err != nil {
			return err
		}
		// Only the pinned CDP-attached Node/stdio component keeps all spawn paths
		// in the LaunchAgent's inherited PGID. CEF retains its quiescent barrier.
		mcpRecoverable := s.Component == "owned-cef-mcp" && (s.Phase == "mcp-running" || s.Phase == "mcp-retiring")
		if !mcpRecoverable && s.Phase != "empty" && s.Phase != "quiescent" && s.Phase != "starting" {
			return ErrCleanupPending
		}
		owner, e := instance(s.Owner)
		if e != nil {
			return errors.Join(ErrCleanupPending, e)
		}
		if owner.State == "gone" || owner.State == "reused" {
			// Death is the changed premise: the final write may have happened
			// after the read above. This post-death record cannot be overtaken
			// by another write from that original guard instance.
			s, e = freshRecoveryState(dir, key, original)
			if e != nil {
				return e
			}
			mcpRecoverable = s.Component == "owned-cef-mcp" && (s.Phase == "mcp-running" || s.Phase == "mcp-retiring")
			if !mcpRecoverable && s.Phase != "empty" && s.Phase != "quiescent" && s.Phase != "starting" {
				return ErrCleanupPending
			}
			if s.Phase == "empty" {
				return nil
			}
			ready, e := empty(s, false)
			if e != nil {
				return errors.Join(ErrCleanupPending, e)
			}
			if ready {
				s.Phase = "empty"
				// Full tree-empty is not a wait-status observation. If the
				// guard died before its waiter persisted a result, report the
				// known started broker's exit as unknown, never default zero.
				if s.Broker.PID != 0 && !s.Exit.Started {
					s.Exit = supervisor.ExitResult{Started: true, Code: -1, Err: "MAC_CEF_BROKER_EXIT_UNOBSERVED"}
				}
				return save(dir, key, s)
			}
		}
		select {
		case <-ctx.Done():
			return errors.Join(ErrCleanupPending, ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func recoverGeneration(ctx context.Context, dir string, key []byte) error {
	s, err := load(dir, key)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	return recoverState(ctx, dir, key, s)
}
func (w *Worker) recover(ctx context.Context) error {
	w.mu.Lock()
	id := w.state.Generation
	w.mu.Unlock()
	s, err := loadGeneration(w.directory, w.key, id)
	if err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	if s.Job != w.job {
		return ErrCleanupPending
	}
	if err = recoverState(ctx, w.directory, w.key, s); err != nil {
		return err
	}
	terminal, err := loadGeneration(w.directory, w.key, id)
	if err != nil || terminal.Job != w.job || terminal.Phase != "empty" {
		return errors.Join(ErrCleanupPending, err)
	}
	// The resource owner may delete its temporary generated-code directory
	// immediately after Close. Retain the authenticated completion snapshot,
	// not the key or raw journal, for Diagnostics and safe final evidence.
	w.mu.Lock()
	w.state = terminal
	w.mu.Unlock()
	return nil
}

func (a *Adapter) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (result supervisor.Worker, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if a.mcp == nil {
		profile, profileErr := filepath.EvalSymlinks(plan.Env["DSH_WORK_CEF_PROFILE"])
		if profileErr != nil || plan.Executable != a.publication.Executable || len(plan.Args) != 0 || plan.WorkingDirectory != filepath.Dir(a.publication.Executable) || profile != a.profile {
			return nil, errors.New("MAC_CEF_LAUNCH_PLAN_REJECTED")
		}
	} else if !sameJobFile(plan.Executable, a.mcp.Node) || len(plan.Args) != 1 || !sameJobFile(plan.Args[0], a.mcp.Adapter) {
		return nil, errors.New("MAC_MCP_LAUNCH_PLAN_REJECTED")
	}
	if plan.ExpectedOrigin != workeripc.Origin || !regexp.MustCompile(`^cef-[A-Za-z0-9]+$`).MatchString(plan.GenerationID) {
		return nil, errors.New("MAC_CEF_LAUNCH_PLAN_REJECTED")
	}
	plan.Env = maps.Clone(plan.Env)
	if a.mcp == nil {
		plan.Env["DSH_WORK_CEF_PROFILE"] = a.profile
	}
	if a.worker != nil {
		if err := a.worker.Close(); err != nil {
			return nil, err
		}
		a.worker = nil
	}
	key, err := ownerKey(a.directory)
	if err != nil {
		return nil, err
	}
	if err = a.recoverAttempts(ctx, key); err != nil {
		return nil, err
	}
	if err = recoverGeneration(ctx, a.directory, key); err != nil {
		return nil, err
	}
	run, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-cef-")
	if err != nil {
		return nil, err
	}
	job := "dev.dsh.cef." + strings.ToLower(rand.Text())
	w := &Worker{directory: a.directory, key: key, job: job, run: run, exited: make(chan struct{}), empty: make(chan struct{}), events: make(chan supervisor.OutputEvent, 16), raw: raw, readDone: make(chan struct{}), state: journal{Generation: plan.GenerationID}}
	w.attempt = &launchAttempt{Generation: plan.GenerationID, Job: job, Run: run, Program: a.publication.Owner}
	a.worker = w
	defer func() {
		if w.attempt != nil && !w.attempt.Submitted {
			// No launchctl invocation has occurred; only this attempt's files exist.
			cleanupErr := w.removeRun()
			if cleanupErr == nil {
				cleanupErr = w.clearAttempt()
			}
			if cleanupErr == nil {
				a.worker = nil
			} else {
				retErr = errors.Join(retErr, ErrCleanupPending, cleanupErr)
			}
		} else if w.attempt != nil && w.attempt.Submitted && !w.handedOff && retErr != nil {
			retErr = errors.Join(ErrCleanupPending, retErr)
		}
	}()
	// launchd resolves the submitted plist's filesystem path. Keep our actual
	// run path canonical too; /tmp is a symlink on macOS. Ownership was already
	// installed above so failed resolution still has an exact cleanup owner.
	canonicalRun, e := filepath.EvalSymlinks(run)
	if e != nil {
		return nil, e
	}
	run = canonicalRun
	w.run = run
	w.attempt.Run = run
	host, err := process(uint32(os.Getpid()))
	if err != nil {
		return nil, err
	}
	cfg := launchConfig{Directory: a.directory, Profile: a.profile, Socket: filepath.Join(run, "owner.sock"), Secret: rand.Text(), Host: host, Helpers: a.publication.Helpers, Plan: plan, Job: job, MCP: a.mcp}
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(run, "launch.json")
	if err = writePrivate(configPath, data); err != nil {
		return nil, err
	}
	plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>` + escaped(job) + `</string><key>Program</key><string>` + escaped(a.publication.Owner) + `</string><key>ProgramArguments</key><array><string>` + escaped(a.publication.Owner) + `</string><string>--guard</string><string>` + escaped(configPath) + `</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><false/><key>AbandonProcessGroup</key><false/><key>ExitTimeOut</key><integer>3</integer><key>StandardOutPath</key><string>/dev/null</string><key>StandardErrorPath</key><string>/dev/null</string></dict></plist>`
	plistPath := filepath.Join(run, "owner.plist")
	if err = writePrivate(plistPath, []byte(plist)); err != nil {
		return nil, err
	}
	// Commit ownership before invoking a command whose cancelled/error return
	// does not establish whether launchd accepted the job.
	if err = w.sealAttemptFiles(); err != nil {
		return nil, err
	}
	w.attempt.Submitted = true
	if err = w.saveAttempt(); err != nil {
		w.attempt.Submitted = false
		return nil, err
	}
	if _, err = launchctl(ctx, "bootstrap", fmt.Sprintf("gui/%d", os.Geteuid()), plistPath); err != nil {
		return nil, fmt.Errorf("MAC_CEF_LAUNCHAGENT_FAILED: %w", err)
	}
	var conn net.Conn
	for ctx.Err() == nil {
		status, e := launchctl(ctx, "print", service(job))
		if e == nil {
			match := jobPID.FindSubmatch(status)
			if len(match) == 2 {
				pid, _ := strconv.ParseUint(string(match[1]), 10, 32)
				owner, e := process(uint32(pid))
				if e == nil && owner.State == "live" && owner.Path == a.publication.Owner {
					socketBefore, _ := os.Lstat(cfg.Socket)
					conn, e = localipc.Dial(ctx, cfg.Socket)
					if e == nil {
						peer, e := localipc.Peer(conn)
						socketAfter, statErr := os.Lstat(cfg.Socket)
						if e == nil && peer.PID == owner.PID && peer.Started == owner.Started && socketBefore != nil && statErr == nil && os.SameFile(socketBefore, socketAfter) {
							w.owner = owner
							w.socketInfo = socketBefore
							break
						}
						conn.Close()
						conn = nil
					}
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(25 * time.Millisecond):
		}
	}
	if conn == nil {
		return nil, errors.Join(ErrCleanupPending, ctx.Err())
	}
	w.conn = conn
	ready := false
	defer func() {
		if !ready {
			conn.Close()
		}
	}()
	if st, ok := w.socketInfo.Sys().(*syscall.Stat_t); ok {
		w.attempt.SocketDevice = uint64(st.Dev)
		w.attempt.SocketInode = st.Ino
	}
	w.attempt.Owner = w.owner
	w.attempt.Hello = true
	if err = w.saveAttempt(); err != nil {
		return nil, errors.Join(ErrCleanupPending, err)
	}
	if err = writeFrame(conn, message{Kind: "daemon", Generation: plan.GenerationID, Secret: cfg.Secret}); err != nil {
		conn.Close()
		return nil, errors.Join(ErrCleanupPending, err)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	for {
		var m message
		if err = readFrame(conn, &m); err != nil {
			conn.Close()
			return nil, errors.Join(ErrCleanupPending, err)
		}
		if m.State == nil || m.State.Generation != plan.GenerationID || m.State.Owner.PID != w.owner.PID || m.State.Owner.Started != w.owner.Started {
			conn.Close()
			return nil, ErrCleanupPending
		}
		w.state = *m.State
		expectedVersion := Version
		if a.mcp != nil {
			expectedVersion = mcpVersion
		}
		if m.Kind == "runtime" && m.State.RuntimeReady && m.State.RuntimeVersion == expectedVersion {
			durable, e := loadGeneration(w.directory, w.key, plan.GenerationID)
			if e != nil || durable.Job != w.job || durable.Owner.PID != w.owner.PID || durable.Owner.Started != w.owner.Started || !durable.RuntimeReady || durable.RuntimeVersion != expectedVersion {
				return nil, errors.Join(ErrCleanupPending, e)
			}
			w.handedOff = true // durable attempt remains until complete job/run retirement.
			ready = true
			w.readStarted = true
			go w.receive()
			return w, nil
		}
		if m.Kind == "pending" {
			conn.Close()
			return nil, ErrCleanupPending
		}
	}
}

type Worker struct {
	mu, write, close    sync.Mutex
	conn                net.Conn
	directory, job, run string
	key                 []byte
	owner               Process
	state               journal
	exited, empty       chan struct{}
	exitOnce, emptyOnce sync.Once
	events              chan supervisor.OutputEvent
	readDone            chan struct{}
	readStarted         bool
	raw                 supervisor.RawOutputHandler
	err                 error
	retired, jobRetired bool
	runRetired          bool
	socketInfo          os.FileInfo
	attempt             *launchAttempt
	handedOff           bool
	jobMeasurement      jobMeasurement
}

func (w *Worker) receive() {
	defer close(w.readDone)
	defer close(w.events)
	for {
		var m message
		if err := readFrame(w.conn, &m); err != nil {
			w.mu.Lock()
			w.err = errors.Join(ErrCleanupPending, err)
			w.mu.Unlock()
			w.observeFailure()
			return
		}
		w.mu.Lock()
		valid := m.State != nil && m.State.Generation == w.state.Generation && m.State.Owner.PID == w.owner.PID && m.State.Owner.Started == w.owner.Started
		if valid {
			w.state = *m.State
			if m.Kind == "pending" {
				w.err = ErrCleanupPending
			}
			if m.Kind == "exited" {
				w.exitOnce.Do(func() { close(w.exited) })
			}
		}
		w.mu.Unlock()
		if !valid {
			w.conn.Close()
			w.observeFailure()
			return
		}
		if w.raw != nil {
			w.raw(supervisor.StreamStdout, "MAC_CEF_OWNER_"+m.Kind)
		}
		select {
		case w.events <- supervisor.OutputEvent{Stream: supervisor.StreamStdout, Text: "MAC_CEF_OWNER_" + m.Kind}:
		default:
		}
		if m.Kind == "empty" {
			w.observeFailure()
			return
		}
	}
}
func (w *Worker) observeFailure() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		w.mu.Lock()
		broker := w.state.Broker
		w.mu.Unlock()
		if broker.PID != 0 {
			if p, e := instance(broker); e == nil && (p.State == "gone" || p.State == "reused") {
				w.exitOnce.Do(func() { close(w.exited) })
			}
		}
		if err := w.recover(ctx); err == nil {
			w.mu.Lock()
			w.err = nil
			w.mu.Unlock()
			w.emptyOnce.Do(func() { close(w.empty) })
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}
func (w *Worker) command(kind string) error {
	w.write.Lock()
	defer w.write.Unlock()
	if w.conn == nil {
		return ErrCleanupPending
	}
	return writeFrame(w.conn, struct {
		Kind string `json:"kind"`
	}{kind})
}
func (w *Worker) Events() <-chan supervisor.OutputEvent { return w.events }
func (w *Worker) Exited() <-chan struct{}               { return w.exited }
func (w *Worker) ExitResult() supervisor.ExitResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state.Exit
}
func (w *Worker) RequestStop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.command("stop")
}
func (w *Worker) ForceStop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return w.command("force")
}
func (w *Worker) WaitEmpty(ctx context.Context) error {
	for {
		select {
		case <-w.empty:
			return nil
		default:
		}
		err := w.recover(ctx)
		if err == nil {
			w.emptyOnce.Do(func() { close(w.empty) })
			return nil
		}
		w.mu.Lock()
		failure := w.err
		w.mu.Unlock()
		owner, e := instance(w.owner)
		if e != nil || owner.State == "gone" || owner.State == "reused" || failure != nil {
			return errors.Join(ErrCleanupPending, err, e, failure)
		}
		select {
		case <-ctx.Done():
			return errors.Join(ErrCleanupPending, ctx.Err())
		case <-w.empty:
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (w *Worker) Diagnostics() supervisor.Diagnostics {
	w.mu.Lock()
	defer w.mu.Unlock()
	d := supervisor.Diagnostics{PID: w.state.Broker.PID, ProcessStarted: w.state.Broker.Started, Exit: w.state.Exit}
	for _, r := range w.state.Owned {
		p, e := instance(r.Process)
		if e != nil || p.State != "gone" && p.State != "reused" {
			d.ActiveProcesses++
		}
	}
	return d
}
func (w *Worker) Close() error {
	w.close.Lock()
	defer w.close.Unlock()
	if w.retired {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if w.attempt != nil && !w.handedOff {
		return w.closeAttempt(ctx)
	}
	if err := w.WaitEmpty(ctx); err != nil {
		return err
	}
	if w.conn != nil {
		w.conn.Close()
	}
	if w.readStarted {
		select {
		case <-w.readDone:
		case <-ctx.Done():
			return errors.Join(ErrCleanupPending, ctx.Err())
		}
	}
	if !w.jobRetired {
		if _, err := w.verifiedJob(ctx); err != nil {
			return err
		}
		if _, err := launchctl(ctx, "bootout", service(w.job)); err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
		w.jobRetired = true
		if w.attempt != nil {
			w.attempt.JobRetired = true
			w.attempt.Owner = w.owner
			if err := w.saveAttempt(); err != nil {
				return errors.Join(ErrCleanupPending, err)
			}
		}
	}
	if err := w.removeRun(); err != nil {
		return err
	}
	if w.attempt != nil {
		if err := w.clearAttempt(); err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
	}
	w.retired = true
	return nil
}

func (w *Worker) removeRun() error {
	if current, err := os.Lstat(filepath.Join(w.run, "owner.sock")); err == nil {
		if w.socketInfo == nil || !os.SameFile(w.socketInfo, current) {
			return errors.New("MAC_CEF_OWNER_ENDPOINT_REPLACED")
		}
		if err = os.Remove(filepath.Join(w.run, "owner.sock")); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, name := range []string{"launch.json", "owner.plist", "owner.sock.lock", "mcp-bootstrap.json", "mcp-bootstrap.mjs"} {
		if err := os.Remove(filepath.Join(w.run, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.Remove(w.run); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	w.runRetired = true
	return nil
}
