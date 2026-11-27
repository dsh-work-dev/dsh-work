//go:build windows

package desktopapp

import (
	"context"
	"fmt"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/settings"
	"github.com/wailsapp/wails/v3/pkg/application"
	win "golang.org/x/sys/windows"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// beta16's Windows Question only maps literal Yes/No labels. Use the system
// three-choice MessageBox directly, as the existing download risk UI does.
// Default Cancel preserves downloads. Same-thread timer cancellation joins the
// real modal return before its caller can release the admission barrier.
var shutdownUser32 = win.NewLazySystemDLL("user32.dll")
var shutdownMessageBox = shutdownUser32.NewProc("MessageBoxW")
var shutdownSetTimer = shutdownUser32.NewProc("SetTimer")
var shutdownKillTimer = shutdownUser32.NewProc("KillTimer")
var shutdownGUIThread = shutdownUser32.NewProc("GetGUIThreadInfo")
var shutdownGetWindow = shutdownUser32.NewProc("GetWindow")
var shutdownPostMessage = shutdownUser32.NewProc("PostMessageW")

type shutdownGUIInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	Left, Top, Right, Bottom                           int32
}
type shutdownModal struct {
	ctx         context.Context
	thread, pid uint32
	birth       uint64
	window      uintptr
}

var shutdownModalMu sync.Mutex
var shutdownModals = map[uint32]*shutdownModal{}

func (m *shutdownModal) sameWindow() bool {
	var pid uint32
	thread, err := win.GetWindowThreadProcessId(win.HWND(m.window), &pid)
	var c, e, k, u win.Filetime
	owner, _, _ := shutdownGetWindow.Call(m.window, 4)
	return m.window != 0 && err == nil && pid == m.pid && thread == m.thread && owner == 0 && win.GetProcessTimes(win.CurrentProcess(), &c, &e, &k, &u) == nil && uint64(c.HighDateTime)<<32|uint64(c.LowDateTime) == m.birth
}

var shutdownTimer = syscall.NewCallback(func(_, _, _, _ uintptr) uintptr {
	shutdownModalMu.Lock()
	m := shutdownModals[win.GetCurrentThreadId()]
	shutdownModalMu.Unlock()
	if m == nil {
		return 0
	}
	if m.window == 0 {
		info := shutdownGUIInfo{Size: uint32(unsafe.Sizeof(shutdownGUIInfo{}))}
		ok, _, _ := shutdownGUIThread.Call(uintptr(m.thread), uintptr(unsafe.Pointer(&info)))
		if ok != 0 {
			m.window = info.Active
			if !m.sameWindow() {
				m.window = 0
			}
		}
	}
	if m.ctx.Err() != nil && m.sameWindow() {
		shutdownPostMessage.Call(m.window, 0x10, 0, 0)
	}
	return 0
})

func shutdownQuestion(ctx context.Context, message string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var c, e, k, u win.Filetime
	if err := win.GetProcessTimes(win.CurrentProcess(), &c, &e, &k, &u); err != nil {
		return 0, err
	}
	m := &shutdownModal{ctx: ctx, thread: win.GetCurrentThreadId(), pid: win.GetCurrentProcessId(), birth: uint64(c.HighDateTime)<<32 | uint64(c.LowDateTime)}
	timer, _, err := shutdownSetTimer.Call(0, 0, 30, shutdownTimer)
	if timer == 0 {
		return 0, err
	}
	shutdownModalMu.Lock()
	shutdownModals[m.thread] = m
	shutdownModalMu.Unlock()
	defer func() {
		shutdownKillTimer.Call(0, timer)
		shutdownModalMu.Lock()
		delete(shutdownModals, m.thread)
		shutdownModalMu.Unlock()
	}()
	text, _ := win.UTF16PtrFromString(message)
	title, _ := win.UTF16PtrFromString("DSH Work")
	id, _, err := shutdownMessageBox.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), 0x3|0x20|0x200|0x10000)
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if id == 0 {
		return 0, fmt.Errorf("download shutdown dialog: %w", err)
	}
	return int(id), nil
}
func askDownloadShutdownPlatform(ctx context.Context, _ *application.App, locale settings.Locale) (bool, error) {
	message := "Wait for downloads to finish?\nNo cancels downloads and continues. Cancel keeps the workbench running."
	if locale == settings.LocaleChinese {
		message = "是否等待下载完成？\n选择“否”将取消下载并继续；“取消”返回工作台。"
	}
	if locale == settings.LocaleJapanese {
		message = "ダウンロードの完了を待ちますか？\n「いいえ」はダウンロードを中止して続行、「キャンセル」は作業に戻ります。"
	}
	id, err := shutdownQuestion(ctx, message)
	if err != nil {
		return false, err
	}
	switch id {
	case 6:
		return false, nil
	case 7:
		return true, nil
	default:
		return false, embeddedbrowser.ErrDownloadShutdownDeclined
	}
}
