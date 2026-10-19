package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Process string

const (
	ProcessDaemon Process = "daemon"
	ProcessUI     Process = "ui"
)

const (
	ModApp       = "app"
	ModIPC       = "ipc"
	ModLifecycle = "lifecycle"
	ModWorker    = "worker"
	ModRuntime   = "runtime"
	ModProfile   = "profile"
	ModPlugin    = "plugin"
	ModUpdate    = "update"
	ModStorage   = "storage"
	ModSettings  = "settings"
	ModAccount   = "account"
	ModNotify    = "notify"
	ModPet       = "pet"
	ModShell     = "shell"
	ModFrontend  = "frontend"
	ModWails     = "wails"
)

type Logger struct {
	proc       Process
	dir        string
	run        string
	level      slog.LevelVar
	wailsLevel slog.LevelVar
	baseLevel  slog.Level
	override   bool
	stderr     slog.Handler
	specs      map[Process]fileSpec
	worker     fileSpec
	exit       func(int)
	now        func() time.Time

	mu        sync.Mutex
	attachMu  sync.Mutex
	file      slog.Handler
	pending   []slog.Record
	workerLg  *slog.Logger
	closers   []io.Closer
	closeOnce sync.Once
}

var fallbackRun atomic.Uint32

func newLogger(proc Process, dir string, env func(string) string, stderr io.Writer, development bool) *Logger {
	var random [4]byte
	if _, err := rand.Read(random[:]); err != nil {
		binary := uint32(time.Now().UnixNano()) ^ fallbackRun.Add(1)
		random[0], random[1], random[2], random[3] = byte(binary>>24), byte(binary>>16), byte(binary>>8), byte(binary)
	}
	logger := &Logger{
		proc:   proc,
		dir:    dir,
		run:    hex.EncodeToString(random[:]),
		specs:  map[Process]fileSpec{ProcessDaemon: daemonFile, ProcessUI: uiFile},
		worker: workerFile,
		exit:   os.Exit,
		now:    time.Now,
	}
	logger.baseLevel = slog.LevelInfo
	if env("DSH_WORK_DESKTOP_REPORT") != "" {
		logger.baseLevel = slog.LevelDebug
	}
	if override, ok := parseLevel(env("DSH_WORK_LOG_LEVEL")); ok {
		logger.baseLevel = override
		logger.override = true
	}
	logger.level.Set(logger.baseLevel)
	logger.syncWailsLevel()
	if development || env("DSH_WORK_LOG_STDERR") == "1" || env("DSH_WORK_DESKTOP_REPORT") != "" {
		logger.stderr = slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: redactAttr})
	}
	return logger
}

func parseLevel(value string) (slog.Level, bool) {
	switch value {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return 0, false
	}
}

func (l *Logger) syncWailsLevel() {
	level := l.level.Level()
	if level <= slog.LevelDebug {
		l.wailsLevel.Set(slog.LevelInfo)
		return
	}
	l.wailsLevel.Set(max(level, slog.LevelWarn))
}

func (l *Logger) setVerbose(on bool) {
	if l.override {
		return
	}
	if on {
		l.level.Set(slog.LevelDebug)
	} else {
		l.level.Set(l.baseLevel)
	}
	l.syncWailsLevel()
}

func (l *Logger) module(name string) *slog.Logger {
	return slog.New(&core{l: l, floor: &l.level}).With("mod", name)
}

func (l *Logger) wailsLogger() *slog.Logger {
	return slog.New(&core{l: l, floor: &l.wailsLevel}).With("mod", ModWails)
}

