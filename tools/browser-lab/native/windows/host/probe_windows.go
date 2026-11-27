//go:build windows && amd64

package main

// Read-only diagnostics. Callers must supply a known demo HWND; these probes
// never enumerate top-level windows or read window titles/content.
import (
	"fmt"
	"runtime"
	"syscall"
	"unsafe"
)

var (
	nativeProbeUser32           = syscall.NewLazyDLL("user32.dll")
	nativeProbeShcore           = syscall.NewLazyDLL("shcore.dll")
	nativeProbeDwm              = syscall.NewLazyDLL("dwmapi.dll")
	nativeProbeIsWindow         = nativeProbeUser32.NewProc("IsWindow")
	nativeProbeGetParent        = nativeProbeUser32.NewProc("GetParent")
	nativeProbeGetAncestor      = nativeProbeUser32.NewProc("GetAncestor")
	nativeProbeIsChild          = nativeProbeUser32.NewProc("IsChild")
	nativeProbeGetWindowRect    = nativeProbeUser32.NewProc("GetWindowRect")
	nativeProbeGetClientRect    = nativeProbeUser32.NewProc("GetClientRect")
	nativeProbeIsVisible        = nativeProbeUser32.NewProc("IsWindowVisible")
	nativeProbeIsIconic         = nativeProbeUser32.NewProc("IsIconic")
	nativeProbeGetDpi           = nativeProbeUser32.NewProc("GetDpiForWindow")
	nativeProbeWindowContext    = nativeProbeUser32.NewProc("GetWindowDpiAwarenessContext")
	nativeProbeThreadContext    = nativeProbeUser32.NewProc("GetThreadDpiAwarenessContext")
	nativeProbeAwareness        = nativeProbeUser32.NewProc("GetAwarenessFromDpiAwarenessContext")
	nativeProbeEqualContext     = nativeProbeUser32.NewProc("AreDpiAwarenessContextsEqual")
	nativeProbeThreadProcess    = nativeProbeUser32.NewProc("GetWindowThreadProcessId")
	nativeProbeGUIThread        = nativeProbeUser32.NewProc("GetGUIThreadInfo")
	nativeProbeForeground       = nativeProbeUser32.NewProc("GetForegroundWindow")
	nativeProbeEnumMonitors     = nativeProbeUser32.NewProc("EnumDisplayMonitors")
	nativeProbeMonitorInfo      = nativeProbeUser32.NewProc("GetMonitorInfoW")
	nativeProbeDpiMonitor       = nativeProbeShcore.NewProc("GetDpiForMonitor")
	nativeProbeProcessAwareness = nativeProbeShcore.NewProc("GetProcessDpiAwareness")
	nativeProbeEnumChildren     = nativeProbeUser32.NewProc("EnumChildWindows")
	nativeProbeChildAtPoint     = nativeProbeUser32.NewProc("ChildWindowFromPointEx")
	nativeProbeFrameBounds      = nativeProbeDwm.NewProc("DwmGetWindowAttribute")
	nativeProbeMonitorForWindow = nativeProbeUser32.NewProc("MonitorFromWindow")
)

type nativeProbeRect struct{ Left, Top, Right, Bottom int32 }
type nativeProbeMonitorInfoEx struct {
	Size          uint32
	Monitor, Work nativeProbeRect
	Flags         uint32
	Device        [32]uint16
}
type nativeProbeGUIInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	CaretRect                                          nativeProbeRect
}
type nativeProbeCollector struct {
	Rows      []map[string]any
	Truncated bool
}

func nativeProbeRectValue(r nativeProbeRect) map[string]any {
	return map[string]any{"left": r.Left, "top": r.Top, "right": r.Right, "bottom": r.Bottom,
		"width": r.Right - r.Left, "height": r.Bottom - r.Top}
}
func nativeProbeContext(ctx uintptr) map[string]any {
	result := map[string]any{"raw": fmt.Sprintf("0x%x", ctx)}
	if ctx == 0 {
		result["valid"] = false
		return result
	}
	a, _, _ := nativeProbeAwareness.Call(ctx)
	result["awareness"] = int32(a) // -1 invalid; 0 unaware; 1 system; 2 per-monitor
	result["valid"] = int32(a) >= 0
	for _, c := range []struct {
		Value int
		Name  string
	}{
		{-1, "unaware"}, {-2, "system"}, {-3, "per-monitor-v1"}, {-4, "per-monitor-v2"}, {-5, "unaware-gdi-scaled"},
	} {
		eq, _, _ := nativeProbeEqualContext.Call(ctx, uintptr(c.Value))
		if eq != 0 {
			result["mode"] = c.Name
			break
		}
	}
	return result
}
func nativeProbeCallerDPI() map[string]any {
	ctx, _, _ := nativeProbeThreadContext.Call()
	var awareness int32 = -1
	hr, _, _ := nativeProbeProcessAwareness.Call(0, uintptr(unsafe.Pointer(&awareness)))
	return map[string]any{"thread": nativeProbeContext(ctx),
		"processAwareness": awareness, "processAwarenessHRESULT": fmt.Sprintf("0x%08x", uint32(hr))}
}

