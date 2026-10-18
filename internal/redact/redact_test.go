package redact

import (
	"strings"
	"testing"
)

func TestSecretsRemovesSecretShapedValuesWithoutDroppingText(t *testing.T) {
	input := "token=abc123 request=ok tokenizer=keep password:top-secret"
	got := Secrets(input)
	want := "token=[REDACTED] request=ok tokenizer=keep password:[REDACTED]"
	if got != want {
		t.Fatalf("Secrets() = %q, want %q", got, want)
	}
}

func TestSecretsCoversHeaderAndJSONSecretFormats(t *testing.T) {
	input := `Authorization: Bearer bearer-secret Cookie: session-secret {"token":"json-secret","safe":"value"}`
	got := Secrets(input)
	for _, secret := range []string{"bearer-secret", "session-secret", "json-secret"} {
		if strings.Contains(got, secret) {
			t.Fatalf("Secrets() leaked %q in %q", secret, got)
		}
	}
	if !strings.Contains(got, `"safe":"value"`) {
		t.Fatalf("Secrets() dropped safe content: %q", got)
	}
}
