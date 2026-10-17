package logging

import (
	"errors"
	"log/slog"
	"os"
	"runtime"
	"sync/atomic"

	"github.com/local/dsh-work/internal/version"
)

var current atomic.Pointer[Logger]

// Init configures structured logging before this process acquires its singleton boundary.
func Init(proc Process, dir string) {
	logger := newLogger(proc, dir, os.Getenv, os.Stderr, version.IsDevelopment())
	current.Store(logger)
	slog.SetDefault(slog.New(&core{l: logger, floor: &logger.level}))
	logger.module(ModApp).Info("process started", "pid", os.Getpid(), "version", version.String(), "os", runtime.GOOS, "arch", runtime.GOARCH)
}

// Attach begins writing this process's records to its log files.
func Attach() error {
	if logger := current.Load(); logger != nil {
		return logger.attach()
	}
	return errors.New("logging is not initialized")
}

// Module returns a logger tagged with the owning module name.
func Module(name string) *slog.Logger {
	if logger := current.Load(); logger != nil {
		return logger.module(name)
	}
	return slog.Default().With("mod", name)
}

// WailsLogger returns the logger Wails uses for its own system messages.
func WailsLogger() *slog.Logger {
	if logger := current.Load(); logger != nil {
		return logger.wailsLogger()
	}
	return slog.Default().With("mod", ModWails)
}

// SetVerbose changes the process log level unless a development override is set.
func SetVerbose(on bool) {
	if logger := current.Load(); logger != nil {
		logger.setVerbose(on)
	}
}

// WorkerOutput records one already-bounded and redacted Worker output line.
func WorkerOutput(generation, stream, text string) {
	if logger := current.Load(); logger != nil {
		logger.workerOutput(generation, stream, text)
	}
}

// Fatal records a fatal application error, closes log files and exits with code 1.
func Fatal(logger *slog.Logger, msg string, args ...any) {
	if instance := current.Load(); instance != nil {
		instance.fatal(logger, msg, args...)
		return
	}
	if logger != nil {
		logger.Error(msg, args...)
	}
	os.Exit(1)
}

// Close writes the final process record and closes all rolling log files.
func Close() {
	if logger := current.Load(); logger != nil {
		logger.close()
	}
}
