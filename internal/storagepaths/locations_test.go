package storagepaths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func contents(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func TestRelocateAndReopenWithDefaultAndExternalData(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "external"}[external], func(t *testing.T) {
			base := t.TempDir()
			old := filepath.Join(base, "old")
			next := filepath.Join(base, "new")
			locator := filepath.Join(base, "control", "locations.json")
			m, e := Open(locator, old)
			if e != nil {
				t.Fatal(e)
			}
			user := filepath.Join(old, "user-data")
			if external {
				user = filepath.Join(base, "personal")
				m.state.Current.UserData = user
			}
			put(t, filepath.Join(user, "sessions", "chat.jsonl"), "conversation\n")
			put(t, filepath.Join(user, "storages", "workspace.json"), "project registry")
			put(t, filepath.Join(old, "environment", "profiles", "web", "package.json"), "profile")
			pkg := filepath.Join(old, "runtimes", "dsh", "package.json")
			put(t, pkg, "runtime")
			state, _ := json.Marshal(map[string]any{"runtimes": []any{map[string]any{"path": pkg}}, "unknown": "keep"})
			put(t, filepath.Join(old, "manager.json"), string(state))
			if _, e = m.Save(Locations{Root: next, UserData: m.state.Current.UserData}); e != nil {
				t.Fatal(e)
			}
			if e = m.ApplyPending(); e != nil {
				t.Fatal(e)
			}
			reopened, e := Open(locator, old)
			if e != nil {
				t.Fatal(e)
			}
			s := reopened.Snapshot()
			if s.Current.Root != next || s.Pending != nil {
				t.Fatalf("bad locator: %+v", s)
			}
			wantUser := filepath.Join(next, "user-data")
			if external {
				wantUser = user
			}
			if contents(t, filepath.Join(wantUser, "sessions", "chat.jsonl")) != "conversation\n" {
				t.Fatal("chat changed")
			}
			if contents(t, filepath.Join(next, "runtimes", "dsh", "package.json")) != "runtime" {
				t.Fatal("runtime lost")
			}
			if strings.Contains(contents(t, filepath.Join(next, "manager.json")), strings.ReplaceAll(old, "\\", "\\\\")) {
				t.Fatal("stale runtime path")
			}
			if _, e = os.Stat(old); !os.IsNotExist(e) {
				t.Fatalf("old store remains: %v", e)
			}
		})
	}
}
func TestMoveUserDataOutsideAndBackWithoutMovingEnvironment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	external := filepath.Join(base, "personal")
	m, _ := Open(filepath.Join(base, "locations.json"), root)
	put(t, filepath.Join(root, "environment", "profile"), "unchanged")
	put(t, filepath.Join(root, "user-data", "sessions", "s"), "history")
	for _, next := range []Locations{{Root: root, UserData: external}, {Root: root}} {
		if _, e := m.Save(next); e != nil {
			t.Fatal(e)
		}
		if e := m.ApplyPending(); e != nil {
			t.Fatal(e)
		}
		if contents(t, filepath.Join(next.UserDataPath(), "sessions", "s")) != "history" {
			t.Fatal("history lost")
		}
		if contents(t, filepath.Join(root, "environment", "profile")) != "unchanged" {
			t.Fatal("environment changed")
		}
	}
}
func TestPendingDestinationCollisionPreservesSourceAndActiveLocator(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "app")
	next := filepath.Join(base, "new")
	locator := filepath.Join(base, "locations.json")
	m, _ := Open(locator, root)
	put(t, filepath.Join(root, "user-data", "sessions", "s"), "history")
	if _, e := m.Save(Locations{Root: next}); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(next, "someone-elses-file"), "preserve")
	if e := m.ApplyPending(); e == nil {
		t.Fatal("accepted occupied destination")
	}
	reopened, e := Open(locator, root)
	if e != nil {
		t.Fatal(e)
	}
	if reopened.Snapshot().Current.Root != root {
		t.Fatal("published failed location")
	}
	if contents(t, filepath.Join(root, "user-data", "sessions", "s")) != "history" || contents(t, filepath.Join(next, "someone-elses-file")) != "preserve" {
		t.Fatal("lost data")
	}
	if _, e = m.Save(Locations{Root: filepath.Join(root, "nested")}); e == nil {
		t.Fatal("accepted nested root")
	}
}
func TestRelocatedDependencyLinkResolvesAfterOriginalRemoved(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "old")
	next := filepath.Join(base, "new")
	m, _ := Open(filepath.Join(base, "locations.json"), root)
	pkg := filepath.Join(root, "runtimes", "pkg")
	put(t, filepath.Join(pkg, "package.json"), "dependency")
	link := filepath.Join(root, "environment", "profiles", "node_modules", "pkg")
	if e := os.MkdirAll(filepath.Dir(link), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(pkg, link); e != nil {
		t.Skipf("symlink creation unavailable: %v", e)
	}
	if _, e := m.Save(Locations{Root: next}); e != nil {
		t.Fatal(e)
	}
	if e := m.ApplyPending(); e != nil {
		t.Fatal(e)
	}
	if contents(t, filepath.Join(next, "environment", "profiles", "node_modules", "pkg", "package.json")) != "dependency" {
		t.Fatal("broken link")
	}
}

func TestPreparationFailureDoesNotPublishOrDeleteData(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "old")
	next := filepath.Join(base, "new")
	m, _ := Open(filepath.Join(base, "locations.json"), root)
	put(t, filepath.Join(root, "manager.json"), "invalid JSON")
	put(t, filepath.Join(root, "user-data", "sessions", "s"), "history")
	if _, e := m.Save(Locations{Root: next}); e != nil {
		t.Fatal(e)
	}
	if e := m.ApplyPending(); e == nil {
		t.Fatal("accepted malformed manager state")
	}
	if m.Snapshot().Current.Root != root {
		t.Fatal("active location changed")
	}
	if contents(t, filepath.Join(root, "user-data", "sessions", "s")) != "history" {
		t.Fatal("source changed")
	}
	if _, e := os.Stat(next); !os.IsNotExist(e) {
		t.Fatal("published failed migration")
	}
	matches, _ := filepath.Glob(filepath.Join(base, ".dsh-work-migrate-*"))
	if len(matches) != 0 {
		t.Fatal("left partial staging directories")
	}
}
