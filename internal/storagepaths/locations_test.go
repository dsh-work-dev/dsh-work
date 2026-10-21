package storagepaths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
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

func TestConfigDefaultsMoveAndFutureTable(t *testing.T) {
	base := tempDir(t)
	root, data := filepath.Join(base, "app"), filepath.Join(base, "data")
	path := filepath.Join(base, "config.toml")
	m, err := Open(path, root, data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Current != (Locations{Root: root, UserData: data}) {
		t.Fatalf("defaults = %+v", m.Snapshot())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Open created config: %v", err)
	}
	put(t, path, "[future]\nvalue = 'keep'\nflags = [true, false]\n")
	m, err = Open(path, root, data)
	if err != nil {
		t.Fatal(err)
	}
	next := Locations{Root: filepath.Join(base, "app2"), UserData: filepath.Join(base, "data2")}
	if _, err = m.Save(next); err != nil {
		t.Fatal(err)
	}
	text := contents(t, path)
	if !strings.HasPrefix(text, configHeader) {
		t.Fatalf("header missing: %s", text)
	}
	var config configFile
	if err = toml.Unmarshal([]byte(text), &config); err != nil {
		t.Fatal(err)
	}
	if config.Storage.App != "" || config.Storage.Data != "" || config.Storage.Move == nil || config.Storage.Move.App != filepath.ToSlash(next.Root) || config.Storage.Move.Data != filepath.ToSlash(next.UserData) {
		t.Fatalf("config = %+v", config)
	}
	var document map[string]any
	if err = toml.Unmarshal([]byte(text), &document); err != nil {
		t.Fatal(err)
	}
	future := document["future"].(map[string]any)
	if future["value"] != "keep" || len(future["flags"].([]any)) != 2 {
		t.Fatalf("future = %#v", future)
	}
	if _, err = m.Cancel(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(contents(t, path), "[storage.move]") {
		t.Fatal("cancel kept move")
	}
}

func TestConfigKeepsTopLevelKeysOutsideStorageMove(t *testing.T) {
	base := tempDir(t)
	root, data := filepath.Join(base, "app"), filepath.Join(base, "data")
	path := filepath.Join(base, "config.toml")
	put(t, path, "foo = 'bar'\n[storage]\napp = ''\ndata = ''\n")

	m, err := Open(path, root, data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save(Locations{Root: filepath.Join(base, "app2"), UserData: data}); err != nil {
		t.Fatal(err)
	}

	text := contents(t, path)
	var document map[string]any
	if err := toml.Unmarshal([]byte(text), &document); err != nil {
		t.Fatal(err)
	}
	if document["foo"] != "bar" {
		t.Fatalf("top-level foo = %#v in %s", document["foo"], text)
	}
	storage := document["storage"].(map[string]any)
	move := storage["move"].(map[string]any)
	if _, exists := move["foo"]; exists {
		t.Fatalf("foo was emitted under storage.move: %#v", move)
	}

	reopened, err := Open(path, root, data)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.extra["foo"] != "bar" {
		t.Fatalf("reopened top-level extra = %#v", reopened.extra)
	}
}

func TestConfigPathsAndFailedMove(t *testing.T) {
	base := tempDir(t)
	root, data := filepath.Join(base, "app"), filepath.Join(base, "data")
	path := filepath.Join(base, "config.toml")
	custom := filepath.Join(base, "custom")
	put(t, path, "[storage]\ndata = '"+filepath.ToSlash(custom)+"'\n")
	m, err := Open(path, root, data)
	if err != nil || m.Snapshot().Current.UserData != custom {
		t.Fatalf("custom data: %v, %v", m, err)
	}
	put(t, path, "[storage]\ndata = 'relative'\n")
	if _, err = Open(path, root, data); err == nil || err.Error() != "invalid storage location in config.toml: storage.data" {
		t.Fatalf("relative path = %v", err)
	}
	put(t, path, "")
	m, err = Open(path, root, data)
	if err != nil {
		t.Fatal(err)
	}
	next := filepath.Join(base, "next")
	if _, err = m.Save(Locations{Root: next, UserData: data}); err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(next, "collision"), "keep")
	if err = m.ApplyPending(); err == nil || m.Snapshot().Error == "" {
		t.Fatal("move failure not reported")
	}
	text := contents(t, path)
	if strings.Contains(text, "[storage.move]") || strings.Contains(text, "error =") {
		t.Fatalf("failed move persisted state: %s", text)
	}
}

func TestConfigInsideMovedRootStaysAtFixedPath(t *testing.T) {
	base := tempDir(t)
	root, data := filepath.Join(base, "app"), filepath.Join(base, "data")
	path := filepath.Join(root, "config.toml")
	m, err := Open(path, root, data)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(root, "runtime"), "runtime")
	put(t, filepath.Join(data, "sessions", "chat"), "history")
	next := filepath.Join(base, "app2")
	if _, err = m.Save(Locations{Root: next, UserData: data}); err != nil {
		t.Fatal(err)
	}
	if err = m.ApplyPending(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(next, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config copied: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "config.toml" {
		t.Fatalf("old root = %v, %v", entries, err)
	}
	reopened, err := Open(path, root, data)
	if err != nil || reopened.Snapshot().Current.Root != next {
		t.Fatalf("reopened = %v, %v", reopened, err)
	}
	if contents(t, filepath.Join(data, "sessions", "chat")) != "history" {
		t.Fatal("app move changed user data")
	}
}

// tempDir returns t.TempDir in resolved form, matching what Save stores
// (macOS /var -> /private/var, Windows 8.3 short names).
func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
func contents(t *testing.T, path string) string {
	t.Helper()
	b, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestDefaultDirectoriesIncludeLogRoot(t *testing.T) {
	paths, err := DefaultDirectories()
	if err != nil {
		t.Fatalf("DefaultDirectories() error = %v", err)
	}
	if paths.LogRoot == "" || !filepath.IsAbs(paths.LogRoot) {
		t.Fatalf("LogRoot = %q, want an absolute path", paths.LogRoot)
	}
	if runtime.GOOS == "windows" && filepath.Dir(paths.LogRoot) != paths.CacheRoot {
		t.Fatalf("LogRoot parent = %q, want CacheRoot %q", filepath.Dir(paths.LogRoot), paths.CacheRoot)
	}
}

func TestRelocateAndReopenWithDefaultAndExternalData(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "external"}[external], func(t *testing.T) {
			base := tempDir(t)
			old := filepath.Join(base, "old")
			next := filepath.Join(base, "new")
			locator := filepath.Join(base, "control", "config.toml")
			m, e := Open(locator, old, filepath.Join(base, "data"))
			if e != nil {
				t.Fatal(e)
			}
			user := filepath.Join(base, "data")
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
			put(t, filepath.Join(old, "state.json"), string(state))
			if _, e = m.Save(Locations{Root: next, UserData: m.state.Current.UserData}); e != nil {
				t.Fatal(e)
			}
			if e = m.ApplyPending(); e != nil {
				t.Fatal(e)
			}
			reopened, e := Open(locator, old, filepath.Join(base, "data"))
			if e != nil {
				t.Fatal(e)
			}
			s := reopened.Snapshot()
			if s.Current.Root != next || s.Pending != nil {
				t.Fatalf("bad locator: %+v", s)
			}
			wantUser := user
			if external {
				wantUser = user
			}
			if contents(t, filepath.Join(wantUser, "sessions", "chat.jsonl")) != "conversation\n" {
				t.Fatal("chat changed")
			}
			if contents(t, filepath.Join(next, "runtimes", "dsh", "package.json")) != "runtime" {
				t.Fatal("runtime lost")
			}
			if strings.Contains(contents(t, filepath.Join(next, "state.json")), strings.ReplaceAll(old, "\\", "\\\\")) {
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
	m, _ := Open(filepath.Join(base, "config.toml"), root, filepath.Join(base, "data"))
	put(t, filepath.Join(root, "environment", "profile"), "unchanged")
	put(t, filepath.Join(base, "data", "sessions", "s"), "history")
	for _, next := range []Locations{{Root: root, UserData: external}, {Root: root}} {
		if _, e := m.Save(next); e != nil {
			t.Fatal(e)
		}
		if e := m.ApplyPending(); e != nil {
			t.Fatal(e)
		}
		if contents(t, filepath.Join(m.Snapshot().Current.UserDataPath(), "sessions", "s")) != "history" {
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
	locator := filepath.Join(base, "config.toml")
	m, _ := Open(locator, root, filepath.Join(base, "data"))
	put(t, filepath.Join(base, "data", "sessions", "s"), "history")
	if _, e := m.Save(Locations{Root: next}); e != nil {
		t.Fatal(e)
	}
	put(t, filepath.Join(next, "someone-elses-file"), "preserve")
	if e := m.ApplyPending(); e == nil {
		t.Fatal("accepted occupied destination")
	}
	reopened, e := Open(locator, root, filepath.Join(base, "data"))
	if e != nil {
		t.Fatal(e)
	}
	if reopened.Snapshot().Current.Root != root {
		t.Fatal("published failed location")
	}
	if contents(t, filepath.Join(base, "data", "sessions", "s")) != "history" || contents(t, filepath.Join(next, "someone-elses-file")) != "preserve" {
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
	m, _ := Open(filepath.Join(base, "config.toml"), root, filepath.Join(base, "data"))
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
	m, _ := Open(filepath.Join(base, "config.toml"), root, filepath.Join(base, "data"))
	put(t, filepath.Join(root, "state.json"), "invalid JSON")
	put(t, filepath.Join(base, "data", "sessions", "s"), "history")
	if _, e := m.Save(Locations{Root: next}); e != nil {
		t.Fatal(e)
	}
	if e := m.ApplyPending(); e == nil {
		t.Fatal("accepted malformed manager state")
	}
	if m.Snapshot().Current.Root != root {
		t.Fatal("active location changed")
	}
	if contents(t, filepath.Join(base, "data", "sessions", "s")) != "history" {
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

func TestNestedRootIsRejectedWhenCurrentRootIsALink(t *testing.T) {
	base := tempDir(t)
	target := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(target, "app"), 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	m, err := Open(filepath.Join(base, "config.toml"), filepath.Join(link, "app"), filepath.Join(base, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Save(Locations{Root: filepath.Join(target, "app", "nested")}); err == nil {
		t.Fatal("accepted a root nested in the current root reached through a link")
	}
}
