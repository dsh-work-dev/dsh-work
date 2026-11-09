//go:build linux

package linux

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// An application may exit before adoption. Until the authenticated bootstrap
// calls Wait (only after the adoption ACK), its direct child is unreaped and
// cannot be replaced. Verify that kernel pidfd, parent and creation identity;
// unlike public IPC peer admission, a zombie here is a valid owned exit, not a
// live peer. This helper is never used to authenticate a socket connection.
func ownedProcessStarted(pid, parent uint32, pidfd int) (uint64, error) {
	actual, err := pidFromFD(pidfd)
	if err != nil || actual != pid {
		return 0, errors.New("owned pidfd instance rejected")
	}
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	end := strings.LastIndexByte(string(data), ')')
	if end < 0 {
		return 0, errors.New("owned process stat rejected")
	}
	fields := strings.Fields(string(data[end+1:]))
	if len(fields) < 20 || fields[0] == "X" || fields[0] == "x" {
		return 0, errors.New("owned process state rejected")
	}
	ppid, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil || uint32(ppid) != parent {
		return 0, errors.New("owned process parent changed")
	}
	started, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil || started == 0 {
		return 0, errors.New("owned creation identity rejected")
	}
	actual, err = pidFromFD(pidfd)
	if err != nil || actual != pid {
		return 0, errors.New("owned pidfd instance changed")
	}
	return started, nil
}
