//go:build linux

package linux

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestLiveSystemdCommandExecutor(t *testing.T) {
	if os.Getenv("DSH_WORK_LINUX_SUPERVISOR_TEST") != "1" {
		t.Skip("requires real systemd user manager")
	}
	t.Setenv("E67_COMMAND_INHERITED", "native-environment")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := &SystemdAdapter{beforeInstanceCheck: func(fd int) {
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, 5000)
		if err != nil || n != 1 {
			t.Fatalf("short command did not exit before parent instance check: %d %v", n, err)
		}
	}}
	executor := NewCommandExecutor(owner)
	result, err := executor.Run(ctx, "/bin/sh", []string{"-c", `printf '%s/%s' "$E67_COMMAND_INHERITED" "$E67_COMMAND_OVERLAY"; printf 'command-stderr' >&2`}, map[string]string{"E67_COMMAND_OVERLAY": "child-only"}, t.TempDir())
	if err != nil || !strings.Contains(result.Stdout, "native-environment/child-only") || !strings.Contains(result.Stderr, "command-stderr") {
		t.Fatalf("actual command seam output=%+v error=%v", result, err)
	}
	if os.Getenv("E67_COMMAND_OVERLAY") != "" {
		t.Fatal("child overlay modified caller")
	}
	if len(executor.pending) != 0 {
		t.Fatal("normal command did not join owner")
	}
	t.Log("real command exited before adoption; inherited environment/child-only overlay/output/normal tree join passed")
}

func TestOwnedProcessExitedBeforePublish(t *testing.T) {
	fd := -1
	cmd := exec.Command("/bin/true")
	cmd.SysProcAttr = &syscall.SysProcAttr{PidFD: &fd}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	defer cmd.Wait()
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	if n, err := unix.Poll(poll, 5000); err != nil || n != 1 {
		t.Fatalf("controlled child exit: %d %v", n, err)
	}
	started, err := ownedProcessStarted(uint32(cmd.Process.Pid), uint32(os.Getpid()), fd)
	if err != nil || started == 0 {
		t.Fatal("unreaped owned exit before bootstrap publication rejected", started, err)
	}
	if _, err = ownedProcessStarted(uint32(cmd.Process.Pid), uint32(os.Getpid()+1), fd); err == nil {
		t.Fatal("foreign parent accepted")
	}
}
