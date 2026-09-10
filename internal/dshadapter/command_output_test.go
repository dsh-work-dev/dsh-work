package dshadapter

import (
	"context"
	"strings"
	"testing"
)

func TestCommandOutputRedactsAcrossWritesAndBoundsLongLines(t *testing.T) {
	var lines []string
	ctx := WithCommandOutput(context.Background(), func(line string) { lines = append(lines, line) })
	writer := NewCommandOutputWriter(ctx)
	for _, part := range []string{"\x1b[32mDownloading\x1b[0m https://user:pass@example.test/pkg\nAuthorization: Bear", "er split-secret\r", "token=", "another-secret\n", strings.Repeat("x", 20*1024), "\nfinished"} {
		if _, err := writer.Write([]byte(part)); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.Close()
	got := strings.Join(lines, "\n")
	for _, secret := range []string{"user:pass", "split-secret", "another-secret", "\x1b", strings.Repeat("x", 100)} {
		if strings.Contains(got, secret) {
			t.Fatalf("unsafe output retained: %q", got)
		}
	}
	if len(lines) != 5 || lines[4] != "finished" || !strings.Contains(lines[0], "example.test/pkg") {
		t.Fatalf("output = %#v", lines)
	}
}
