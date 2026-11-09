//go:build linux

package embeddedbrowser

import (
	"github.com/local/dsh-work/internal/localipc"
	"os"
	"strconv"
)

func nativeEnvironment(env map[string]string) error {
	pid := uint32(os.Getpid())
	started, err := localipc.ProcessStarted(pid)
	if err != nil {
		return err
	}
	env["DSH_WORK_CEF_HOST_PID"] = strconv.FormatUint(uint64(pid), 10)
	env["DSH_WORK_CEF_HOST_STARTED"] = strconv.FormatUint(started, 10)
	return nil
}
