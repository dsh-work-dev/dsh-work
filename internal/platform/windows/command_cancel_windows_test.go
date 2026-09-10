//go:build windows

package windows

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshadapter"
)

func TestCommandCancellationStopsDescendants(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{}, 1)
	ctx = dshadapter.WithCommandOutput(ctx, func(line string) {
		if line == "child ready" {
			select {
			case ready <- struct{}{}:
			default:
			}
		}
	})
	done := make(chan error, 1)
	go func() {
		_, err := (CommandExecutor{}).Run(ctx, executable, []string{"-test.run=TestCommandCancellationHelper", "parent"}, map[string]string{"DSH_CANCEL_HELPER": "1"}, "")
		done <- err
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("child did not start")
	}
	start := time.Now()
	cancel()
	err = <-done
	elapsed := time.Since(start)
	t.Logf("cancellation completed in %s", elapsed)
	if elapsed > time.Second {
		t.Fatalf("cancellation waited for child: %s", elapsed)
	}
	if err == nil {
		t.Fatal("cancellation reported success")
	}
}

func TestCommandCancellationHelper(t *testing.T) {
	if os.Getenv("DSH_CANCEL_HELPER") != "1" {
		return
	}
	if os.Args[len(os.Args)-1] == "parent" {
		cmd := exec.Command(os.Args[0], "-test.run=TestCommandCancellationHelper", "child")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
	} else {
		_, _ = os.Stdout.WriteString("child ready\n")
		time.Sleep(5 * time.Second)
	}
	os.Exit(0)
}
