package desktopapp

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/presence"
)

func TestFocusReporterPreservesOrderAndDrainsBeforeClose(t *testing.T) {
	started := make(chan focusReport, 2)
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	tracker := presence.New()
	windowService := &daemon.WindowService{Presence: tracker}
	var mu sync.Mutex
	var got []focusReport
	reporter := newFocusReporter(func(surface string, focused bool) {
		update := focusReport{surface: surface, focused: focused}
		mu.Lock()
		got = append(got, update)
		index := len(got)
		mu.Unlock()
		ctx := app.LocalClientContext(context.Background(), surface)
		if err := windowService.SetFocus(ctx, focused); err != nil {
			t.Errorf("SetFocus(%q, %t): %v", surface, focused, err)
		}
		started <- update
		if index == 1 {
			<-releaseFirst
		}
	})
	defer reporter.close()
	defer release()

	reporter.enqueue("workspace-2", true)
	select {
	case update := <-started:
		if update != (focusReport{surface: "workspace-2", focused: true}) {
			t.Fatalf("first focus update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("focus reporter did not send its first update")
	}
	reporter.enqueue("workspace-2", false)
	closed := make(chan struct{})
	go func() {
		reporter.close()
		close(closed)
	}()
	release()
	select {
	case update := <-started:
		if update != (focusReport{surface: "workspace-2", focused: false}) {
			t.Fatalf("second focus update = %+v", update)
		}
	case <-time.After(time.Second):
		t.Fatal("focus reporter did not send its second update")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not finish after draining queued reports")
	}
	reporter.enqueue("workspace-2", true)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || !got[0].focused || got[1].focused {
		t.Fatalf("focus report order = %+v", got)
	}
	if tracker.AnyFocused() {
		t.Fatal("last focus state was not applied before close returned")
	}
}
