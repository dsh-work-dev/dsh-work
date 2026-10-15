package selfupdate

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInstallerFailed reports an installer that ran and did not succeed.
	ErrInstallerFailed = errors.New("the update installer did not complete successfully")
	// ErrInstallerTimeout reports an installer that outlived the maintenance
	// budget and was left alone rather than killed mid-replacement.
	ErrInstallerTimeout = errors.New("the update installer did not finish in time")
)

// installerExitBudget bounds how long the handoff waits for the installer to
// replace the release unit. The installer stops the background process (up to
// 30s in the CLI), swaps the directory, writes the uninstaller and starts the
// new workbench, so the budget is generous but finite: a stuck installer must
// be reported rather than waited on forever.
const installerExitBudget = 5 * time.Minute

// HandoffOutcome reports what the handoff managed to do.
type HandoffOutcome struct {
	// InstallerExited reports that the installer process finished.
	InstallerExited bool
	// InstallerExitCode is the installer's exit code when it finished.
	InstallerExitCode int
	// Err reports why the handoff could not complete.
	Err error
}

// RunInstaller hands the verified installer to an independent process tree and
// waits for it to finish.
//
// The installer owns the whole maintenance sequence: it takes the per-user
// maintenance boundary, stops the running background process through the
// installed CLI, replaces the release unit, releases the boundary and starts
// the new workbench. This process is expected to be stopped by that handshake,
// so nothing here may be required for the update to complete: the installer is
// started detached, in its own process group and outside any job object, and
// the wait is only used to report an installer that failed before the handshake
// even began.
func (i *Installer) RunInstaller(ctx context.Context) HandoffOutcome {
	outcome := HandoffOutcome{}
	if i == nil || i.Path == "" {
		outcome.Err = ErrInstallerUnavailable
		return outcome
	}
	args, err := i.InstallerArgs()
	if err != nil {
		outcome.Err = err
		return outcome
	}
	process, err := startDetachedProcess(i.Path, args)
	if err != nil {
		outcome.Err = fmt.Errorf("%w: could not start the installer: %v", ErrInstallerUnavailable, err)
		return outcome
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(installerExitBudget)
	defer timer.Stop()
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			outcome.Err = fmt.Errorf("%w: %v", ErrInstallerFailed, err)
			return outcome
		}
	case <-ctx.Done():
		outcome.Err = context.Cause(ctx)
		return outcome
	case <-timer.C:
		outcome.Err = fmt.Errorf("%w: still running after %s", ErrInstallerTimeout, installerExitBudget)
		return outcome
	}
	outcome.InstallerExited = true
	return outcome
}