var nativeProbeMonitorCallback = syscall.NewCallback(func(monitor, _dc, _rect, data uintptr) uintptr {
	c := (*nativeProbeCollector)(unsafe.Pointer(data))
	info := nativeProbeMonitorInfoEx{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, err := nativeProbeMonitorInfo.Call(monitor, uintptr(unsafe.Pointer(&info)))
	row := map[string]any{"monitor": monitor}
	if ok == 0 {
		row["error"] = err.Error()
		c.Rows = append(c.Rows, row)
		return 1
	}
	row["device"] = syscall.UTF16ToString(info.Device[:])
	row["bounds"] = nativeProbeRectValue(info.Monitor)
	row["workArea"] = nativeProbeRectValue(info.Work)
	row["primary"] = info.Flags&1 != 0
	var dx, dy uint32
	hr, _, _ := nativeProbeDpiMonitor.Call(monitor, 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy)))
	row["effectiveDpiHRESULT"] = fmt.Sprintf("0x%08x", uint32(hr))
	if int32(hr) >= 0 {
		row["effectiveDpiX"] = dx
		row["effectiveDpiY"] = dy
	}
	row["callerDPI"] = nativeProbeCallerDPI()
	row["dpiInterpretation"] = "GetDpiForMonitor(MDT_EFFECTIVE_DPI) depends on process awareness; per-monitor thread callers should use window GetDpiForWindow for acceptance."
	c.Rows = append(c.Rows, row)
	return 1
})

// MonitorInventory inventories monitor geometry, never display pixels. It does
// not change DPI awareness. Each row records the caller context needed to
// interpret GetDpiForMonitor; a failure is returned as a diagnostic row.
func MonitorInventory() []map[string]any {
	c := nativeProbeCollector{Rows: []map[string]any{}}
	ok, _, err := nativeProbeEnumMonitors.Call(0, 0, nativeProbeMonitorCallback, uintptr(unsafe.Pointer(&c)))
	runtime.KeepAlive(&c)
	if ok == 0 {
		c.Rows = append(c.Rows, map[string]any{"error": "EnumDisplayMonitors: " + err.Error()})
	}
	return c.Rows
}

func nativeProbeBasicWindow(hwnd uintptr) map[string]any {
	row := map[string]any{"hwnd": hwnd}
	valid, _, _ := nativeProbeIsWindow.Call(hwnd)
	row["valid"] = valid != 0
	if valid == 0 {
		return row
	}
	var pid uint32
	tid, _, _ := nativeProbeThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	row["pid"], row["threadId"] = pid, uint32(tid)
	parent, _, _ := nativeProbeGetParent.Call(hwnd)
	ancestorParent, _, _ := nativeProbeGetAncestor.Call(hwnd, 1)
	root, _, _ := nativeProbeGetAncestor.Call(hwnd, 2)
	row["parentHWND"], row["rootHWND"] = parent, root
	row["ancestorParentHWND"] = ancestorParent // GetAncestor(GA_PARENT) does not follow popup ownership.
	row["parentSemantics"] = "GetParent: parent for WS_CHILD; owner for an owned popup"
	visible, _, _ := nativeProbeIsVisible.Call(hwnd)
	minimized, _, _ := nativeProbeIsIconic.Call(hwnd)
	row["visible"], row["minimized"] = visible != 0, minimized != 0
	var rect, client nativeProbeRect
	if ok, _, err := nativeProbeGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok != 0 {
		row["windowRect"] = nativeProbeRectValue(rect)
	} else {
		row["windowRectError"] = err.Error()
	}
	if ok, _, err := nativeProbeGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); ok != 0 {
		row["clientRect"] = nativeProbeRectValue(client)
	} else {
		row["clientRectError"] = err.Error()
	}
	dpi, _, _ := nativeProbeGetDpi.Call(hwnd)
	context, _, _ := nativeProbeWindowContext.Call(hwnd)
	row["dpi"], row["dpiContext"] = uint32(dpi), nativeProbeContext(context)
	monitor, _, _ := nativeProbeMonitorForWindow.Call(hwnd, 0)
	row["monitor"] = monitor
	// DWM frame bounds are not DPI-virtualized; normal GetWindowRect may be.
	if nativeProbeFrameBounds.Find() == nil {
		var frame nativeProbeRect
		hr, _, _ := nativeProbeFrameBounds.Call(hwnd, 9, uintptr(unsafe.Pointer(&frame)), unsafe.Sizeof(frame))
		row["physicalFrameBoundsHRESULT"] = fmt.Sprintf("0x%08x", uint32(hr))
		if int32(hr) >= 0 {
			row["physicalFrameBounds"] = nativeProbeRectValue(frame)
		}
	}
	return row
}

