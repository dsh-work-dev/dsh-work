//go:build windows

package windows

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
)

type installerCommandFake struct {
	executable string
	args       []string
	directory  string
}

func (f *installerCommandFake) Run(_ context.Context, executable string, args []string, _ map[string]string, directory string) (dshadapter.CommandResult, error) {
	f.executable = executable
	f.args = append([]string(nil), args...)
	f.directory = directory
	prefixIndex := -1
	for index, arg := range args {
		if arg == "--prefix" && index+1 < len(args) {
			prefixIndex = index + 1
			break
		}
	}
	if prefixIndex >= 0 {
		launcher := filepath.Join(args[prefixIndex], "node_modules", ".bin", "dsh.cmd")
		if err := os.MkdirAll(filepath.Dir(launcher), 0o700); err != nil {
			return dshadapter.CommandResult{}, err
		}
		if err := os.WriteFile(launcher, []byte("test launcher"), 0o600); err != nil {
			return dshadapter.CommandResult{}, err
		}
	}
	return dshadapter.CommandResult{}, nil
}

func TestRuntimeInstallerUsesExplicitNativeNpmCommand(t *testing.T) {
	fake := &installerCommandFake{}
	store := t.TempDir()
	installer := NewRuntimeInstaller(fake, store)
	runtime, err := installer.Install(context.Background(), "0.1.2-alpha.3")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if runtime.Version != "0.1.2-alpha.3" || runtime.Source != dshmanager.RuntimeSourceManaged || !runtime.Installed || !runtime.Removable {
		t.Fatalf("unexpected installed runtime: %+v", runtime)
	}
	if fake.executable != "npm.cmd" || fake.directory != filepath.Join(store, "dsh-0.1.2-alpha.3") {
		t.Fatalf("native install target = executable %q directory %q", fake.executable, fake.directory)
	}
	if got := strings.Join(fake.args, " "); got != "install --prefix "+filepath.Join(store, "dsh-0.1.2-alpha.3")+" --ignore-scripts --no-save @deepseek-ai/dsh@0.1.2-alpha.3" {
		t.Fatalf("npm args = %q", got)
	}
}
