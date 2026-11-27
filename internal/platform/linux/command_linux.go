//go:build linux

package linux

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
)

// CommandExecutor preserves the existing command seam: argv is not shell
// syntax and env overlays the calling application's environment. The complete
// resulting Env is passed to the exact-environment Supervisor contract.
type CommandExecutor struct {
	owner   supervisor.Adapter
	mu      sync.Mutex
	pending map[supervisor.Worker]struct{}
}

func NewCommandExecutor(owner supervisor.Adapter) *CommandExecutor {
	return &CommandExecutor{owner: owner, pending: map[supervisor.Worker]struct{}{}}
}

func commandEnvironment(overrides map[string]string) map[string]string {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			env[key] = value
		}
	}
	for key, value := range overrides {
		env[key] = value
	}
	return env
}

func (e *CommandExecutor) release(w supervisor.Worker) error {
	if err := w.Close(); err != nil {
		e.mu.Lock()
		e.pending[w] = struct{}{}
		e.mu.Unlock()
		return err
	}
	e.mu.Lock()
	delete(e.pending, w)
	e.mu.Unlock()
	return nil
}

func (e *CommandExecutor) Run(ctx context.Context, executable string, args []string, env map[string]string, directory string) (dshadapter.CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return dshadapter.CommandResult{}, err
	}
	// Failed joins retain the actual owner. A later command retries those joins
	// before starting additional work; process exit also closes each EOF lease.
	e.mu.Lock()
	pending := make([]supervisor.Worker, 0, len(e.pending))
	for w := range e.pending {
		pending = append(pending, w)
	}
	e.mu.Unlock()
	for _, w := range pending {
		if err := e.release(w); err != nil {
			return dshadapter.CommandResult{}, fmt.Errorf("previous command cleanup pending: %w", err)
		}
	}
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return dshadapter.CommandResult{}, err
		}
	}
	w, err := e.owner.Start(ctx, supervisor.LaunchPlan{GenerationID: "command-" + rand.Text(), Executable: executable, Args: args, Env: commandEnvironment(env), WorkingDirectory: filepath.Clean(directory), ExpectedOrigin: workeripc.Origin}, func(_ supervisor.OutputStream, line string) { dshadapter.ReportCommandOutput(ctx, line) })
	if err != nil {
		return dshadapter.CommandResult{}, err
	}
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case <-w.Exited():
		result := w.ExitResult()
		if result.Code != 0 || result.Err != "" {
			err = fmt.Errorf("command exited with code %d: %s", result.Code, result.Err)
		}
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Commands, unlike browser protocol shutdown, own no persistent descendants.
	// Match the existing command seam by reclaiming any remaining child tree.
	grace, stopGrace := context.WithTimeout(cleanup, 250*time.Millisecond)
	joined := w.WaitEmpty(grace)
	stopGrace()
	if joined != nil {
		if forceErr := w.ForceStop(cleanup); forceErr != nil {
			err = errors.Join(err, forceErr)
		}
	}
	if joinErr := w.WaitEmpty(cleanup); joinErr != nil {
		err = errors.Join(err, joinErr)
		e.mu.Lock()
		e.pending[w] = struct{}{}
		e.mu.Unlock()
	} else {
		err = errors.Join(err, e.release(w))
	}
	d := w.Diagnostics()
	if err != nil {
		dshadapter.ReportCommandOutput(ctx, "Command failed: "+err.Error())
	}
	return dshadapter.CommandResult{Stdout: d.StdoutTail, Stderr: d.StderrTail}, err
}
