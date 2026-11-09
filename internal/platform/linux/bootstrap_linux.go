//go:build linux

package linux

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/supervisor"
	"golang.org/x/sys/unix"
)

const bootstrapArgument = "--dsh-owned-systemd-bootstrap-v1"
const bootstrapLimit = 8 << 20

type bootstrapMessage struct {
	Kind    string                `json:"kind"`
	Started uint64                `json:"started,omitempty"`
	Errno   int                   `json:"errno,omitempty"`
	Result  supervisor.ExitResult `json:"result,omitempty"`
}

func frameWrite(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) > bootstrapLimit {
		return errors.New("bootstrap frame too large")
	}
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(data)))
	if _, err = w.Write(head[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
func frameRead(r io.Reader, value any) error {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n == 0 || n > bootstrapLimit {
		return errors.New("bootstrap frame size rejected")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}
func markerWrite(c *net.UnixConn, kind byte, fds ...int) error {
	var rights []byte
	if len(fds) > 0 {
		rights = unix.UnixRights(fds...)
	}
	n, _, err := c.WriteMsgUnix([]byte{kind}, rights, nil)
	if err == nil && n != 1 {
		err = io.ErrShortWrite
	}
	return err
}
func markerRead(c *net.UnixConn) (kind byte, fds []int, cred *unix.Ucred, err error) {
	data := make([]byte, 1)
	ancillary := make([]byte, unix.CmsgSpace(4*4)+unix.CmsgSpace(unix.SizeofUcred))
	n, oob, flags, _, err := c.ReadMsgUnix(data, ancillary)
	defer func() {
		if err != nil {
			for _, fd := range fds {
				unix.Close(fd)
			}
			fds = nil
		}
	}()
	if err != nil {
		return
	}
	messages, e := unix.ParseSocketControlMessage(ancillary[:oob])
	if e != nil {
		err = e
		return
	}
	for _, m := range messages {
		if m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SCM_RIGHTS {
			values, parseErr := unix.ParseUnixRights(&m)
			err = errors.Join(err, parseErr)
			fds = append(fds, values...)
		} else if m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SCM_CREDENTIALS {
			if cred != nil {
				err = errors.Join(err, errors.New("duplicate bootstrap credentials"))
			} else {
				value, parseErr := unix.ParseUnixCredentials(&m)
				cred = value
				err = errors.Join(err, parseErr)
			}
		} else {
			err = errors.Join(err, errors.New("bootstrap ancillary data rejected"))
		}
	}
	if err != nil {
		return
	}
	if n != 1 || flags&(unix.MSG_CTRUNC|unix.MSG_TRUNC) != 0 || cred == nil {
		err = errors.New("bootstrap message credentials/truncation rejected")
		return
	}
	kind = data[0]
	return
}
func unixConnection(file *os.File) (*net.UnixConn, error) {
	conn, err := net.FileConn(file)
	if err != nil {
		return nil, err
	}
	c, ok := conn.(*net.UnixConn)
	if !ok {
		conn.Close()
		return nil, errors.New("private Unix bootstrap socket required")
	}
	raw, err := c.SyscallConn()
	if err != nil {
		c.Close()
		return nil, err
	}
	var setErr error
	if err = raw.Control(func(fd uintptr) { setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_PASSCRED, 1) }); err == nil {
		err = setErr
	}
	if err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
func pidFromFD(fd int) (uint32, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/self/fdinfo/%d", fd))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "Pid:") {
			value, e := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "Pid:")), 10, 32)
			if e != nil || value == 0 {
				return 0, errors.New("actual application pidfd has no live namespace PID")
			}
			return uint32(value), nil
		}
	}
	return 0, errors.New("bootstrap did not return a kernel pidfd")
}

