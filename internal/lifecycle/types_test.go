package lifecycle

import "testing"

func TestMachineLegalLifecycle(t *testing.T) {
	machine := NewMachine()
	generation, status, err := machine.BeginStart()
	if err != nil {
		t.Fatalf("BeginStart() error = %v", err)
	}
	if status.State != StateStarting || status.Phase != PhaseConfiguration || generation == "" {
		t.Fatalf("unexpected starting status: %+v", status)
	}
	for _, phase := range []Phase{PhaseRuntime, PhaseWorker, PhaseReadiness} {
		if _, err := machine.SetPhase(generation, phase); err != nil {
			t.Fatalf("SetPhase(%q) error = %v", phase, err)
		}
	}
	status, err = machine.MarkReady(generation, "http://127.0.0.1:4321/")
	if err != nil {
		t.Fatalf("MarkReady() error = %v", err)
	}
	if status.State != StateReady || status.WorkspaceURL == "" || status.CanCancel != true {
		t.Fatalf("unexpected ready status: %+v", status)
	}
	status, err = machine.BeginStop(generation)
	if err != nil {
		t.Fatalf("BeginStop() error = %v", err)
	}
	if status.State != StateStopping || status.Phase != PhaseStopping {
		t.Fatalf("unexpected stopping status: %+v", status)
	}
	status, err = machine.CompleteStop(generation, nil)
	if err != nil {
		t.Fatalf("CompleteStop() error = %v", err)
	}
	if status.State != StateStopped || status.WorkspaceURL != "" || status.Error != nil {
		t.Fatalf("unexpected stopped status: %+v", status)
	}
}

func TestMachineRejectsIllegalAndStaleTransitions(t *testing.T) {
	machine := NewMachine()
	generation, _, err := machine.BeginStart()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := machine.BeginStart(); err == nil {
		t.Fatal("expected duplicate start to fail")
	}
	if _, err := machine.MarkReady("stale-generation", "http://127.0.0.1:4321/"); err == nil {
		t.Fatal("expected stale generation to fail")
	}
	if _, err := machine.CompleteStop(generation, nil); err == nil {
		t.Fatal("expected stop without BeginStop to fail")
	}
	if _, err := machine.MarkReady(generation, ""); err == nil {
		t.Fatal("expected empty workspace URL to fail")
	}
	if _, err := machine.MarkReady(generation, "http://127.0.0.1:4321/"); err != nil {
		t.Fatal(err)
	}
	if _, err := machine.MarkReady(generation, "http://127.0.0.1:4321/"); err == nil {
		t.Fatal("expected duplicate readiness to fail")
	}
}

func TestMachineRejectsOutOfOrderPhases(t *testing.T) {
	machine := NewMachine()
	generation, _, err := machine.BeginStart()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := machine.SetPhase(generation, PhaseReadiness); err == nil {
		t.Fatal("expected a phase jump to be rejected")
	}
	if _, err := machine.SetPhase(generation, PhaseRuntime); err != nil {
		t.Fatalf("configuration to runtime should be legal: %v", err)
	}
	if _, err := machine.SetPhase(generation, PhaseConfiguration); err == nil {
		t.Fatal("expected a phase regression to be rejected")
	}
}

func TestMachineFailureCanBeRetriedAndCarriesCorrelation(t *testing.T) {
	machine := NewMachine()
	generation, status, err := machine.BeginStart()
	if err != nil {
		t.Fatal(err)
	}
	failure := Failure{Code: ErrorDSHRuntimeNotFound, Summary: "runtime missing", Retryable: true}
	status, err = machine.Fail(generation, failure)
	if err != nil {
		t.Fatalf("Fail() error = %v", err)
	}
	if status.State != StateFailed || status.Error == nil || status.Error.CorrelationID != generation || !status.CanRetry {
		t.Fatalf("unexpected failed status: %+v", status)
	}
	newGeneration, status, err := machine.BeginStart()
	if err != nil {
		t.Fatalf("retry BeginStart() error = %v", err)
	}
	if newGeneration == generation || status.Error != nil || status.State != StateStarting {
		t.Fatalf("unexpected retry status: %+v", status)
	}
}
