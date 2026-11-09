//go:build darwin && cgo

package macowner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

// Contains no launch environment, HMAC secret or Mach token. An uncertain
// launchctl result remains owned even if the creating Adapter is replaced.
type launchAttempt struct {
	FileIdentityVersion int     `json:"fileIdentityVersion,omitempty"`
	Generation          string  `json:"generation"`
	Job                 string  `json:"job"`
	Run                 string  `json:"run"`
	Program             string  `json:"program"`
	Submitted           bool    `json:"submitted"`
	Hello               bool    `json:"hello"`
	JobRetired          bool    `json:"jobRetired"`
	Owner               Process `json:"owner"`
	SocketDevice        uint64  `json:"socketDevice"`
	SocketInode         uint64  `json:"socketInode"`
	PlistDevice         uint64  `json:"plistDevice"`
	PlistInode          uint64  `json:"plistInode"`
	ProgramDevice       uint64  `json:"programDevice"`
	ProgramInode        uint64  `json:"programInode"`
}

func attemptName(generation string) (string, error) {
	if _, err := generationName(generation); err != nil {
		return "", err
	}
	return "attempt-" + generation + ".json", nil
}
func (w *Worker) saveAttempt() error { return w.saveAttemptValue(w.attempt) }
func (w *Worker) saveAttemptValue(attempt *launchAttempt) error {
	name, err := attemptName(attempt.Generation)
	if err != nil {
		return err
	}
	body, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	return signedFile(w.directory, name, "cef-attempt-v1", w.key, body)
}
func (w *Worker) clearAttempt() error {
	name, err := attemptName(w.attempt.Generation)
	if err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(w.directory, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	dir, err := os.Open(w.directory)
	if err != nil {
		return err
	}
	err = errors.Join(dir.Sync(), dir.Close())
	if err != nil {
		return err
	}
	w.attempt = nil
	return nil
}
func jobField(status []byte, key string) string {
	for _, line := range strings.Split(string(status), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), key+" = "); ok {
			return value
		}
	}
	return ""
}

// This is an allowlist projection, never launchctl's full output (which can
// contain the launch environment). A failed lookup does not establish absence.
type jobMeasurement struct {
	QuerySucceeded     bool   `json:"querySucceeded"`
	Status             string `json:"status"`
	Path               string `json:"path,omitempty"`
	Program            string `json:"program,omitempty"`
	PID                uint32 `json:"pid,omitempty"`
	State              string `json:"state,omitempty"`
	PlistFileMatched   bool   `json:"plistFileMatched"`
	ProgramFileMatched bool   `json:"programFileMatched"`
}

