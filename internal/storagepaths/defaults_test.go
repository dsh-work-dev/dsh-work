package storagepaths

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultStorageLayout(t *testing.T) {
	paths, err := DefaultDirectories()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if paths.UserDataRoot != filepath.Join(home, ".dsh-work") {
		t.Fatalf("user data = %q", paths.UserDataRoot)
	}
	rel, err := filepath.Rel(paths.Root, paths.UserDataRoot)
	if err != nil || !strings.HasPrefix(rel, "..") {
		t.Fatalf("user data relative to app = %q, %v", rel, err)
	}
	if filepath.Base(paths.ConfigFile) != "config.toml" {
		t.Fatalf("config = %q", paths.ConfigFile)
	}
	if runtime.GOOS == "windows" {
		cache, _ := os.UserCacheDir()
		config, _ := os.UserConfigDir()
		if paths.ConfigFile != filepath.Join(cache, "dsh-work", "config.toml") || paths.Root != filepath.Join(config, "dsh-work") {
			t.Fatalf("Windows defaults = %+v", paths)
		}
	}
}

func TestLayout(t *testing.T) {
	base := t.TempDir()
	locations := Locations{Root: filepath.Join(base, "app"), UserData: filepath.Join(base, "data")}
	home, runtimes, state := Layout(locations)
	if home != locations.UserData || runtimes != filepath.Join(locations.Root, "runtimes") || state != filepath.Join(locations.Root, "state.json") {
		t.Fatalf("layout = %q %q %q", home, runtimes, state)
	}
}
