package automation

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/settings"
)

func helloFor(instance string) BrowserHello {
	return BrowserHello{Protocol: BrowserProtocol, ExtensionVersion: "0.1.0", ExtensionID: DevelopmentExtensionID, Instance: instance}
}
func mustConnect(t *testing.T, s *Service, instance string) string {
	t.Helper()
	id, e := s.BrowserConnected("chrome", helloFor(instance))
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func mustBind(t *testing.T, s *Service, c, name string) string {
	t.Helper()
	p, e := s.RequestBrowserBinding(c, name)
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.ConfirmBrowserBinding(context.Background(), p.ID)
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func TestBrowserBindingSelectionReconnectAndConflict(t *testing.T) {
	ctx := context.Background()
	m := managerForTest(t)
	s := New(m, nil)
	defer s.Close()
	a := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b := mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, e := s.SelectedBrowserConnection(); !errors.Is(e, ErrBrowserOffline) {
		t.Fatal("hello selected a profile", e)
	}
	ba, bb := mustBind(t, s, a, "A"), mustBind(t, s, b, "B")
	if s.Snapshot().Profiles.Selected != "" {
		t.Fatal("confirmation implicitly selected first connection")
	}
	if e := s.SelectBrowserBinding(ctx, ba); e != nil {
		t.Fatal(e)
	}
	la, e := s.SelectedBrowserConnection()
	if e != nil || la.Connection != a {
		t.Fatal(la, e)
	}
	if e := s.SelectBrowserBinding(ctx, bb); e != nil {
		t.Fatal(e)
	}
	lb, e := s.SelectedBrowserConnection()
	if e != nil || lb.Connection != b {
		t.Fatal(lb, e)
	}
	if !errors.Is(s.ValidateBrowserLease(la), ErrRevoked) {
		t.Fatal("selection revived old lease")
	}
	s.BrowserDisconnected(a)
	a = mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if e := s.ValidateBrowserLease(lb); e != nil {
		t.Fatal("unselected reconnect revoked B", e)
	}
	s.BrowserDisconnected(b)
	if _, e := s.SelectedBrowserConnection(); !errors.Is(e, ErrBrowserOffline) {
		t.Fatal("offline B fell back to A")
	}
	b = mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	current, e := s.SelectedBrowserConnection()
	if e != nil || current.Connection != b || current.Binding != bb {
		t.Fatal(current, e)
	}
	if e := s.ValidateBrowserLease(lb); !errors.Is(e, ErrRevoked) {
		t.Fatal("reconnect revived old connection lease")
	}
	dup := mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, e := s.SelectedBrowserConnection(); !errors.Is(e, ErrBrowserConflict) {
		t.Fatal("duplicate was admitted as selected", e)
	}
	s.BrowserDisconnected(dup)
	if _, e := s.SelectedBrowserConnection(); !errors.Is(e, ErrBrowserConflict) {
		t.Fatal("one duplicate disconnect auto-restored old route")
	}
	s.BrowserDisconnected(b)
	b = mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if _, e := s.SelectedBrowserConnection(); e != nil {
		t.Fatal(e)
	}
	if e := s.ForgetBrowserBinding(ctx, bb); e != nil {
		t.Fatal(e)
	}
	if _, e := s.SelectedBrowserConnection(); !errors.Is(e, ErrBrowserOffline) {
		t.Fatal("forget selected another binding")
	}
	if s.BrowserConnectionStatus(a).Name != "A" || s.BrowserConnectionStatus(b).Status != "unbound" {
		t.Fatal("forget affected wrong profile")
	}
}
func TestBrowserBindingPendingAndPersistentBoundary(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "settings.json")
	m, e := settings.New(settings.Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	s := New(m, nil)
	defer s.Close()
	a := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	p, e := s.RequestBrowserBinding(a, "A")
	if e != nil {
		t.Fatal(e)
	}
	s.CancelBrowserBinding(p.ID)
	if _, e := s.ConfirmBrowserBinding(ctx, p.ID); !errors.Is(e, ErrBrowserBinding) {
		t.Fatal("cancelled request revived")
	}
	p, _ = s.RequestBrowserBinding(a, "A")
	s.mu.Lock()
	v := s.profiles.pending[p.ID]
	v.Expires = time.Now().Add(-time.Second)
	s.profiles.pending[p.ID] = v
	s.mu.Unlock()
	if _, e := s.ConfirmBrowserBinding(ctx, p.ID); !errors.Is(e, ErrBrowserBinding) {
		t.Fatal("expired request revived")
	}
	p, _ = s.RequestBrowserBinding(a, "A")
	s.BrowserDisconnected(a)
	a = mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if _, e := s.ConfirmBrowserBinding(ctx, p.ID); !errors.Is(e, ErrBrowserBinding) {
		t.Fatal("late confirmation rebound new connection")
	}
	id := mustBind(t, s, a, "A")
	if e := s.SelectBrowserBinding(ctx, id); e != nil {
		t.Fatal(e)
	}
	loaded, e := settings.New(settings.Config{Path: path})
	if e != nil {
		t.Fatal(e)
	}
	restarted := New(loaded, nil)
	defer restarted.Close()
	if restarted.Snapshot().Profiles.Selected != id {
		t.Fatal("selection not persistent")
	}
	if _, e := restarted.SelectedBrowserConnection(); !errors.Is(e, ErrBrowserOffline) {
		t.Fatal("connections persisted")
	}
	values, _ := loaded.Snapshot(ctx)
	values.BrowserProfiles.Bindings[0].Name = "mutated"
	values, _ = loaded.Snapshot(ctx)
	if values.BrowserProfiles.Bindings[0].Name != "A" {
		t.Fatal("settings snapshot aliased preferences")
	}
	if _, e := s.Execute(ctx, Request{Capability: Browser, Operation: "browser", Tool: "browser_navigate", Session: "s", Lifetime: "l"}); !errors.Is(e, ErrRevoked) {
		t.Fatal("setup enabled execution", e)
	}
	if _, e := s.BrowserConnected("chrome", BrowserHello{Protocol: 99}); !errors.Is(e, ErrBrowserProtocol) {
		t.Fatal("incompatible admitted")
	}
}

type bindingFailStore struct {
	settings.FileStore
	fail atomic.Bool
}

func (f *bindingFailStore) Save(ctx context.Context, path string, v settings.Values) error {
	if f.fail.Load() {
		return errors.New("fixture persistence failure")
	}
	return f.FileStore.Save(ctx, path, v)
}
func TestBrowserBindingCommitFailureAndConcurrentConfirm(t *testing.T) {
	ctx := context.Background()
	store := &bindingFailStore{}
	m, e := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json"), Store: store})
	if e != nil {
		t.Fatal(e)
	}
	s := New(m, nil)
	defer s.Close()
	c := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	p, e := s.RequestBrowserBinding(c, "A")
	if e != nil {
		t.Fatal(e)
	}
	store.fail.Store(true)
	if _, e := s.ConfirmBrowserBinding(ctx, p.ID); e == nil {
		t.Fatal("persistence failure accepted")
	}
	if len(s.Snapshot().Profiles.Bindings) != 0 || len(s.Snapshot().Profiles.Pending) != 1 {
		t.Fatal("failed commit changed binding state")
	}
	store.fail.Store(false)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, e := s.ConfirmBrowserBinding(ctx, p.ID); e == nil {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 1 || len(s.Snapshot().Profiles.Bindings) != 1 {
		t.Fatal("same request committed more than once")
	}
	id := s.Snapshot().Profiles.Bindings[0].ID
	store.fail.Store(true)
	if e := s.SelectBrowserBinding(ctx, id); e == nil || s.Snapshot().Profiles.Selected != "" {
		t.Fatal("failed selection published")
	}
}

func TestBrowserRenamePreservesLeaseAndFailedSave(t *testing.T) {
	store := &bindingFailStore{}
	m, e := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json"), Store: store})
	if e != nil {
		t.Fatal(e)
	}
	s := New(m, nil)
	defer s.Close()
	c := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	id := mustBind(t, s, c, "A")
	if e = s.SelectBrowserBinding(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	lease, e := s.SelectedBrowserConnection()
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RenameBrowserBinding(context.Background(), id, "Renamed"); e != nil {
		t.Fatal(e)
	}
	if e = s.ValidateBrowserLease(lease); e != nil {
		t.Fatal("rename revoked connection", e)
	}
	store.fail.Store(true)
	if e = s.RenameBrowserBinding(context.Background(), id, "Lost"); e == nil {
		t.Fatal("accepted failed persistence")
	}
	if s.Snapshot().Profiles.Bindings[0].Name != "Renamed" {
		t.Fatal("published failed rename")
	}
}

func TestBrowserBindingAutomaticallyLabelsTheConnectedInstance(t *testing.T) {
	s := New(managerForTest(t), nil)
	defer s.Close()
	a := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	b := mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	pa, e := s.RequestBrowserBinding(a, "")
	if e != nil {
		t.Fatal(e)
	}
	pb, e := s.RequestBrowserBinding(b, "")
	if e != nil {
		t.Fatal(e)
	}
	if pa.Name != "Profile AAAAAAAA" || pb.Name != "Profile BBBBBBBB" {
		t.Fatal("label did not come from actual connection identity", pa, pb)
	}
	id, e := s.ConfirmBrowserBinding(context.Background(), pa.ID)
	if e != nil {
		t.Fatal(e)
	}
	if s.Snapshot().Profiles.Selected != "" {
		t.Fatal("automatic label implicitly selected an instance")
	}
	if s.BrowserConnectionStatus(a).Name != pa.Name {
		t.Fatal("automatic label was not persisted")
	}
	if e = s.SelectBrowserBinding(context.Background(), id); e != nil {
		t.Fatal(e)
	}
	lease, e := s.SelectedBrowserConnection()
	if e != nil || lease.Connection != a {
		t.Fatal("bound wrong profile", lease, e)
	}
	if len(s.Snapshot().BrowserTools) != 0 {
		t.Fatal("binding authorized browser execution")
	}
}
