//go:build windows

package downloads

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	win "golang.org/x/sys/windows"
)

// Destination retains a directory handle. All children and renames are relative
// to that handle, never re-resolved through a mutable absolute parent path.
type Destination struct {
	mu     sync.Mutex
	parent win.Handle
	stage  *os.File
	record FileRecord
	closed bool
}

func newOwnedFile(h win.Handle, path string) *os.File { return os.NewFile(uintptr(h), path) }

func PrepareDestination(path string, overwrite bool) (*Destination, error) {
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\.\`) {
		return nil, ErrDestinationChanged
	}
	path = filepath.Clean(path)
	name := filepath.Base(path)
	if !validChild(name) {
		return nil, ErrDestinationChanged
	}
	parentPath := filepath.Dir(path)
	ptr, err := win.UTF16PtrFromString(parentPath)
	if err != nil {
		return nil, err
	}
	parent, err := win.CreateFile(ptr, win.FILE_READ_ATTRIBUTES|win.FILE_TRAVERSE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, nil, win.OPEN_EXISTING, win.FILE_FLAG_BACKUP_SEMANTICS|win.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, err
	}
	d := &Destination{parent: parent}
	ok := false
	defer func() {
		if !ok {
			_ = d.Abort()
			_ = d.Close()
		}
	}()
	info, err := fileInfo(parent)
	if err != nil {
		return nil, err
	}
	if info.FileAttributes&win.FILE_ATTRIBUTE_REPARSE_POINT != 0 || info.FileAttributes&win.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return nil, ErrDestinationChanged
	}
	canonical, err := finalPath(parent)
	if err != nil {
		return nil, err
	}
	d.record = FileRecord{Parent: canonical, ParentIdentity: identity(info), Name: name}
	target, err := openChild(parent, name, win.FILE_READ_ATTRIBUTES, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE|win.FILE_SHARE_DELETE, win.FILE_OPEN)
	if err == nil {
		ti, e := fileInfo(target)
		win.CloseHandle(target)
		if e != nil {
			return nil, e
		}
		if ti.FileAttributes&(win.FILE_ATTRIBUTE_DIRECTORY|win.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
			return nil, ErrDestinationChanged
		}
		if !overwrite {
			return nil, ErrDestinationChanged
		}
		v := version(ti)
		d.record.Previous = &v
	} else if !notExist(err) {
		return nil, err
	}
	// A random file is exclusively created, with its final extension retained for AES.
	d.record.Stage = ".dsh-download-" + rand.Text() + filepath.Ext(name)
	h, err := openChild(parent, d.record.Stage, win.FILE_GENERIC_READ|win.FILE_GENERIC_WRITE|win.DELETE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_CREATE)
	if err != nil {
		return nil, err
	}
	d.stage = os.NewFile(uintptr(h), filepath.Join(canonical, d.record.Stage))
	si, err := fileInfo(h)
	if err != nil {
		return nil, err
	}
	d.record.StageIdentity = identity(si)
	ok = true
	return d, nil
}
func validChild(s string) bool {
	return s != "" && s != "." && s != ".." && filepath.IsLocal(s) && !strings.ContainsAny(s, `<>:"/\|?*`) && !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, " ")
}
func fileInfo(h win.Handle) (win.ByHandleFileInformation, error) {
	var i win.ByHandleFileInformation
	e := win.GetFileInformationByHandle(h, &i)
	return i, e
}
func identity(i win.ByHandleFileInformation) FileIdentity {
	return FileIdentity{i.VolumeSerialNumber, uint64(i.FileIndexHigh)<<32 | uint64(i.FileIndexLow)}
}
func version(i win.ByHandleFileInformation) FileVersion {
	return FileVersion{identity(i), uint64(i.FileSizeHigh)<<32 | uint64(i.FileSizeLow), uint64(i.LastWriteTime.HighDateTime)<<32 | uint64(i.LastWriteTime.LowDateTime)}
}
func finalPath(h win.Handle) (string, error) {
	b := make([]uint16, 32768)
	n, e := win.GetFinalPathNameByHandle(h, &b[0], uint32(len(b)), 0)
	if e != nil {
		return "", e
	}
	if n >= uint32(len(b)) {
		return "", ErrDestinationChanged
	}
	return win.UTF16ToString(b[:n]), nil
}
func notExist(err error) bool {
	return errors.Is(err, win.STATUS_OBJECT_NAME_NOT_FOUND) || errors.Is(err, win.STATUS_OBJECT_PATH_NOT_FOUND) || errors.Is(err, win.ERROR_FILE_NOT_FOUND) || errors.Is(err, win.ERROR_PATH_NOT_FOUND)
}
func openChild(parent win.Handle, name string, access, share, disposition uint32) (win.Handle, error) {
	if !validChild(name) {
		return 0, ErrDestinationChanged
	}
	u, e := win.NewNTUnicodeString(name)
	if e != nil {
		return 0, e
	}
	a := win.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: u, Attributes: win.OBJ_CASE_INSENSITIVE | win.OBJ_DONT_REPARSE}
	a.Length = uint32(unsafe.Sizeof(a))
	var h win.Handle
	var ios win.IO_STATUS_BLOCK
	e = win.NtCreateFile(&h, access|win.SYNCHRONIZE, &a, &ios, nil, win.FILE_ATTRIBUTE_NORMAL, share, disposition, win.FILE_NON_DIRECTORY_FILE|win.FILE_OPEN_REPARSE_POINT|win.FILE_SYNCHRONOUS_IO_NONALERT, 0, 0)
	return h, e
}
func (d *Destination) Record() FileRecord {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.record
	if r.Previous != nil {
		v := *r.Previous
		r.Previous = &v
	}
	return r
}

