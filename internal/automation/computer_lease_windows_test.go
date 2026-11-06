//go:build windows

package automation

import (
	"crypto/rand"
	"golang.org/x/sys/windows"
	"runtime"
	"strings"
	"testing"
)

func TestComputerNamedLeaseHasThreadSafeOwnershipAndRelease(t *testing.T) {
	name := `Local\DSH.Work.ComputerLease.Test.` + rand.Text()
	release, e := acquireNamedComputerLease(name)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if second, e := acquireNamedComputerLease(name); e == nil {
		_ = second()
		t.Fatal("two owners acquired one desktop mutex")
	} else if !strings.Contains(e.Error(), "COMPUTER_BUSY") {
		t.Fatal(e)
	}
	if e = release(); e != nil {
		t.Fatal(e)
	}
	again, e := acquireNamedComputerLease(name)
	if e != nil {
		t.Fatal(e)
	}
	if e = again(); e != nil {
		t.Fatal(e)
	}
}

func TestComputerNamedLeaseRecoversAbandonedOwner(t *testing.T) {
	name := `Local\DSH.Work.ComputerLease.Test.` + rand.Text()
	type held struct {
		mutex, thread windows.Handle
		err           error
	}
	ready := make(chan held, 1)
	go func() {
		runtime.LockOSThread()
		pointer, _ := windows.UTF16PtrFromString(name)
		mutex, e := windows.CreateMutex(nil, true, pointer)
		if e != nil {
			ready <- held{err: e}
			return
		}
		thread, e := windows.OpenThread(windows.SYNCHRONIZE, false, windows.GetCurrentThreadId())
		ready <- held{mutex: mutex, thread: thread, err: e}
		runtime.Goexit()
	}()
	owner := <-ready
	if owner.err != nil {
		t.Fatal(owner.err)
	}
	defer windows.CloseHandle(owner.mutex)
	defer windows.CloseHandle(owner.thread)
	if status, e := windows.WaitForSingleObject(owner.thread, 1000); e != nil || status != windows.WAIT_OBJECT_0 {
		t.Fatal("owner thread did not exit", status, e)
	}
	release, e := acquireNamedComputerLease(name)
	if e != nil {
		t.Fatal(e)
	}
	if e = release(); e != nil {
		t.Fatal(e)
	}
}
