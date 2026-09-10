package dshadapter

import (
	"context"
	"regexp"
	"strings"
	"sync"

	"github.com/local/dsh-work/internal/supervisor"
)

type commandOutputKey struct{}
type commandOutputSink struct {
	mu      sync.Mutex
	publish func(string)
}

// WithCommandOutput observes diagnostic output without changing command results.
// stdout and stderr are serialized before crossing the UI event boundary.
func WithCommandOutput(ctx context.Context, publish func(string)) context.Context {
	return context.WithValue(ctx, commandOutputKey{}, &commandOutputSink{publish: publish})
}

type CommandOutputWriter struct {
	sink     *commandOutputSink
	pending  []byte
	overflow bool
}

func NewCommandOutputWriter(ctx context.Context) *CommandOutputWriter {
	sink, _ := ctx.Value(commandOutputKey{}).(*commandOutputSink)
	return &CommandOutputWriter{sink: sink}
}

// ReportCommandOutput also accepts adapter diagnostics such as download URLs
// and HTTP errors, using the same bounded, redacted presentation boundary.
func ReportCommandOutput(ctx context.Context, message string) {
	w := NewCommandOutputWriter(ctx)
	_, _ = w.Write([]byte(message))
	_ = w.Close()
}

// Keep complete lines before redaction: a token may span several pipe writes.
// Oversized lines are omitted rather than emitting an unredacted fragment.
func (w *CommandOutputWriter) Write(data []byte) (int, error) {
	if w.sink == nil {
		return len(data), nil
	}
	for _, b := range data {
		if b == '\n' || b == '\r' {
			w.flush()
			continue
		}
		if len(w.pending) < 16*1024 {
			w.pending = append(w.pending, b)
		} else {
			w.overflow = true
		}
	}
	return len(data), nil
}
func (w *CommandOutputWriter) Close() error { w.flush(); return nil }

var commandANSI = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
var commandURLCredentials = regexp.MustCompile(`(?i)(https?://)[^/\s@]+@`)

func (w *CommandOutputWriter) flush() {
	if w.sink == nil {
		return
	}
	line := string(w.pending)
	if w.overflow {
		line = "[Long output line omitted]"
	}
	w.pending = w.pending[:0]
	w.overflow = false
	line = commandANSI.ReplaceAllString(line, "")
	line = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' {
			return -1
		}
		return r
	}, line)
	line = supervisor.Redact(commandURLCredentials.ReplaceAllString(line, "${1}[REDACTED]@"))
	if strings.TrimSpace(line) == "" {
		return
	}
	w.sink.mu.Lock()
	defer w.sink.mu.Unlock()
	if w.sink.publish != nil {
		w.sink.publish(line)
	}
}
