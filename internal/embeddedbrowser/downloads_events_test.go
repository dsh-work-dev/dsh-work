package embeddedbrowser

import (
	"sync"
	"testing"
	"time"
)

func TestDownloadMailboxCoalescesBeforeReadyAndPreservesTerminal(t *testing.T) {
	q := newDownloadMailbox()
	ready, done := make(chan struct{}), make(chan struct{})
	defer close(done)
	var mu sync.Mutex
	var received []int64
	terminal := make(chan struct{})
	go q.run(done, ready, func(e downloadEvent) {
		mu.Lock()
		received = append(received, e.received)
		mu.Unlock()
		if e.finished {
			close(terminal)
		}
	})
	// These are strictly ordered pipe events whose old goroutine scheduling could
	// apply 128KiB before 64KiB. No worker is admitted until the native start ACK.
	for n := int64(1); n <= 100000; n++ {
		if err := q.push(downloadEvent{received: n, total: 100000}); err != nil {
			t.Fatal(err)
		}
	}
	if len(q.wake) != 1 {
		t.Fatal("unbounded pending notifications")
	}
	if err := q.push(downloadEvent{received: 100000, total: 100000, finished: true, success: true}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(received) != 0 {
		t.Fatal("applied before start ACK")
	}
	mu.Unlock()
	close(ready)
	select {
	case <-terminal:
	case <-time.After(time.Second):
		t.Fatal("terminal lost behind coalesced progress")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || received[0] != 100000 || received[1] != 100000 {
		t.Fatalf("order %v", received)
	}
	if err := q.push(downloadEvent{received: 100001}); err == nil {
		t.Fatal("post-terminal event admitted")
	}
}
func TestDownloadMailboxArrivalWhileConsumerBusy(t *testing.T) {
	q := newDownloadMailbox()
	ready, done := make(chan struct{}), make(chan struct{})
	close(ready)
	defer close(done)
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var seen []int64
	go q.run(done, ready, func(e downloadEvent) {
		seen = append(seen, e.received)
		if len(seen) == 1 {
			close(entered)
			<-release
		}
		if e.finished {
			close(finished)
		}
	})
	if err := q.push(downloadEvent{received: 64 << 10}); err != nil {
		t.Fatal(err)
	}
	<-entered
	for _, n := range []int64{128 << 10, 192 << 10} {
		if err := q.push(downloadEvent{received: n}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.push(downloadEvent{received: 256 << 10, finished: true}); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("terminal not consumed")
	}
	if len(seen) != 3 || seen[0] != 64<<10 || seen[1] != 192<<10 || seen[2] != 256<<10 {
		t.Fatalf("reordered %v", seen)
	}
	if err := newDownloadMailbox().push(downloadEvent{received: -1}); err == nil {
		t.Fatal("invalid wire progress ignored")
	}
}
