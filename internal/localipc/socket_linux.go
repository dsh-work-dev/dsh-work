//go:build linux

package localipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// PrivateDirectory refuses pre-existing symlinks and permissive directories.
// The shared parent must already exist (normally the OS temporary directory).
func PrivateDirectory(parent, name string) (string, error) {
	path := filepath.Join(parent, name)
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return "", err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&0777 != 0700 {
		return "", errors.New("local IPC directory is not private")
	}
	return path, nil
}

type listener struct {
	*net.UnixListener
	lock      *os.File
	path      string
	inode     os.FileInfo
	removeDir bool
	once      sync.Once
	err       error
}

// Listen serializes endpoint ownership with flock. Lock files remain in shared
// directories so a waiter cannot hold a detached lock inode. Only the lock
// owner may remove a stale socket, and only its own socket is removed on Close.
func Listen(path string, removeDir bool) (net.Listener, error) {
	// Fail at startup, rather than leaving a listener that rejects every peer
	// without explaining that this kernel cannot authenticate process instances.
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	pidfd, probeErr := unix.GetsockoptInt(pair[0], unix.SOL_SOCKET, unix.SO_PEERPIDFD)
	unix.Close(pair[0])
	unix.Close(pair[1])
	if probeErr != nil {
		return nil, fmt.Errorf("local IPC requires kernel SO_PEERPIDFD: %w", probeErr)
	}
	unix.Close(pidfd)
	dir := filepath.Dir(path)
	if _, err := PrivateDirectory(filepath.Dir(dir), filepath.Base(dir)); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path+".lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), path+".lock")
	ok := false
	defer func() {
		if !ok {
			lock.Close()
		}
	}()
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil {
		return nil, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Nlink != 1 {
		return nil, errors.New("local IPC lock rejected")
	}
	if err = unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, fmt.Errorf("local IPC endpoint already owned: %w", err)
	}
	if info, e := os.Lstat(path); e == nil {
		var existing unix.Stat_t
		if e = unix.Lstat(path, &existing); e != nil {
			return nil, e
		}
		if info.Mode()&os.ModeSocket == 0 || existing.Uid != uint32(os.Geteuid()) || existing.Mode&0777 != 0600 {
			return nil, errors.New("local IPC stale endpoint rejected")
		}
		if e = os.Remove(path); e != nil {
			return nil, e
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	l.SetUnlinkOnClose(false)
	if err = os.Chmod(path, 0600); err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		l.Close()
		os.Remove(path)
		return nil, err
	}
	ok = true
	return &listener{UnixListener: l, lock: lock, path: path, inode: info, removeDir: removeDir}, nil
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		c, err := l.UnixListener.AcceptUnix()
		if err != nil {
			return nil, err
		}
		if _, err = Peer(c); err == nil {
			return c, nil
		}
		c.Close()
		// Reject individual peers without terminating the daemon's Serve loop.
	}
}

func (l *listener) Close() error {
	l.once.Do(func() {
		l.err = l.UnixListener.Close()
		if info, err := os.Lstat(l.path); err == nil && os.SameFile(l.inode, info) {
			if err = os.Remove(l.path); err != nil {
				l.err = errors.Join(l.err, err)
			}
		}
		// A random per-launch directory has no other owners or waiters. Shared
		// daemon directories retain their lock inode across daemon lifetimes.
		if l.removeDir {
			if err := os.Remove(l.path + ".lock"); err != nil {
				l.err = errors.Join(l.err, err)
			}
		}
		l.err = errors.Join(l.err, l.lock.Close())
		if l.removeDir {
			l.err = errors.Join(l.err, os.Remove(filepath.Dir(l.path)))
		}
	})
	return l.err
}

func Dial(ctx context.Context, path string) (net.Conn, error) {
	if _, err := PrivateDirectory(filepath.Dir(filepath.Dir(path)), filepath.Base(filepath.Dir(path))); err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return nil, err
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&unix.S_IFMT != unix.S_IFSOCK || st.Mode&0777 != 0600 {
		return nil, errors.New("local IPC endpoint rejected")
	}
	c, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, err
	}
	if _, err = Peer(c); err != nil {
		c.Close()
		return nil, err
	}
	return c, nil
}
