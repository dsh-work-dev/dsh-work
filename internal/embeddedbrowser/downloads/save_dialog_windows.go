//go:build windows

package downloads

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	win "golang.org/x/sys/windows"
)

// DialogOwner must come from the admitted native presenter, never page fields.
type DialogOwner struct {
	Window  uintptr
	PID     uint32
	Started uint64
}

var uiDLL = win.NewLazySystemDLL("user32.dll")
var setTimer = uiDLL.NewProc("SetTimer")
var killTimer = uiDLL.NewProc("KillTimer")
var postMessage = uiDLL.NewProc("PostMessageW")
var getWindow = uiDLL.NewProc("GetWindow")
var getAncestor = uiDLL.NewProc("GetAncestor")
var getGUIThreadInfo = uiDLL.NewProc("GetGUIThreadInfo")
var messageBox = uiDLL.NewProc("MessageBoxW")

type guiThreadInfo struct {
	Size                                               uint32
	Flags                                              uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	Left, Top, Right, Bottom                           int32
}
type saveDialogVtbl struct {
	QueryInterface, AddRef, Release, Show                                               uintptr
	SetFileTypes, SetFileTypeIndex, GetFileTypeIndex, Advise, Unadvise                  uintptr
	SetOptions, GetOptions, SetDefaultFolder, SetFolder, GetFolder, GetCurrentSelection uintptr
	SetFileName, GetFileName, SetTitle, SetOkButtonLabel, SetFileNameLabel, GetResult   uintptr
	AddPlace, SetDefaultExtension, Close, SetClientGuid, ClearClientData, SetFilter     uintptr
}
type saveDialog struct{ Vtable *saveDialogVtbl }
type saveModal struct {
	ctx     context.Context
	owner   DialogOwner
	process win.Handle
	dialog  *saveDialog
	window  uintptr
	thread  uint32
}

var modalMu sync.Mutex

type modalKey struct {
	thread uint32
	id     uintptr
}

var modals = map[modalKey]*saveModal{}
var timerCallback = syscall.NewCallback(func(_ uintptr, _ uintptr, id uintptr, _ uintptr) uintptr {
	modalMu.Lock()
	m := modals[modalKey{win.GetCurrentThreadId(), id}]
	modalMu.Unlock()
	if m == nil {
		return 0
	}
	if m.window == 0 {
		if m.dialog != nil {
			iid := ole.NewGUID("{00000114-0000-0000-C000-000000000046}")
			var object *ole.IUnknown
			hr, _, _ := syscall.SyscallN(m.dialog.Vtable.QueryInterface, uintptr(unsafe.Pointer(m.dialog)), uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&object)))
			if comResult(hr) == nil && object != nil {
				type windowVtbl struct{ QueryInterface, AddRef, Release, GetWindow, ContextSensitiveHelp uintptr }
				v := *(**windowVtbl)(unsafe.Pointer(object))
				var h uintptr
				hr, _, _ = syscall.SyscallN(v.GetWindow, uintptr(unsafe.Pointer(object)), uintptr(unsafe.Pointer(&h)))
				object.Release()
				if comResult(hr) == nil {
					m.window = h
				}
			}
		} else {
			info := guiThreadInfo{Size: uint32(unsafe.Sizeof(guiThreadInfo{}))}
			ok, _, _ := getGUIThreadInfo.Call(uintptr(m.thread), uintptr(unsafe.Pointer(&info)))
			if ok != 0 {
				var pid uint32
				thread, _ := win.GetWindowThreadProcessId(win.HWND(info.Active), &pid)
				owner, _, _ := getWindow.Call(info.Active, 4)
				if thread == m.thread && pid == win.GetCurrentProcessId() && owner == m.owner.Window {
					m.window = info.Active
				}
			}
		}
	}
	canceled := m.ctx.Err() != nil || !validOwner(m.owner, m.process)
	if canceled && m.window != 0 {
		var pid uint32
		thread, _ := win.GetWindowThreadProcessId(win.HWND(m.window), &pid)
		owner, _, _ := getWindow.Call(m.window, 4) // GW_OWNER, not process-wide search.
		if thread == m.thread && pid == win.GetCurrentProcessId() && owner == m.owner.Window {
			// IFileDialog::Close is not reliable outside an IFileDialogEvents callback.
			// Same-object IOleWindow HWND + same STA/PID/owner then WM_CLOSE matches
			// Chromium156 AutoCloseDialogEventHandler. No global input or enumeration.
			postMessage.Call(m.window, 0x10, 0, 0)
		}
	}
	return 0
})

