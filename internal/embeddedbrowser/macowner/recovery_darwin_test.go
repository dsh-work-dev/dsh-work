//go:build darwin && cgo

package macowner

import (
	"errors"
	"os"
	"testing"

	"github.com/local/dsh-work/internal/supervisor"
)

func TestRecoveryRefreshPreservesGuardTerminalFacts(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	old := journal{Generation: "cef-Recovery", Job: "dev.dsh.cef.recovery", Owner: Process{PID: 101, Started: 111}, Broker: Process{PID: 102, Started: 222}, Phase: "quiescent", Owned: map[string]owned{}}
	latest := old
	latest.Phase = "empty"
	latest.Exit = supervisor.ExitResult{Started: true, Code: 75, Err: "observed-failure"}
	latest.Owned = map[string]owned{"actual-child": {Role: "helper:code-sign-clone-cleanup", Process: Process{PID: 103, Started: 333, PGID: 103}}}
	if err := save(dir, key, latest); err != nil {
		t.Fatal(err)
	}
	actual, err := freshRecoveryState(dir, key, old)
	if err != nil || actual.Exit != latest.Exit || actual.Phase != "empty" || len(actual.Owned) != 1 {
		t.Fatal("newer terminal facts were lost", actual, err)
	}
	changed := old
	changed.Owner.Started++
	if _, err = freshRecoveryState(dir, key, changed); !errors.Is(err, ErrCleanupPending) {
		t.Fatal("foreign guard instance accepted", err)
	}
	changed = old
	changed.Broker.Started++
	if _, err = freshRecoveryState(dir, key, changed); !errors.Is(err, ErrCleanupPending) {
		t.Fatal("foreign broker instance accepted", err)
	}
}

func TestBrokerExitRecoveryRequiresAnObservedNativeBroker(t *testing.T) {
	state := journal{
		Phase:  "broker-exited",
		Owner:  Process{PID: 101, Started: 111, PGID: 101},
		Broker: Process{PID: 102, Started: 222, PPID: 101, PGID: 101},
	}
	state.Owned = map[string]owned{processKey(state.Broker): {Process: state.Broker}}
	if ownerStateRecoverable(state) {
		t.Fatal("exit without a wait result was accepted")
	}
	state.Exit.Started = true
	if !ownerStateRecoverable(state) {
		t.Fatal("authenticated broker exit was rejected")
	}
	delete(state.Owned, processKey(state.Broker))
	if ownerStateRecoverable(state) {
		t.Fatal("exit without the tracked broker identity was accepted")
	}
	state.Owned[processKey(state.Broker)] = owned{Process: state.Broker}
	state.Component = "owned-auv"
	if ownerStateRecoverable(state) {
		t.Fatal("native broker exit phase was accepted for another component")
	}
}
