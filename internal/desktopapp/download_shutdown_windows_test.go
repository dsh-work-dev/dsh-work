//go:build windows

package desktopapp

import (
	"context"
	"errors"
	win "golang.org/x/sys/windows"
	"os"
	"testing"
	"time"
	"unsafe"
)

func TestOwnedDownloadShutdownNativeChoicesAndCancelJoin(t *testing.T) {
	if os.Getenv("DSH_WORK_OWNED_SHUTDOWN_DIALOG_TEST") != "1" {
		t.Skip("explicit self-owned modal fixture")
	}
	getItem := shutdownUser32.NewProc("GetDlgItem")
	send := shutdownUser32.NewProc("SendMessageW")
	for _, id := range []int{6, 7, 2, 0, -1} {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		result := make(chan struct {
			id  int
			err error
		}, 1)
		go func() {
			chosen, err := shutdownQuestion(ctx, "Owned download shutdown fixture")
			result <- struct {
				id  int
				err error
			}{chosen, err}
		}()
		var owned shutdownModal
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			shutdownModalMu.Lock()
			for _, m := range shutdownModals {
				owned = shutdownModal{thread: m.thread, pid: m.pid, birth: m.birth}
			}
			shutdownModalMu.Unlock()
			if owned.thread != 0 {
				info := shutdownGUIInfo{Size: uint32(unsafe.Sizeof(shutdownGUIInfo{}))}
				ok, _, _ := shutdownGUIThread.Call(uintptr(owned.thread), uintptr(unsafe.Pointer(&info)))
				owned.window = info.Active
				if ok != 0 && owned.sameWindow() {
					button, _, _ := getItem.Call(owned.window, 6)
					if button != 0 {
						break
					}
				}
			}
			owned.window = 0
			time.Sleep(10 * time.Millisecond)
		}
		if !owned.sameWindow() {
			stop()
			<-result
			t.Fatal("owned modal not observed")
		}
		want := id
		if id == -1 {
			stop()
		} else {
			if id == 0 {
				// DM_GETDEFID reads the system's actual default button, then the
				// exact native button is invoked without keyboard/global input.
				def, _, _ := send.Call(owned.window, 0x400, 0, 0)
				want = int(def & 0xffff)
				if want != 2 {
					stop()
					<-result
					t.Fatalf("default button=%d, want Cancel", want)
				}
			}
			button, _, _ := getItem.Call(owned.window, uintptr(want))
			var pid uint32
			thread, err := win.GetWindowThreadProcessId(win.HWND(button), &pid)
			if button == 0 || err != nil || pid != owned.pid || thread != owned.thread || !owned.sameWindow() {
				stop()
				<-result
				t.Fatal("exact owned button identity rejected")
			}
			send.Call(button, 0xf5, 0, 0) // BM_CLICK on this fixture's exact child only.
		}
		r := <-result
		if id == -1 {
			if !errors.Is(r.err, context.Canceled) {
				t.Fatal("cancel did not join modal", r.err)
			}
		} else if r.err != nil || r.id != want {
			t.Fatalf("native choice=%d err=%v want=%d", r.id, r.err, want)
		}
		stop()
		if win.IsWindow(win.HWND(owned.window)) {
			t.Fatal("modal still alive after joined return")
		}
		shutdownModalMu.Lock()
		count := len(shutdownModals)
		shutdownModalMu.Unlock()
		if count != 0 {
			t.Fatal("modal timer owner retained")
		}
		t.Logf("actual system modal result=%d request=%d; exact PID/birth/thread/HWND/button; destroyed and timer joined", r.id, id)
	}
}
