//go:build linux

package linux

import (
	"context"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Available checks the prerequisites without starting a process or mutating
// manager configuration. Actual unit creation and adoption remain mandatory at
// every Start; this is not a promise of delegation or future resource capacity.
func Available(ctx context.Context) error {
	var fs unix.Statfs_t
	if err := unix.Statfs("/sys/fs/cgroup", &fs); err != nil {
		return err
	}
	if fs.Type != unix.CGROUP2_SUPER_MAGIC {
		return errors.New(unavailable + ": cgroup v2 required")
	}
	fd, err := unix.PidfdOpen(os.Getpid(), 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	c, err := manager(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, c.Close)
	defer stop()
	_, err = c.GetUnitPropertyContext(ctx, "-.slice", "ActiveState")
	return err
}
