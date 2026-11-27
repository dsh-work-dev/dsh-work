//go:build windows

package devrunner

import (
	"context"
	"errors"
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
	"unsafe"
)

func hideCommand(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
func ordinaryContext() error {
	var length uint32
	code, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentPackageFullName").Call(uintptr(unsafe.Pointer(&length)), 0)
	if code != 15700 {
		return errors.New("serve must run in an ordinary unpackaged terminal; Codex uses controller commands only")
	}
	return nil
}
func verifyNode(ctx context.Context, root, node string) error {
	command := exec.CommandContext(ctx, node, root+"/scripts/verify-node.mjs")
	hideCommand(command)
	output, err := command.CombinedOutput()
	if err != nil {
		return errors.New("ordinary-terminal Node preflight failed: " + string(output))
	}
	return nil
}

func processStarted() (uint64, error) {
	var created, exit, kernel, user windows.Filetime
	err := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exit, &kernel, &user)
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), err
}
