//go:build windows

package automation

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"sync"
	"unsafe"
)

// Local names are scoped by Windows Session. The opaque suffix distinguishes
// the OS user and window-station/desktop without putting their names in it.
func AcquireComputerDesktopLease() (func() error, error) {
	user, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return nil, fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", e)
	}
	station, _, _ := computerUser32.NewProc("GetProcessWindowStation").Call()
	desktop, _, _ := computerUser32.NewProc("GetThreadDesktop").Call(uintptr(windows.GetCurrentThreadId()))
	nameOf := func(h uintptr) (string, error) {
		buffer := make([]uint16, 256)
		var needed uint32
		ok, _, e := computerUser32.NewProc("GetUserObjectInformationW").Call(h, 2, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)*2), uintptr(unsafe.Pointer(&needed)))
		if ok == 0 {
			return "", e
		}
		return windows.UTF16ToString(buffer), nil
	}
	stationName, e := nameOf(station)
	if e != nil {
		return nil, fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", e)
	}
	desktopName, e := nameOf(desktop)
	if e != nil {
		return nil, fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", e)
	}
	// This digest is a synchronization namespace, not an artifact integrity check.
	identity := sha256.Sum256([]byte(user.User.Sid.String() + "\x00" + stationName + "\x00" + desktopName))
	return acquireNamedComputerLease(fmt.Sprintf(`Local\DSH.Work.ComputerLease.v1.%x`, identity[:16]))
}
func acquireNamedComputerLease(name string) (func() error, error) {
	pointer, e := windows.UTF16PtrFromString(name)
	if e != nil {
		return nil, e
	}
	ready := make(chan error, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	var releaseErr error
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		handle, e := windows.CreateMutex(nil, false, pointer)
		if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
			ready <- fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", e)
			return
		}
		status, e := windows.WaitForSingleObject(handle, 0)
		if e != nil {
			_ = windows.CloseHandle(handle)
			ready <- fmt.Errorf("COMPUTER_DESKTOP_LEASE_UNAVAILABLE: %w", e)
			return
		}
		if status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED {
			_ = windows.CloseHandle(handle)
			ready <- errors.New("COMPUTER_BUSY: another DSH Work process owns this desktop")
			return
		}
		// Both normal and abandoned acquisition are ownership on this exact thread.
		ready <- nil
		<-stop
		releaseErr = errors.Join(windows.ReleaseMutex(handle), windows.CloseHandle(handle))
	}()
	if e = <-ready; e != nil {
		return nil, e
	}
	var once sync.Once
	return func() error { once.Do(func() { close(stop) }); <-done; return releaseErr }, nil
}
