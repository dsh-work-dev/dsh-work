//go:build windows

package windows

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/supervisor"
)

func TestOwnedProcessIDsEnumeratesJobDescendants(t *testing.T) {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	worker, err := NewJobObjectAdapter().Start(context.Background(), supervisor.LaunchPlan{
		GenerationID: "metrics-membership", Executable: comspec, Args: []string{"/d", "/c", "ping -n 30 127.0.0.1 >nul"}, WorkingDirectory: t.TempDir(), ExpectedOrigin: "http://127.0.0.1:4321",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	provider := worker.(interface {
		OwnedProcessIDs(context.Context) ([]int32, error)
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		pids, err := provider.OwnedProcessIDs(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		foundRoot := false
		for _, pid := range pids {
			if pid == int32(os.Getpid()) {
				t.Fatal("collector process leaked into Worker ownership")
			}
			if uint32(pid) == worker.Diagnostics().PID {
				foundRoot = true
			}
		}
		if foundRoot && len(pids) >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Job did not enumerate parent and descendant: %v", pids)
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := worker.ForceStop(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.WaitEmpty(ctx); err != nil {
		t.Fatal(err)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	pids, err := provider.OwnedProcessIDs(ctx)
	if err != nil || len(pids) != 0 {
		t.Fatalf("closed Job membership = %v, %v", pids, err)
	}
}