func (l *Logger) attach() error {
	if l.dir == "" {
		return errors.New("log directory is unknown")
	}
	l.attachMu.Lock()
	defer l.attachMu.Unlock()
	l.mu.Lock()
	alreadyAttached := l.file != nil
	l.mu.Unlock()
	if alreadyAttached {
		return errors.New("logging is already attached")
	}
	if err := os.MkdirAll(filepath.Join(l.dir, CrashDirName), 0o700); err != nil {
		return err
	}
	prev := l.rotateCrash()

	var crashErr error
	crashFile, err := os.OpenFile(filepath.Join(l.dir, CrashDirName, string(l.proc)+".current"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		crashErr = err
	} else {
		crashErr = debug.SetCrashOutput(crashFile, debug.CrashOptions{})
		_ = crashFile.Close()
	}

	spec, ok := l.specs[l.proc]
	if !ok {
		return fmt.Errorf("unsupported logging process %q", l.proc)
	}
	mainWriter := newRollingWriter(l.dir, spec)
	mainHandler := jsonHandler(mainWriter)
	closers := []io.Closer{mainWriter}
	var workerLogger *slog.Logger
	if l.proc == ProcessDaemon {
		workerWriter := newRollingWriter(l.dir, l.worker)
		workerLogger = slog.New(jsonHandler(workerWriter))
		closers = append(closers, workerWriter)
	}

	l.mu.Lock()
	if l.file != nil {
		l.mu.Unlock()
		for _, closer := range closers {
			_ = closer.Close()
		}
		return errors.New("logging is already attached")
	}
	l.file = mainHandler
	l.workerLg = workerLogger
	l.closers = append(l.closers, closers...)
	for _, record := range l.pending {
		if err := mainHandler.Handle(context.Background(), record); err != nil {
			l.mu.Unlock()
			return err
		}
	}
	l.pending = nil
	l.mu.Unlock()

	if prev != "" {
		l.module(ModApp).Error("previous run crashed", "code", string(lifecycle.ErrorProcessCrashed), "prev", prev)
	}
	if crashErr != nil {
		l.module(ModApp).Warn("crash output unavailable", ErrAttrs(crashErr, "")...)
	}
	return nil
}

func (l *Logger) workerOutput(generation, stream, text string) {
	if l.workerLg == nil {
		return
	}
	level := slog.LevelWarn
	if stream == "stdout" {
		level = slog.LevelInfo
	}
	l.workerLg.Log(context.Background(), level, text, "gen", generation, "stream", stream)
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, startupLogPrefix) {
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, startupLogPrefix))
		l.module(ModWorker).Warn("dsh startup diagnostics written", "gen", generation, "dshlog", path)
	}
}

func (l *Logger) fatal(logger *slog.Logger, msg string, args ...any) {
	logger.Error(msg, args...)
	l.mu.Lock()
	unattached := l.file == nil
	l.mu.Unlock()
	if unattached {
		l.writeEarly()
	}
	l.close()
	l.exit(1)
}

func (l *Logger) writeEarly() {
	if l.dir == "" {
		return
	}
	_ = os.MkdirAll(l.dir, 0o700)
	path := filepath.Join(l.dir, string(l.proc)+"-early.log")
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if info, err := os.Stat(path); err == nil && info.Size() > earlyLimit {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	handler := jsonHandler(f)
	l.mu.Lock()
	pending := make([]slog.Record, len(l.pending))
	for i := range l.pending {
		pending[i] = l.pending[i].Clone()
	}
	l.pending = nil
	l.mu.Unlock()
	for _, record := range pending {
		_ = handler.Handle(context.Background(), record)
	}
}

func (l *Logger) close() {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		attached := l.file != nil
		if !attached {
			l.pending = nil
		}
		closers := append([]io.Closer(nil), l.closers...)
		l.mu.Unlock()
		if attached {
			l.module(ModApp).Info("process exiting")
		}
		for _, closer := range closers {
			_ = closer.Close()
		}
	})
}

func (l *Logger) dispatch(ctx context.Context, record slog.Record) error {
	l.mu.Lock()
	var fileErr error
	if l.file == nil {
		if len(l.pending) == pendingLimit {
			copy(l.pending, l.pending[1:])
			l.pending = l.pending[:pendingLimit-1]
		}
		l.pending = append(l.pending, record.Clone())
	} else {
		fileErr = l.file.Handle(ctx, record)
	}
	l.mu.Unlock()
	if l.stderr != nil {
		if err := l.stderr.Handle(ctx, record); fileErr == nil {
			fileErr = err
		}
	}
	return fileErr
}

func jsonHandler(writer io.Writer) slog.Handler {
	return slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: redactAttr})
}

func newRollingWriter(dir string, spec fileSpec) *lumberjack.Logger {
	return &lumberjack.Logger{
		Filename:   filepath.Join(dir, spec.name),
		MaxSize:    spec.maxSizeMB,
		MaxBackups: spec.maxBackups,
		MaxAge:     spec.maxAgeDays,
		LocalTime:  true,
		Compress:   true,
	}
}