// DuplicateWriter accepts a trusted already-held broker process, not a page PID.
// The duplicate can write this file only; it has no DELETE or read permission.
// The caller owns closing the remote handle and must report drained first.
func (d *Destination) DuplicateWriter(process win.Handle) (uintptr, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil {
		return 0, ErrClosed
	}
	var h win.Handle
	err := win.DuplicateHandle(win.CurrentProcess(), win.Handle(d.stage.Fd()), process, &h, win.FILE_WRITE_DATA|win.SYNCHRONIZE, false, 0)
	return uintptr(h), err
}
func (d *Destination) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil {
		return 0, ErrClosed
	}
	return d.stage.Write(p)
}
func (d *Destination) Sync() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil {
		return ErrClosed
	}
	return d.stage.Sync()
}

func (d *Destination) ContentType() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil {
		return "", ErrClosed
	}
	b := make([]byte, 512)
	n, err := d.stage.ReadAt(b, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return http.DetectContentType(b[:n]), nil
}

// renameNoReplace uses a held source and parent handle. It intentionally does
// not use os.Root.Rename (which uses replacement/POSIX flags on Windows).
func renameNoReplace(source, parent win.Handle, name string) error {
	if !validChild(name) {
		return ErrDestinationChanged
	}
	u, e := win.UTF16FromString(name)
	if e != nil {
		return e
	}
	type renameInfo struct {
		Replace uint32
		Root    win.Handle
		Length  uint32
		Name    [1]uint16
	}
	var header renameInfo
	offset := unsafe.Offsetof(header.Name)
	b := make([]byte, int(offset)+(len(u)-1)*2)
	r := (*renameInfo)(unsafe.Pointer(&b[0]))
	r.Root = parent
	r.Length = uint32((len(u) - 1) * 2)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(&b[offset])), len(u)-1), u[:len(u)-1])
	var ios win.IO_STATUS_BLOCK
	e = win.NtSetInformationFile(source, &ios, &b[0], uint32(len(b)), win.FileRenameInformation)
	runtime.KeepAlive(b)
	return e
}
func deleteHeld(h win.Handle) error {
	value := uint32(1)
	return win.SetFileInformationByHandle(h, win.FileDispositionInfo, (*byte)(unsafe.Pointer(&value)), 4)
}
func (d *Destination) checkParent() error {
	info, e := fileInfo(d.parent)
	if e != nil {
		return e
	}
	if identity(info) != d.record.ParentIdentity {
		return ErrDestinationChanged
	}
	path, e := finalPath(d.parent)
	if e != nil {
		return e
	}
	if !strings.EqualFold(path, d.record.Parent) {
		return ErrDestinationChanged
	}
	return nil
}