func validOwner(o DialogOwner, process win.Handle) bool {
	if o.Window == 0 || o.PID == 0 || process == 0 || !win.IsWindowVisible(win.HWND(o.Window)) {
		return false
	}
	var created, exit, kernel, user win.Filetime
	if win.GetProcessTimes(process, &created, &exit, &kernel, &user) != nil || uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != o.Started {
		return false
	}
	state, e := win.WaitForSingleObject(process, 0)
	if e != nil || state != uint32(win.WAIT_TIMEOUT) {
		return false
	}
	var pid uint32
	if _, e = win.GetWindowThreadProcessId(win.HWND(o.Window), &pid); e != nil || pid != o.PID {
		return false
	}
	root, _, _ := getAncestor.Call(o.Window, 2)
	return root == o.Window
}
func runOwnedModal(ctx context.Context, owner DialogOwner, show func(*saveModal) (string, error)) (string, error) {
	result := make(chan struct {
		value string
		err   error
	}, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		value, err := "", error(nil)
		defer func() {
			result <- struct {
				value string
				err   error
			}{value, err}
		}()
		process, e := win.OpenProcess(win.PROCESS_QUERY_LIMITED_INFORMATION|win.SYNCHRONIZE, false, owner.PID)
		if e != nil {
			err = e
			return
		}
		defer win.CloseHandle(process)
		if !validOwner(owner, process) || ctx.Err() != nil {
			err = ErrApprovalExpired
			return
		}
		if e = ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED|ole.COINIT_DISABLE_OLE1DDE); e != nil {
			if oe, ok := e.(*ole.OleError); !ok || oe.Code() != 1 {
				err = e
				return
			}
		}
		defer ole.CoUninitialize()
		m := &saveModal{ctx: ctx, owner: owner, process: process, thread: win.GetCurrentThreadId()}
		id, _, e := setTimer.Call(0, 0, 50, timerCallback)
		if id == 0 {
			err = e
			return
		}
		modalMu.Lock()
		key := modalKey{m.thread, id}
		modals[key] = m
		modalMu.Unlock()
		defer func() { killTimer.Call(0, id); modalMu.Lock(); delete(modals, key); modalMu.Unlock() }()
		value, err = show(m)
		if !validOwner(owner, process) || ctx.Err() != nil {
			value = ""
			err = ErrApprovalExpired
		}
	}()
	r := <-result
	return r.value, r.err // Drain the actual STA before returning.
}
func dialogString(d *saveDialog, method uintptr, s string) error {
	p, e := win.UTF16PtrFromString(s)
	if e != nil {
		return e
	}
	hr, _, _ := syscall.SyscallN(method, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(p)))
	runtime.KeepAlive(p)
	return comResult(hr)
}
func chooseNativePath(ctx context.Context, owner DialogOwner, p Pending) (string, error) {
	return runOwnedModal(ctx, owner, func(m *saveModal) (string, error) {
		clsid := ole.NewGUID("{C0B4E2F3-BA21-4773-8DBA-335EC946EB8B}")
		iid := ole.NewGUID("{84BCCD23-5FDE-4CDB-AEA4-AF64B83D78AB}")
		unknown, e := ole.CreateInstance(clsid, iid)
		if e != nil {
			return "", e
		}
		defer unknown.Release()
		d := (*saveDialog)(unsafe.Pointer(unknown))
		m.dialog = d
		var flags uint32
		hr, _, _ := syscall.SyscallN(d.Vtable.GetOptions, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&flags)))
		if e = comResult(hr); e != nil {
			return "", e
		}
		flags = flags&^uint32(2) | 0x40 | 0x800 | 0x8 | 0x2000000 // no overwrite prompt; filesystem/path required.
		hr, _, _ = syscall.SyscallN(d.Vtable.SetOptions, uintptr(unsafe.Pointer(d)), uintptr(flags))
		if e = comResult(hr); e != nil {
			return "", e
		}
		if e = dialogString(d, d.Vtable.SetFileName, SuggestedName(p.SuggestedName)); e != nil {
			return "", e
		}
		if e = dialogString(d, d.Vtable.SetTitle, "保存来自 "+p.SourceOrigin+" 的文件"); e != nil {
			return "", e
		}
		if e = dialogString(d, d.Vtable.SetOkButtonLabel, "选择位置"); e != nil {
			return "", e
		}
		if !validOwner(owner, m.process) || ctx.Err() != nil {
			return "", ErrApprovalExpired
		}
		hr, _, _ = syscall.SyscallN(d.Vtable.Show, uintptr(unsafe.Pointer(d)), owner.Window)
		if uint32(hr) == 0x800704c7 {
			return "", nil
		}
		if e = comResult(hr); e != nil {
			return "", e
		}
		var item *ole.IUnknown
		hr, _, _ = syscall.SyscallN(d.Vtable.GetResult, uintptr(unsafe.Pointer(d)), uintptr(unsafe.Pointer(&item)))
		if e = comResult(hr); e != nil {
			return "", e
		}
		defer item.Release()
		type shellVtbl struct{ QueryInterface, AddRef, Release, BindToHandler, GetParent, GetDisplayName uintptr }
		table := *(**shellVtbl)(unsafe.Pointer(item))
		var path *uint16
		hr, _, _ = syscall.SyscallN(table.GetDisplayName, uintptr(unsafe.Pointer(item)), 0x80058000, uintptr(unsafe.Pointer(&path)))
		if e = comResult(hr); e != nil {
			return "", e
		}
		defer ole.CoTaskMemFree(uintptr(unsafe.Pointer(path)))
		return win.UTF16PtrToString(path), nil
	})
}
func confirmNative(ctx context.Context, owner DialogOwner, title, message string) (bool, error) {
	value, e := runOwnedModal(ctx, owner, func(m *saveModal) (string, error) {
		titlePtr, e := win.UTF16PtrFromString(title)
		if e != nil {
			return "", e
		}
		textPtr, e := win.UTF16PtrFromString(message)
		if e != nil {
			return "", e
		}
		// OK/CANCEL, default CANCEL; WM_CLOSE is a real cancel, unlike YES/NO.
		response, _, e := messageBox.Call(owner.Window, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), 0x1|0x100|0x30)
		if response == 0 {
			return "", e
		}
		if response == 1 {
			return "allow", nil
		}
		return "", nil
	})
	return value == "allow", e
}

