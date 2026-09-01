//go:build windows

package windows

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/local/work/internal/supervisor"
	win "golang.org/x/sys/windows"
)

const (
	outputTailLimit = 64 * 1024
	outputLineLimit = 64 * 1024
	pollInterval    = 25 * time.Millisecond
	waitTimeout     = 258
)

type JobObjectAdapter struct{}

func NewJobObjectAdapter() supervisor.Adapter {
	return JobObjectAdapter{}
}

func (JobObjectAdapter) Start(_ context.Context, plan supervisor.LaunchPlan) (supervisor.Worker, error) {
	if err := plan.Validate(); err != nil {
		return nil, fmt.Errorf("validate launch plan: %w", err)
	}

	job, err := win.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create DSH job object: %w", err)
	}
	closeJob := true
	defer func() {
		if closeJob {
			_ = win.CloseHandle(job)
		}
	}()

	limits := win.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = win.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := win.SetInformationJobObject(job, win.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return nil, fmt.Errorf("configure DSH job object: %w", err)
	}

	stdoutRead, stdoutWrite, err := createPipe()
	if err != nil {
		return nil, fmt.Errorf("create DSH stdout pipe: %w", err)
	}
	closeStdout := true
	defer func() {
		if closeStdout {
			closeHandle(stdoutRead)
			closeHandle(stdoutWrite)
		}
	}()

	stderrRead, stderrWrite, err := createPipe()
	if err != nil {
		return nil, fmt.Errorf("create DSH stderr pipe: %w", err)
	}
	closeStderr := true
	defer func() {
		if closeStderr {
			closeHandle(stderrRead)
			closeHandle(stderrWrite)
		}
	}()

	stdin, err := createNullInput()
	if err != nil {
		return nil, fmt.Errorf("create DSH stdin: %w", err)
	}
	closeStdin := true
	defer func() {
		if closeStdin {
			closeHandle(stdin)
		}
	}()

	application, argv := processCommand(plan.Executable, plan.Args)
	application16, err := win.UTF16PtrFromString(application)
	if err != nil {
		return nil, fmt.Errorf("encode DSH executable: %w", err)
	}
	commandLine16, err := win.UTF16FromString(win.ComposeCommandLine(argv))
	if err != nil {
		return nil, fmt.Errorf("encode DSH command line: %w", err)
	}
	environment16, err := environmentBlock(plan.Env)
	if err != nil {
		return nil, fmt.Errorf("encode DSH environment: %w", err)
	}
	workingDirectory16, err := win.UTF16PtrFromString(filepath.Clean(plan.WorkingDirectory))
	if err != nil {
		return nil, fmt.Errorf("encode DSH working directory: %w", err)
	}

	startup := win.StartupInfo{
		Cb:         uint32(unsafe.Sizeof(win.StartupInfo{})),
		Flags:      win.STARTF_USESTDHANDLES,
		StdInput:   stdin,
		StdOutput:  stdoutWrite,
		StdErr:     stderrWrite,
		ShowWindow: win.SW_HIDE,
	}
	processInfo := win.ProcessInformation{}
	creationFlags := uint32(win.CREATE_UNICODE_ENVIRONMENT | win.CREATE_SUSPENDED | win.CREATE_NEW_PROCESS_GROUP | win.CREATE_NO_WINDOW)
	if err := win.CreateProcess(application16, &commandLine16[0], nil, nil, true, creationFlags, &environment16[0], workingDirectory16, &startup, &processInfo); err != nil {
		return nil, fmt.Errorf("create DSH process: %w", err)
	}

	// The parent never needs the child-side handles. Closing them immediately
	// also guarantees that EOF is observable once the child exits.
	closeHandle(stdoutWrite)
	closeHandle(stderrWrite)
	closeHandle(stdin)
	closeStdout = false
	closeStderr = false
	closeStdin = false

	if err := win.AssignProcessToJobObject(job, processInfo.Process); err != nil {
		_ = win.TerminateProcess(processInfo.Process, 1)
		_, _ = win.WaitForSingleObject(processInfo.Process, 5000)
		closeHandle(processInfo.Thread)
		closeHandle(processInfo.Process)
		return nil, fmt.Errorf("assign DSH process to job object: %w", err)
	}

	if resumed, err := win.ResumeThread(processInfo.Thread); err != nil || resumed == ^uint32(0) {
		if err == nil {
			err = fmt.Errorf("ResumeThread returned %d", resumed)
		}
		_ = win.TerminateJobObject(job, 1)
		_, _ = win.WaitForSingleObject(processInfo.Process, 5000)
		closeHandle(processInfo.Thread)
		closeHandle(processInfo.Process)
		return nil, fmt.Errorf("resume DSH process: %w", err)
	}
	closeHandle(processInfo.Thread)

	worker := newJobWorker(job, processInfo.Process, processInfo.ProcessId, stdoutRead, stderrRead)
	closeJob = false
	closeStdout = false
	closeStderr = false
	return worker, nil
}

