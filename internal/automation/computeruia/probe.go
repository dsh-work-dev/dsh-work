package computeruia

import (
	"context"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browsernative"
	"github.com/local/dsh-work/internal/supervisor"
	"github.com/local/dsh-work/internal/workeripc"
	"os"
	"path/filepath"
	"time"
)

type request struct {
	Target  automation.ComputerTarget `json:"target"`
	Element map[string]any            `json:"element"`
}
type response struct {
	Identity automation.ComputerElementIdentity `json:"identity"`
	Error    string                             `json:"error,omitempty"`
}

// Probe creates a short-lived, Job-owned UIA reader. Provider stalls cannot
// strand the Host or turn a cancellation into an unbounded COM wait.
func NewProbe(adapter supervisor.Adapter, executable string) automation.ComputerElementProbe {
	return func(ctx context.Context, target automation.ComputerTarget, element map[string]any) (_ automation.ComputerElementIdentity, err error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		empty := automation.ComputerElementIdentity{}
		ipc, e := workeripc.New()
		if e != nil {
			return empty, e
		}
		defer ipc.Close()
		env := map[string]string{}
		for k, v := range ipc.ChildEnvironment() {
			env[k] = v
		}
		for _, k := range []string{"SystemRoot", "WINDIR", "TEMP", "TMP", "LOCALAPPDATA", "USERPROFILE"} {
			if v := os.Getenv(k); v != "" {
				env[k] = v
			}
		}
		worker, e := adapter.Start(ctx, supervisor.LaunchPlan{GenerationID: "computer-uia", Executable: executable, Args: []string{"computer-uia-probe"}, Env: env, WorkingDirectory: filepath.Dir(executable), ExpectedOrigin: workeripc.Origin}, nil)
		if e != nil {
			return empty, e
		}
		go func() {
			for range worker.Events() {
			}
		}()
		defer func() {
			cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			cleanupErr := errors.Join(worker.ForceStop(cleanup), worker.WaitEmpty(cleanup), worker.Close())
			if cleanupErr != nil {
				err = errors.Join(err, fmt.Errorf("COMPUTER_HELPER_CLEANUP_FAILED: %w", cleanupErr))
			}
		}()
		connected := make(chan struct{})
		go func() {
			select {
			case <-worker.Exited():
				select {
				case <-connected:
				default:
					cancel()
				}
			case <-connected:
			case <-ctx.Done():
			}
		}()
		conn, e := ipc.Connect(ctx)
		close(connected)
		if e != nil {
			return empty, fmt.Errorf("COMPUTER_UIA_PROBE_START_FAILED: %w (exit=%d, %s)", e, worker.ExitResult().Code, worker.Diagnostics().StderrTail)
		}
		defer conn.Close()
		deadline, _ := ctx.Deadline()
		conn.SetDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
		defer stop()
		// Never send element value or surrounding text into this checker.
		safe := map[string]any{}
		for _, k := range []string{"role", "label", "frame", "automation_id"} {
			if v, ok := element[k]; ok {
				safe[k] = v
			}
		}
		if e = browsernative.WriteWireMessage(conn, request{target, safe}); e != nil {
			return empty, e
		}
		var reply response
		if e = browsernative.ReadWireMessage(conn, &reply); e != nil {
			return empty, e
		}
		if reply.Error != "" {
			return empty, errors.New(reply.Error)
		}
		if reply.Identity.RuntimeID == "" {
			return empty, errors.New("COMPUTER_ELEMENT_UNVERIFIED")
		}
		return reply.Identity, nil
	}
}
