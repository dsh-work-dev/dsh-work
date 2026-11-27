//go:build darwin && cgo && e67nativefixture

package automation

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

func TestLiveMacComputerTargetRejectsStaleAndForeignWindow(t *testing.T) {
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	executable := filepath.Join(t.TempDir(), "computer-window-fixture")
	compile := exec.CommandContext(ctx, "clang", "-fblocks", "-framework", "AppKit", "testdata/computer-window-fixture.m", "-o", executable)
	if data, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("compile native fixture: %v %s", err, data)
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	type fixture struct {
		pid    uint32
		window uint64
		send   io.WriteCloser
		lines  *bufio.Scanner
	}
	start := func() fixture {
		t.Helper()
		cmd := exec.CommandContext(ctx, executable)
		cmd.Stderr = os.Stderr
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = input.Close()
			_ = cmd.Wait()
		})
		lines := bufio.NewScanner(out)
		if !lines.Scan() {
			t.Fatalf("fixture did not publish its owned window: %v", lines.Err())
		}
		window, err := strconv.ParseUint(lines.Text(), 10, 32)
		if err != nil || window == 0 {
			t.Fatalf("invalid native window number: %v", err)
		}
		return fixture{pid: uint32(cmd.Process.Pid), window: window, send: input, lines: lines}
	}
	read := func(f fixture) ComputerTarget {
		t.Helper()
		for ctx.Err() == nil {
			target, err := InspectComputerTarget(f.pid, f.window)
			if err == nil {
				return target
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatal("real Core Graphics window did not become inspectable")
		return ComputerTarget{}
	}
	first := start()
	a := read(first)
	started, err := localipc.ProcessStarted(first.pid)
	if err != nil || a.PID != first.pid || a.WindowID != first.window || a.Created != started ||
		a.Level != "blocked" || a.Executable != executable || a.WindowIdentity == "" {
		t.Fatalf("live process/window identity or own-child exclusion mismatch: %+v %v", a, err)
	}
	if again := read(first); again != a {
		t.Fatalf("unchanged window lost its identity: %+v → %+v", a, again)
	}
	second := start()
	b := read(second)
	if a.PID == b.PID || a.WindowIdentity == b.WindowIdentity {
		t.Fatal("distinct owned windows received the same identity")
	}
	if background := read(first); background.WindowID != a.WindowID || background.Created != a.Created {
		t.Fatalf("ordering a second window affected the first target: %+v → %+v", a, background)
	}
	if stale, err := InspectComputerTarget(second.pid, first.window); err == nil || stale != (ComputerTarget{}) {
		t.Fatalf("window accepted with a different live PID: %+v %v", stale, err)
	}
	seen := map[uint64]bool{first.window: true, second.window: true}
	for range 128 {
		retired := first.window
		if _, err := fmt.Fprintln(first.send, "replace"); err != nil {
			t.Fatal(err)
		}
		if !first.lines.Scan() {
			t.Fatalf("fixture did not publish the replacement window: %v", first.lines.Err())
		}
		id, err := strconv.ParseUint(first.lines.Text(), 10, 32)
		if err != nil || id == 0 || seen[id] {
			t.Fatalf("replacement reused an earlier Quartz window ID: %d (%v)", id, err)
		}
		seen[id] = true
		first.window = id
		target := read(first)
		if target.Created != a.Created || target.Title != a.Title || target.WindowIdentity == a.WindowIdentity {
			t.Fatalf("replacement did not keep process and title while changing window identity: %+v", target)
		}
		if stale, err := InspectComputerTarget(first.pid, retired); err == nil || stale != (ComputerTarget{}) {
			t.Fatalf("previous window survived replacement in the same process: %+v %v", stale, err)
		}
	}
	t.Logf("checked %d identical replacements in PID %d", len(seen)-2, first.pid)
	if _, err := fmt.Fprintln(first.send, "close"); err != nil {
		t.Fatal(err)
	}
	if !first.lines.Scan() || first.lines.Text() != "closed" {
		t.Fatal("fixture window close was not confirmed")
	}
	for ctx.Err() == nil {
		if stale, err := InspectComputerTarget(first.pid, first.window); err != nil && stale == (ComputerTarget{}) {
			if _, err := localipc.ProcessStarted(first.pid); err != nil {
				t.Fatal("window rejection was only observed after its owning process exited")
			}
			_ = read(second)
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("closed window remained inspectable")
}
