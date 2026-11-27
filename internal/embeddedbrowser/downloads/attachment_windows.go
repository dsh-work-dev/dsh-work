//go:build windows

package downloads

import (
	"fmt"
	"net/url"
	"runtime"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	win "golang.org/x/sys/windows"
)

// IAttachmentExecute is used only for Save (never Execute or SaveWithUI).
// Windows' Attachment Services owns policy checks and source-zone marking.
// The vtable is the public shobjidl_core.h interface, not a private COM API.
type attachmentVtbl struct {
	QueryInterface, AddRef, Release                                                          uintptr
	SetClientTitle, SetClientGuid, SetLocalPath, SetFileName                                 uintptr
	SetSource, SetReferrer, CheckPolicy, Prompt, Save, Execute, SaveWithUI, ClearClientState uintptr
}
type attachment struct{ Vtable *attachmentVtbl }

var attachmentClass = ole.NewGUID("{4125DD96-E03A-4103-8F70-E0597D803B9C}")
var attachmentIID = ole.NewGUID("{73DB1241-1E85-4581-8E4F-A81E1D0F8C57}")
var downloadClient = ole.NewGUID("{E738C7BB-AC12-405E-9C0B-3A47785BC8F7}")

func comResult(hr uintptr) error {
	if int32(hr) < 0 {
		return ole.NewError(hr)
	}
	return nil
}
func attachmentString(a *attachment, method uintptr, value string) error {
	p, e := win.UTF16PtrFromString(value)
	if e != nil {
		return e
	}
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
	return comResult(hr)
}
func saveAttachment(path, name, source, referrer string) error {
	for _, s := range []string{source, referrer} {
		if s == "" {
			continue
		}
		u, e := url.Parse(s)
		if e != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return ErrRisk
		}
	}
	// Never initialize COM on Wails' UI thread.
	result := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if e := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED|ole.COINIT_DISABLE_OLE1DDE); e != nil {
			if oe, ok := e.(*ole.OleError); !ok || oe.Code() != 1 {
				result <- fmt.Errorf("%w: %v", ErrRisk, e)
				return
			}
		}
		defer ole.CoUninitialize()
		unknown, e := ole.CreateInstance(attachmentClass, attachmentIID)
		if e != nil {
			result <- fmt.Errorf("%w: %v", ErrRisk, e)
			return
		}
		defer unknown.Release()
		a := (*attachment)(unsafe.Pointer(unknown))
		hr, _, _ := syscall.SyscallN(a.Vtable.SetClientGuid, uintptr(unsafe.Pointer(a)), uintptr(unsafe.Pointer(downloadClient)))
		if e = comResult(hr); e == nil {
			e = attachmentString(a, a.Vtable.SetLocalPath, path)
		}
		if e == nil {
			e = attachmentString(a, a.Vtable.SetFileName, name)
		}
		if e == nil {
			e = attachmentString(a, a.Vtable.SetSource, source)
		}
		if e == nil && referrer != "" {
			e = attachmentString(a, a.Vtable.SetReferrer, referrer)
		}
		if e == nil {
			hr, _, _ = syscall.SyscallN(a.Vtable.Save, uintptr(unsafe.Pointer(a)))
			e = comResult(hr)
		}
		if e != nil {
			e = fmt.Errorf("%w: %v", ErrRisk, e)
		}
		result <- e
	}()
	return <-result
}

// CheckRisk runs Attachment Services against this owned stage only, with a
// non-delete share lock preventing replacement while a path-based scanner runs.
// It is called after the broker has drained all writing handles.
func (d *Destination) CheckRisk(source, referrer string) error {
	return d.checkRisk(source, referrer, saveAttachment)
}

func (d *Destination) checkRisk(source, referrer string, inspect func(string, string, string, string) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed || d.stage == nil || d.record.Published {
		return ErrClosed
	}
	if e := d.stage.Sync(); e != nil {
		return e
	}
	path := d.record.Parent + string(winPathSeparator) + d.record.Stage
	// AES needs to open the file itself. Drop our DELETE-access handle but hold a
	// read-attributes lease without FILE_SHARE_DELETE for the entire Save call.
	original, e := fileInfo(win.Handle(d.stage.Fd()))
	if e != nil {
		return e
	}
	if identity(original) != d.record.StageIdentity {
		return ErrOwnedFileChanged
	}
	if e = d.stage.Close(); e != nil {
		d.stage = nil
		return e
	}
	d.stage = nil
	lock, e := openChild(d.parent, d.record.Stage, win.FILE_READ_ATTRIBUTES, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_OPEN)
	if e != nil {
		return e
	}
	locked, e := fileInfo(lock)
	if e == nil && identity(locked) != d.record.StageIdentity {
		e = ErrOwnedFileChanged
	}
	if e == nil {
		e = inspect(path, d.record.Name, source, referrer)
	}
	closeErr := win.CloseHandle(lock)
	reopened, reopenErr := openChild(d.parent, d.record.Stage, win.FILE_GENERIC_READ|win.FILE_GENERIC_WRITE|win.DELETE, win.FILE_SHARE_READ|win.FILE_SHARE_WRITE, win.FILE_OPEN)
	if reopenErr == nil {
		i, ie := fileInfo(reopened)
		if ie != nil || identity(i) != d.record.StageIdentity {
			win.CloseHandle(reopened)
			reopenErr = ErrOwnedFileChanged
		} else {
			d.stage = newOwnedFile(reopened, path)
		}
	}
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return reopenErr
}

const winPathSeparator = '\\'
