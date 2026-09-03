//go:build !windows

package app

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockProcessFile(file *os.File) (func() error, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errProcessLockHeld
		}
		return nil, err
	}
	return func() error {
		return unix.Flock(int(file.Fd()), unix.LOCK_UN)
	}, nil
}
