//go:build windows

package windows

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/supervisor"
	win "golang.org/x/sys/windows"
)

type CommandExecutor struct{}

func terminateCommandJob(worker *jobWorker) error {
	return win.TerminateJobObject(worker.job, 1)
}

func NewCommandExecutor() dshadapter.CommandExecutor {
	return CommandExecutor{}
}

func (CommandExecutor) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshadapter.CommandResult, error) {
	if err := validateBatchInvocation(executable, args); err != nil {
		dshadapter.ReportCommandOutput(ctx, "Command could not start ("+executable+"): "+err.Error())
		return dshadapter.CommandResult{}, err
	}
	worker, err := startJobCommand(ctx, executable, args, env, dir, func(_ supervisor.OutputStream, line string) {
		dshadapter.ReportCommandOutput(ctx, line)
	})
	if err != nil {
		dshadapter.ReportCommandOutput(ctx, "Command could not start ("+executable+"): "+err.Error())
		return dshadapter.CommandResult{}, err
	}
	defer worker.Close()
	select {
	case <-ctx.Done():
		err = ctx.Err()
		// Terminate the job even if its root has exited but descendants remain.
		if stopErr := terminateCommandJob(worker); stopErr != nil {
			err = fmt.Errorf("%w: %v", err, stopErr)
		}
	case <-worker.Exited():
		if result := worker.ExitResult(); result.Code != 0 || result.Err != "" {
			err = fmt.Errorf("command exited with code %d: %s", result.Code, result.Err)
		}
		if stopErr := terminateCommandJob(worker); err == nil {
			err = stopErr
		}
	}
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if cleanupErr := worker.WaitEmpty(cleanup); err == nil {
		err = cleanupErr
	}
	select {
	case <-worker.readersDone:
	case <-cleanup.Done():
		if err == nil {
			err = cleanup.Err()
		}
	}
	diagnostics := worker.Diagnostics()
	if err != nil {
		dshadapter.ReportCommandOutput(ctx, "Command failed: "+err.Error())
	}
	return dshadapter.CommandResult{
		Stdout: diagnostics.StdoutTail,
		Stderr: diagnostics.StderrTail,
	}, err
}

func isBatchFile(executable string) bool {
	lower := strings.ToLower(executable)
	return strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat")
}

// Batch files require cmd.exe, whose metacharacter rules are separate from
// normal argv parsing. Rejecting those characters at this native seam keeps
// user-controlled plugin specs and catalog paths from becoming shell syntax.
func validateBatchInvocation(executable string, args []string) error {
	if !isBatchFile(executable) {
		return nil
	}
	if containsBatchMeta(executable) {
		return fmt.Errorf("batch executable path contains shell metacharacters")
	}
	for _, arg := range args {
		if containsBatchMeta(arg) {
			return fmt.Errorf("batch argument contains shell metacharacters")
		}
	}
	return nil
}

func containsBatchMeta(value string) bool {
	return strings.ContainsAny(value, "&|<>()^%!\"\r\n")
}
