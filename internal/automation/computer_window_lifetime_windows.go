//go:build windows

package automation

import (
	"errors"
	"golang.org/x/sys/windows"
	"runtime"
	"sync"
	"time"
	"unsafe"
)

type computerWindowTokenRequest struct {
	hwnd  uint64
	reply chan uint64
}

var computerWindowTokens struct {
	once     sync.Once
	ready    chan struct{}
	requests chan computerWindowTokenRequest
	thread   uint32
	err      error
}

// Out-of-context WinEvents distinguish HWND incarnations even when a handle is
// reused inside the same still-running process. Queries run on the hook thread.
func computerWindowGeneration(hwnd uint64) (uint64, error) {
	t := &computerWindowTokens
	t.once.Do(func() {
		t.ready = make(chan struct{})
		t.requests = make(chan computerWindowTokenRequest, 64)
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			t.thread = windows.GetCurrentThreadId()
			dll := windows.NewLazySystemDLL("user32.dll")
			type msg struct {
				window         uintptr
				message        uint32
				wparam, lparam uintptr
				at             uint32
				x, y           int32
				private        uint32
			}
			var m msg
			dll.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, 0)
			versions := map[uint64]uint64{}
			callback := windows.NewCallback(func(_ uintptr, event uint32, w uintptr, object, child int32, _ uint32, _ uint32) uintptr {
				if w != 0 && object == 0 && child == 0 && (event == 0x8000 || event == 0x8001) {
					if _, tracked := versions[uint64(w)]; tracked {
						versions[uint64(w)]++
					}
				}
				return 0
			})
			hook, _, e := dll.NewProc("SetWinEventHook").Call(0x8000, 0x8001, 0, callback, 0, 0, 0)
			if hook == 0 {
				t.err = e
				close(t.ready)
				return
			}
			defer dll.NewProc("UnhookWinEvent").Call(hook)
			close(t.ready)
			for {
				v, _, _ := dll.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
				if int32(v) <= 0 {
					return
				}
				if m.message == 0x8001 {
					for {
						select {
						case req := <-t.requests:
							if _, tracked := versions[req.hwnd]; !tracked {
								versions[req.hwnd] = 1
							}
							req.reply <- versions[req.hwnd]
						default:
							goto drained
						}
					}
				}
			drained:
				dll.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&m)))
				dll.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&m)))
			}
		}()
	})
	<-t.ready
	if t.err != nil {
		return 0, errors.New("COMPUTER_WINDOW_TRACKER_UNAVAILABLE")
	}
	req := computerWindowTokenRequest{hwnd: hwnd, reply: make(chan uint64, 1)}
	select {
	case t.requests <- req:
	default:
		return 0, errors.New("COMPUTER_WINDOW_TRACKER_BUSY")
	}
	v, _, _ := computerUser32.NewProc("PostThreadMessageW").Call(uintptr(t.thread), 0x8001, 0, 0)
	if v == 0 {
		return 0, errors.New("COMPUTER_WINDOW_TRACKER_UNAVAILABLE")
	}
	select {
	case version := <-req.reply:
		return version, nil
	case <-time.After(time.Second):
		return 0, errors.New("COMPUTER_WINDOW_TRACKER_TIMEOUT")
	}
}
