package desktopclient

import (
	"context"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/logging"
)

// LogEntry is one record from the trusted workbench frontend.
type LogEntry struct {
	Level   string `json:"level"`
	Module  string `json:"module"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
	Code    string `json:"code,omitempty"`
}

// LogService writes the workbench frontend's log entries into this process's log.
type LogService struct{}

const (
	maxFrontendLogEntries = 50
	maxFrontendLogMessage = 500
	maxFrontendLogDetail  = 4000
)

// Write records entries from the trusted workbench window. Extra entries beyond
// maxFrontendLogEntries are dropped and counted in one warning.
func (LogService) Write(ctx context.Context, entries []LogEntry) error {
	if _, err := trustedWindow(ctx); err != nil {
		return err
	}

	count := len(entries)
	if count > maxFrontendLogEntries {
		count = maxFrontendLogEntries
	}
	for _, entry := range entries[:count] {
		level := slog.LevelInfo
		switch entry.Level {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}

		module := entry.Module
		switch module {
		case logging.ModFrontend, logging.ModShell, logging.ModSettings, logging.ModPlugin:
		default:
			module = logging.ModFrontend
		}

		message := strings.TrimSpace(entry.Message)
		if message == "" {
			message = "frontend event"
		}
		message = truncateUTF8(message, maxFrontendLogMessage)
		detail := truncateUTF8(entry.Detail, maxFrontendLogDetail)

		var attrs []any
		if detail != "" {
			attrs = append(attrs, "detail", detail)
		}
		if entry.Code == string(lifecycle.ErrorUIScriptError) {
			attrs = append(attrs, "code", entry.Code)
		}
		logging.Module(module).Log(ctx, level, message, attrs...)
	}
	if dropped := len(entries) - count; dropped > 0 {
		logging.Module(logging.ModFrontend).Warn("frontend log entries dropped", "count", dropped)
	}
	return nil
}

func truncateUTF8(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