func sameJobFile(actual, expected string) bool {
	if !filepath.IsAbs(actual) || !filepath.IsAbs(expected) {
		return false
	}
	a, err := os.Stat(actual)
	if err != nil || !a.Mode().IsRegular() {
		return false
	}
	e, err := os.Stat(expected)
	return err == nil && e.Mode().IsRegular() && os.SameFile(a, e)
}
func fileIdentity(path string) (uint64, uint64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || stat.Ino == 0 {
		return 0, 0, ErrCleanupPending
	}
	return uint64(stat.Dev), stat.Ino, nil
}
func pinnedJobFile(actual, expected string, device, inode uint64) bool {
	if inode == 0 || !sameJobFile(actual, expected) {
		return false
	}
	dev, ino, err := fileIdentity(actual)
	return err == nil && dev == device && ino == inode
}
func (w *Worker) sealAttemptFiles() error {
	var err error
	w.attempt.PlistDevice, w.attempt.PlistInode, err = fileIdentity(filepath.Join(w.run, "owner.plist"))
	if err != nil {
		return err
	}
	w.attempt.ProgramDevice, w.attempt.ProgramInode, err = fileIdentity(w.attempt.Program)
	if err == nil {
		w.attempt.FileIdentityVersion = 1
	}
	return err
}
func (w *Worker) verifiedJob(ctx context.Context) ([]byte, error) {
	status, err := launchctl(ctx, "print", service(w.job))
	measure := jobMeasurement{Status: "lookup-failed-absence-unconfirmed"}
	defer func() { w.mu.Lock(); w.jobMeasurement = measure; w.mu.Unlock() }()
	if err != nil {
		return nil, errors.Join(ErrCleanupPending, err)
	}
	measure, err = w.verifyJobMetadata(status)
	return status, err
}
func (w *Worker) verifyJobMetadata(status []byte) (jobMeasurement, error) {
	measure := jobMeasurement{QuerySucceeded: true, Status: "present-unmatched", Path: jobField(status, "path"), Program: jobField(status, "program"), State: jobField(status, "state")}
	pid, _ := strconv.ParseUint(jobField(status, "pid"), 10, 32)
	measure.PID = uint32(pid)
	reject := func() (jobMeasurement, error) {
		return measure, errors.Join(ErrCleanupPending, errors.New("MAC_CEF_JOB_OWNERSHIP_UNKNOWN"))
	}
	if w.attempt == nil {
		return reject()
	}
	a := w.attempt
	if a.FileIdentityVersion == 0 {
		// Authenticated old v1 attempts predate file pins. Preserve their original
		// trusted path/program contract; a canonical alias must name the same file.
		// Commit an explicit identity upgrade before granting any retirement action.
		measure.PlistFileMatched = sameJobFile(measure.Path, filepath.Join(w.run, "owner.plist"))
		measure.ProgramFileMatched = sameJobFile(measure.Program, a.Program)
		if !measure.PlistFileMatched || !measure.ProgramFileMatched {
			return reject()
		}
		candidate := *a
		if a.PlistInode != 0 || a.ProgramInode != 0 || a.PlistDevice != 0 || a.ProgramDevice != 0 {
			// A pre-version checkpoint that already wrote pins keeps those pins; do
			// not silently resnapshot a replacement under the legacy branch.
			if !pinnedJobFile(measure.Path, filepath.Join(w.run, "owner.plist"), a.PlistDevice, a.PlistInode) || !pinnedJobFile(measure.Program, a.Program, a.ProgramDevice, a.ProgramInode) {
				return reject()
			}
		} else {
			var err error
			candidate.PlistDevice, candidate.PlistInode, err = fileIdentity(filepath.Join(w.run, "owner.plist"))
			if err != nil {
				return measure, errors.Join(ErrCleanupPending, err)
			}
			candidate.ProgramDevice, candidate.ProgramInode, err = fileIdentity(a.Program)
			if err != nil {
				return measure, errors.Join(ErrCleanupPending, err)
			}
		}
		candidate.FileIdentityVersion = 1
		if err := w.saveAttemptValue(&candidate); err != nil {
			return measure, errors.Join(ErrCleanupPending, err)
		}
		w.attempt = &candidate
		a = w.attempt
	}
	if a.FileIdentityVersion != 1 {
		return reject()
	}
	measure.PlistFileMatched = pinnedJobFile(measure.Path, filepath.Join(w.run, "owner.plist"), a.PlistDevice, a.PlistInode)
	measure.ProgramFileMatched = pinnedJobFile(measure.Program, a.Program, a.ProgramDevice, a.ProgramInode)
	if !measure.PlistFileMatched || !measure.ProgramFileMatched {
		return reject()
	}
	measure.Status = "present-owned"
	return measure, nil
}
func ownedRunParent(run string) bool {
	parent := filepath.Dir(run)
	root := filepath.Clean(localipc.SocketTempRoot())
	canonical, err := filepath.EvalSymlinks(root)
	return parent == root || (err == nil && parent == canonical)
}
func (a *Adapter) recoverAttempts(ctx context.Context, key []byte) error {
	entries, err := os.ReadDir(a.directory)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "attempt-") || !strings.HasSuffix(name, ".json") {
			continue
		}
		body, err := readSigned(a.directory, name, "cef-attempt-v1", key)
		if err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
		var attempt launchAttempt
		if err = json.Unmarshal(body, &attempt); err != nil {
			return errors.Join(ErrCleanupPending, err)
		}
		expected, err := attemptName(attempt.Generation)
		if err != nil || name != expected || attempt.Program != a.publication.Owner || !strings.HasPrefix(attempt.Job, "dev.dsh.cef.") || !ownedRunParent(attempt.Run) || !strings.HasPrefix(filepath.Base(attempt.Run), "dsh-cef-") {
			return ErrCleanupPending
		}
		w := &Worker{directory: a.directory, key: key, job: attempt.Job, run: attempt.Run, attempt: &attempt, state: journal{Generation: attempt.Generation}}
		a.worker = w
		if err = w.closeAttempt(ctx); err != nil {
			return err
		}
		a.worker = nil
	}
	return nil
}
func (w *Worker) closeAttempt(ctx context.Context) error {
	if w.conn != nil {
		w.conn.Close()
	}
	if w.attempt.Submitted {
		if !w.jobRetired && !w.attempt.JobRetired {
			// Success and exact launchd metadata are required; error exit codes do not
			// establish absence. In particular bootstrap cancellation is not rollback.
			status, err := w.verifiedJob(ctx)
			if err != nil {
				return err
			}
			if w.attempt.Hello {
				state, err := loadGeneration(w.directory, w.key, w.attempt.Generation)
				if err != nil || state.Job != w.job {
					return errors.Join(ErrCleanupPending, err)
				}
				if err = recoverState(ctx, w.directory, w.key, state); err != nil {
					return err
				}
				w.owner = state.Owner
			} else if text := jobField(status, "pid"); text != "" {
				// No authenticated hello means RunGuard cannot have launched the broker.
				pid, err := strconv.ParseUint(text, 10, 32)
				if err != nil {
					return ErrCleanupPending
				}
				p, err := process(uint32(pid))
				if err != nil || p.State != "live" || p.Path != w.attempt.Program {
					return errors.Join(ErrCleanupPending, err)
				}
				w.owner = p
			}
			if err := privateDir(w.run); err != nil {
				return errors.Join(ErrCleanupPending, err)
			}
			if info, err := os.Lstat(filepath.Join(w.run, "owner.sock")); err == nil {
				stat, ok := info.Sys().(*syscall.Stat_t)
				if !ok || info.Mode()&os.ModeSocket == 0 || stat.Uid != uint32(os.Geteuid()) {
					return ErrCleanupPending
				}
				if w.socketInfo != nil && !os.SameFile(w.socketInfo, info) {
					return ErrCleanupPending
				}
				w.socketInfo = info
				w.attempt.SocketDevice = uint64(stat.Dev)
				w.attempt.SocketInode = stat.Ino
			} else if !errors.Is(err, os.ErrNotExist) {
				return errors.Join(ErrCleanupPending, err)
			}
			w.attempt.Owner = w.owner
			if err := w.saveAttempt(); err != nil {
				return errors.Join(ErrCleanupPending, err)
			}
			if _, err := launchctl(ctx, "bootout", service(w.job)); err != nil {
				return errors.Join(ErrCleanupPending, err)
			}
			w.jobRetired = true
			w.attempt.JobRetired = true
			if err := w.saveAttempt(); err != nil {
				return errors.Join(ErrCleanupPending, err)
			}
		}
		w.owner = w.attempt.Owner
		if w.owner.PID != 0 {
			for {
				p, err := instance(w.owner)
				if err != nil {
					return errors.Join(ErrCleanupPending, err)
				}
				if p.State == "gone" || p.State == "reused" {
					break
				}
				select {
				case <-ctx.Done():
					return errors.Join(ErrCleanupPending, ctx.Err())
				case <-time.After(25 * time.Millisecond):
				}
			}
		}
		if w.socketInfo == nil && w.attempt.SocketInode != 0 {
			info, err := os.Lstat(filepath.Join(w.run, "owner.sock"))
			if err == nil {
				stat, ok := info.Sys().(*syscall.Stat_t)
				if !ok || uint64(stat.Dev) != w.attempt.SocketDevice || stat.Ino != w.attempt.SocketInode {
					return ErrCleanupPending
				}
				w.socketInfo = info
			} else if !errors.Is(err, os.ErrNotExist) {
				return errors.Join(ErrCleanupPending, err)
			}
		}
	}
	if err := w.removeRun(); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	if err := w.clearAttempt(); err != nil {
		return errors.Join(ErrCleanupPending, err)
	}
	w.retired = true
	return nil
}
