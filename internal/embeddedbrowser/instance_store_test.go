package embeddedbrowser

import (
	"github.com/local/dsh-work/internal/app"
	"io"
	"path/filepath"
	"testing"
)

func instanceStoreLock(dir string) (io.Closer, error) {
	return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
}
func TestInstanceStorePersistentIDsAndSafeDescriptors(t *testing.T) {
	root := t.TempDir()
	s, e := OpenInstanceStore(root, instanceStoreLock)
	if e != nil {
		t.Fatal(e)
	}
	a, e := s.Resolve("default")
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Resolve("candidate-2")
	if e != nil {
		t.Fatal(e)
	}
	if a.ID == b.ID || a.Profile == b.Profile {
		t.Fatal("instances share profile")
	}
	if _, e := OpenInstanceStore(root, instanceStoreLock); e == nil {
		t.Fatal("second store owner admitted")
	}
	if e := s.Remember(a, TabDescriptor{Slot: "session/tab", URL: "https://example.test/path?token=secret#private", Title: "A"}); e != nil {
		t.Fatal(e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = OpenInstanceStore(root, instanceStoreLock)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	again, e := s.Resolve("default")
	if e != nil || again != a {
		t.Fatalf("identity changed: %+v %v", again, e)
	}
	rows, e := s.Descriptors(a)
	if e != nil || len(rows) != 1 || rows[0].URL != "https://example.test/path" {
		t.Fatalf("safe description: %+v %v", rows, e)
	}
	if _, e := s.Resolve("../../escape"); e == nil {
		t.Fatal("untrusted key accepted")
	}
}
