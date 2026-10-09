//go:build windows

package windows

import (
	"context"
	"fmt"
	"github.com/local/dsh-work/internal/dshadapter"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandOutputIncludesProcessCreationFailure(t *testing.T) {
	var lines []string
	ctx := dshadapter.WithCommandOutput(context.Background(), func(line string) { lines = append(lines, line) })
	_, err := (CommandExecutor{}).Run(ctx, filepath.Join(t.TempDir(), "missing.exe"), nil, nil, "")
	if err == nil {
		t.Fatal("missing executable unexpectedly started")
	}
	if len(lines) == 0 || !strings.Contains(strings.Join(lines, "\n"), "missing.exe") {
		t.Fatalf("process creation failure was lost from operation logs: %v", lines)
	}
}

func TestValidateBatchInvocationRejectsShellSyntax(t *testing.T) {
	for _, test := range []struct {
		name       string
		executable string
		args       []string
	}{
		{name: "argument ampersand", executable: `C:\tools\dsh.cmd`, args: []string{"plugin", "@example/plugin&whoami"}},
		{name: "argument percent expansion", executable: `C:\tools\dsh.cmd`, args: []string{"plugin", "%PATH%"}},
		{name: "executable pipe", executable: `C:\tools\safe|dsh.cmd`, args: nil},
		{name: "argument newline", executable: `C:\tools\dsh.cmd`, args: []string{"plugin\nwhoami"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := validateBatchInvocation(test.executable, test.args); err == nil {
				t.Fatal("batch invocation unexpectedly accepted shell syntax")
			}
		})
	}
}

func TestValidateBatchInvocationAllowsFixedDSHArguments(t *testing.T) {
	if err := validateBatchInvocation(`C:\tools\dsh.cmd`, []string{
		"--profile", "web", "--host", "127.0.0.1", "--port", "4321", "--no-open",
	}); err != nil {
		t.Fatalf("fixed DSH launch args rejected: %v", err)
	}
}

func TestCommandExecutorStreamsBeforeCommandExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	lines := make(chan string, 4)
	ctx = dshadapter.WithCommandOutput(ctx, func(line string) { lines <- line })
	done := make(chan error, 1)
	go func() {
		// The test binary itself is the child: PowerShell's cold start on a CI
		// runner can exceed the whole budget and says nothing about streaming.
		self, err := os.Executable()
		if err != nil {
			done <- err
			return
		}
		_, err = (CommandExecutor{}).Run(ctx, self, []string{"-test.run=^TestStreamingCommandHelper$", "--", streamingHelperArg}, nil, "")
		done <- err
	}()
	select {
	case line := <-lines:
		if line != "download started" {
			t.Fatalf("first line = %q", line)
		}
	case <-ctx.Done():
		t.Fatal("no live command output")
	}
	select {
	case err := <-done:
		t.Fatalf("output arrived only after exit: %v", err)
	default:
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if line := <-lines; line != "installed" {
		t.Fatalf("last line = %q", line)
	}
}

const streamingHelperArg = "dsh-work-streaming-helper"

// TestStreamingCommandHelper is the child process of
// TestCommandExecutorStreamsBeforeCommandExits; run directly it does nothing.
func TestStreamingCommandHelper(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != streamingHelperArg {
		return
	}
	fmt.Println("download started")
	time.Sleep(700 * time.Millisecond)
	fmt.Println("installed")
	os.Exit(0)
}
