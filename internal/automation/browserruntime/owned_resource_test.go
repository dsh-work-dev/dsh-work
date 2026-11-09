package browserruntime

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/supervisor"
	"testing"
)

type retryExecutor struct {
	calls   int
	failure error
}

func (r *retryExecutor) Execute(context.Context, string, map[string]any) (any, error) {
	return nil, nil
}
func (r *retryExecutor) Close() error {
	r.calls++
	if r.calls == 1 {
		return r.failure
	}
	return nil
}

type revokeOnly struct {
	automation.OwnedBrowserTarget
	calls int
}

func (t *revokeOnly) RevokeOwned(context.Context) error { t.calls++; return nil }

type retryJoinWorker struct {
	supervisor.Worker
	calls   int
	failure error
}

func (w *retryJoinWorker) ForceStop(context.Context) error { return nil }
func (w *retryJoinWorker) WaitEmpty(context.Context) error { return nil }
func (w *retryJoinWorker) Close() error {
	w.calls++
	if w.calls == 1 {
		return w.failure
	}
	return nil
}

func TestOwnedCloseRetainsRetryableExecutor(t *testing.T) {
	failure := errors.New("supervisor join incomplete")
	child := &retryExecutor{failure: failure}
	target := &revokeOnly{}
	_, cancel := context.WithCancel(context.Background())
	r := &ownedResource{cancel: cancel, target: target, current: child}
	if err := r.Close(); !errors.Is(err, failure) || r.current != child {
		t.Fatal("failed owner lost", err)
	}
	if err := r.Close(); !errors.Is(err, failure) || r.current != nil || child.calls != 2 || target.calls != 1 {
		t.Fatal("retry did not finish retained owner or preserve original error", err, child.calls, target.calls)
	}
}
func TestOwnedCloseRetriesActualResourceSupervisorJoin(t *testing.T) {
	failure := errors.New("join incomplete")
	worker := &retryJoinWorker{failure: failure}
	target := &revokeOnly{}
	_, childCancel := context.WithCancel(context.Background())
	child := &resource{cancel: childCancel, done: make(chan struct{}), worker: worker}
	_, cancel := context.WithCancel(context.Background())
	r := &ownedResource{cancel: cancel, target: target, current: child}
	if err := r.Close(); !errors.Is(err, failure) || !child.CleanupPending() || r.current != child {
		t.Fatal("actual backend owner lost", err)
	}
	if err := r.Close(); !errors.Is(err, failure) || child.CleanupPending() || r.current != nil || worker.calls != 2 {
		t.Fatal("real backend did not retry retained supervisor join", err, worker.calls)
	}
}

func TestInitializationKeepsFirstFailureWhenItCancelsResource(t *testing.T) {
	first := errors.New("Cannot find module 'ws'")
	r := &resource{failed: first}
	err := r.initializationFailure(context.Canceled)
	if !errors.Is(err, first) || !errors.Is(err, context.Canceled) {
		t.Fatal("initial failure masked by cancellation", err)
	}
	r.failed = nil
	if r.initializationFailure(context.DeadlineExceeded) != context.DeadlineExceeded {
		t.Fatal("unrelated cancellation changed")
	}
}