// ChooseDestination is Host-only and returns an object capability captured
// before its one overwrite/risk confirmation. A late result is always discarded.
func ChooseDestination(ctx context.Context, owner DialogOwner, p Pending, stillCurrent func(context.Context, Pending) bool) (Selection, error) {
	if stillCurrent == nil || !stillCurrent(ctx, p) {
		return Selection{}, ErrApprovalExpired
	}
	path, e := chooseNativePath(ctx, owner, p)
	if e != nil || path == "" {
		return Selection{}, e
	}
	if !stillCurrent(ctx, p) {
		return Selection{}, ErrApprovalExpired
	}
	d, e := PrepareDestination(path, true)
	if e != nil {
		return Selection{}, e
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = d.Abort()
			_ = d.Close()
		}
	}()
	r := d.Record()
	risk := RiskyName(r.Name, p.MIME)
	overwrite := r.Previous != nil
	if overwrite || risk {
		action := "保存此文件？"
		if overwrite {
			action = "替换现有文件并保存？"
		}
		text := fmt.Sprintf("%s\n\n来源：%s\n文件：%s", action, p.SourceOrigin, r.Name)
		if risk {
			text += "\n\n此类文件可能运行代码或改变设备设置。"
		}
		yes, e := confirmNative(ctx, owner, "确认下载", text)
		if e != nil {
			return Selection{}, e
		}
		if !yes {
			return Selection{}, nil
		}
	}
	if ctx.Err() != nil || !stillCurrent(ctx, p) {
		return Selection{}, ErrApprovalExpired
	}
	accepted = true
	return Selection{Path: path, OverwriteConfirmed: overwrite, RiskConfirmed: risk, Prepared: d}, nil
}
func ConfirmContentRisk(ctx context.Context, owner DialogOwner, s Snapshot) bool {
	yes, e := confirmNative(ctx, owner, "确认下载", "文件内容与预期类型不同。\n\n来源："+s.SourceOrigin+"\n文件："+s.Filename+"\n\n仍要保存此文件？")
	return e == nil && yes
}
