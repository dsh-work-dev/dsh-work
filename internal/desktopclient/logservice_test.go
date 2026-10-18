package desktopclient

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type capturedRecords struct {
	mu      sync.Mutex
	records []slog.Record
}

type captureHandler struct {
	captured *capturedRecords
	attrs    []slog.Attr
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, record slog.Record) error {
	record.AddAttrs(h.attrs...)
	h.captured.mu.Lock()
	h.captured.records = append(h.captured.records, record.Clone())
	h.captured.mu.Unlock()
	return nil
}

func (h *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{captured: h.captured, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...)}
}

func (h *captureHandler) WithGroup(string) slog.Handler { return h }

func captureDefaultLogs(t *testing.T) *capturedRecords {
	t.Helper()
	previous := slog.Default()
	captured := &capturedRecords{}
	slog.SetDefault(slog.New(&captureHandler{captured: captured}))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return captured
}

func (c *capturedRecords) snapshot() []slog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]slog.Record(nil), c.records...)
}

func recordAttributes(record slog.Record) map[string]slog.Value {
	attrs := make(map[string]slog.Value, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value
		return true
	})
	return attrs
}

func workspaceLogContext() context.Context {
	return context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
}

func TestLogServiceRequiresWorkspaceWindow(t *testing.T) {
	captured := captureDefaultLogs(t)
	if err := (LogService{}).Write(context.Background(), []LogEntry{{Level: "error", Message: "should not be logged"}}); err == nil {
		t.Fatal("Write() succeeded without a trusted workspace window")
	}
	if records := captured.snapshot(); len(records) != 0 {
		t.Fatalf("records = %v, want none", records)
	}
}

func TestLogServiceMapsLevelsModulesAndCodes(t *testing.T) {
	captured := captureDefaultLogs(t)
	entries := []LogEntry{
		{Level: "debug", Module: "frontend", Message: "debug event"},
		{Level: "info", Module: "shell", Message: "info event"},
		{Level: "warn", Module: "settings", Message: "warn event"},
		{Level: "error", Module: "plugin", Message: "error event", Code: string(lifecycle.ErrorUIScriptError)},
		{Level: "unknown", Module: "other", Message: "unknown event", Code: "PLUGIN_INCOMPATIBLE"},
		{Level: "info", Module: "frontend", Message: "  "},
	}
	if err := (LogService{}).Write(workspaceLogContext(), entries); err != nil {
		t.Fatal(err)
	}

	records := captured.snapshot()
	wantLevels := []slog.Level{slog.LevelDebug, slog.LevelInfo, slog.LevelWarn, slog.LevelError, slog.LevelInfo, slog.LevelInfo}
	wantModules := []string{"frontend", "shell", "settings", "plugin", "frontend", "frontend"}
	wantMessages := []string{"debug event", "info event", "warn event", "error event", "unknown event", "frontend event"}
	if len(records) != len(wantLevels) {
		t.Fatalf("record count = %d, want %d", len(records), len(wantLevels))
	}
	for i, record := range records {
		attrs := recordAttributes(record)
		if record.Level != wantLevels[i] || record.Message != wantMessages[i] || attrs["mod"].String() != wantModules[i] {
			t.Errorf("record[%d] = level %s, message %q, mod %q", i, record.Level, record.Message, attrs["mod"].String())
		}
		if i == 3 && attrs["code"].String() != string(lifecycle.ErrorUIScriptError) {
			t.Errorf("accepted code = %q", attrs["code"].String())
		}
		if i == 4 {
			if _, ok := attrs["code"]; ok {
				t.Errorf("unknown code was retained: %q", attrs["code"].String())
			}
		}
	}
}

func TestLogServiceBoundsEntriesAndText(t *testing.T) {
	captured := captureDefaultLogs(t)
	entries := make([]LogEntry, 60)
	for i := range entries {
		entries[i] = LogEntry{Level: "info", Module: "frontend", Message: "frontend event"}
	}
	if err := (LogService{}).Write(workspaceLogContext(), entries); err != nil {
		t.Fatal(err)
	}
	records := captured.snapshot()
	if len(records) != maxFrontendLogEntries+1 {
		t.Fatalf("record count = %d, want %d", len(records), maxFrontendLogEntries+1)
	}
	dropped := records[len(records)-1]
	if dropped.Message != "frontend log entries dropped" || recordAttributes(dropped)["count"].Int64() != 10 {
		t.Fatalf("dropped record = %s %#v", dropped.Message, recordAttributes(dropped))
	}

	message := strings.Repeat("你", 200)
	if err := (LogService{}).Write(workspaceLogContext(), []LogEntry{{Level: "info", Module: "frontend", Message: message}}); err != nil {
		t.Fatal(err)
	}
	records = captured.snapshot()
	got := records[len(records)-1].Message
	if !utf8.ValidString(got) || len(got) > maxFrontendLogMessage {
		t.Fatalf("bounded message has %d bytes and valid UTF-8 = %v", len(got), utf8.ValidString(got))
	}
}
