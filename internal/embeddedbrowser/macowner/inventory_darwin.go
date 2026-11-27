//go:build darwin && cgo

package macowner

import (
	"context"
	"errors"
	"os"
	"slices"
	"strconv"
	"sync"
	"syscall"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/unix"
)

var errRendererInventory = errors.New("MAC_CEF_RENDERER_INVENTORY_UNCERTAIN")

type rendererInventoryError struct {
	stage string
	cause error
}

func (e rendererInventoryError) Error() string                  { return errRendererInventory.Error() }
func (e rendererInventoryError) Unwrap() error                  { return errors.Join(errRendererInventory, e.cause) }
func (e rendererInventoryError) RendererInventoryStage() string { return e.stage }

var _ supervisor.ProcessInventory = (*Worker)(nil)

// RendererImage identifies the renderer helper pinned from the signed CEF
// publication before launching this worker.
func (w *Worker) RendererImage() os.FileInfo { return w.rendererImage }

func (w *Worker) OwnedProcesses(ctx context.Context) ([]supervisor.ProcessLease, error) {
	if w.rendererImage == nil {
		return nil, rendererInventoryError{"renderer-image-pin", nil}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	generation := w.state.Generation
	w.mu.Unlock()
	state, err := loadGeneration(w.directory, w.key, generation)
	if err != nil || state.Owner.PID != w.owner.PID || state.Owner.Started != w.owner.Started ||
		state.Owner.PGID != w.owner.PGID || state.Job != w.job || state.Broker.PID == 0 {
		return nil, rendererInventoryError{"signed-generation", err}
	}
	root, err := instance(state.Broker)
	if err != nil || root.State != "live" || root.PPID != w.owner.PID || root.PGID != w.owner.PGID {
		return nil, rendererInventoryError{"broker-instance-before-census", err}
	}
	members, err := group(w.owner.PGID)
	if err != nil {
		return nil, rendererInventoryError{"process-group-census", err}
	}
	children, err := processCensus(root.PID, true)
	if err != nil {
		return nil, rendererInventoryError{"broker-child-census", err}
	}
	// A helper can register during enumeration. Read the latest signed record
	// after the census. Helpers cannot enter CEF before registration is durable.
	state, err = freshRecoveryState(w.directory, w.key, state)
	if err != nil {
		return nil, rendererInventoryError{"signed-generation-refresh", err}
	}
	for _, member := range append(members.Members, children.Members...) {
		if processKey(member) == processKey(w.owner) || processKey(member) == processKey(root) {
			continue
		}
		if err := checkRendererCensusMember(member, root.PID, w.helperPaths, instance); err != nil {
			return nil, err
		}
	}
	root, err = instance(root)
	if err != nil || root.State != "live" {
		return nil, rendererInventoryError{"broker-instance-after-census", err}
	}
	var leases []supervisor.ProcessLease
	for _, entry := range state.Owned {
		if entry.Role != "helper:renderer" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return leases, err
		}
		current, err := instance(entry.Process)
		if err != nil {
			return leases, rendererInventoryError{"registered-renderer-instance", err}
		}
		if current.State == "gone" || current.State == "reused" || current.State == "zombie" {
			continue
		}
		if current.State != "live" || current.PPID != root.PID ||
			entry.RendererChild == 0 || entry.RendererChild > 0x7fffffff || entry.Target == nil ||
			entry.Target.PID != current.PID || entry.Target.Started != current.Started {
			return leases, rendererInventoryError{"registered-renderer-identity", nil}
		}
		lease, err := openRendererLease(*entry.Target, entry.RendererChild, w.rendererImage)
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, localipc.ErrTargetStale) {
			continue
		}
		if err != nil {
			return leases, rendererInventoryError{"renderer-process-lease", err}
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

func checkRendererCensusMember(member Process, brokerPID uint32, helpers []string, recheck func(Process) (Process, error)) error {
	matches := func(p Process) bool {
		return p.State == "gone" || p.State == "reused" || p.State == "zombie" ||
			(p.State == "live" && p.PPID == brokerPID && slices.Contains(helpers, p.Path))
	}
	if matches(member) {
		return nil
	}
	// BSD status and executable path are separate kernel reads. A process
	// can exit between them. Recheck this instance before counting it as an
	// unknown live process, and keep rejecting a foreign or unreadable image.
	current, err := recheck(member)
	if err != nil || !matches(current) {
		return rendererInventoryError{"census-member-identity", err}
	}
	return nil
}

type rendererProcessLease struct {
	mu       sync.Mutex
	fd       int
	exited   bool
	invalid  bool
	identity supervisor.ProcessInstance
	child    uint32
	target   localipc.ProcessTarget
}

func rendererTargetImage(target localipc.ProcessTarget, image os.FileInfo) (string, error) {
	if target.PID == 0 || target.Started == 0 || target.AuditToken[5] != target.PID || target.AuditToken[7] == 0 {
		return "", errRendererInventory
	}
	current, err := instance(Process{PID: target.PID, Started: target.Started})
	if err != nil {
		return "", err
	}
	switch current.State {
	case "gone", "zombie":
		return "", os.ErrProcessDone
	case "reused":
		return "", localipc.ErrTargetStale
	case "live":
	default:
		return "", errRendererInventory
	}
	path, err := targetPath(target)
	if err != nil {
		if current, checkErr := instance(current); checkErr == nil && current.State != "live" {
			return "", os.ErrProcessDone
		}
		return "", err
	}
	actual, err := os.Stat(path)
	if err != nil || !os.SameFile(image, actual) {
		return "", errors.Join(errRendererInventory, err)
	}
	return path, nil
}

func openRendererLease(target localipc.ProcessTarget, child uint32, image os.FileInfo) (*rendererProcessLease, error) {
	if _, err := rendererTargetImage(target, image); err != nil {
		return nil, err
	}
	syscall.ForkLock.RLock()
	fd, err := unix.Kqueue()
	if err == nil {
		unix.CloseOnExec(fd)
	}
	syscall.ForkLock.RUnlock()
	if err != nil {
		return nil, err
	}
	var event unix.Kevent_t
	unix.SetKevent(&event, int(target.PID), unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	event.Fflags = unix.NOTE_EXIT | unix.NOTE_EXEC
	if _, err = rendererKevent(unix.Kevent, fd, []unix.Kevent_t{event}, nil, nil); errors.Is(err, unix.ESRCH) {
		err = os.ErrProcessDone
	}
	var path string
	if err == nil {
		// Recheck the exact token and image after attaching the kernel event.
		path, err = rendererTargetImage(target, image)
	}
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return &rendererProcessLease{
		fd: fd, target: target, child: child,
		identity: supervisor.ProcessInstance{PID: target.PID, Started: target.Started, Image: path},
	}, nil
}

func (l *rendererProcessLease) Instance() supervisor.ProcessInstance { return l.identity }

func (l *rendererProcessLease) Arguments(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// These are the two Chromium switches authenticated by the signed helper.
	return []string{"--type=renderer", "--renderer-client-id=" + strconv.FormatUint(uint64(l.child), 10)}, nil
}

func (l *rendererProcessLease) Alive() (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fd < 0 {
		return false, os.ErrClosed
	}
	if l.exited {
		return false, nil
	}
	if l.invalid {
		return false, errRendererInventory
	}
	var event [1]unix.Kevent_t
	n, err := rendererKevent(unix.Kevent, l.fd, nil, event[:], &unix.Timespec{})
	if err != nil {
		return false, err
	}
	if n == 1 {
		if event[0].Ident != uint64(l.target.PID) || event[0].Filter != unix.EVFILT_PROC ||
			event[0].Flags&unix.EV_ERROR != 0 || event[0].Fflags&unix.NOTE_EXIT == 0 {
			l.invalid = true
			return false, errRendererInventory
		}
		l.exited = true
	}
	return !l.exited, nil
}

func rendererKevent(call func(int, []unix.Kevent_t, []unix.Kevent_t, *unix.Timespec) (int, error), fd int, changes, events []unix.Kevent_t, timeout *unix.Timespec) (int, error) {
	for attempt := 0; ; attempt++ {
		n, err := call(fd, changes, events, timeout)
		if !errors.Is(err, unix.EINTR) || attempt == 2 {
			return n, err
		}
	}
}

func (l *rendererProcessLease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fd < 0 {
		return nil
	}
	fd := l.fd
	l.fd = -1
	return unix.Close(fd)
}