var nativeProbeChildCallback = syscall.NewCallback(func(hwnd, data uintptr) uintptr {
	c := (*nativeProbeCollector)(unsafe.Pointer(data))
	if len(c.Rows) >= 128 {
		c.Truncated = true
		return 0
	}
	c.Rows = append(c.Rows, nativeProbeBasicWindow(hwnd))
	return 1
})

// NativeWindowState reads only the given demo window and its descendant HWNDs.
func NativeWindowState(hwnd uintptr) map[string]any {
	row := nativeProbeBasicWindow(hwnd)
	if valid, _ := row["valid"].(bool); !valid {
		return row
	}
	row["callerDPI"] = nativeProbeCallerDPI()
	row["coordinateNote"] = "GetWindowRect/client geometry follows caller DPI virtualization; physicalFrameBounds is DWM physical geometry."
	c := nativeProbeCollector{Rows: []map[string]any{}}
	nativeProbeEnumChildren.Call(hwnd, nativeProbeChildCallback, uintptr(unsafe.Pointer(&c)))
	runtime.KeepAlive(&c)
	row["children"], row["childrenTruncated"] = c.Rows, c.Truncated
	var client nativeProbeRect
	if ok, _, _ := nativeProbeGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&client))); ok != 0 {
		// POINT is passed by value on Windows amd64. This probe never moves the cursor.
		x, y := uint32((client.Right-client.Left)/2), uint32((client.Bottom-client.Top)/2)
		point := uintptr(uint64(x) | uint64(y)<<32)
		child, _, _ := nativeProbeChildAtPoint.Call(hwnd, point, 1|2|4)
		row["centerChildHitHWND"] = child
		row["centerClientPoint"] = map[string]any{"x": x, "y": y}
	}
	return row
}

func nativeProbeInTree(root, hwnd uintptr) bool {
	if hwnd == 0 {
		return false
	}
	if root == hwnd {
		return true
	}
	child, _, _ := nativeProbeIsChild.Call(root, hwnd)
	return child != 0
}

// NativeFocus reads the known demo HWND's GUI thread, not thread zero (which
// would inspect the foreground application's thread). Unrelated handles are
// redacted; foreground is reported only as equality to the supplied demo root.
func NativeFocus(hwnd uintptr) map[string]any {
	row := map[string]any{"hwnd": hwnd}
	valid, _, _ := nativeProbeIsWindow.Call(hwnd)
	if valid == 0 {
		row["valid"] = false
		return row
	}
	row["valid"] = true
	var pid uint32
	tid, _, err := nativeProbeThreadProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	row["pid"], row["threadId"] = pid, uint32(tid)
	if tid == 0 {
		row["error"] = err.Error()
		return row
	}
	root, _, _ := nativeProbeGetAncestor.Call(hwnd, 2)
	row["demoRootHWND"] = root
	foreground, _, _ := nativeProbeForeground.Call()
	row["foregroundIsDemoRoot"] = foreground != 0 && foreground == root
	info := nativeProbeGUIInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	ok, _, err := nativeProbeGUIThread.Call(tid, uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		row["error"] = "GetGUIThreadInfo: " + err.Error()
		return row
	}
	row["guiFlags"] = info.Flags
	for name, h := range map[string]uintptr{"active": info.Active, "focus": info.Focus} {
		row[name+"InRequestedTree"] = nativeProbeInTree(hwnd, h)
		inTree := nativeProbeInTree(root, h)
		row[name+"InDemoTree"] = inTree
		row[name+"EqualsRequestedHWND"] = h == hwnd
		if inTree {
			row[name+"HWND"] = h
		} else {
			row[name+"HWND"] = uintptr(0)
		}
	}
	return row
}
