package automation

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/settings"
	"path/filepath"
	"testing"
	"time"
)

type launchFixture struct{ started chan struct{} }

func (f launchFixture) Resolve(string, string, string) (settings.BrowserLaunchHint, error) {
	return settings.BrowserLaunchHint{}, nil
}
func (f launchFixture) Open(ctx context.Context, _ string, _ settings.BrowserLaunchHint) error {
	close(f.started)
	return nil
}
func TestOpenProfileWaitsForExactBindingAndSelectionCancels(t *testing.T) {
	for _, cancelSelection := range []bool{false, true} {
		t.Run(map[bool]string{false: "exact", true: "selection"}[cancelSelection], func(t *testing.T) {
			s := New(managerForTest(t), nil)
			defer s.Close()
			s.Worker("launch", true, true)
			if _, err := enableAndInstallForTest(s, context.Background(), Browser, true); err != nil {
				t.Fatal(err)
			}
			a := mustConnect(t, s, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			b := mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			aid, bid := mustBind(t, s, a, "A"), mustBind(t, s, b, "B")
			if err := s.SelectBrowserBinding(context.Background(), bid); err != nil {
				t.Fatal(err)
			}
			s.mu.Lock()
			s.profiles.prefs.Bindings[1].Launch = &settings.BrowserLaunchHint{Executable: filepath.Join(t.TempDir(), "msedge.exe"), UserDataDirectory: t.TempDir(), ProfileDirectory: "Default"}
			s.mu.Unlock()
			s.BrowserDisconnected(b)
			f := launchFixture{make(chan struct{})}
			s.ConfigureProfileLauncher(f)
			result := make(chan error, 1)
			go func() { result <- s.OpenBrowserProfile(context.Background(), bid) }()
			select {
			case <-f.started:
			case <-time.After(time.Second):
				t.Fatal("no launch")
			}
			// An unrelated live instance must not satisfy the selected offline profile.
			select {
			case e := <-result:
				t.Fatal("accepted wrong instance", e)
			case <-time.After(80 * time.Millisecond):
			}
			if cancelSelection {
				if e := s.SelectBrowserBinding(context.Background(), aid); e != nil {
					t.Fatal(e)
				}
			} else {
				mustConnect(t, s, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
			}
			select {
			case e := <-result:
				if cancelSelection && !errors.Is(e, context.Canceled) && !errors.Is(e, ErrRevoked) {
					t.Fatal(e)
				}
				if !cancelSelection && e != nil {
					t.Fatal(e)
				}
			case <-time.After(time.Second):
				t.Fatal("launch did not finish")
			}
			if len(s.Snapshot().BrowserTools) != 0 {
				t.Fatal("setup granted product execution")
			}
		})
	}
}
