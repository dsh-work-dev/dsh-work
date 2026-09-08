package lifecycle

import (
	"errors"
	"testing"
	"time"
)

func TestQuitFlowHidesBeforeBackgroundShutdownAndQuitsAfterSuccess(t *testing.T) {
	flow := &QuitFlow{}
	hidden := make(chan struct{})
	shutdownStarted := make(chan struct{})
	releaseShutdown := make(chan struct{})
	quit := make(chan struct{})

	if !flow.Begin(
		func() { close(hidden) },
		func() error {
			select {
			case <-hidden:
			default:
				t.Error("shutdown started before windows were hidden")
			}
			close(shutdownStarted)
			<-releaseShutdown
			return nil
		},
		func() { close(quit) },
		nil,
	) {
		t.Fatal("Begin() rejected the first quit request")
	}

	select {
	case <-shutdownStarted:
	case <-time.After(time.Second):
		t.Fatal("background shutdown did not start")
	}
	select {
	case <-quit:
		t.Fatal("quit completed before cleanup finished")
	default:
	}

	close(releaseShutdown)
	select {
	case <-quit:
	case <-time.After(time.Second):
		t.Fatal("quit did not run after cleanup succeeded")
	}
}

func TestQuitFlowSuppressesDuplicateRequestsAndRecoversAfterFailure(t *testing.T) {
	flow := &QuitFlow{}
	releaseShutdown := make(chan struct{})
	shutdowns := make(chan struct{}, 2)
	recoveries := make(chan error, 1)

	begin := func() bool {
		return flow.Begin(
			nil,
			func() error {
				shutdowns <- struct{}{}
				<-releaseShutdown
				return errors.New("cleanup failed")
			},
			nil,
			func(err error) { recoveries <- err },
		)
	}

	if !begin() {
		t.Fatal("Begin() rejected the first quit request")
	}
	if begin() {
		t.Fatal("Begin() accepted a duplicate quit request")
	}
	select {
	case <-shutdowns:
	case <-time.After(time.Second):
		t.Fatal("background shutdown did not start")
	}
	close(releaseShutdown)

	select {
	case err := <-recoveries:
		if err == nil || err.Error() != "cleanup failed" {
			t.Fatalf("recovery error = %v, want cleanup failure", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup failure was not reported")
	}
	waitForQuitFlowIdle(t, flow)
	if flow.InProgress() {
		t.Fatal("quit flow remained active after a failed cleanup")
	}

	if !begin() {
		t.Fatal("Begin() did not allow a retry after cleanup failure")
	}
}

func TestQuitFlowNotifiesAfterRecoveryStateIsReleased(t *testing.T) {
	flow := &QuitFlow{}
	releaseShutdown := make(chan struct{})
	stateChanges := make(chan bool, 2)
	flow.SetStateChanged(func() {
		stateChanges <- flow.InProgress()
	})

	if !flow.Begin(
		nil,
		func() error {
			<-releaseShutdown
			return errors.New("cleanup failed")
		},
		nil,
		nil,
	) {
		t.Fatal("Begin() rejected the quit request")
	}
	if active := <-stateChanges; !active {
		t.Fatal("start notification reported an inactive quit flow")
	}

	close(releaseShutdown)
	select {
	case active := <-stateChanges:
		if active {
			t.Fatal("recovery notification arrived before the quit flow was released")
		}
	case <-time.After(time.Second):
		t.Fatal("recovery notification was not published")
	}
}

func TestQuitFlowDoesNotLetRecoveryRaceWithRetry(t *testing.T) {
	flow := &QuitFlow{}
	releaseShutdown := make(chan struct{})
	retryDuringRecovery := make(chan bool, 1)

	if !flow.Begin(
		nil,
		func() error {
			<-releaseShutdown
			return errors.New("cleanup failed")
		},
		nil,
		func(error) {
			retryDuringRecovery <- flow.Begin(nil, nil, nil, nil)
		},
	) {
		t.Fatal("Begin() rejected the first quit request")
	}
	close(releaseShutdown)

	select {
	case accepted := <-retryDuringRecovery:
		if accepted {
			t.Fatal("retry started before failure recovery completed")
		}
	case <-time.After(time.Second):
		t.Fatal("cleanup failure recovery did not run")
	}
	waitForQuitFlowIdle(t, flow)
	if flow.InProgress() {
		t.Fatal("quit flow remained active after failure recovery")
	}
}

func waitForQuitFlowIdle(t *testing.T, flow *QuitFlow) {
	t.Helper()
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	for flow.InProgress() {
		select {
		case <-deadline.C:
			t.Fatal("quit flow did not finish failure recovery")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}
