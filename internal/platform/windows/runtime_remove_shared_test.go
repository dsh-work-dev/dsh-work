//go:build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/local/dsh-work/internal/dshmanager"
	sys "golang.org/x/sys/windows"
)

// pnpm hard-links one store file into every runtime. A native module loaded by
// the running runtime is open without delete sharing, so no link name of that
// file can be deleted until the process exits.
func TestRuntimeInstallerRemoveDoesNotBlockOnFileSharedWithRunningRuntime(t *testing.T) {
	store := t.TempDir()
	runtimeInfo := func(id string) dshmanager.RuntimeInfo {
		launcher := filepath.Join(store, id, "node_modules", ".bin", "dsh.cmd")
		if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(launcher, []byte("runtime"), 0o600); err != nil {
			t.Fatal(err)
		}
		return dshmanager.RuntimeInfo{ID: id, Path: launcher, Source: dshmanager.RuntimeSourceManaged}
	}
	running, old, older := runtimeInfo("dsh-0.1.7-rc.2"), runtimeInfo("dsh-0.1.7-alpha.2"), runtimeInfo("dsh-0.1.5-rc.3")
	// A loaded native module is mapped as an executable image. Map a copy of a
	// small system DLL the same way through the running runtime's link.
	loaded := filepath.Join(store, running.ID, "node_modules", "koffi.node")
	system, err := os.ReadFile(filepath.Join(os.Getenv("SystemRoot"), "System32", "version.dll"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loaded, system, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(loaded, filepath.Join(store, old.ID, "node_modules", "koffi.node")); err != nil {
		t.Fatal(err)
	}
	module, err := sys.LoadLibraryEx(loaded, 0, sys.LOAD_LIBRARY_AS_IMAGE_RESOURCE)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_ = sys.FreeLibrary(module)
		}
	}
	defer release()

	installer := NewRuntimeInstaller(nil, store)
	if err := installer.Remove(context.Background(), old); err != nil {
		t.Fatalf("Remove(old) error = %v, want success while the shared file is in use", err)
	}
	if _, err := os.Stat(filepath.Join(store, old.ID)); !os.IsNotExist(err) {
		t.Fatalf("old runtime directory still present: %v", err)
	}
	if err := installer.Remove(context.Background(), older); err != nil {
		t.Fatalf("Remove(older) error = %v, want an unrelated removal to ignore the pending leftover", err)
	}

	release()
	if err := installer.Remove(context.Background(), older); err != nil {
		t.Fatalf("repeated Remove() error = %v", err)
	}
	entries, err := os.ReadDir(store)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != running.ID {
			t.Fatalf("store retained %q after the shared file was released", entry.Name())
		}
	}
}
