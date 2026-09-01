package supervisor

import "testing"

func TestLaunchPlanRequiresLoopbackHTTPOrigin(t *testing.T) {
	plan := LaunchPlan{
		GenerationID:     "generation",
		Executable:       "dsh",
		WorkingDirectory: t.TempDir(),
		ExpectedOrigin:   "http://127.0.0.1:4321",
		ExpectedHost:     "127.0.0.1",
		ExpectedPort:     4321,
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
	for _, mutate := range []func(*LaunchPlan){
		func(value *LaunchPlan) { value.ExpectedHost = "localhost" },
		func(value *LaunchPlan) { value.ExpectedOrigin = "https://127.0.0.1:4321" },
		func(value *LaunchPlan) { value.ExpectedOrigin = "http://127.0.0.1:4322" },
		func(value *LaunchPlan) { value.ExpectedOrigin = "http://127.0.0.1:4321/?external=1" },
	} {
		copy := plan
		mutate(&copy)
		if err := copy.Validate(); err == nil {
			t.Errorf("mutated plan unexpectedly validated: %+v", copy)
		}
	}
}

func TestRedactRemovesSecretShapedValuesWithoutDroppingText(t *testing.T) {
	input := "token=abc123 request=ok tokenizer=keep password:top-secret"
	got := Redact(input)
	want := "token=[REDACTED] request=ok tokenizer=keep password:[REDACTED]"
	if got != want {
		t.Fatalf("Redact() = %q, want %q", got, want)
	}
}