// Publish persists the journal before moving an approved old object. On a
// collision after that move, the original is restored without replacement or
// retained at Recovery; an unrelated new occupant is never removed.
func (d *Destination) Publish(journal func(FileRecord) error) (result error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	defer func() {
		if d.record.Published && d.stage != nil {
			result = errors.Join(result, d.stage.Close())
			d.stage = nil
		}
	}()
	if d.closed || d.stage == nil {
		return ErrClosed
	}
	if journal == nil {
		return errors.New("DOWNLOAD_JOURNAL_REQUIRED")
	}
	if e := d.checkParent(); e != nil {
		return e
	}
	if e := d.stage.Sync(); e != nil {
		return e
	}
	si, e := fileInfo(win.Handle(d.stage.Fd()))
	if e != nil {
		return e
	}
	if identity(si) != d.record.StageIdentity {
		return ErrOwnedFileChanged
	}
	var old win.Handle
	if d.record.Previous != nil {
		old, e = openChild(d.parent, d.record.Name, win.FILE_READ_ATTRIBUTES|win.DELETE, win.FILE_SHARE_READ, win.FILE_OPEN)
		if e != nil {
			return fmt.Errorf("%w: %v", ErrDestinationBusy, e)
		}
		defer win.CloseHandle(old)
		oi, e := fileInfo(old)
		if e != nil {
			return e
		}
		if version(oi) != *d.record.Previous {
			return ErrDestinationChanged
		}
		d.record.Recovery = ".dsh-download-recovery-" + rand.Text() + filepath.Ext(d.record.Name)
		if e = journal(d.record); e != nil {
			return e
		}
		if e = renameNoReplace(old, d.parent, d.record.Recovery); e != nil {
			return e
		}
		d.record.Phase = "previous-moved"
		if e = journal(d.record); e != nil {
			if restore := renameNoReplace(old, d.parent, d.record.Name); restore == nil {
				d.record.Recovery = ""
				d.record.Phase = ""
			}
			return e
		}
	}
	if e = renameNoReplace(win.Handle(d.stage.Fd()), d.parent, d.record.Name); e != nil {
		if old != 0 {
			if restore := renameNoReplace(old, d.parent, d.record.Name); restore == nil {
				d.record.Recovery = ""
			} else {
				_ = journal(d.record)
				return fmt.Errorf("DOWNLOAD_RECOVERY_REQUIRED: %s: %w", filepath.Join(d.record.Parent, d.record.Recovery), e)
			}
		}
		_ = journal(d.record)
		return e
	}
	d.record.Published = true
	d.record.Phase = "published"
	if e = journal(d.record); e != nil {
		return e
	} // Keep the original recovery on journal failure.
	if old != 0 {
		// Explicit overwrite grant covers only this held original, after durable publication.
		if e = deleteHeld(old); e != nil {
			return fmt.Errorf("DOWNLOAD_RECOVERY_RETAINED: %s: %w", filepath.Join(d.record.Parent, d.record.Recovery), e)
		}
		d.record.Recovery = ""
		if e = journal(d.record); e != nil {
			return e
		}
	}
	return nil
}

// Abort removes the held owned stage, never a name re-opened without identity.
// It must be called only after the remote writer has drained.
func (d *Destination) Abort() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	if d.record.Published {
		return nil
	}
	if d.stage == nil {
		// A released handle is not evidence that the owned partial disappeared.
		h, err := openChild(d.parent, d.record.Stage, win.FILE_READ_ATTRIBUTES|win.DELETE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_OPEN)
		if notExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		defer win.CloseHandle(h)
		info, err := fileInfo(h)
		if err != nil {
			return err
		}
		if identity(info) != d.record.StageIdentity {
			return ErrOwnedFileChanged
		}
		return deleteHeld(h)
	}
	i, e := fileInfo(win.Handle(d.stage.Fd()))
	if e != nil {
		return e
	}
	if identity(i) != d.record.StageIdentity {
		return ErrOwnedFileChanged
	}
	return deleteHeld(win.Handle(d.stage.Fd()))
}
func (d *Destination) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	var e error
	if d.stage != nil {
		e = d.stage.Close()
		d.stage = nil
	}
	if d.parent != 0 {
		e = errors.Join(e, win.CloseHandle(d.parent))
		d.parent = 0
	}
	return e
}
