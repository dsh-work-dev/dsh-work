package embeddedbrowser

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// Registry scheduling/ownership seam only; no synthetic native-drain evidence.
type downloadDrainOwner struct {
	paused  atomic.Bool
	waiting chan struct{}
}

func (*downloadDrainOwner) message(json.RawMessage) error              { return nil }
func (*downloadDrainOwner) state() []DownloadView                      { return nil }
func (*downloadDrainOwner) cancel(context.Context, string, bool) error { return nil }
func (*downloadDrainOwner) afterJoin() error                           { return nil }
func (*downloadDrainOwner) pending() bool                              { return true }
func (*downloadDrainOwner) busy() bool                                 { return true }
func (o *downloadDrainOwner) quiesce() (func(), error) {
	if !o.paused.CompareAndSwap(false, true) {
		return nil, errors.New("already paused")
	}
	return func() { o.paused.Store(false) }, nil
}
func (o *downloadDrainOwner) drain(ctx context.Context, cancel bool) error {
	close(o.waiting)
	<-ctx.Done()
	return ctx.Err()
}

func TestInstanceDownloadWaitKeepsProviderUsableAndRestoresOnFailure(t *testing.T) {
	o := &downloadDrainOwner{waiting: make(chan struct{})}
	b := &Broker{downloads: o}
	i := &instancePresenter{peer: PresenterPeer{PID: 7, Started: 11}, broker: b}
	r := &InstancePresenterRegistry{surfaces: map[string]*instancePresenter{"workspace": i}, config: InstanceRegistryConfig{DownloadDecision: func(context.Context) (bool, error) { return false, nil }}}
	ctx, stop := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- r.PrepareCloseInstance(ctx, i.peer, "workspace") }()
	select {
	case <-o.waiting:
	case <-time.After(time.Second):
		t.Fatal("wait not reached")
	}
	// The same admission used by State/CancelDownload remains available. A
	// held serial lock here would freeze progress and prevent manual cancel.
	locked := make(chan error, 1)
	go func() {
		owner, err := r.lockSurface(i.peer, "workspace")
		if err == nil {
			owner.serial.Unlock()
		}
		locked <- err
	}()
	select {
	case err := <-locked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("wait blocked provider")
	}
	i.serial.Lock()
	if err := r.startLocked(context.Background(), i); err == nil {
		t.Fatal("shutdown admitted a broker start")
	}
	i.serial.Unlock()
	stop()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if o.paused.Load() || i.downloadResume != nil || i.downloadPreparing || i.broker != b || i.retired {
		t.Fatal("failed wait lost original owner/admission")
	}
}

func TestGlobalDownloadDecisionRefusalRestoresEveryBroker(t *testing.T) {
	first, second := &downloadDrainOwner{}, &downloadDrainOwner{}
	r := &InstancePresenterRegistry{instances: map[string]*instancePresenter{"one": {broker: &Broker{downloads: first}}, "two": {broker: &Broker{downloads: second}}}, config: InstanceRegistryConfig{DownloadDecision: func(context.Context) (bool, error) {
		if !first.paused.Load() || !second.paused.Load() {
			t.Fatal("decision preceded whole registry admission barrier")
		}
		return false, ErrDownloadShutdownDeclined
	}}}
	if _, err := r.PrepareDownloadShutdown(context.Background()); !errors.Is(err, ErrDownloadShutdownDeclined) {
		t.Fatal(err)
	}
	if r.downloadPaused || first.paused.Load() || second.paused.Load() || r.closed {
		t.Fatal("refusal changed live registry")
	}
}

func TestMaintenanceReopenRequiresJoinedOwnersAndDropsOnlyRetiredBindings(t *testing.T) {
	profile := &retainedProfile{}
	i := &instancePresenter{profile: profile}
	r := &InstancePresenterRegistry{closed: true, quitAdmission: true, instances: map[string]*instancePresenter{"owned": i}, surfaces: map[string]*instancePresenter{"workspace": i}, processes: map[PresenterPeer]*instancePresenter{i.peer: i}}
	if err := r.ReopenAfterMaintenance(); err == nil || len(r.instances) != 1 || profile.closes != 0 {
		t.Fatal("live profile owner discarded", err)
	}
	i.profile = nil
	if err := r.ReopenAfterMaintenance(); err != nil {
		t.Fatal(err)
	}
	if r.closed || r.quitAdmission || len(r.instances) != 0 || len(r.surfaces) != 0 || len(r.processes) != 0 {
		t.Fatal("retired registry could not re-admit fresh UI")
	}
}
