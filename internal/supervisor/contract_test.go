package supervisor

import (
	"strings"
	"testing"
)

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

func TestRedactRemovesSecretShapedValuesWithoutDroppingText(t *testing.T) {
	input := "token=abc123 request=ok tokenizer=keep password:top-secret"
	got := Redact(input)
	want := "token=[REDACTED] request=ok tokenizer=keep password:[REDACTED]"
	if got != want {
		t.Fatalf("Redact() = %q, want %q", got, want)
	}
}

func TestRedactCoversHeaderAndJSONSecretFormats(t *testing.T) {
	input := `Authorization: Bearer bearer-secret Cookie: session-secret {"token":"json-secret","safe":"value"}`
	got := Redact(input)
	for _, secret := range []string{"bearer-secret", "session-secret", "json-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("Redact() leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, `"safe":"value"`) {
		t.Fatalf("Redact() dropped safe content: %q", got)
	}
}
