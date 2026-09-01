//go:build windows

package windows

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"

	"github.com/local/work/internal/dshadapter"
	"github.com/local/work/internal/supervisor"
	win "golang.org/x/sys/windows"
)

const commandOutputLimit = 64 * 1024

type CommandExecutor struct{}

func NewCommandExecutor() dshadapter.CommandExecutor {
	return CommandExecutor{}
}

func (CommandExecutor) Run(ctx context.Context, executable string, args []string, env map[string]string, dir string) (dshadapter.CommandResult, error) {
	command, commandArgs := commandFor(executable, args)
	cmd := exec.CommandContext(ctx, command, commandArgs...)
	cmd.Dir = dir
	cmd.Env = mergedEnvironment(env)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: win.CREATE_NO_WINDOW,
	}

	stdout := &boundedBuffer{limit: commandOutputLimit}
	stderr := &boundedBuffer{limit: commandOutputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	return dshadapter.CommandResult{
		Stdout: supervisor.Redact(stdout.String()),
		Stderr: supervisor.Redact(stderr.String()),
	}, err
}

func commandFor(executable string, args []string) (string, []string) {
	lower := strings.ToLower(executable)
	if strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") {
		comspec := os.Getenv("ComSpec")
		if comspec == "" {
			comspec = "cmd.exe"
		}
		commandArgs := make([]string, 0, len(args)+4)
		commandArgs = append(commandArgs, "/d", "/s", "/c", executable)
		commandArgs = append(commandArgs, args...)
		return comspec, commandArgs
	}
	return executable, append([]string(nil), args...)
}

func mergedEnvironment(overrides map[string]string) []string {
	entries := make(map[string]string)
	for _, entry := range os.Environ() {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			entries[strings.ToLower(key)] = entry
		}
	}
	for key, value := range overrides {
		entries[strings.ToLower(key)] = key + "=" + value
	}

	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool {
		left, _, _ := strings.Cut(result[i], "=")
		right, _, _ := strings.Cut(result[j], "=")
		return strings.ToLower(left) < strings.ToLower(right)
	})
	return result
}

type boundedBuffer struct {
	limit int
	data  []byte
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return len(p), nil
	}
	b.data = append(b.data, p...)
	if len(b.data) > b.limit {
		b.data = bytes.Clone(b.data[len(b.data)-b.limit:])
	}
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return string(b.data)
}
