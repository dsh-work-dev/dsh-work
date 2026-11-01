package automation

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/settings"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type prepareFunc func(context.Context, Capability, func(Progress)) error

func (f prepareFunc) Prepare(ctx context.Context, c Capability, p func(Progress)) error {
	return f(ctx, c, p)
}
func managerForTest(t *testing.T) *settings.Manager {
	t.Helper()
	m, e := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func awaitState(t *testing.T, s *Service, predicate func(Snapshot) bool) Snapshot {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		v := s.Snapshot()
		if predicate(v) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("state did not converge: %+v", s.Snapshot())
	return Snapshot{}
}
func TestPreparationIntentRetryCancelAndRestart(t *testing.T) {
	ctx := context.Background()
	m := managerForTest(t)
	var attempts, computerAttempts atomic.Int32
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	s := New(m, prepareFunc(func(ctx context.Context, c Capability, p func(Progress)) error {
		if c == Computer && computerAttempts.Add(1) == 1 {
			return errors.New("fixture failure")
		}
		if c == Computer {
			return nil
		}
		n := attempts.Add(1)
		p(Progress{Step: "verify"})
		entered <- struct{}{}
		if n == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}))
	defer s.Close()
	s.Worker("one", true, true)
	s.Gateway("one", true)
	if attempts.Load() != 0 {
		t.Fatal("startup prepared a disabled capability")
	}
	if _, e := s.SetEnabled(ctx, Browser, true); e != nil {
		t.Fatal(e)
	}
	<-entered
	for range 8 {
		if _, e := s.SetEnabled(ctx, Browser, true); e != nil {
			t.Fatal(e)
		}
	}
	if attempts.Load() != 1 {
		t.Fatal("duplicate enable was not merged")
	}
	if _, e := s.SetEnabled(ctx, Computer, true); e != nil {
		t.Fatal(e)
	}
	v := awaitState(t, s, func(v Snapshot) bool { return v.Computer.Dependency == "failed" })
	if !v.Computer.Enabled || v.Browser.Dependency != "preparing" || !v.CanChange {
		t.Fatal("preparation failure escaped its capability")
	}
	if _, e := s.SetEnabled(ctx, Browser, false); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SetEnabled(ctx, Browser, true); e != nil {
		t.Fatal(e)
	}
	<-entered
	close(release)
	v = awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	if v.Browser.Availability != "unavailable" {
		t.Fatal("dependency preparation claimed execution readiness")
	}
	if _, e := s.Retry(Computer); e != nil {
		t.Fatal(e)
	}
	awaitState(t, s, func(v Snapshot) bool { return v.Computer.Dependency == "prepared" })
	values, e := m.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	reloaded := New(m, prepareFunc(func(context.Context, Capability, func(Progress)) error { return nil }))
	defer reloaded.Close()
	if !reloaded.Snapshot().Browser.Enabled || reloaded.Snapshot().Generation != "" || reloaded.Snapshot().Browser.Dependency != "unprepared" || !values.Automation.Computer.Enabled {
		t.Fatal("runtime availability or intent restored incorrectly")
	}

}

type fixtureResource struct {
	started   chan struct{}
	closed    atomic.Int32
	failClose bool
	block     bool
}

func (r *fixtureResource) Execute(ctx context.Context, _ string, _ map[string]any) (any, error) {
	if r.block {
		if r.started != nil {
			r.started <- struct{}{}
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return map[string]any{"content": []any{map[string]string{"type": "text", "text": "host-routed"}}}, nil
}
func (r *fixtureResource) Close() error {
	r.closed.Add(1)
	if r.failClose {
		return errors.New("close fixture")
	}
	return nil
}
func TestResourceEpochIsolationAndFailureCleanup(t *testing.T) {
	ctx := context.Background()
	s := New(managerForTest(t), prepareFunc(func(context.Context, Capability, func(Progress)) error { return nil }))
	defer s.Close()
	s.Worker("w1", true, true)
	s.Gateway("w1", true)
	s.SetEnabled(ctx, Browser, true)
	v := awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	a := Request{Generation: "w1", Epoch: v.Browser.Epoch, Capability: Browser, Session: "a", Lifetime: "life", Operation: "probe"}
	if _, e := s.Execute(ctx, a); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("product backend should refuse: %v", e)
	}
	var creates atomic.Int32
	var mu sync.Mutex
	resources := map[string]*fixtureResource{}
	s.SetValidationFactory(func(ctx context.Context, r Request) (Resource, error) {
		creates.Add(1)
		time.Sleep(15 * time.Millisecond)
		resource := &fixtureResource{failClose: r.Session == "a"}
		mu.Lock()
		resources[r.Session] = resource
		mu.Unlock()
		return resource, nil
	})
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.Execute(ctx, a); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if creates.Load() != 1 {
		t.Fatal("concurrent first creation was duplicated")
	}
	b := a
	b.Session = "b"
	s.Execute(ctx, b)
	s.StopSession("w1", "a", "life")
	if _, e := s.Execute(ctx, a); !errors.Is(e, ErrRevoked) {
		t.Fatal("late call survived stop")
	}
	if _, e := s.Execute(ctx, b); e != nil {
		t.Fatal("stopping a affected b", e)
	}
	s.SetEnabled(ctx, Browser, false)
	if resources["a"].closed.Load() != 1 || resources["b"].closed.Load() != 1 {
		t.Fatal("cleanup failure blocked another resource")
	}
	s.SetEnabled(ctx, Browser, true)
	v = s.Snapshot()
	if _, e := s.Execute(ctx, a); !errors.Is(e, ErrRevoked) {
		t.Fatal("old epoch revived")
	}
	fresh := a
	fresh.Epoch = v.Browser.Epoch
	if _, e := s.Execute(ctx, fresh); e != nil {
		t.Fatal("reenable did not allow a new lifecycle", e)
	}
	if _, err := s.SetDefaultBrowser(ctx, false); err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().Browser.Enabled || s.Snapshot().DefaultBrowserEnabled {
		t.Fatal("default path toggle changed master intent")
	}
	if _, err := s.Execute(ctx, fresh); !errors.Is(err, ErrRevoked) {
		t.Fatal("disabled browser path executed")
	}
	s.SetDefaultBrowser(ctx, true)
	if _, err := s.Execute(ctx, fresh); !errors.Is(err, ErrRevoked) {
		t.Fatal("default path reenable revived old epoch")
	}
	fresh.Epoch = s.Snapshot().Browser.Epoch
	if _, err := s.Execute(ctx, fresh); err != nil {
		t.Fatal("new default path lifetime refused", err)
	}
	s.Worker("w2", true, true)
	s.Gateway("w2", true)
	if _, e := s.Execute(ctx, fresh); !errors.Is(e, ErrRevoked) {
		t.Fatal("old Worker reference revived")
	}
	// Partially created resources are owned even when factory initialization fails.
	failed := &fixtureResource{}
	s.SetValidationFactory(func(context.Context, Request) (Resource, error) { return failed, errors.New("initialize fixture") })
	fresh.Generation = "w2"
	fresh.Epoch = s.Snapshot().Browser.Epoch
	if _, e := s.Execute(ctx, fresh); e == nil {
		t.Fatal("initialization failure accepted")
	}
	s.StopSession("w2", "a", "life")
	if failed.closed.Load() != 1 {
		t.Fatal("partial initialization leaked")
	}
}

func TestCallCancellationKeepsTheResourceAndStopRevokesIt(t *testing.T) {
	ctx := context.Background()
	s := New(managerForTest(t), prepareFunc(func(context.Context, Capability, func(Progress)) error { return nil }))
	defer s.Close()
	s.Worker("worker", true, true)
	s.Gateway("worker", true)
	s.SetEnabled(ctx, Browser, true)
	v := awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	r := Request{Generation: "worker", Epoch: v.Browser.Epoch, Capability: Browser, Session: "a", Lifetime: "one", Operation: "probe"}
	resource := &fixtureResource{block: true, started: make(chan struct{}, 1)}
	var creates atomic.Int32
	s.SetValidationFactory(func(context.Context, Request) (Resource, error) { creates.Add(1); return resource, nil })
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.Execute(cancelled, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if creates.Load() != 0 {
		t.Fatal("already-cancelled call created a resource")
	}
	call, cancelCall := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { _, err := s.Execute(call, r); done <- err }()
	<-resource.started
	cancelCall()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	resource.block = false
	if _, err := s.Execute(ctx, r); err != nil {
		t.Fatal("cancelled call destroyed the resource", err)
	}
	if creates.Load() != 1 || resource.closed.Load() != 0 {
		t.Fatal("cancellation released or duplicated resource")
	}
	s.StopSession("worker", "a", "one")
	s.StopSession("worker", "a", "one")
	if resource.closed.Load() != 1 {
		t.Fatal("stop cleanup was not idempotent")
	}
}

func TestGatewayLossDoesNotCancelPreparationOrDisableSettings(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	s := New(managerForTest(t), prepareFunc(func(ctx context.Context, c Capability, p func(Progress)) error {
		close(entered)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-finish:
			return nil
		}
	}))
	defer s.Close()
	s.Worker("worker", true, true)
	s.Gateway("worker", true)
	s.SetEnabled(context.Background(), Browser, true)
	<-entered
	s.Gateway("worker", false)
	close(finish)
	v := awaitState(t, s, func(v Snapshot) bool { return v.Browser.Dependency == "prepared" })
	if !v.CanChange || v.GatewayConnected || !v.Browser.Enabled {
		t.Fatal("connection health changed readiness or intent")
	}
	if _, err := s.SetEnabled(context.Background(), Browser, false); err != nil {
		t.Fatal("could not turn off after channel loss", err)
	}
}
