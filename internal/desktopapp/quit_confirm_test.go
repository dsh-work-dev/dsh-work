package desktopapp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/lifecycle"
)

func TestHostQuitInspectionKeepsUnavailableWorkersBehindConfirmation(t *testing.T) {
	for _, state := range []lifecycle.State{lifecycle.StateStopped, lifecycle.StateStarting, lifecycle.StateReady, lifecycle.StateStopping, lifecycle.StateFailed} {
		t.Run(string(state), func(t *testing.T) {
			bridge := dshactivity.New()
			asked, proceeded := false, false
			finished := make(chan struct{})
			q := quitConfirmation{
				inspect: func(ctx context.Context) (dshactivity.QuitInspection, error) {
					return inspectQuitForHost(ctx, func() lifecycle.Status {
						return lifecycle.Status{State: state, GenerationID: "current"}
					}, bridge.InspectQuit)
				},
				ask: func(_ dshactivity.QuitInspection, done func(bool)) {
					asked = true
					done(false)
					close(finished)
				},
			}
			q.confirm(func() { proceeded = true; close(finished) })
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("inspection did not finish")
			}
			if asked != (state != lifecycle.StateStopped) || proceeded != (state == lifecycle.StateStopped) {
				t.Fatalf("asked=%v proceeded=%v", asked, proceeded)
			}
		})
	}
}

func TestHostQuitInspectionRejectsLifecycleChange(t *testing.T) {
	state := lifecycle.Status{State: lifecycle.StateReady, GenerationID: "before"}
	_, err := inspectQuitForHost(context.Background(), func() lifecycle.Status { return state }, func(_ context.Context, generation string) (dshactivity.QuitInspection, error) {
		if generation != "before" {
			t.Fatalf("generation=%s", generation)
		}
		state.GenerationID = "after"
		return dshactivity.QuitInspection{}, nil
	})
	if err == nil {
		t.Fatal("obsolete empty inspection was accepted")
	}
}

func TestQuitConfirmationDecisions(t *testing.T) {
	for _, tc := range []struct {
		name       string
		inspection dshactivity.QuitInspection
		err        error
		yes        bool
	}{
		{name: "empty"},
		{name: "active", inspection: dshactivity.QuitInspection{ActiveTasks: true}, yes: true},
		{name: "scheduled", inspection: dshactivity.QuitInspection{ScheduledTasks: true}},
		{name: "both", inspection: dshactivity.QuitInspection{ActiveTasks: true, ScheduledTasks: true}, yes: true},
		{name: "failure", err: errors.New("inspection failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proceeded := make(chan struct{}, 1)
			asked := make(chan dshactivity.QuitInspection, 1)
			finished := make(chan struct{})
			q := &quitConfirmation{inspect: func(context.Context) (dshactivity.QuitInspection, error) { return tc.inspection, tc.err }, ask: func(got dshactivity.QuitInspection, done func(bool)) {
				asked <- got
				done(tc.yes)
				close(finished)
			}}
			q.confirm(func() { proceeded <- struct{}{} })
			if tc.name == "empty" {
				select {
				case <-proceeded:
				case <-time.After(time.Second):
					t.Fatal("empty inspection did not proceed")
				}
				select {
				case <-asked:
					t.Fatal("empty inspection asked")
				default:
				}
				return
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("confirmation did not finish")
			}
			got := <-asked
			if tc.err != nil && !got.ActiveTasks {
				t.Fatal("inspection failure did not assume active tasks")
			}
			select {
			case <-proceeded:
				if !tc.yes {
					t.Fatal("cancel proceeded")
				}
			default:
				if tc.yes {
					t.Fatal("yes did not proceed")
				}
			}
		})
	}
}

func TestQuitConfirmationDoesNotStackOrCompleteTwice(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	prompts := make(chan func(bool), 2)
	proceeded := 0
	q := &quitConfirmation{inspect: func(context.Context) (dshactivity.QuitInspection, error) {
		close(started)
		<-release
		return dshactivity.QuitInspection{ActiveTasks: true}, nil
	}, ask: func(_ dshactivity.QuitInspection, done func(bool)) { prompts <- done }}
	q.confirm(func() { proceeded++ })
	<-started
	q.confirm(func() { t.Error("stacked inspection") })
	close(release)
	var done func(bool)
	select {
	case done = <-prompts:
	case <-time.After(time.Second):
		t.Fatal("no prompt")
	}
	q.confirm(func() { t.Error("stacked dialog") })
	done(true)
	done(false)
	if proceeded != 1 || q.busy.Load() {
		t.Fatalf("proceeded=%d busy=%v", proceeded, q.busy.Load())
	}
}
