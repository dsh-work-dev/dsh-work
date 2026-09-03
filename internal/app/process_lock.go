package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/local/work/internal/lifecycle"
)

var errProcessLockHeld = errors.New("Work manager process lock is already held")

// ProcessLock coordinates the desktop Host and the standalone dsh-work CLI.
// It is deliberately a kernel-owned file lock rather than a marker file: the
// operating system releases it if Work exits unexpectedly, so a stale marker
// can never block recovery or permit a second manager to write state.
type ProcessLock struct {
	file   *os.File
	unlock func() error

	closeOnce sync.Once
	closeErr  error
}

// AcquireManagerProcessLock locks the application-data manager boundary. A
// running desktop Host owns this lock for its whole process lifetime; the CLI
// takes it for one explicit offline command. There is intentionally no lock
// or state file under a Go, Node or package-manager cache directory.
func AcquireManagerProcessLock(settingsPath string) (*ProcessLock, error) {
	if strings.TrimSpace(settingsPath) == "" {
		return nil, nil
	}
	lockPath := filepath.Join(filepath.Dir(settingsPath), "manager.lock")
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create Work manager lock directory: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Work manager process lock: %w", err)
	}
	unlock, err := lockProcessFile(file)
	if err != nil {
		_ = file.Close()
		if errors.Is(err, errProcessLockHeld) {
			return nil, lifecycle.Failure{
				Code:          lifecycle.ErrorManagerOperationBusy,
				Summary:       "Work is already running.",
				Retryable:     true,
				CorrelationID: lifecycle.NewCorrelationID(),
				Detail:        "Use the running Work Settings window for Run context and profile plugin changes.",
			}
		}
		return nil, fmt.Errorf("lock Work manager process boundary: %w", err)
	}
	return &ProcessLock{file: file, unlock: unlock}, nil
}

// Close releases the kernel lock and the file handle. It is safe to call from
// a deferred cleanup and is idempotent for shutdown paths.
func (l *ProcessLock) Close() error {
	if l == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		if l.unlock != nil {
			l.closeErr = l.unlock()
		}
		if err := l.file.Close(); l.closeErr == nil {
			l.closeErr = err
		}
	})
	return l.closeErr
}
