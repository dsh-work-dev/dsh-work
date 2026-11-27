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
	"net"
	"net/url"
	"os"
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
	mu                  sync.Mutex
	pending             map[string]*systemdWorker
	beforeAdopt         func(int)
	beforeInstanceCheck func(int)
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
	control        *net.UnixConn
	anchorFinished bool
	anchorPID      uint32
	bootstrapFD    int
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
	if err = plan.Validate(); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(plan.Executable) || !filepath.IsAbs(plan.WorkingDirectory) {
		return nil, errors.New("absolute Linux launch paths required")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = a.Close(); err != nil {
		return nil, err
	}
	c, err := manager(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s: user manager: %w", unavailable, err)
	}
	w := &systemdWorker{conn: c, unit: "dsh-owned-" + strings.ToLower(rand.Text()) + ".service", pidfd: -1, bootstrapFD: -1, exited: make(chan struct{}), readersDone: make(chan struct{}), events: make(chan supervisor.OutputEvent, 128)}
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		c.Close()
		return nil, err
	}
	parentFile, childFile := os.NewFile(uintptr(pair[0]), "bootstrap-parent"), os.NewFile(uintptr(pair[1]), "bootstrap-child")
	defer childFile.Close()
	w.control, err = unixConnection(parentFile)
	parentFile.Close()
	if err != nil {
		c.Close()
		return nil, err
	}
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
	control := w.control
	stopCancellation := context.AfterFunc(ctx, func() { control.Close() })
	defer stopCancellation()
	deadline := time.Now().Add(15 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	w.control.SetDeadline(deadline)
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	props := []dbus.Property{property("Description", w.unit), property("Type", "exec"), property("KillMode", "control-group"), property("TimeoutStopUSec", uint64(3*time.Second/time.Microsecond)), property("StandardInputFileDescriptor", bus.UnixFD(childFile.Fd())), property("StandardOutput", "null"), property("StandardError", "null"), dbus.PropExecStart([]string{self, bootstrapArgument}, false)}
	ready := make(chan string, 1)
	if _, err = c.StartTransientUnitContext(ctx, w.unit, "fail", props, ready); err != nil {
		return nil, fmt.Errorf("%s: bootstrap unit: %w", unavailable, err)
	}
	select {
	case status := <-ready:
		if status != "done" {
			return nil, fmt.Errorf("%s: bootstrap start %s", unavailable, status)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	service, err := c.GetAllPropertiesContext(ctx, w.unit)
	if err != nil {
		return nil, err
	}
	w.anchorPID, _ = service["MainPID"].(uint32)
	group, _ := service["ControlGroup"].(string)
	if w.anchorPID == 0 || !strings.HasPrefix(group, "/") || filepath.Clean(group) != group || !strings.HasSuffix(group, "/"+w.unit) {
		return nil, fmt.Errorf("%s: own bootstrap cgroup unavailable", unavailable)
	}
	w.group = filepath.Join("/sys/fs/cgroup", group)
	w.bootstrapFD, err = unix.PidfdOpen(int(w.anchorPID), 0)
	if err != nil {
		return nil, err
	}
	anchorBirth, err := localipc.ProcessStarted(w.anchorPID)
	if err != nil {
		return nil, err
	}
	expected, err := os.Stat(self)
	if err != nil {
		return nil, err
	}
	actual, err := os.Stat(fmt.Sprintf("/proc/%d/exe", w.anchorPID))
	if err != nil || !os.SameFile(expected, actual) {
		return nil, errors.New("bootstrap executable changed")
	}
	kind, fds, cred, err := markerRead(w.control)
	for _, fd := range fds {
		unix.Close(fd)
	}
	if err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	current, e := localipc.ProcessStarted(w.anchorPID)
	if kind != 'R' || len(fds) != 0 || cred.Pid != int32(w.anchorPID) || cred.Uid != uint32(os.Getuid()) || e != nil || current != anchorBirth || unix.PidfdSendSignal(w.bootstrapFD, 0, nil, 0) != nil {
		return nil, errors.New("bootstrap kernel sender instance rejected")
	}
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
	w.readers.Add(2)
	go w.output(supervisor.StreamStdout, stdout, raw)
	go w.output(supervisor.StreamStderr, stderr, raw)
	go func() { w.readers.Wait(); close(w.events); close(w.readersDone) }()
	if err = markerWrite(w.control, 'P', int(stdoutWrite.Fd()), int(stderrWrite.Fd())); err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	if err = frameWrite(w.control, plan); err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	kind, fds, cred, err = markerRead(w.control)
	if err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	adopted := false
	defer func() {
		if !adopted {
			for _, fd := range fds {
				unix.Close(fd)
			}
		}
	}()
	if cred.Pid != int32(w.anchorPID) || cred.Uid != uint32(os.Getuid()) {
		return nil, errors.New("application pidfd sender rejected")
	}
	var message bootstrapMessage
	if err = frameRead(w.control, &message); err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	if kind == 'E' && len(fds) == 0 && message.Kind == "launch-error" {
		return nil, fmt.Errorf("bootstrap application start: %w", syscall.Errno(message.Errno))
	}
	if kind != 'A' || len(fds) != 1 || message.Kind != "started" || message.Started == 0 {
		return nil, errors.New("application launch handshake rejected")
	}
	pid, err := pidFromFD(fds[0])
	if err != nil {
		return nil, err
	}
	if a.beforeInstanceCheck != nil {
		a.beforeInstanceCheck(fds[0])
	}
	birth, err := ownedProcessStarted(pid, w.anchorPID, fds[0])
	if err != nil || birth != message.Started {
		return nil, errors.New("application kernel instance changed")
	}
	membership, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil || !strings.Contains(string(membership), "0::"+group+"\n") {
		// An exited, unreaped child no longer executes in any cgroup. Accept
		// only the already-exited kernel pidfd plus the same direct child
		// creation of our verified bootstrap, never a live foreign process.
		poll := []unix.PollFd{{Fd: int32(fds[0]), Events: unix.POLLIN}}
		n, pollErr := unix.Poll(poll, 0)
		exitedBirth, birthErr := ownedProcessStarted(pid, w.anchorPID, fds[0])
		if pollErr != nil || n != 1 || poll[0].Revents&unix.POLLIN == 0 || birthErr != nil || exitedBirth != birth {
			return nil, errors.New("application not in owned bootstrap cgroup")
		}
	}
	w.pid = pid
	w.started = birth
	w.pidfd = fds[0]
	adopted = true
	if a.beforeAdopt != nil {
		a.beforeAdopt(w.pidfd)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = frameWrite(w.control, bootstrapMessage{Kind: "adopt"}); err != nil {
		return nil, errors.Join(ctx.Err(), err)
	}
	stopCancellation()
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	w.control.SetDeadline(time.Time{})
	w.exit = supervisor.ExitResult{Started: true}
	go w.watchApplication()
	failed = false
	return w, nil
}

func (w *systemdWorker) watchApplication() {
	var message bootstrapMessage
	err := frameRead(w.control, &message)
	result := message.Result
	if err != nil || message.Kind != "exit" || !result.Started {
		result = supervisor.ExitResult{Started: true, Code: -1, Err: "bootstrap ended before actual application exit result"}
		for {
			poll := []unix.PollFd{{Fd: int32(w.pidfd), Events: unix.POLLIN}}
			n, e := unix.Poll(poll, 100)
			if n > 0 || e != nil {
				break
			}
		}
	}
	w.mu.Lock()
	w.exit = result
	w.mu.Unlock()
	close(w.exited)
}

// Startup failure cannot leave an unknown late unit: EOF already belongs to
// the manager-held read descriptor, even if its D-Bus response was lost.
func (w *systemdWorker) abandonAnchor() error {
	if w.control != nil {
		w.control.Close()
		w.control = nil
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
		if w.group == "" {
			if group, ok := properties["ControlGroup"].(string); ok && strings.HasPrefix(group, "/") && filepath.Clean(group) == group && strings.HasSuffix(group, "/"+w.unit) {
				w.group = filepath.Join("/sys/fs/cgroup", group)
			}
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
	if w.group != "" {
		for {
			n, e := w.population()
			if e != nil {
				return e
			}
			if n == 0 {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	if e := w.conn.ResetFailedUnitContext(ctx, w.unit); e != nil && !missingUnit(e) {
		return e
	}
	if w.pidfd >= 0 {
		unix.Close(w.pidfd)
		w.pidfd = -1
	}
	if w.bootstrapFD >= 0 {
		unix.Close(w.bootstrapFD)
		w.bootstrapFD = -1
	}
	w.conn.Close()
	return nil
}

func missingUnit(err error) bool {
	var e bus.Error
	return errors.As(err, &e) && (e.Name == "org.freedesktop.systemd1.NoSuchUnit" || e.Name == "org.freedesktop.DBus.Error.UnknownObject")
}

// Retain a failed startup's unit owner until its real cleanup succeeds. No
// application child was returned in this branch; EOF still retires bootstrap.
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
				if !w.anchorFinished {
					err = frameWrite(w.control, bootstrapMessage{Kind: "finish"})
					if err == nil {
						w.anchorFinished = true
					}
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
	if w.bootstrapFD >= 0 {
		result = errors.Join(result, unix.Close(w.bootstrapFD))
		w.bootstrapFD = -1
	}
	if w.pidfd >= 0 {
		result = errors.Join(result, unix.Close(w.pidfd))
		w.pidfd = -1
	}
	if w.control != nil {
		result = errors.Join(result, w.control.Close())
		w.control = nil
	}
	w.conn.Close()
	w.closed = true
	return result
}
