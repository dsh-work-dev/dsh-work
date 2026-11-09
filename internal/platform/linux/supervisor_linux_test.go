//go:build linux

package linux

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
	"golang.org/x/sys/unix"
)

func TestMain(m *testing.M) {
	if RunBootstrap() {
		return
	}
	os.Exit(m.Run())
}

func TestSystemdOwnedHelper(t *testing.T) {
	role := os.Getenv("E67_SYSTEMD_HELPER")
	if role == "" {
		return
	}
	root := os.Getenv("E67_SYSTEMD_DIRECTORY")
	if os.Getenv("HOME") != "" || os.Getenv("PATH") != "" {
		os.Exit(77)
	}
	if role == "root" {
		exe, _ := os.Executable()
		child := exec.Command(exe, "-test.run=^TestSystemdOwnedHelper$")
		child.Env = []string{"E67_SYSTEMD_HELPER=child", "E67_SYSTEMD_DIRECTORY=" + root}
		child.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		child.Stdout = os.Stdout
		child.Stderr = os.Stderr
		if child.Start() != nil {
			os.Exit(78)
		}
		data, _ := json.Marshal(map[string]int{"pid": child.Process.Pid})
		if os.WriteFile(filepath.Join(root, "child.json"), data, 0600) != nil {
			os.Exit(79)
		}
		for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
			if _, err := os.Stat(filepath.Join(root, "root-release")); err == nil {
				os.Exit(0)
			}
		}
		os.Exit(80)
	}
	if role != "child" {
		os.Exit(81)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if _, err := os.Stat(filepath.Join(root, "child-release")); err == nil {
			os.Exit(0)
		}
	}
	os.Exit(82)
}

func TestLiveSystemdAtomicCgroupOwner(t *testing.T) {
	if os.Getenv("DSH_WORK_LINUX_SUPERVISOR_TEST") != "1" {
		t.Skip("requires explicitly authorized real user-manager lifecycle test")
	}
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			root := t.TempDir()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			adapter := &SystemdAdapter{}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			w, err := adapter.Start(ctx, supervisor.LaunchPlan{GenerationID: "e67-actual-unit", Executable: exe, Args: []string{"-test.run=^TestSystemdOwnedHelper$"}, WorkingDirectory: root, ExpectedOrigin: workeripc.Origin,
				Env: map[string]string{"E67_SYSTEMD_HELPER": "root", "E67_SYSTEMD_DIRECTORY": root}}, nil)
			if err != nil {
				t.Fatal("actual adapter start", err)
			}
			defer func() {
				if e := w.Close(); e != nil {
					t.Error("owned cleanup", e)
				}
			}()
			native := w.(*systemdWorker)
			d := w.Diagnostics()
			started, e := localipc.ProcessStarted(d.PID)
			if e != nil || d.ProcessStarted == 0 || started != d.ProcessStarted || d.ProcessStartedError != "" {
				t.Fatal("actual immutable launch instance", d, e)
			}
			if err = unix.PidfdSendSignal(native.pidfd, 0, nil, 0); err != nil {
				t.Fatal(err)
			}
			var child struct{ PID int }
			for {
				data, e := os.ReadFile(filepath.Join(root, "child.json"))
				if e == nil && json.Unmarshal(data, &child) == nil && child.PID != 0 {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(5 * time.Millisecond):
				}
			}
			childFD, e := unix.PidfdOpen(child.PID, 0)
			if e != nil {
				t.Fatal(e)
			}
			defer unix.Close(childFD)
			sid, e := unix.Getsid(child.PID)
			if e != nil || sid != child.PID {
				t.Fatal("fixture did not create independent session", sid, e)
			}
			if err = os.WriteFile(filepath.Join(root, "root-release"), []byte("release"), 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case <-w.Exited():
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if result := w.ExitResult(); result.Code != 0 || result.Err != "" {
				t.Fatal("root or explicit environment failed", result)
			}
			short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
			e = w.WaitEmpty(short)
			stop()
			if !errors.Is(e, context.DeadlineExceeded) || unix.PidfdSendSignal(childFD, 0, nil, 0) != nil {
				t.Fatal("root exit was mistaken for tree empty or child was killed", e)
			}
			if force {
				if e = w.ForceStop(ctx); e != nil {
					t.Fatal(e)
				}
			} else {
				if e = os.WriteFile(filepath.Join(root, "child-release"), []byte("release"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			if e = w.WaitEmpty(ctx); e != nil {
				t.Fatal("kernel cgroup empty barrier", e)
			}
			if w.Diagnostics().ActiveProcesses != 0 {
				t.Fatal("unfinished tree reported empty")
			}
			if e = w.Close(); e != nil {
				t.Fatal(e)
			}
			if e = w.Close(); e != nil {
				t.Fatal("close retry", e)
			}
			t.Logf("actual adapter unit=%s launchPID=%d startTicks=%d rootExit=0 escapedSessionChild=%d force=%v cgroupEmpty=true", native.unit, d.PID, d.ProcessStarted, child.PID, force)
		})
	}
}

func TestLiveSystemdFailedLaunchCleanup(t *testing.T) {
	if os.Getenv("DSH_WORK_LINUX_SUPERVISOR_TEST") != "1" {
		t.Skip("requires real user-manager")
	}
	a := &SystemdAdapter{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w, e := a.Start(ctx, supervisor.LaunchPlan{GenerationID: "e67-launch-failure", Executable: "/nonexistent/e67-not-a-program", WorkingDirectory: t.TempDir(), ExpectedOrigin: workeripc.Origin}, nil)
	if e == nil || w != nil {
		t.Fatal("nonexistent launch accepted", e)
	}
	if len(a.pending) != 0 {
		t.Fatal("failed launch cleanup retained unresolved unit", e)
	}
	if !errors.Is(e, syscall.ENOENT) {
		t.Fatal("owned startup cleanup completed, but missing-executable gate was not reached; earlier launch failure", e)
	}
	if e = a.Close(); e != nil {
		t.Fatal(e)
	}
	t.Log("actual failed os/exec launch drained owned anchor/unit; no application process returned")
}

func TestLiveSystemdCancelBeforeAdoption(t *testing.T) {
	if os.Getenv("DSH_WORK_LINUX_SUPERVISOR_TEST") != "1" {
		t.Skip("requires real user-manager")
	}
	root := t.TempDir()
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	held := -1
	a := &SystemdAdapter{beforeAdopt: func(fd int) {
		var err error
		held, err = unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		if err != nil {
			t.Error(err)
		}
		cancel()
	}}
	w, e := a.Start(ctx, supervisor.LaunchPlan{GenerationID: "e67-cancel", Executable: exe, Args: []string{"-test.run=^TestSystemdOwnedHelper$"}, WorkingDirectory: root, ExpectedOrigin: workeripc.Origin, Env: map[string]string{"E67_SYSTEMD_HELPER": "root", "E67_SYSTEMD_DIRECTORY": root}}, nil)
	if held < 0 {
		t.Fatal("actual application handshake not reached", e)
	}
	defer unix.Close(held)
	if !errors.Is(e, context.Canceled) || w != nil {
		t.Fatal("canceled adoption returned usable owner", e)
	}
	if len(a.pending) != 0 {
		t.Fatal("canceled partial launch retained unfinished unit", e)
	}
	poll := []unix.PollFd{{Fd: int32(held), Events: unix.POLLIN}}
	if n, err := unix.Poll(poll, 3000); err != nil || n != 1 {
		t.Fatal("original partially launched application did not exit", n, err)
	}
	t.Log("actual application pidfd received before adoption; caller canceled; independent unit cleanup joined original process and all descendants")
}
