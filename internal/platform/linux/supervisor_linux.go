//go:build linux

// Package linux implements native process ownership without changing the
// platform-neutral launch contract. Unsupported facilities fail closed.
package linux

import (
	"bufio"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	bus "github.com/godbus/dbus/v5"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/redact"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/unix"
)

const unavailable = "LINUX_SUPERVISOR_UNAVAILABLE"

type SystemdAdapter struct {
	mu      sync.Mutex
	pending map[string]*systemdWorker
}

func NewSystemdAdapter() supervisor.Adapter { return &SystemdAdapter{} }

// manager deliberately ignores a private DBUS_SESSION_BUS_ADDRESS. The GTK
// session bus is not necessarily the systemd user-manager bus.
func manager(ctx context.Context) (*dbus.Conn, error) {
	runtime := os.Getenv("XDG_RUNTIME_DIR")
	if runtime == "" {
		runtime = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	info, err := os.Stat(runtime)
	if err != nil || !filepath.IsAbs(runtime) || !info.IsDir() || info.Mode().Perm() != 0700 {
		return nil, fmt.Errorf("%s: private runtime directory required", unavailable)
	}
	if s, ok := info.Sys().(*syscall.Stat_t); !ok || s.Uid != uint32(os.Geteuid()) {
		return nil, fmt.Errorf("%s: runtime directory owner rejected", unavailable)
	}
	address := "unix:path=" + url.PathEscape(filepath.Join(runtime, "bus"))
	return dbus.NewConnection(func() (*bus.Conn, error) {
		c, err := bus.Dial(address)
		if err != nil {
			return nil, err
		}
		stop := context.AfterFunc(ctx, func() { c.Close() })
		defer stop()
		if err = c.Auth([]bus.Auth{bus.AuthExternal(strconv.Itoa(os.Geteuid()))}); err == nil {
			err = c.Hello()
		}
		if err != nil {
			c.Close()
			return nil, err
		}
		return c, nil
	})
}

type systemdWorker struct {
	mu, cleanup    sync.Mutex
	conn           *dbus.Conn
	unit, group    string
	groupFD        *os.File
	anchorInput    *os.File
	anchorPID      uint32
	cmd            *exec.Cmd
	pidfd          int
	pid            uint32
	started        uint64
	startedError   string
	exited         chan struct{}
	events         chan supervisor.OutputEvent
	readers        sync.WaitGroup
	readersDone    chan struct{}
	stdout, stderr string
	exit           supervisor.ExitResult
	closed         bool
}

func property(name string, value any) dbus.Property {
	return dbus.Property{Name: name, Value: bus.MakeVariant(value)}
}

func (a *SystemdAdapter) Start(ctx context.Context, plan supervisor.LaunchPlan, raw supervisor.RawOutputHandler) (worker supervisor.Worker, err error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(plan.Executable) || !filepath.IsAbs(plan.WorkingDirectory) {
		return nil, errors.New("absolute Linux launch paths required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := a.Close(); err != nil {
		return nil, fmt.Errorf("prior Linux startup cleanup unfinished: %w", err)
	}
	c, err := manager(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: user manager: %w", unavailable, err)
	}
	w := &systemdWorker{conn: c, unit: "dsh-owned-" + strings.ToLower(rand.Text()) + ".service", pidfd: -1,
		exited: make(chan struct{}), readersDone: make(chan struct{}), events: make(chan supervisor.OutputEvent, 128)}
	anchorRead, anchorWrite, err := os.Pipe()
	if err != nil {
		c.Close()
		return nil, err
	}
	w.anchorInput = anchorWrite
	// A standard cat holds a private EOF lease. On daemon death EOF makes the
	// manager deactivate this unit and reclaim its complete cgroup. The actual
	// child is still launched by os/exec with exactly plan.Env, not manager Env.
	defer anchorRead.Close()
	failed := true
	defer func() {
		if failed {
			cleanupErr := w.abandonAnchor()
			if cleanupErr != nil {
				a.mu.Lock()
				if a.pending == nil {
					a.pending = map[string]*systemdWorker{}
				}
				a.pending[w.unit] = w
				a.mu.Unlock()
			}
			err = errors.Join(err, cleanupErr)
		}
	}()
	cat, err := exec.LookPath("cat")
	if err != nil {
		return nil, err
	}
	props := []dbus.Property{property("Description", w.unit), property("Type", "exec"), property("Delegate", true),
		property("KillMode", "control-group"), property("TimeoutStopUSec", uint64(3*time.Second/time.Microsecond)),
		property("StandardInputFileDescriptor", bus.UnixFD(anchorRead.Fd())), property("StandardOutput", "null"), property("StandardError", "null"),
		dbus.PropExecStart([]string{cat}, false)}
	ready := make(chan string, 1)
	if _, err = c.StartTransientUnitContext(ctx, w.unit, "fail", props, ready); err != nil {
		return nil, fmt.Errorf("%s: transient unit: %w", unavailable, err)
	}
	select {
	case status := <-ready:
		if status != "done" {
			return nil, fmt.Errorf("%s: unit start %s", unavailable, status)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	service, err := c.GetAllPropertiesContext(ctx, w.unit)
	if err != nil {
		return nil, err
	}
	w.anchorPID, _ = service["MainPID"].(uint32)
	delegated, _ := service["Delegate"].(bool)
	group, _ := service["ControlGroup"].(string)
	if w.anchorPID == 0 || !delegated || !strings.HasPrefix(group, "/") || filepath.Clean(group) != group || !strings.HasSuffix(group, "/"+w.unit) {
		return nil, fmt.Errorf("%s: delegated owned cgroup unavailable", unavailable)
	}
	w.group = filepath.Join("/sys/fs/cgroup", group)
	fd, err := unix.Open(w.group, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	w.groupFD = os.NewFile(uintptr(fd), "owned-cgroup")
	cmd := exec.Command(plan.Executable, plan.Args...)
	cmd.Dir = plan.WorkingDirectory
	cmd.Env = make([]string, 0, len(plan.Env))
	for key, value := range plan.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: fd, PidFD: &w.pidfd}
	stdout, stdoutWrite, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer stdoutWrite.Close()
	stderr, stderrWrite, err := os.Pipe()
	if err != nil {
		stdout.Close()
		return nil, err
	}
	defer stderrWrite.Close()
	cmd.Stdout = stdoutWrite
	cmd.Stderr = stderrWrite
	if err = ctx.Err(); err == nil {
		err = cmd.Start()
	}
	if err != nil {
		stdout.Close()
		stderr.Close()
		return nil, fmt.Errorf("%s: atomic cgroup/pidfd launch: %w", unavailable, err)
	}
	w.cmd = cmd
	w.pid = uint32(cmd.Process.Pid)
	w.started, err = localipc.ProcessStarted(w.pid)
	if err == nil {
		err = unix.PidfdSendSignal(w.pidfd, 0, nil, 0)
	}
	if err != nil {
		w.started = 0
		w.startedError = err.Error()
	}
	w.exit = supervisor.ExitResult{Started: true}
	w.readers.Add(2)
	go w.output(supervisor.StreamStdout, stdout, raw)
	go w.output(supervisor.StreamStderr, stderr, raw)
	go func() {
		err := cmd.Wait()
		result := supervisor.ExitResult{Started: true}
		if cmd.ProcessState != nil {
			result.Code = cmd.ProcessState.ExitCode()
		}
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				result.Err = err.Error()
			}
		}
		w.mu.Lock()
		w.exit = result
		w.mu.Unlock()
		close(w.exited)
		w.readers.Wait()
		close(w.events)
		close(w.readersDone)
	}()
	failed = false
	return w, nil
}

// Startup failure cannot leave an unknown late unit: EOF already belongs to
// the manager-held read descriptor, even if its D-Bus response was lost.
func (w *systemdWorker) abandonAnchor() error {
	if w.anchorInput != nil {
		w.anchorInput.Close()
		w.anchorInput = nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	properties, err := w.conn.GetAllPropertiesContext(ctx, w.unit)
	if err != nil && !missingUnit(err) {
		return err
	}
	if err == nil {
		if properties["Description"] != w.unit {
			return errors.New("startup cleanup unit ownership changed")
		}
		finished := make(chan string, 1)
		if _, err = w.conn.StopUnitContext(ctx, w.unit, "replace", finished); err != nil && !missingUnit(err) {
			return err
		}
		if err == nil {
			select {
			case status := <-finished:
				if status != "done" {
					return fmt.Errorf("startup cleanup %s", status)
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if w.groupFD != nil {
		w.groupFD.Close()
	}
	w.conn.Close()
	return nil
}

func missingUnit(err error) bool {
	var e bus.Error
	return errors.As(err, &e) && (e.Name == "org.freedesktop.systemd1.NoSuchUnit" || e.Name == "org.freedesktop.DBus.Error.UnknownObject")
}

// Retain a failed startup's unit owner until its real cleanup succeeds. No
// application child was returned in this branch; EOF still deactivates cat.
func (a *SystemdAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var result error
	for name, w := range a.pending {
		if err := w.abandonAnchor(); err != nil {
			result = errors.Join(result, err)
		} else {
			delete(a.pending, name)
		}
	}
	return result
}

func (w *systemdWorker) output(stream supervisor.OutputStream, r io.ReadCloser, raw supervisor.RawOutputHandler) {
	defer w.readers.Done()
	defer r.Close()
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), 64*1024)
	for s.Scan() {
		line := s.Text()
		if raw != nil {
			raw(stream, line)
		}
		text := redact.Secrets(line)
		w.mu.Lock()
		tail := &w.stdout
		if stream == supervisor.StreamStderr {
			tail = &w.stderr
		}
		*tail += text + "\n"
		if len(*tail) > 64*1024 {
			*tail = (*tail)[len(*tail)-64*1024:]
		}
		w.mu.Unlock()
		select {
		case w.events <- supervisor.OutputEvent{Stream: stream, Text: text}:
		default:
		}
	}
}
func (w *systemdWorker) Events() <-chan supervisor.OutputEvent { return w.events }
func (w *systemdWorker) Exited() <-chan struct{}               { return w.exited }
func (w *systemdWorker) ExitResult() supervisor.ExitResult {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.exit
}

func (w *systemdWorker) population() (uint32, error) {
	data, err := os.ReadFile(filepath.Join(w.group, "cgroup.events"))
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 1, err
	}
	if strings.Contains(string(data), "populated 0\n") {
		return 0, nil
	}
	// The kernel population bit covers nested cgroups. A nonzero diagnostic
	// means at least one process, not a fabricated exact descendant count.
	return 1, nil
}
func (w *systemdWorker) onlyAnchor() (bool, error) {
	only := true
	err := filepath.WalkDir(w.group, func(path string, d os.DirEntry, e error) error {
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if d.IsDir() || d.Name() != "cgroup.procs" {
			return nil
		}
		data, e := os.ReadFile(path)
		if errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		for _, p := range strings.Fields(string(data)) {
			if p != strconv.FormatUint(uint64(w.anchorPID), 10) {
				only = false
			}
		}
		return nil
	})
	return only, err
}
func (w *systemdWorker) WaitEmpty(ctx context.Context) error {
	for {
		active, err := w.population()
		if err != nil {
			return err
		}
		if active == 0 {
			return nil
		}
		select {
		case <-w.exited:
			only, err := w.onlyAnchor()
			if err != nil {
				return err
			}
			if only {
				w.mu.Lock()
				if w.anchorInput != nil {
					err = w.anchorInput.Close()
					w.anchorInput = nil
				}
				w.mu.Unlock()
				if err != nil {
					return err
				}
			}
		default:
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}
func (w *systemdWorker) RequestStop(ctx context.Context) error {
	if err := w.verifyUnit(ctx); err != nil {
		return err
	}
	finished := make(chan string, 1)
	_, err := w.conn.StopUnitContext(ctx, w.unit, "replace", finished)
	if err != nil {
		if n, e := w.population(); e == nil && n == 0 {
			return nil
		}
		return err
	}
	select {
	case value := <-finished:
		if value != "done" {
			return fmt.Errorf("unit stop %s", value)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (w *systemdWorker) ForceStop(ctx context.Context) error {
	if n, e := w.population(); e == nil && n == 0 {
		return nil
	}
	if err := w.verifyUnit(ctx); err != nil {
		return err
	}
	return w.conn.KillUnitWithTarget(ctx, w.unit, dbus.All, int32(unix.SIGKILL))
}
func (w *systemdWorker) verifyUnit(ctx context.Context) error {
	p, err := w.conn.GetUnitPropertyContext(ctx, w.unit, "Description")
	if err != nil {
		if missingUnit(err) {
			if n, e := w.population(); e == nil && n == 0 {
				return nil
			}
		}
		return err
	}
	if p.Value.Value() != w.unit {
		return errors.New("supervisor unit ownership changed")
	}
	return nil
}
func (w *systemdWorker) Diagnostics() supervisor.Diagnostics {
	w.mu.Lock()
	d := supervisor.Diagnostics{PID: w.pid, ProcessStarted: w.started, ProcessStartedError: w.startedError, StdoutTail: w.stdout, StderrTail: w.stderr, Exit: w.exit}
	w.mu.Unlock()
	n, e := w.population()
	d.ActiveProcesses = n
	if e != nil {
		d.Exit.Err = errors.Join(errors.New(d.Exit.Err), e).Error()
	}
	return d
}
func (w *systemdWorker) Close() error {
	w.cleanup.Lock()
	defer w.cleanup.Unlock()
	if w.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if n, e := w.population(); e != nil || n != 0 {
		if err := w.RequestStop(ctx); err != nil {
			return err
		}
	}
	if err := w.WaitEmpty(ctx); err != nil {
		return err
	}
	select {
	case <-w.exited:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-w.readersDone:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := w.conn.ResetFailedUnitContext(ctx, w.unit); err != nil && !missingUnit(err) {
		return err
	}
	var result error
	if w.pidfd >= 0 {
		result = errors.Join(result, unix.Close(w.pidfd))
		w.pidfd = -1
	}
	if w.anchorInput != nil {
		result = errors.Join(result, w.anchorInput.Close())
		w.anchorInput = nil
	}
	if w.groupFD != nil {
		result = errors.Join(result, w.groupFD.Close())
		w.groupFD = nil
	}
	w.conn.Close()
	w.closed = true
	return result
}
