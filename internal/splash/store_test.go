package splash

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "splash")
	return NewStore(dir), t.TempDir()
}

func TestEmptyStoreListsTheWhaleSelected(t *testing.T) {
	store, _ := newTestStore(t)
	items, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != BuiltinWhale || !items[0].Selected || !items[0].Builtin || items[0].Kind != "scene" || items[0].URL != "" {
		t.Fatalf("unexpected list %+v", items)
	}
	if store.Current().ID != BuiltinWhale {
		t.Fatal("current should be the whale")
	}
}

func TestImportSelectAndRemove(t *testing.T) {
	store, src := newTestStore(t)
	imported, err := store.Import(writeFile(t, src, "Sea View.PNG", 10))
	if err != nil {
		t.Fatal(err)
	}
	if imported.Kind != "image" || imported.Name != "Sea View" || imported.Selected || imported.URL != "/__splash/"+imported.ID || len(imported.ID) != 32 {
		t.Fatalf("unexpected import %+v", imported)
	}
	if _, err := os.Stat(filepath.Join(store.dir, imported.ID+".png")); err != nil {
		t.Fatal("imported file missing:", err)
	}
	if _, err := store.Select(imported.ID); err != nil {
		t.Fatal(err)
	}
	if store.Current().ID != imported.ID {
		t.Fatal("selection not persisted")
	}
	reopened := NewStore(store.dir)
	if reopened.Current().ID != imported.ID {
		t.Fatal("selection not read back")
	}
	if err := store.Remove(imported.ID); err != nil {
		t.Fatal(err)
	}
	if store.Current().ID != BuiltinWhale {
		t.Fatal("removing the selection must fall back to the whale")
	}
	if _, err := os.Stat(filepath.Join(store.dir, imported.ID+".png")); !os.IsNotExist(err) {
		t.Fatal("file not removed")
	}
}

func TestVideoAndSVGKinds(t *testing.T) {
	store, src := newTestStore(t)
	video, err := store.Import(writeFile(t, src, "intro.webm", 10))
	if err != nil || video.Kind != "video" {
		t.Fatalf("video: %+v %v", video, err)
	}
	svg, err := store.Import(writeFile(t, src, "mark.svg", 10))
	if err != nil || svg.Kind != "image" {
		t.Fatalf("svg: %+v %v", svg, err)
	}
}

func TestImportRejections(t *testing.T) {
	store, src := newTestStore(t)
	for _, name := range []string{"page.html", "script.js", "data.xml", "noext"} {
		if _, err := store.Import(writeFile(t, src, name, 1)); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("%s: got %v", name, err)
		}
	}
	if _, err := store.Import(src); !errors.Is(err, ErrUnsupported) && !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory: got %v", err)
	}
	store.maxBytes = 5
	if _, err := store.Import(writeFile(t, src, "big.png", 6)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: got %v", err)
	}
	assertNoLeftovers(t, store.dir)
}

func TestImportRejectsSymlinks(t *testing.T) {
	store, src := newTestStore(t)
	target := writeFile(t, src, "real.png", 1)
	link := filepath.Join(src, "link.png")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	if _, err := store.Import(link); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("got %v", err)
	}
}

func TestFailedCopyLeavesNothing(t *testing.T) {
	store, src := newTestStore(t)
	store.copyFile = func(dst io.Writer, _ io.Reader) (int64, error) {
		_, _ = dst.Write([]byte{1, 2})
		return 2, errors.New("disk full")
	}
	if _, err := store.Import(writeFile(t, src, "a.png", 4)); err == nil {
		t.Fatal("expected failure")
	}
	assertNoLeftovers(t, store.dir)
	if items, _ := store.List(); len(items) != 1 {
		t.Fatal("failed import must not be listed")
	}
}

func TestBrokenStateFallsBackToTheWhale(t *testing.T) {
	store, src := newTestStore(t)
	imported, err := store.Import(writeFile(t, src, "a.png", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Select(imported.ID); err != nil {
		t.Fatal(err)
	}
	// media deleted by hand
	if err := os.Remove(filepath.Join(store.dir, imported.ID+".png")); err != nil {
		t.Fatal(err)
	}
	if store.Current().ID != BuiltinWhale {
		t.Fatal("missing media must fall back")
	}
	if items, _ := store.List(); len(items) != 1 {
		t.Fatalf("missing media must not be listed: %+v", items)
	}
	// catalog corrupted by hand
	if err := os.WriteFile(filepath.Join(store.dir, "catalog.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if store.Current().ID != BuiltinWhale {
		t.Fatal("corrupt catalog must fall back")
	}
	// selected id unknown
	if err := os.WriteFile(filepath.Join(store.dir, "catalog.json"), []byte(`{"version":1,"selected":"`+strings.Repeat("a", 32)+`","items":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if store.Current().ID != BuiltinWhale {
		t.Fatal("unknown selection must fall back")
	}
}

func TestBuiltinCannotBeRemovedAndUnknownIsNotFound(t *testing.T) {
	store, _ := newTestStore(t)
	if err := store.Remove(BuiltinWhale); !errors.Is(err, ErrBuiltin) {
		t.Fatalf("got %v", err)
	}
	if err := store.Remove(strings.Repeat("b", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if _, err := store.Select(strings.Repeat("b", 32)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestNewStoreCleansInterruptedImports(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "splash")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".import-123", 1)
	writeFile(t, dir, ".catalog-456.json", 1)
	NewStore(dir)
	assertNoLeftovers(t, dir)
}

func TestNoDirectoryMeansWhaleOnly(t *testing.T) {
	store := NewStore("")
	if store.Current().ID != BuiltinWhale {
		t.Fatal("whale expected")
	}
	if _, err := store.Import("x.png"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}

func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".import-") || strings.HasPrefix(entry.Name(), ".catalog-") {
			t.Fatalf("leftover %s", entry.Name())
		}
	}
}
