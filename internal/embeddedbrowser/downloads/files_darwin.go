//go:build darwin

package downloads

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

const macStagePrefix = ".dsh-download-"
const macPartial = "partial"
const macPrevious = "previous"

// Destination holds both the approved directory and the private transaction
// directory open. Only the additional write-only file can leave the Host.
type Destination struct {
	mu       sync.Mutex
	parent   int
	stageDir int
	stage    *os.File
	record   FileRecord
	closed   bool
}

func PrepareDestination(path string, overwrite bool) (*Destination, error) {
	if !filepath.IsAbs(path) || !macChild(filepath.Base(path)) {
		return nil, ErrDestinationChanged
	}
	parentPath, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	parent, err := openMacDownloadDirectory(unix.AT_FDCWD, parentPath)
	if err != nil {
		return nil, err
	}
	d := &Destination{parent: parent, stageDir: -1}
	complete := false
	defer func() {
		if !complete {
			_ = d.Abort()
			_ = d.Close()
		}
	}()
	var pi unix.Stat_t
	if err = unix.Fstat(parent, &pi); err != nil {
		return nil, err
	}
	d.record = FileRecord{Parent: parentPath, ParentIdentity: macIdentity(pi), Name: filepath.Base(path)}
	previous, err := macStatAt(parent, d.record.Name)
	if err == nil {
		if !overwrite || !macRegular(previous) {
			return nil, ErrDestinationChanged
		}
		v := macVersion(previous)
		d.record.Previous = &v
	} else if !errors.Is(err, unix.ENOENT) {
		return nil, err
	}
	directory := macStagePrefix + rand.Text()
	if err = unix.Mkdirat(parent, directory, 0700); err != nil {
		return nil, err
	}
	d.stageDir, err = openMacDownloadDirectory(parent, directory)
	if err != nil {
		return nil, err
	}
	var di unix.Stat_t
	if err = unix.Fstat(d.stageDir, &di); err != nil || !macPrivateDirectory(di) {
		return nil, errors.Join(ErrOwnedFileChanged, err)
	}
	diIdentity := macIdentity(di)
	d.record.StageDirectory = &diIdentity
	d.record.Stage = filepath.Join(directory, macPartial)
	writer, err := unix.Openat(d.stageDir, macPartial, unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	d.stage = os.NewFile(uintptr(writer), macPartial)
	var si unix.Stat_t
	if err = unix.Fstat(writer, &si); err != nil || !macRegular(si) {
		return nil, errors.Join(ErrOwnedFileChanged, err)
	}
	d.record.StageIdentity = macIdentity(si)
	if err = errors.Join(unix.Fsync(parent), unix.Fsync(d.stageDir)); err != nil {
		return nil, err
	}
	complete = true
	return d, nil
}

func macChild(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.IsLocal(name) &&
		!strings.ContainsAny(name, "/\\:\x00")
}

func openMacDownloadDirectory(parent int, path string) (int, error) {
	return unix.Openat(parent, path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW_ANY|unix.O_CLOEXEC, 0)
}

func macStatAt(parent int, name string) (unix.Stat_t, error) {
	var info unix.Stat_t
	err := unix.Fstatat(parent, name, &info, unix.AT_SYMLINK_NOFOLLOW)
	return info, err
}

func macIdentity(info unix.Stat_t) FileIdentity {
	return FileIdentity{Volume: uint32(info.Dev), Index: info.Ino}
}

func macVersion(info unix.Stat_t) FileVersion {
	return FileVersion{Identity: macIdentity(info), Size: uint64(info.Size), Modified: uint64(info.Mtim.Nano())}
}

func macRegular(info unix.Stat_t) bool {
	return info.Mode&unix.S_IFMT == unix.S_IFREG && info.Nlink == 1
}

func macPrivateDirectory(info unix.Stat_t) bool {
	return info.Mode&unix.S_IFMT == unix.S_IFDIR && info.Mode&0777 == 0700 && info.Uid == uint32(os.Geteuid())
}

func (d *Destination) Record() FileRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.record
	if r.Previous != nil {
		v := *r.Previous
		r.Previous = &v
	}
	if r.StageDirectory != nil {
		v := *r.StageDirectory
		r.StageDirectory = &v
	}
	return r
}