func createPipe() (win.Handle, win.Handle, error) {
	security := &win.SecurityAttributes{Length: uint32(unsafe.Sizeof(win.SecurityAttributes{})), InheritHandle: 1}
	var readHandle, writeHandle win.Handle
	if err := win.CreatePipe(&readHandle, &writeHandle, security, 0); err != nil {
		return 0, 0, err
	}
	if err := win.SetHandleInformation(readHandle, win.HANDLE_FLAG_INHERIT, 0); err != nil {
		closeHandle(readHandle)
		closeHandle(writeHandle)
		return 0, 0, err
	}
	return readHandle, writeHandle, nil
}

func createNullInput() (win.Handle, error) {
	path, err := win.UTF16PtrFromString("NUL")
	if err != nil {
		return 0, err
	}
	security := &win.SecurityAttributes{Length: uint32(unsafe.Sizeof(win.SecurityAttributes{})), InheritHandle: 1}
	return win.CreateFile(path, win.GENERIC_READ, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, security, win.OPEN_EXISTING, win.FILE_ATTRIBUTE_NORMAL, 0)
}

func processCommand(executable string, args []string) (string, []string) {
	if lower := strings.ToLower(executable); strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		commandArgs := make([]string, 0, len(args)+4)
		commandArgs = append(commandArgs, comspec, "/d", "/s", "/c", executable)
		commandArgs = append(commandArgs, args...)
		return comspec, commandArgs
	}
	commandArgs := make([]string, 0, len(args)+1)
	commandArgs = append(commandArgs, executable)
	commandArgs = append(commandArgs, args...)
	return executable, commandArgs
}

func environmentBlock(overrides map[string]string) ([]uint16, error) {
	entries := make(map[string]string)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			entries[strings.ToLower(key)] = entry
		}
	}
	for key, value := range overrides {
		entries[strings.ToLower(key)] = key + "=" + value
	}
	ordered := make([]string, 0, len(entries))
	for _, entry := range entries {
		ordered = append(ordered, entry)
	}
	sort.Slice(ordered, func(i, j int) bool {
		left, _, _ := strings.Cut(ordered[i], "=")
		right, _, _ := strings.Cut(ordered[j], "=")
		return strings.ToLower(left) < strings.ToLower(right)
	})

	block := make([]uint16, 0)
	for _, entry := range ordered {
		encoded, err := win.UTF16FromString(entry)
		if err != nil {
			return nil, err
		}
		block = append(block, encoded[:len(encoded)-1]...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return block, nil
}

type jobWorker struct {
	job         win.Handle
	process     win.Handle
	pid         uint32
	stdout      *os.File
	stderr      *os.File
	events      chan supervisor.OutputEvent
	stop        chan struct{}
	stopOnce    sync.Once
	closeOnce   sync.Once
	readers     sync.WaitGroup
	readersDone chan struct{}
	exited      chan struct{}
	exitMu      sync.RWMutex
	exit        supervisor.ExitResult
	tailMu      sync.RWMutex
	stdoutTail  string
	stderrTail  string
}

func newJobWorker(job, process win.Handle, pid uint32, stdoutHandle, stderrHandle win.Handle) *jobWorker {
	worker := &jobWorker{
		job:         job,
		process:     process,
		pid:         pid,
		stdout:      os.NewFile(uintptr(stdoutHandle), "dsh-stdout"),
		stderr:      os.NewFile(uintptr(stderrHandle), "dsh-stderr"),
		events:      make(chan supervisor.OutputEvent, 128),
		stop:        make(chan struct{}),
		readersDone: make(chan struct{}),
		exited:      make(chan struct{}),
	}
	worker.readers.Add(2)
	go worker.readOutput(supervisor.StreamStdout, worker.stdout)
	go worker.readOutput(supervisor.StreamStderr, worker.stderr)
	go worker.waitForExit()
	go func() {
		worker.readers.Wait()
		close(worker.readersDone)
		close(worker.events)
	}()
	return worker
}

func (w *jobWorker) Events() <-chan supervisor.OutputEvent { return w.events }

func (w *jobWorker) Exited() <-chan struct{} { return w.exited }

func (w *jobWorker) ExitResult() supervisor.ExitResult {
	w.exitMu.RLock()
	defer w.exitMu.RUnlock()
	return w.exit
}

func (w *jobWorker) RequestStop(ctx context.Context) error {
	select {
	case <-w.exited:
		return nil
	default:
	}
	command := exec.CommandContext(ctx, "taskkill.exe", "/PID", strconv.FormatUint(uint64(w.pid), 10), "/T")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: win.CREATE_NO_WINDOW}
	if err := command.Run(); err != nil {
		select {
		case <-w.exited:
			return nil
		default:
		}
		return err
	}
	return nil
}

