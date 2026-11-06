//go:build windows

package automation

import (
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/selfupdate"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var computerUser32 = windows.NewLazySystemDLL("user32.dll")
var computerWindowPID = computerUser32.NewProc("GetWindowThreadProcessId")
var computerWindowText = computerUser32.NewProc("GetWindowTextW")
var computerClassName = computerUser32.NewProc("GetClassNameW")
var computerIsWindow = computerUser32.NewProc("IsWindow")
var computerIsWindowVisible = computerUser32.NewProc("IsWindowVisible")

// InspectComputerTarget reads identity and metadata, never window contents.
// Query failure and elevated targets fail closed; no privilege adjustment occurs.
func InspectComputerTarget(pid uint32, hwnd uint64) (ComputerTarget, error) {
	fail := func() (ComputerTarget, error) { return ComputerTarget{}, errors.New("COMPUTER_TARGET_UNAVAILABLE") }
	generation, e := computerWindowGeneration(hwnd)
	if e != nil {
		return ComputerTarget{}, e
	}
	if pid == 0 || hwnd == 0 {
		return fail()
	}
	if v, _, _ := computerIsWindow.Call(uintptr(hwnd)); v == 0 {
		return fail()
	}
	if v, _, _ := computerIsWindowVisible.Call(uintptr(hwnd)); v == 0 {
		return fail()
	}
	var owner uint32
	computerWindowPID.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&owner)))
	if owner != pid {
		return fail()
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return fail()
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, 32768)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return fail()
	}
	exe := windows.UTF16ToString(buf[:n])
	var created, exited, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exited, &kernel, &user) != nil {
		return fail()
	}
	var token windows.Token
	if windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token) != nil {
		return fail()
	}
	elevated := token.IsElevated()
	token.Close()
	if elevated {
		return ComputerTarget{}, errors.New("COMPUTER_ELEVATED_TARGET_BLOCKED")
	}
	title := make([]uint16, 1024)
	computerWindowText.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&title[0])), uintptr(len(title)))
	class := make([]uint16, 256)
	computerClassName.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&class[0])), uintptr(len(class)))
	var rect struct{ Left, Top, Right, Bottom int32 }
	if ok, _, _ := computerUser32.NewProc("GetWindowRect").Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return fail()
	}
	kind, level := windowsComputerAppPolicy(exe)
	self, _ := os.Executable()
	if pid == uint32(os.Getpid()) || strings.EqualFold(self, exe) || strings.Contains(strings.ToLower(filepath.Base(exe)), "dsh-work") || computerOwnDescendant(pid) {
		level = "blocked"
	}
	identity := computerAppIdentity(h, exe)
	return ComputerTarget{kind: kind, Identity: identity, PID: pid, WindowID: hwnd, Executable: exe, Created: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime), WindowIdentity: fmt.Sprintf("%d/%s/%s/%d,%d,%d,%d", generation, windows.UTF16ToString(class), windows.UTF16ToString(title), rect.Left, rect.Top, rect.Right, rect.Bottom), App: strings.TrimSuffix(filepath.Base(exe), filepath.Ext(exe)), Title: windows.UTF16ToString(title), Level: level}, nil
}
func computerOwnDescendant(pid uint32) bool {
	snap, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return true
	}
	defer windows.CloseHandle(snap)
	parents := map[uint32]uint32{}
	p := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for e = windows.Process32First(snap, &p); e == nil; e = windows.Process32Next(snap, &p) {
		parents[p.ProcessID] = p.ParentProcessID
	}
	for range 64 {
		if pid == uint32(os.Getpid()) {
			return true
		}
		next := parents[pid]
		if next == 0 || next == pid {
			return false
		}
		pid = next
	}
	return true
}

var computerSignerCache struct {
	sync.Mutex
	values map[string]struct {
		stamp  time.Time
		size   int64
		signer string
	}
}

func computerAppIdentity(h windows.Handle, exe string) string {
	var length uint32 = 256
	family := make([]uint16, length)
	status, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetPackageFamilyName").Call(uintptr(h), uintptr(unsafe.Pointer(&length)), uintptr(unsafe.Pointer(&family[0])))
	if status == 0 {
		return "package:" + strings.ToLower(windows.UTF16ToString(family))
	}
	fallback := "path:" + strings.ToLower(filepath.Base(exe)) + "|" + strings.ToLower(exe)
	info, e := os.Stat(exe)
	if e != nil {
		return fallback
	}
	computerSignerCache.Lock()
	defer computerSignerCache.Unlock()
	if computerSignerCache.values == nil {
		computerSignerCache.values = map[string]struct {
			stamp  time.Time
			size   int64
			signer string
		}{}
	}
	record, ok := computerSignerCache.values[exe]
	if !ok || record.stamp != info.ModTime() || record.size != info.Size() {
		signer, _ := selfupdate.VerifiedAuthenticodeSigner(exe)
		record = struct {
			stamp  time.Time
			size   int64
			signer string
		}{info.ModTime(), info.Size(), signer}
		computerSignerCache.values[exe] = record
	}
	if record.signer != "" {
		return "signer:" + strings.ToLower(filepath.Base(exe)) + "|" + strings.ToLower(record.signer)
	}
	return fallback
}