// OpenWriter verifies the stage name from the held private directory and
// creates an independent open file description with write-only access.
func (d *Destination) OpenWriter() (*os.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil || d.record.Published {
		return nil, ErrClosed
	}
	fd, err := unix.Openat(d.stageDir, macPartial, unix.O_WRONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var info unix.Stat_t
	if err = unix.Fstat(fd, &info); err != nil || !macRegular(info) || macIdentity(info) != d.record.StageIdentity {
		_ = unix.Close(fd)
		return nil, errors.Join(ErrOwnedFileChanged, err)
	}
	return os.NewFile(uintptr(fd), macPartial), nil
}

func (d *Destination) ContentType() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil || d.record.Published {
		return "", ErrClosed
	}
	buffer := make([]byte, 512)
	n, err := d.stage.ReadAt(buffer, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return http.DetectContentType(buffer[:n]), nil
}

func macRenameExclusive(source int, old string, target int, name string) error {
	return unix.RenameatxNp(source, old, target, name, unix.RENAME_EXCL|unix.RENAME_NOFOLLOW_ANY)
}

func (d *Destination) checkMacParent() error {
	fd, err := openMacDownloadDirectory(unix.AT_FDCWD, d.record.Parent)
	if err != nil {
		return errors.Join(ErrDestinationChanged, err)
	}
	defer unix.Close(fd)
	var parent unix.Stat_t
	if err = unix.Fstat(fd, &parent); err != nil || macIdentity(parent) != d.record.ParentIdentity {
		return errors.Join(ErrDestinationChanged, err)
	}
	return nil
}

func (d *Destination) checkMacNames() error {
	if err := d.checkMacParent(); err != nil {
		return err
	}
	if err := d.checkMacStageDirectory(); err != nil {
		return err
	}
	stage, err := macStatAt(d.stageDir, macPartial)
	if err != nil || !macRegular(stage) || macIdentity(stage) != d.record.StageIdentity {
		return errors.Join(ErrOwnedFileChanged, err)
	}
	return nil
}

func (d *Destination) checkMacStageDirectory() error {
	dir, err := macStatAt(d.parent, filepath.Dir(d.record.Stage))
	if err != nil || !macPrivateDirectory(dir) || macIdentity(dir) != *d.record.StageDirectory {
		return errors.Join(ErrOwnedFileChanged, err)
	}
	return nil
}

func (d *Destination) checkMacPublished() error {
	if err := d.checkMacParent(); err != nil {
		return err
	}
	info, err := macStatAt(d.parent, d.record.Name)
	if err != nil || !macRegular(info) || macIdentity(info) != d.record.StageIdentity {
		return errors.Join(ErrDestinationChanged, err)
	}
	return nil
}

// Publish is called after the writer drains. Replacement has two exclusive
// renames and a durable recovery intent. A late occupant is never overwritten.
func (d *Destination) Publish(journal func(FileRecord) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil || d.record.Published || d.record.Recovery != "" {
		return ErrClosed
	}
	if journal == nil {
		return errors.New("DOWNLOAD_JOURNAL_REQUIRED")
	}
	if err := d.checkMacNames(); err != nil {
		return err
	}
	if err := d.stage.Sync(); err != nil {
		return err
	}
	if d.record.Previous != nil {
		original, err := macStatAt(d.parent, d.record.Name)
		if err != nil || !macRegular(original) || macVersion(original) != *d.record.Previous {
			return errors.Join(ErrDestinationChanged, err)
		}
		d.record.Recovery = filepath.Join(filepath.Dir(d.record.Stage), macPrevious)
		if err = journal(d.record); err != nil {
			d.record.Recovery = ""
			return err
		}
		if err = macRenameExclusive(d.parent, d.record.Name, d.stageDir, macPrevious); err != nil {
			return err
		}
		moved, err := macStatAt(d.stageDir, macPrevious)
		if err != nil || !macRegular(moved) || macVersion(moved) != *d.record.Previous {
			return errors.Join(ErrDestinationChanged, err, d.restoreMacPrevious(journal))
		}
		d.record.Phase = "previous-moved"
		if err = errors.Join(unix.Fsync(d.parent), unix.Fsync(d.stageDir)); err != nil {
			return errors.Join(err, d.restoreMacPrevious(journal))
		}
		if err = journal(d.record); err != nil {
			return errors.Join(err, d.restoreMacPrevious(journal))
		}
	} else if err := journal(d.record); err != nil {
		return err
	}
	if err := d.checkMacNames(); err != nil {
		return errors.Join(err, d.restoreMacPrevious(journal))
	}
	if err := macRenameExclusive(d.stageDir, macPartial, d.parent, d.record.Name); err != nil {
		return errors.Join(err, d.restoreMacPrevious(journal))
	}
	if err := d.checkMacPublished(); err != nil {
		return err
	}
	d.record.Published, d.record.Phase = true, "published"
	if err := errors.Join(unix.Fsync(d.parent), unix.Fsync(d.stageDir)); err != nil {
		return err
	}
	if err := journal(d.record); err != nil {
		return err
	}
	if d.record.Recovery != "" {
		if err := d.checkMacPublished(); err != nil {
			d.record.Published, d.record.Phase = false, "previous-moved"
			return err
		}
		if err := d.checkMacStageDirectory(); err != nil {
			return err
		}
		if err := removeMacPrevious(d.stageDir, d.record.Previous); err != nil {
			return err
		}
		d.record.Recovery = ""
		if err := journal(d.record); err != nil {
			return err
		}
	}
	return d.removeMacStageDir()
}

func (d *Destination) restoreMacPrevious(journal func(FileRecord) error) error {
	if d.record.Recovery == "" {
		return nil
	}
	if err := macRenameExclusive(d.stageDir, macPrevious, d.parent, d.record.Name); err != nil {
		return fmt.Errorf("DOWNLOAD_RECOVERY_REQUIRED: %w", err)
	}
	d.record.Recovery, d.record.Phase = "", ""
	return errors.Join(unix.Fsync(d.parent), unix.Fsync(d.stageDir), journal(d.record))
}

func removeMacPrevious(stageDir int, expected *FileVersion) error {
	old, err := macStatAt(stageDir, macPrevious)
	if err != nil || expected == nil || !macRegular(old) || macVersion(old) != *expected {
		return errors.Join(ErrOwnedFileChanged, err)
	}
	if err = unix.Unlinkat(stageDir, macPrevious, 0); err != nil {
		return err
	}
	return unix.Fsync(stageDir)
}

func (d *Destination) Abort() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stageDir < 0 || d.record.Published {
		return nil
	}
	if err := removeMacPartial(d.stageDir, d.record.StageIdentity); err != nil {
		return err
	}
	if d.record.Recovery != "" {
		return errors.New("DOWNLOAD_RECOVERY_REQUIRED")
	}
	return d.removeMacStageDir()
}

func removeMacPartial(dir int, expected FileIdentity) error {
	info, err := macStatAt(dir, macPartial)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil || !macRegular(info) || macIdentity(info) != expected {
		return errors.Join(ErrOwnedFileChanged, err)
	}
	return unix.Unlinkat(dir, macPartial, 0)
}

func (d *Destination) removeMacStageDir() error {
	if d.record.StageDirectory == nil {
		return ErrOwnedFileChanged
	}
	return removeMacStageDir(d.parent, filepath.Dir(d.record.Stage), *d.record.StageDirectory)
}

func removeMacStageDir(parent int, name string, expected FileIdentity) error {
	info, err := macStatAt(parent, name)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil || !macPrivateDirectory(info) || macIdentity(info) != expected {
		return errors.Join(ErrOwnedFileChanged, err)
	}
	if err = unix.Unlinkat(parent, name, unix.AT_REMOVEDIR); err != nil {
		return err
	}
	return unix.Fsync(parent)
}

func (d *Destination) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	var err error
	if d.stage != nil {
		err = d.stage.Close()
	}
	if d.stageDir >= 0 {
		err = errors.Join(err, unix.Close(d.stageDir))
	}
	return errors.Join(err, unix.Close(d.parent))
}
