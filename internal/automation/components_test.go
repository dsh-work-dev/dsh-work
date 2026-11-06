package automation

import (
	"context"
	"sync/atomic"
	"testing"
)

type managedPreparer struct {
	calls atomic.Int32
	ready atomic.Bool
}

func (p *managedPreparer) Prepare(context.Context, Capability, func(Progress)) error {
	p.calls.Add(1)
	p.ready.Store(true)
	return nil
}
func (p *managedPreparer) Ready(Capability) bool { return p.ready.Load() }

func TestIntentStartupAndSelectionNeverInstall(t *testing.T) {
	ctx := context.Background()
	m := managerForTest(t)
	p := &managedPreparer{}
	s := New(m, p)
	defer s.Close()
	s.Worker("one", true, true)
	if _, err := s.SetEnabled(ctx, Browser, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled(ctx, Computer, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SelectComputerEngine(ctx, "auv"); err != nil {
		t.Fatal(err)
	}
	s.Worker("two", true, true)
	if p.calls.Load() != 0 || s.Snapshot().Computer.Availability == "available" {
		t.Fatal("intent installed or opened backend", s.Snapshot())
	}
	if _, err := s.Retry(AUV); err != nil {
		t.Fatal(err)
	}
	awaitState(t, s, func(v Snapshot) bool { return v.Computer.Dependency == "prepared" })
	if p.calls.Load() != 1 || s.Snapshot().ComputerEngine != "auv" {
		t.Fatal("explicit install changed selection")
	}
	p.ready.Store(false)
	s.Worker("three", true, true)
	if s.Snapshot().Computer.Dependency != "unprepared" {
		t.Fatal("missing installation stayed prepared")
	}
	reloaded := New(m, p)
	defer reloaded.Close()
	if reloaded.Snapshot().ComputerEngine != "auv" {
		t.Fatal("engine did not persist")
	}
}

func TestExtensionPreparedAndLoadedIdentityRemainSeparate(t *testing.T) {
	s := New(managerForTest(t), nil)
	defer s.Close()
	s.ConfigureComponents(func() []Component {
		return []Component{{ID: "browser-extension", Capability: Browser, State: "prepared", Source: CurrentVersionInputs().ExtensionSource, InstalledVersion: "0.2.0"}}
	})
	v := s.Snapshot().Components[0]
	if v.State != "prepared" || v.LoadedVersion != "" {
		t.Fatal("prepared files fabricated a loaded browser", v)
	}
}

func TestUnselectedEngineReportsItsOwnPreparationProgress(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	s := New(managerForTest(t), prepareFunc(func(ctx context.Context, c Capability, report func(Progress)) error {
		report(Progress{Step: "download", ReceivedBytes: 42, TotalBytes: 100})
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	defer s.Close()
	defer close(release)
	s.ConfigureComponents(func() []Component { return []Component{{ID: "auv", Capability: AUV, State: "missing"}} })
	s.Worker("progress", true, true)
	if _, err := s.Retry(AUV); err != nil {
		t.Fatal(err)
	}
	<-entered
	v := s.Snapshot()
	r := v.Components[0]
	if v.ComputerEngine != "cua-driver" || v.Computer.Dependency == "preparing" || r.State != "preparing" || r.Step != "download" || r.ReceivedBytes != 42 || r.TotalBytes != 100 {
		t.Fatal("engine progress was assigned to selection instead of installed component", v)
	}
}
