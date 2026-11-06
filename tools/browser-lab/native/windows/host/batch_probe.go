package main

import (
	"runtime"
	"syscall"
	"unsafe"
)

type guestWindowCollector struct {
	PID  uint32
	Rows []map[string]any
}

var guestWindowCallback = syscall.NewCallback(func(hwnd, data uintptr) uintptr {
	c := (*guestWindowCollector)(unsafe.Pointer(data))
	var actual uint32
	processOf.Call(hwnd, uintptr(unsafe.Pointer(&actual)))
	if actual == c.PID {
		row := NativeWindowState(hwnd)
		row["title"] = textAt(hwnd)
		c.Rows = append(c.Rows, row)
	}
	return 1
})

// One static callback, per-call collector. No other applications' metadata read.
func GuestTopLevelWindows(pid uint32) []map[string]any {
	c := guestWindowCollector{PID: pid, Rows: []map[string]any{}}
	if pid != 0 {
		user.NewProc("EnumWindows").Call(guestWindowCallback, uintptr(unsafe.Pointer(&c)))
		runtime.KeepAlive(&c)
	}
	return c.Rows
}
