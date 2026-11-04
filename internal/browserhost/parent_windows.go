//go:build windows

package browserhost

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/local/dsh-work/internal/selfupdate"
	"golang.org/x/sys/windows"
)

func parentBrowserInfo() (ParentInfo, error) {
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if e != nil {
		return ParentInfo{}, e
	}
	defer windows.CloseHandle(snapshot)
	parents := map[uint32]uint32{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for e = windows.Process32First(snapshot, &entry); e == nil; e = windows.Process32Next(snapshot, &entry) {
		parents[entry.ProcessID] = entry.ParentProcessID
	}
	pid := uint32(os.Getpid())
	for range 3 {
		pid = parents[pid]
		if pid == 0 {
			break
		}
		h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
		if e != nil {
			return ParentInfo{}, e
		}
		buf := make([]uint16, 32768)
		n := uint32(len(buf))
		e = windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
		windows.CloseHandle(h)
		if e != nil {
			return ParentInfo{}, e
		}
		path := windows.UTF16ToString(buf[:n])
		name := strings.ToLower(filepath.Base(path))
		family, publisher := "", ""
		if name == "chrome.exe" {
			family, publisher = "chrome", "Google LLC"
		}
		if name == "msedge.exe" {
			family, publisher = "edge", "Microsoft Corporation"
		}
		if family != "" {
			if e := selfupdate.VerifyAuthenticodePublisher(path, publisher); e != nil {
				return ParentInfo{}, e
			}
			return ParentInfo{Browser: family, Executable: path}, nil
		}
		// Chromium can launch a stdio host through the system command interpreter.
		if name != "cmd.exe" {
			break
		}
		system, e := windows.GetSystemDirectory()
		if e != nil || !strings.EqualFold(path, filepath.Join(system, "cmd.exe")) {
			break
		}
	}
	return ParentInfo{}, errors.New("BROWSER_NATIVE_PARENT_REJECTED")
}
