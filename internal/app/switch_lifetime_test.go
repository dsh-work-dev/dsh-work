package app

import (
	"context"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

func TestSwitchedWorkerOutlivesSwitchRequest(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		name := "candidate"
		if rollback {
			name = "rollback"
		}
		t.Run(name, func(t *testing.T) {
			fixture := newRunContextSwitchFixture(t)
			defer fixture.close()
			fixture.startReady(t)
			if rollback {
				fixture.supervisor.startErrors["beta"] = lifecycle.Failure{Code: lifecycle.ErrorProcessStartFailed, Summary: "candidate rejected"}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			_, err := fixture.host.SwitchRunContext(ctx, fixture.target("beta"))
			if (err != nil) != rollback {
				t.Fatalf("switch error = %v, rollback = %v", err, rollback)
			}
			cancel() // A completed UI request no longer owns the ready Worker.
			run := fixture.host.activeRun()
			if run == nil {
				t.Fatal("successful Worker disappeared after switch returned")
			}
			if err := run.ctx.Err(); err != nil {
				t.Fatalf("ready Worker inherited completed switch cancellation: %v", err)
			}
			select {
			case <-run.done:
				t.Fatal("ready Worker stopped after switch returned")
			case <-time.After(20 * time.Millisecond):
			}
			if status := fixture.host.Status(); status.State != lifecycle.StateReady {
				t.Fatalf("post-switch state = %v", status.State)
			}
			fixture.host.Cancel()
			select {
			case <-run.done:
			case <-time.After(time.Second):
				t.Fatal("Host could not stop the switched Worker")
			}
		})
	}
}
