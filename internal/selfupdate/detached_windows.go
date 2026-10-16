//go:build windows

package selfupdate

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// detachedProcess is a process started outside this process's console and job,
// with a handle that outlives the parent so its exit can still be observed.
type detachedProcess struct {
	handle windows.Handle
	// PID is the process id of the started process.
	PID int
}

// startDetachedProcess runs an executable without attaching this process's
// console or job object.
//
// Detaching matters twice over. A worker or installer that inherited this
// process's job object would be terminated with it, and the installer stops the
// background process as part of its maintenance handshake, so the installer
// must survive that stop and finish the replacement on its own.
func startDetachedProcess(executable string, args []string) (*detachedProcess, error) {
	return startDetachedWithFlags(executable, args, true)
}

func startDetachedWithFlags(executable string, args []string, breakaway bool) (*detachedProcess, error) {
	application, err := windows.UTF16PtrFromString(executable)
	if err != nil {
		return nil, err
	}
	commandLine, err := windows.UTF16FromString(windows.ComposeCommandLine(append([]string{executable}, args...)))
	if err != nil {
		return nil, err
	}
	// The executable runs in the user's own session: it needs no inherited
	// handles, and a hidden window keeps a console application from flashing.
	startup := windows.StartupInfo{ShowWindow: windows.SW_HIDE}
	startup.Flags = windows.STARTF_USESHOWWINDOW
	flags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	if breakaway {
		flags |= windows.CREATE_BREAKAWAY_FROM_JOB
	}
	info := windows.ProcessInformation{}
	err = windows.CreateProcess(application, &commandLine[0], nil, nil, false, flags, nil, nil, &startup, &info)
	if err != nil && breakaway && errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The containing job object does not permit breakaway, the process is
		// not in a job, or the caller may not escape it. None of those prevent
		// starting the process, so retry without the flag instead of failing
		// the update; the flag is used whenever the operating system allows it.
		return startDetachedWithFlags(executable, args, false)
	}
	if err != nil {
		return nil, err
	}
	_ = windows.CloseHandle(info.Thread)
	return &detachedProcess{handle: info.Process, PID: int(info.ProcessId)}, nil
}

// Wait blocks until the process exits and reports a non-nil error when the
// exit code is not zero.
func (p *detachedProcess) Wait() error {
	if p == nil || p.handle == 0 {
		return errors.New("no process to wait for")
	}
	defer func() {
		_ = windows.CloseHandle(p.handle)
		p.handle = 0
	}()
	if _, err := windows.WaitForSingleObject(p.handle, windows.INFINITE); err != nil {
		return err
	}
	code, err := p.ExitCode()
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("process exited with code %d", code)
	}
	return nil
}

// ExitCode reports the exit code of the finished process.
func (p *detachedProcess) ExitCode() (int, error) {
	if p == nil || p.handle == 0 {
		return 0, errors.New("the process has already been reaped")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(p.handle, &code); err != nil {
		return 0, err
	}
	return int(code), nil
}
