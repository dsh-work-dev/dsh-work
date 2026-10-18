package dshmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/lifecycle"
)

const incompatibleRefusal = "dsh: to accept the risk, run: dsh plugin --profile web allow-version @scope/pkg@1.0.20 --dsh-version 0.2.1-rc.2 --accept-risk"

func TestParseIncompatiblePlugins(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []IncompatiblePluginVersion
	}{
		{
			name:   "scoped package",
			output: incompatibleRefusal,
			want:   []IncompatiblePluginVersion{{Name: "@scope/pkg", Version: "1.0.20", DSHVersion: "0.2.1-rc.2"}},
		},
		{
			name:   "unscoped package",
			output: "dsh: to accept the risk, run: dsh plugin --profile web allow-version package@2.3.4 --dsh-version 0.2.1-rc.2 --accept-risk",
			want:   []IncompatiblePluginVersion{{Name: "package", Version: "2.3.4", DSHVersion: "0.2.1-rc.2"}},
		},
		{
			name:   "duplicate lines",
			output: incompatibleRefusal + "\n" + incompatibleRefusal,
			want:   []IncompatiblePluginVersion{{Name: "@scope/pkg", Version: "1.0.20", DSHVersion: "0.2.1-rc.2"}},
		},
		{name: "no match", output: "plugin failed", want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := parseIncompatiblePlugins(test.output); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("parseIncompatiblePlugins() = %#v, want %#v", got, test.want)
			}
		})
	}
}

type incompatibleCommandRunner struct {
	calls          [][]string
	updateFailures int
}

func (r *incompatibleCommandRunner) Run(_ context.Context, _ string, args []string, _ map[string]string, _ string) (CommandResult, error) {
	r.calls = append(r.calls, append([]string(nil), args...))
	if strings.Contains(strings.Join(args, " "), "allow-version") {
		return CommandResult{}, nil
	}
	if strings.Contains(strings.Join(args, " "), " update ") && r.updateFailures > 0 {
		r.updateFailures--
		return CommandResult{Stderr: incompatibleRefusal}, errors.New("exit status 1")
	}
	return CommandResult{}, nil
}

func newIncompatiblePluginTestManager(t *testing.T, runner CommandRunner) (*Manager, PluginTarget) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "dsh-home")
	profile := filepath.Join(home, "profiles", "web")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profile, "package.json"), []byte(`{"dependencies":{"@scope/pkg":"1.0.20"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(root, "dsh.cmd")
	if err := os.WriteFile(runtimePath, []byte("test runtime"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := New(Config{
		StatePath:       filepath.Join(root, "manager.json"),
		DataDirectories: []DataDirectoryInfo{{ID: "home", Name: "test", Path: home, Ownership: DataDirectoryOwnershipDSHWork}},
		Runtimes:        []RuntimeInfo{{ID: "dsh-test", Version: "0.2.1-rc.2", Path: runtimePath}},
		CommandRunner:   runner,
		PluginCommands:  testPluginCommands{},
		DefaultRunContext: RunContext{
			RuntimeID: "dsh-test",
			Profile:   ProfileRef{DataDirectoryID: "home", Name: "web"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	current := RunContext{RuntimeID: "dsh-test", Profile: ProfileRef{DataDirectoryID: "home", Name: "web"}}
	if _, err := manager.CommitCurrent(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	return manager, PluginTarget{Profile: current.Profile}
}

func TestRunPluginCommandReportsIncompatible(t *testing.T) {
	runner := &incompatibleCommandRunner{updateFailures: 1}
	manager, target := newIncompatiblePluginTestManager(t, runner)
	_, err := manager.runPluginCommand(context.Background(), target, "@scope/pkg@1.0.20", "update")
	assertFailureCode(t, err, lifecycle.ErrorPluginIncompatible)
	if len(runner.calls) != 1 || strings.Contains(strings.Join(runner.calls[0], " "), "allow-version") {
		t.Fatalf("runner calls = %#v, want one update and no allow-version", runner.calls)
	}
}

func TestRunPluginCommandAllowsIncompatibleWhenAccepted(t *testing.T) {
	runner := &incompatibleCommandRunner{updateFailures: 1}
	manager, target := newIncompatiblePluginTestManager(t, runner)
	result, err := manager.runPluginCommand(WithAcceptIncompatible(context.Background()), target, "@scope/pkg@1.0.20", "update")
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.Name != "web" {
		t.Fatalf("result profile = %q, want web", result.Profile.Name)
	}
	want := [][]string{
		{"plugin", "--profile", "web", "update", "@scope/pkg@1.0.20", "--registry", defaultPluginOfficialRegistry},
		{"plugin", "--profile", "web", "allow-version", "@scope/pkg@1.0.20", "--dsh-version", "0.2.1-rc.2", "--accept-risk"},
		{"plugin", "--profile", "web", "update", "@scope/pkg@1.0.20", "--registry", defaultPluginOfficialRegistry},
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("runner calls = %#v, want %#v", runner.calls, want)
	}
}
