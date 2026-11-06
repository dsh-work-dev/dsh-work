package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

type inputSearch struct{ root, excluded, found uintptr }

var inputSearchCallback = syscall.NewCallback(func(h, p uintptr) uintptr {
	s := (*inputSearch)(unsafe.Pointer(p))
	if s.excluded != 0 && nativeProbeInTree(s.excluded, h) {
		return 1
	}
	name := make([]uint16, 256)
	classOf.Call(h, uintptr(unsafe.Pointer(&name[0])), uintptr(len(name)))
	if windows.UTF16ToString(name) == "Chrome_RenderWidgetHostHWND" {
		s.found = h
		return 0
	}
	return 1
})

// Fixed ASCII probe for the owned CI desktop. Never enumerates other top levels
// or sends input to the foreground application selected by the user.
func (l *lab) nativeInput(target string) error {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return fmt.Errorf("native synthetic input requires owned GitHub desktop")
	}
	root, excluded := l.tabs[l.selected].HWND, uintptr(0)
	parent := l.mainHWND
	if l.floating {
		parent = l.floatHWND
	}
	if target == "host" {
		root = l.mainHWND
		excluded = l.tabs[l.selected].HWND
		parent = l.mainHWND
	} else if target != "guest" {
		return fmt.Errorf("invalid input target")
	}
	s := inputSearch{root: root, excluded: excluded}
	enumChildren.Call(root, inputSearchCallback, uintptr(unsafe.Pointer(&s)))
	runtime.KeepAlive(&s)
	if s.found == 0 {
		return fmt.Errorf("owned native input widget unavailable")
	}
	user.NewProc("SetForegroundWindow").Call(parent)
	tid, _, _ := processOf.Call(s.found, 0)
	current, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetCurrentThreadId").Call()
	attached := false
	if tid != current {
		r, _, _ := user.NewProc("AttachThreadInput").Call(current, tid, 1)
		attached = r != 0
		if !attached {
			return fmt.Errorf("input thread attach failed")
		}
	}
	user.NewProc("SetFocus").Call(s.found)
	if attached {
		user.NewProc("AttachThreadInput").Call(current, tid, 0)
	}
	f := NativeFocus(root)
	if f["focusInRequestedTree"] != true {
		return fmt.Errorf("native focus outside owned target: %v", f)
	}
	ok, _, e := user.NewProc("PostMessageW").Call(s.found, 0x0102, uintptr('r'), 1)
	if ok == 0 {
		return fmt.Errorf("targeted WM_CHAR: %v", e)
	}
	l.record("native-input", map[string]any{"target": target, "focus": f, "method": "targeted WM_CHAR"})
	return nil
}
