//go:build darwin && cgo

package automation

/*
#cgo LDFLAGS: -framework Security
#include <Security/AuthSession.h>
#include <stdint.h>

static uint32_t dsh_computer_graphical_session(void) {
	SecuritySessionId session = noSecuritySession;
	SessionAttributeBits attributes = 0;
	if (SessionGetInfo(callerSecuritySession, &session, &attributes) != errSessionSuccess ||
		!(attributes & sessionHasGraphicAccess)) {
		return 0;
	}
	return session;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/local/dsh-work/internal/localipc"
	"golang.org/x/sys/unix"
)

func AcquireComputerDesktopLease() (func() error, error) {
	session := uint32(C.dsh_computer_graphical_session())
	if session == 0 {
		return nil, fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", ErrUnavailable)
	}
	return acquireMacComputerLease(localipc.SocketTempRoot(), uint32(os.Geteuid()), session)
}

func acquireMacComputerLease(root string, uid, session uint32) (func() error, error) {
	unavailable := func(err error) (func() error, error) {
		return nil, fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", err)
	}
	// The GUI session namespace is independent of a DSH data directory.
	directory := filepath.Join(root, fmt.Sprintf("dsh-work-computer-%d", uid))
	if err := os.Mkdir(directory, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return unavailable(err)
	}
	dir, err := unix.Open(directory, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return unavailable(err)
	}
	defer unix.Close(dir)
	var parent unix.Stat_t
	if err := unix.Fstat(dir, &parent); err != nil {
		return unavailable(err)
	}
	if parent.Uid != uid || parent.Mode&07777 != 0700 {
		return unavailable(errors.New("desktop lease directory is not private"))
	}
	name := fmt.Sprintf("session-%08x.lock", session)
	fd, err := unix.Openat(dir, name, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return unavailable(err)
	}
	acquired := false
	defer func() {
		if !acquired {
			_ = unix.Close(fd)
		}
	}()
	var file unix.Stat_t
	if err := unix.Fstat(fd, &file); err != nil {
		return unavailable(err)
	}
	if file.Uid != uid || file.Mode != unix.S_IFREG|0600 || file.Nlink != 1 {
		return unavailable(errors.New("desktop lease file is not private"))
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.New("COMPUTER_BUSY: another DSH Work process owns this desktop")
		}
		return unavailable(err)
	}
	var current unix.Stat_t
	if err := unix.Fstatat(dir, name, &current, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return unavailable(err)
	}
	if current.Dev != file.Dev || current.Ino != file.Ino || current.Mode != file.Mode || current.Uid != uid || current.Nlink != 1 {
		return unavailable(errors.New("desktop lease file changed during acquisition"))
	}
	acquired = true
	// Keep the lock inode in place. Closing the descriptor also drops an
	// abandoned lease when the owning process exits.
	var once sync.Once
	var releaseErr error
	return func() error {
		once.Do(func() { releaseErr = unix.Close(fd) })
		return releaseErr
	}, nil
}
