package supervisor

import "testing"

func TestLaunchPlanRequiresWorkerRoutingIdentity(t *testing.T) {
	plan := LaunchPlan{
		GenerationID:     "generation",
		Executable:       "dsh",
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:4321",
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	for _, mutate := range []func(*LaunchPlan){
		func(value *LaunchPlan) { value.ExpectedOrigin = "http://example.com" },
		func(value *LaunchPlan) { value.ExpectedOrigin = "https://127.0.0.1:4321" },
		func(value *LaunchPlan) { value.ExpectedOrigin = "http://127.0.0.1:4321/?external=1" },
	} {
		copy := plan
		mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Errorf("mutated plan unexpectedly validated: %+v", copy)
		}
	}
}
