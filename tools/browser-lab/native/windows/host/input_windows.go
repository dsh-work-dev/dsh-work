package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"unsafe"
)

// INPUT has an 8-byte header and 32-byte union on the supported Windows x64.
type nativeKeyInput struct {
	Kind               uint32
	Pad                uint32
	VK, Scan           uint16
	Flags, Time, Align uint32
	Extra              uintptr
	Unused             [8]byte
}

func nativeInputDesktop() map[string]any {
	name := func(handle uintptr) string {
		if handle == 0 {
			return "unavailable"
		}
		buf := make([]uint16, 256)
		var size uint32
		ok, _, _ := user.NewProc("GetUserObjectInformationW").Call(handle, 2, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2), uintptr(unsafe.Pointer(&size)))
		if ok == 0 {
			return "unavailable"
		}
		return windows.UTF16ToString(buf)
	}
	tid, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId").Call()
	thread, _, _ := user.NewProc("GetThreadDesktop").Call(tid)
	input, _, err := user.NewProc("OpenInputDesktop").Call(0, 0, 1)
	r := map[string]any{"threadDesktop": name(thread), "inputDesktop": name(input)}
	if input != 0 {
		user.NewProc("CloseDesktop").Call(input)
	} else {
		r["inputError"] = err.Error()
	}
	var session uint32
	windows.NewLazySystemDLL("kernel32.dll").NewProc("ProcessIdToSessionId").Call(uintptr(os.Getpid()), uintptr(unsafe.Pointer(&session)))
	r["sessionId"] = session
	var data uintptr
	var size uint32
	wts := windows.NewLazySystemDLL("wtsapi32.dll")
	if ok, _, _ := wts.NewProc("WTSQuerySessionInformationW").Call(0, uintptr(session), 8, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&size))); ok != 0 && data != 0 {
		if size >= 4 {
			r["connectionState"] = *(*uint32)(unsafe.Pointer(data))
		}
		wts.NewProc("WTSFreeMemory").Call(data)
	}
	return r
}

// Fixed ASCII probe for the owned CI desktop. Never enumerates other top levels
// or sends input to the foreground application selected by the user.
func (l *lab) nativeInput(target string, focusOnly bool) error {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return fmt.Errorf("native synthetic input requires owned GitHub desktop")
	}
	desktop := nativeInputDesktop()
	if focusOnly {
		l.record("native-input-desktop", desktop)
	}
	if desktop["inputDesktop"] == "unavailable" || desktop["inputDesktop"] != desktop["threadDesktop"] {
		return fmt.Errorf("owned input desktop is unavailable: %v", desktop)
	}
	root := l.tabs[l.selected].HWND
	var excluded [2]uintptr
	parent := l.mainHWND
	if l.floating {
		parent = l.floatHWND
	}
	if target == "host" {
		root = l.mainHWND
		excluded = [2]uintptr{l.tabs[0].HWND, l.tabs[1].HWND}
		parent = l.mainHWND
	} else if target != "guest" {
		return fmt.Errorf("invalid input target")
	}

	if focusOnly {
		if target == "host" {
			// MoveFocus on the real WebView2 controller, not its legacy accessibility HWND.
			l.main.Focus()
		} else {
			user.NewProc("SetForegroundWindow").Call(parent)
			tid, _, _ := processOf.Call(root, 0)
			current, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId").Call()
			attached := false
			if tid != current {
				ok, _, _ := user.NewProc("AttachThreadInput").Call(current, tid, 1)
				if ok == 0 {
					return fmt.Errorf("input thread attach failed")
				}
				attached = true
			}
			// CEF's real native browser window owns input dispatch. Its
			// Chrome_RenderWidgetHostHWND descendant is a legacy IME/a11y helper.
			user.NewProc("SetFocus").Call(root)
			if attached {
				user.NewProc("AttachThreadInput").Call(current, tid, 0)
			}
		}
	}
	f := NativeFocus(root)
	if f["focusInRequestedTree"] != true {
		return fmt.Errorf("native focus outside owned target: %v", f)
	}
	if target == "host" {
		for _, guest := range excluded {
			if nativeProbeInTree(guest, f["focusHWND"].(uintptr)) {
				return fmt.Errorf("host input resolved to a guest widget")
			}
		}
	}
	foreground, _, _ := user.NewProc("GetForegroundWindow").Call()
	if foreground != parent {
		return fmt.Errorf("refusing input outside owned foreground window")
	}
	if focusOnly {
		return nil
	}
	keys := [2]nativeKeyInput{{Kind: 1, Scan: 'r', Flags: 4}, {Kind: 1, Scan: 'r', Flags: 6}}
	if unsafe.Sizeof(keys[0]) != 40 {
		return fmt.Errorf("unsupported native INPUT layout")
	}
	ok, _, e := user.NewProc("SendInput").Call(2, uintptr(unsafe.Pointer(&keys[0])), unsafe.Sizeof(keys[0]))
	if ok != 2 {
		return fmt.Errorf("owned-desktop native key: %v", e)
	}
	l.record("native-input", map[string]any{"target": target, "focus": f, "method": "SendInput after owned foreground + focus-tree validation"})
	return nil
}
