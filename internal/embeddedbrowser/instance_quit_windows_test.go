//go:build windows

package embeddedbrowser

import (
	"context"
	"golang.org/x/sys/windows"
	"os"
	"testing"
	"time"
)

func TestQuitOwnedFencesNativeLiveOwnersAndRestoresOnRefusal(t *testing.T) {
	var created, exit, kernel, user windows.Filetime
	if e := windows.GetProcessTimes(windows.CurrentProcess(), &created, &exit, &kernel, &user); e != nil {
		t.Fatal(e)
	}
	peer := PresenterPeer{PID: uint32(os.Getpid()), Started: uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)}
	r := &InstancePresenterRegistry{instances: map[string]*instancePresenter{"foreign": {peer: peer}}}
	called := 0
	quit := func() { called++ }
	expires := time.Now().Add(time.Second).UnixMilli()
	if err := r.QuitOwned(context.Background(), nil, expires, quit); err == nil || r.closed || r.quitAdmission || called != 0 {
		t.Fatal("foreign live registry owner not fenced safely", err)
	}
	expected := []ExpectedUIOwner{{PID: peer.PID, Started: peer.Started}}
	// Stage a request behind another shutdown. Expiry must be checked after lock.
	r.shutdown.Lock()
	result := make(chan error, 1)
	go func() {
		result <- r.QuitOwned(context.Background(), expected, time.Now().Add(20*time.Millisecond).UnixMilli(), quit)
	}()
	time.Sleep(40 * time.Millisecond)
	r.shutdown.Unlock()
	if err := <-result; err == nil || r.closed || r.quitAdmission || called != 0 {
		t.Fatal("expired queued quit executed", err)
	}
	if err := r.QuitOwned(context.Background(), expected, expires, quit); err != nil || !r.quitAdmission || r.closed || called != 1 {
		t.Fatal("matching native owner not accepted", err)
	}
	r.CancelQuitAdmission()
	if r.closed || r.quitAdmission {
		t.Fatal("download refusal retained runner quit fence")
	}
	t.Log("live kernel PID+creation foreign inventory rejected; admission restored; expired queued quit rejected; matching scope fences admissions before callback")
}