func (w *jobWorker) ForceStop(_ context.Context) error {
	select {
	case <-w.exited:
		return nil
	default:
	}
	if err := win.TerminateJobObject(w.job, 1); err != nil {
		select {
		case <-w.exited:
			return nil
		default:
		}
		return err
	}
	return nil
}

func (w *jobWorker) WaitEmpty(ctx context.Context) error {
	for {
		active, err := w.activeProcesses()
		if err != nil {
			return err
		}
		if active == 0 {
			return nil
		}
		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (w *jobWorker) activeProcesses() (uint32, error) {
	// JOB_OBJECT_BASIC_PROCESS_ID_LIST returns the current process count and
	// avoids depending on the accounting record layout, which differs across
	// Windows SDKs.
	var info [256]byte
	if err := win.QueryInformationJobObject(w.job, win.JobObjectBasicProcessIdList, uintptr(unsafe.Pointer(&info[0])), uint32(len(info)), nil); err == nil {
		return *(*uint32)(unsafe.Pointer(&info[0])), nil
	}
	// A signaled job handle is the authoritative empty boundary. If the
	// process-list query is unavailable, retain a conservative non-zero value.
	event, err := win.WaitForSingleObject(w.job, 0)
	if err != nil {
		return 0, err
	}
	if event == win.WAIT_OBJECT_0 {
		return 0, nil
	}
	if event == waitTimeout {
		return 1, nil
	}
	return 0, fmt.Errorf("unexpected job wait result %d", event)
}

func (w *jobWorker) Diagnostics() supervisor.Diagnostics {
	w.tailMu.RLock()
	stdoutTail, stderrTail := w.stdoutTail, w.stderrTail
	w.tailMu.RUnlock()
	active, _ := w.activeProcesses()
	return supervisor.Diagnostics{
		PID:             w.pid,
		ActiveProcesses: active,
		StdoutTail:      stdoutTail,
		StderrTail:      stderrTail,
		Exit:            w.ExitResult(),
	}
}

func (w *jobWorker) Close() error {
	var closeErr error
	w.closeOnce.Do(func() {
		w.stopOnce.Do(func() { close(w.stop) })
		if active, err := w.activeProcesses(); err == nil && active > 0 {
			if err := win.TerminateJobObject(w.job, 1); err != nil {
				closeErr = err
			}
		}
		_, _ = win.WaitForSingleObject(w.process, 5000)
		_ = w.stdout.Close()
		_ = w.stderr.Close()
		select {
		case <-w.readersDone:
		case <-time.After(2 * time.Second):
			if closeErr == nil {
				closeErr = fmt.Errorf("timed out draining DSH output")
			}
		}
		closeHandle(w.process)
		closeHandle(w.job)
	})
	return closeErr
}

func (w *jobWorker) readOutput(stream supervisor.OutputStream, file *os.File) {
	defer w.readers.Done()
	buffer := make([]byte, 16*1024)
	line := make([]byte, 0, 1024)
	flush := func(force bool) {
		if len(line) == 0 && !force {
			return
		}
		rawText := string(line)
		text := supervisor.Redact(rawText)
		w.tailMu.Lock()
		if stream == supervisor.StreamStdout {
			w.stdoutTail = appendTail(w.stdoutTail, text)
		} else {
			w.stderrTail = appendTail(w.stderrTail, text)
		}
		w.tailMu.Unlock()
		select {
		case w.events <- supervisor.OutputEvent{Stream: stream, Text: text, RawText: rawText}:
		case <-w.stop:
		default:
		}
		line = line[:0]
	}
	for {
		count, err := file.Read(buffer)
		if count > 0 {
			for _, value := range buffer[:count] {
				if value == '\r' {
					continue
				}
				if value == '\n' {
					flush(true)
					continue
				}
				line = append(line, value)
				if len(line) >= outputLineLimit {
					flush(true)
				}
			}
		}
		if err != nil {
			if err != io.EOF {
				line = append(line, []byte(" [output read error]")...)
			}
			flush(false)
			return
		}
	}
}

func (w *jobWorker) waitForExit() {
	_, err := win.WaitForSingleObject(w.process, win.INFINITE)
	result := supervisor.ExitResult{Started: true}
	if err != nil {
		result.Err = err.Error()
	} else {
		var code uint32
		if err := win.GetExitCodeProcess(w.process, &code); err != nil {
			result.Err = err.Error()
		} else {
			result.Code = int(code)
		}
	}
	w.exitMu.Lock()
	w.exit = result
	w.exitMu.Unlock()
	close(w.exited)
}

func appendTail(current, addition string) string {
	if len(addition) >= outputTailLimit {
		return addition[len(addition)-outputTailLimit:]
	}
	combined := current + addition
	if len(combined) > outputTailLimit {
		combined = combined[len(combined)-outputTailLimit:]
	}
	return combined
}

func closeHandle(handle win.Handle) {
	if handle != 0 && handle != win.InvalidHandle {
		_ = win.CloseHandle(handle)
	}
}
