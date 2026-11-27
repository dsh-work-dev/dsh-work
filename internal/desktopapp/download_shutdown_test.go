package desktopapp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

type observedCancelContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (c *observedCancelContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.waiting) })
	return c.Context.Done()
}

func TestDownloadShutdownQueuedPrepareCanCancelWithoutChangingFirstLease(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	begins, released := 0, 0
	lease := &downloadShutdownLease{begin: func(context.Context) (func(), error) {
		begins++
		close(entered)
		<-finish
		return func() { released++ }, nil
	}}
	first := make(chan error, 1)
	go func() { first <- lease.prepare(context.Background()) }()
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	observed := &observedCancelContext{Context: ctx, waiting: make(chan struct{})}
	second := make(chan error, 1)
	go func() { second <- lease.prepare(observed) }()
	<-observed.waiting // The second call is selecting on the in-flight prepare.
	cancel()
	select {
	case err := <-second:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(finish)
		<-first
		t.Fatal("queued cancellation waited for first user decision")
	}
	select {
	case <-first:
		t.Fatal("second request canceled the first preparation")
	default:
	}
	close(finish)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if err := lease.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if begins != 1 || released != 0 || lease.resume == nil {
		t.Fatal("preparations did not share exactly one valid lease")
	}
	lease.reset()
	if released != 1 {
		t.Fatal("shared lease released more than once")
	}
}

func TestQuitFlowStopFailureWithdrawsDownloadAndRunnerAdmission(t *testing.T) {
	// QuitOwned has accepted the current owner set; there is no ready update.
	runnerFenced, downloadFenced := true, false
	resumed := 0
	lease := &downloadShutdownLease{
		begin: func(context.Context) (func(), error) {
			downloadFenced = true
			return func() { downloadFenced = false; resumed++ }, nil
		},
		cancelQuitAdmission: func() { runnerFenced = false },
	}
	if err := lease.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	flow := &lifecycle.QuitFlow{}
	failed := make(chan struct{})
	quit := make(chan struct{}, 1)
	flow.Begin(nil, func() error {
		// StopBrowserUse refuses before Registry.Close can retire any owner.
		return errors.New("browser executor still live")
	}, func() { quit <- struct{}{} }, func(error) { lease.withdraw(); close(failed) })
	<-failed
	if runnerFenced || downloadFenced || resumed != 1 || lease.resume != nil {
		t.Fatal("failed normal quit retained an admission fence")
	}
	select {
	case <-quit:
		t.Fatal("failed stop still quit")
	default:
	}
	// Releasing an ordinary lease is not itself an exit withdrawal. A newly
	// successful QuitOwned request must retain its fence until actual closure.
	runnerFenced = true
	if err := lease.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	lease.reset()
	if !runnerFenced || downloadFenced {
		t.Fatal("ordinary release canceled successful runner fence")
	}
}
