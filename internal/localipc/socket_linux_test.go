//go:build linux

package localipc

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateTestDirectory(t *testing.T) string {
	t.Helper()
	dir, err := PrivateDirectory(t.TempDir(), "private")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPeerProcessAndExit(t *testing.T) {
	if path := os.Getenv("DSH_LOCALIPC_TEST_SOCKET"); path != "" {
		c, err := net.Dial("unix", path)
		if err != nil {
			os.Exit(2)
		}
		defer c.Close()
		var b [1]byte
		os.Stdin.Read(b[:])
		return
	}
	path := filepath.Join(privateTestDirectory(t), "peer.sock")
	l, err := Listen(path, false)
	if err != nil {
		t.Fatal(err)
	} // Missing kernel support is a real failed check.
	defer l.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPeerProcessAndExit$")
	cmd.Env = append(os.Environ(), "DSH_LOCALIPC_TEST_SOCKET="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	l.(*listener).SetDeadline(time.Now().Add(5 * time.Second))
	c, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	p, err := Peer(c)
	if err != nil {
		t.Fatal(err)
	}
	started, err := ProcessStarted(uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	if p.PID != uint32(cmd.Process.Pid) || p.Started != started {
		t.Fatalf("wrong peer: %+v", p)
	}
	stdin.Close()
	if err = cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err = Peer(c); err == nil {
		t.Fatal("exited peer accepted")
	}
}

func TestSocketOwnershipAndCleanup(t *testing.T) {
	dir := privateTestDirectory(t)
	path := filepath.Join(dir, "daemon.sock")
	l, err := Listen(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if other, err := Listen(path, false); err == nil {
		other.Close()
		t.Fatal("second endpoint owner accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := Dial(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	accepted, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket retained: %v", err)
	}
	// A foreign live listener does not participate in this module's lock
	// protocol. Acquiring our lock must not replace its endpoint.
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	if err = os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if replacement, err := Listen(path, false); err == nil {
		replacement.Close()
		stale.Close()
		t.Fatal("foreign live listener replaced")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		stale.Close()
		t.Fatalf("foreign live endpoint changed: %v", err)
	}
	stale.Close()
	// Once that listener exits, only an explicit connection refusal permits
	// recovery of the stale socket under the persistent lock inode.
	l, err = Listen(path, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
}

func TestRejectUnsafePathsAndPreserveReplacement(t *testing.T) {
	dir := privateTestDirectory(t)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := PrivateDirectory(dir, "link"); err == nil {
		t.Fatal("symlink directory accepted")
	}
	path := filepath.Join(dir, "peer.sock")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if l, err := Listen(path, false); err == nil {
		l.Close()
		t.Fatal("regular endpoint removed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	l, err := Listen(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = l.Close(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "replacement" {
		t.Fatalf("replacement removed: %s %v", data, err)
	}
}

func TestParseProcessCreation(t *testing.T) {
	stat := "42 (a ) tricky name) S " + strings.Repeat("0 ", 18) + "123 0"
	if got, err := parseStarted(stat); err != nil || got != 123 {
		t.Fatalf("%d %v", got, err)
	}
	if _, err := parseStarted(strings.Replace(stat, ") S ", ") Z ", 1)); err == nil {
		t.Fatal("zombie accepted")
	}
	fmt.Println("Linux process identity: kernel socket peer pidfd + live /proc start ticks")
}
