package logging

import (
	"context"
	"errors"
	"log/slog"

	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/redact"
)

type core struct {
	l      *Logger
	floor  *slog.LevelVar
	attrs  []slog.Attr
	groups []string
}

func (h *core) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.floor.Level()
}

func (h *core) WithAttrs(attrs []slog.Attr) slog.Handler {
	copy := *h
	copy.attrs = append([]slog.Attr(nil), h.attrs...)
	if len(h.groups) == 0 {
		copy.attrs = append(copy.attrs, attrs...)
	} else if len(attrs) > 0 {
		copy.attrs = append(copy.attrs, nestedGroup(h.groups, attrs))
	}
	copy.groups = append([]string(nil), h.groups...)
	return &copy
}

func (h *core) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	copy := *h
	copy.attrs = append([]slog.Attr(nil), h.attrs...)
	copy.groups = append(append([]string(nil), h.groups...), name)
	return &copy
}

func (h *core) Handle(ctx context.Context, record slog.Record) error {
	rec := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	rec.AddAttrs(slog.String("proc", string(h.l.proc)), slog.String("run", h.l.run))
	rec.AddAttrs(h.attrs...)
	attrs := make([]slog.Attr, 0, record.NumAttrs())
	record.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)
		return true
	})
	if len(h.groups) > 0 && len(attrs) > 0 {
		attrs = []slog.Attr{nestedGroup(h.groups, attrs)}
	}
	rec.AddAttrs(attrs...)
	return h.l.dispatch(ctx, rec)
}

func nestedGroup(groups []string, attrs []slog.Attr) slog.Attr {
	for i := len(groups) - 1; i >= 0; i-- {
		attrs = []slog.Attr{slog.GroupAttrs(groups[i], attrs...)}
	}
	return attrs[0]
}

func redactAttr(_ []string, attr slog.Attr) slog.Attr {
	switch attr.Value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, redact.Secrets(attr.Value.String()))
	case slog.KindAny:
		if err, ok := attr.Value.Any().(error); ok {
			return slog.String(attr.Key, redact.Secrets(err.Error()))
		}
	}
	return attr
}

// ErrAttrs returns "err" and, when known, "code" attributes for err.
// A lifecycle.Failure supplies its own code; otherwise fallback is used when non-empty.
func ErrAttrs(err error, fallback lifecycle.ErrorCode) []any {
	if err == nil {
		return nil
	}
	attrs := []any{"err", err.Error()}
	var pointer *lifecycle.Failure
	if errors.As(err, &pointer) && pointer != nil {
		return append(attrs, "code", string(pointer.Code))
	}
	var failure lifecycle.Failure
	if errors.As(err, &failure) {
		return append(attrs, "code", string(failure.Code))
	}
	if fallback != "" {
		return append(attrs, "code", string(fallback))
	}
	return attrs
}
