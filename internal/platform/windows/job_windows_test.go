//go:build windows

package windows

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/local/work/internal/supervisor"
)

func TestJobObjectWorkerCapturesOutputAndReachesEmpty(t *testing.T) {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	plan := supervisor.LaunchPlan{
		GenerationID:     "windows-job-test",
		Executable:       comspec,
		Args:             []string{"/d", "/c", "echo worker-ready"},
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:4321",
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     4321,
	}
	rawOutput := make(chan string, 4)
	worker, err := NewJobObjectAdapter().Start(context.Background(), plan, func(_ supervisor.OutputStream, text string) {
		rawOutput <- text
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer worker.Close()

	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	select {
	case raw := <-rawOutput:
		if raw != "worker-ready" {
			t.Fatalf("unexpected raw readiness callback: %q", raw)
		}
	case <-deadline.C:
		t.Fatal("timed out waiting for raw output callback")
	}
	seenOutput := false
	for !seenOutput {
		select {
		case event, ok := <-worker.Events():
			if !ok {
				t.Fatalf("output channel closed before worker output: %+v", worker.Diagnostics())
			}
			if event.Stream == supervisor.StreamStdout && event.Text == "worker-ready" {
				seenOutput = true
			}
		case <-deadline.C:
			t.Fatal("timed out waiting for worker output")
		}
	}
	select {
	case <-worker.Exited():
	case <-deadline.C:
		t.Fatal("timed out waiting for worker exit")
	}
	if result := worker.ExitResult(); !result.Started || result.Code != 0 {
		t.Fatalf("unexpected process result: %+v", result)
	}
	emptyContext, cancelEmpty := context.WithTimeout(context.Background(), time.Second)
	defer cancelEmpty()
	if err := worker.WaitEmpty(emptyContext); err != nil {
		t.Fatalf("WaitEmpty() error = %v diagnostics=%+v", err, worker.Diagnostics())
	}
	if diagnostics := worker.Diagnostics(); diagnostics.ActiveProcesses != 0 {
		t.Fatalf("job still has active processes: %+v", diagnostics)
	}
}

func TestJobObjectWorkerPassesExplicitEnvironment(t *testing.T) {
	comspec := os.Getenv("ComSpec")
	if comspec == "" {
		comspec = "cmd.exe"
	}
	plan := supervisor.LaunchPlan{
		GenerationID:     "windows-env-test",
		Executable:       comspec,
		Args:             []string{"/d", "/c", "echo %WORK_TEST_HOME%"},
		Env:              map[string]string{"WORK_TEST_HOME": `C:\work-smoke-home`},
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:4321",
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     4321,
	}
	worker, err := NewJobObjectAdapter().Start(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer worker.Close()
	select {
	case <-worker.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for worker exit")
	}
	if !strings.Contains(worker.Diagnostics().StdoutTail, `C:\work-smoke-home`) {
		t.Fatalf("explicit environment was not passed: %+v", worker.Diagnostics())
	}
}
