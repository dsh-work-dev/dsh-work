//go:build darwin && cgo && e67nativefixture

package macowner

import (
	"context"
	"crypto/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/localipc"
)

func TestAuthenticatedBrokerRegistrationAcceptsKernelExecutableAlias(t *testing.T) {
	const generation = "cef-registrationalias"
	const childSocketEnv = "DSH_WORK_TEST_BROKER_ALIAS_SOCKET"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if socket := os.Getenv(childSocketEnv); socket != "" {
		c, err := localipc.Dial(ctx, socket)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		if err := writeFrame(c, message{Kind: "broker", Generation: generation}); err != nil {
			t.Fatal(err)
		}
		var reply message
		if err := readFrame(c, &reply); err != nil || reply.Kind != "registered" {
			t.Fatalf("broker registration reply = %q, error = %v", reply.Kind, err)
		}
		return
	}

	root, err := os.MkdirTemp(localipc.SocketTempRoot(), "dsh-alias-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(root, "broker")
	if err := os.WriteFile(executable, data, 0700); err != nil {
		t.Fatal(err)
	}
	device, inode, err := fileIdentity(executable)
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "owner.sock")
	listener, err := localipc.Listen(socket, false)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	child := exec.CommandContext(ctx, executable, "-test.run", "^"+t.Name()+"$")
	child.Env = append(os.Environ(), childSocketEnv+"="+socket)
	child.Stdout, child.Stderr = os.Stdout, os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	}()
	type connection struct {
		net.Conn
		err error
	}
	accepted := make(chan connection)
	go func() {
		c, err := listener.Accept()
		select {
		case accepted <- connection{c, err}:
		case <-ctx.Done():
			if c != nil {
				c.Close()
			}
		}
	}()
	var c net.Conn
	select {
	case conn := <-accepted:
		if conn.err != nil {
			t.Fatal(conn.err)
		}
		c = conn.Conn
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	defer c.Close()

	birth, err := process(uint32(child.Process.Pid))
	if err != nil || birth.Path != executable || birth.PPID != uint32(os.Getpid()) || birth.State != "live" {
		t.Fatalf("initial broker identity = %+v, error = %v", birth, err)
	}
	alias := filepath.Join(root, "clone")
	if err := os.Link(executable, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(alias); err != nil {
		t.Fatal(err)
	}
	renamed, err := process(birth.PID)
	if err != nil || renamed.Path != alias || renamed.Started != birth.Started || renamed.PPID != birth.PPID {
		t.Fatalf("kernel did not expose the alias for the same process: %+v, error = %v", renamed, err)
	}

	g := &guard{
		key: make([]byte, 32), brokerReady: make(chan struct{}), wake: make(chan struct{}, 1),
		brokerDevice: device, brokerInode: inode,
		state: journal{Generation: generation, Owner: Process{PID: birth.PPID}, Broker: birth, Owned: make(map[string]owned)},
	}
	if _, err := rand.Read(g.key); err != nil {
		t.Fatal(err)
	}
	g.cfg.Directory = root
	g.cfg.Plan.GenerationID = generation
	g.cfg.Plan.Executable = executable
	close(g.brokerReady)
	g.serve(c)
	if err := child.Wait(); err != nil {
		t.Fatal("authenticated alias was not registered", err, g.state.NativeEvents)
	}
	state, err := loadGeneration(root, g.key, generation)
	if err != nil {
		t.Fatal(err)
	}
	record := state.Owned[processKey(birth)]
	if record.Role != "broker:" || record.Process.Path != alias || record.Target == nil ||
		record.Target.PID != birth.PID || record.Target.Started != birth.Started {
		t.Fatalf("durable broker record did not capture the authenticated executable alias: %+v", record)
	}
}