// RunBootstrap is a fixed internal executable entry, before desktop startup.
// It receives no executable, environment or credentials through argv. The
// private socket must have been created by the same executable's live process.
func RunBootstrap() bool {
	if len(os.Args) != 2 || os.Args[1] != bootstrapArgument {
		return false
	}
	os.Exit(bootstrapMain())
	return true
}
func bootstrapMain() int {
	stdin := os.NewFile(0, "private-bootstrap")
	c, err := unixConnection(stdin)
	if err != nil {
		return 90
	}
	defer c.Close()
	parent, err := localipc.Peer(c)
	if err != nil {
		return 91
	}
	self, _ := os.Executable()
	actual, e := os.Stat(fmt.Sprintf("/proc/%d/exe", parent.PID))
	expected, e2 := os.Stat(self)
	if e != nil || e2 != nil || !os.SameFile(actual, expected) {
		return 92
	}
	os.Clearenv()
	c.SetDeadline(time.Now().Add(10 * time.Second))
	if markerWrite(c, 'R') != nil {
		return 93
	}
	kind, rights, cred, err := markerRead(c)
	defer func() {
		for _, fd := range rights {
			unix.Close(fd)
		}
	}()
	if err != nil || kind != 'P' || len(rights) != 2 || cred.Pid != int32(parent.PID) || cred.Uid != uint32(os.Getuid()) {
		return 94
	}
	var plan supervisor.LaunchPlan
	if frameRead(c, &plan) != nil || plan.Validate() != nil || !filepath.IsAbs(plan.Executable) || !filepath.IsAbs(plan.WorkingDirectory) {
		return 95
	}
	stdout, stderr := os.NewFile(uintptr(rights[0]), "application-stdout"), os.NewFile(uintptr(rights[1]), "application-stderr")
	rights = nil
	defer stdout.Close()
	defer stderr.Close()
	cmd := exec.Command(plan.Executable, plan.Args...)
	cmd.Dir = plan.WorkingDirectory
	cmd.Env = make([]string, 0, len(plan.Env))
	for k, v := range plan.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	pidfd := -1
	cmd.SysProcAttr = &syscall.SysProcAttr{PidFD: &pidfd}
	if err = cmd.Start(); err != nil {
		var errno syscall.Errno
		errors.As(err, &errno)
		if markerWrite(c, 'E') == nil {
			frameWrite(c, bootstrapMessage{Kind: "launch-error", Errno: int(errno)})
		}
		return 96
	}
	defer unix.Close(pidfd)
	adopted := false
	defer func() {
		if !adopted {
			cmd.Process.Kill()
		}
	}()
	started, e := localipc.ProcessStarted(uint32(cmd.Process.Pid))
	if e != nil {
		return 97
	}
	if markerWrite(c, 'A', pidfd) != nil || frameWrite(c, bootstrapMessage{Kind: "started", Started: started}) != nil {
		return 98
	}
	var ack bootstrapMessage
	if frameRead(c, &ack) != nil || ack.Kind != "adopt" {
		return 99
	}
	adopted = true
	c.SetDeadline(time.Time{})
	exit := make(chan supervisor.ExitResult, 1)
	go func() {
		e := cmd.Wait()
		v := supervisor.ExitResult{Started: true}
		if cmd.ProcessState != nil {
			v.Code = cmd.ProcessState.ExitCode()
			if status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
				v.Code = 128 + int(status.Signal())
			}
		}
		if e != nil {
			var status *exec.ExitError
			if !errors.As(e, &status) {
				v.Err = e.Error()
			}
		}
		exit <- v
	}()
	commands := make(chan bootstrapMessage, 1)
	go func() {
		for {
			var m bootstrapMessage
			if frameRead(c, &m) != nil {
				commands <- bootstrapMessage{Kind: "eof"}
				return
			}
			commands <- m
		}
	}()
	finished := false
	for {
		select {
		case result := <-exit:
			finished = true
			if frameWrite(c, bootstrapMessage{Kind: "exit", Result: result}) != nil {
				return 100
			}
		case command := <-commands:
			if command.Kind == "finish" && finished {
				return 0
			}
			// EOF/invalid finish is abnormal owner loss. Killing this root and
			// exiting makes systemd reclaim every remaining process in this unit.
			cmd.Process.Kill()
			return 101
		}
	}
}
