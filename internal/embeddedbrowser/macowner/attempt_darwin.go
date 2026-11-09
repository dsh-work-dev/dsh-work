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
	Generation   string  `json:"generation"`
	Job          string  `json:"job"`
	Run          string  `json:"run"`
	Program      string  `json:"program"`
	Submitted    bool    `json:"submitted"`
	Hello        bool    `json:"hello"`
	JobRetired   bool    `json:"jobRetired"`
	Owner        Process `json:"owner"`
	SocketDevice uint64  `json:"socketDevice"`
	SocketInode  uint64  `json:"socketInode"`
}

func attemptName(generation string) (string, error) {
	if _, err := generationName(generation); err != nil {
		return "", err
	}
	return "attempt-" + generation + ".json", nil
}
func (w *Worker) saveAttempt() error {
	name, err := attemptName(w.attempt.Generation)
	if err != nil {
		return err
	}
	body, err := json.Marshal(w.attempt)
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
func (w *Worker) verifiedJob(ctx context.Context) ([]byte, error) {
	status, err := launchctl(ctx, "print", service(w.job))
	if err != nil {
		return nil, errors.Join(ErrCleanupPending, err)
	}
	program := w.program
	if w.attempt != nil {
		program = w.attempt.Program
	}
	if jobField(status, "path") != filepath.Join(w.run, "owner.plist") || jobField(status, "program") != program {
		return nil, errors.Join(ErrCleanupPending, errors.New("MAC_CEF_JOB_OWNERSHIP_UNKNOWN"))
	}
	return status, nil
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
		if err != nil || name != expected || attempt.Program != a.publication.Owner || !strings.HasPrefix(attempt.Job, "dev.dsh.cef.") || filepath.Dir(attempt.Run) != filepath.Clean(localipc.SocketTempRoot()) || !strings.HasPrefix(filepath.Base(attempt.Run), "dsh-cef-") {
			return ErrCleanupPending
		}
		w := &Worker{directory: a.directory, key: key, job: attempt.Job, run: attempt.Run, program: attempt.Program, attempt: &attempt, state: journal{Generation: attempt.Generation}}
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
