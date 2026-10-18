package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/lifecycle"
)

func testLogger(t *testing.T, proc Process, directory string, env map[string]string, stderr *bytes.Buffer) *Logger {
	t.Helper()
	if directory == "" {
		directory = t.TempDir()
	}
	logger := newLogger(proc, directory, func(key string) string { return env[key] }, stderr, false)
	t.Cleanup(func() {
		logger.close()
		_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	})
	return logger
}

func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	var records []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid JSON record %q: %v", scanner.Text(), err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestAttachWritesJSONLinesWithProcessFields(t *testing.T) {
	logger := testLogger(t, ProcessDaemon, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.module(ModApp).Info("hello", "k", "v")
	logger.close()

	for _, record := range readJSONLines(t, filepath.Join(logger.dir, daemonFile.name)) {
		if record["msg"] != "hello" {
			continue
		}
		if record["level"] != "INFO" || record["proc"] != "daemon" || record["mod"] != "app" || record["k"] != "v" {
			t.Fatalf("hello record = %#v", record)
		}
		if run, ok := record["run"].(string); !ok || !regexp.MustCompile(`^[0-9a-f]{8}$`).MatchString(run) {
			t.Fatalf("run = %#v, want eight lowercase hex digits", record["run"])
		}
		return
	}
	t.Fatal("hello record not found")
}

func TestRecordsBeforeAttachAreFlushedInOrder(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	logger.module(ModApp).Info("a")
	logger.module(ModApp).Info("b")
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.module(ModApp).Info("c")
	logger.close()

	var messages []string
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, uiFile.name)) {
		if message, ok := record["msg"].(string); ok && message != "process exiting" {
			messages = append(messages, message)
		}
	}
	if got := strings.Join(messages, ","); got != "a,b,c" {
		t.Fatalf("messages = %q, want a,b,c", got)
	}
}

func TestPendingRecordsAreBounded(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	for i := 0; i < pendingLimit+5; i++ {
		logger.module(ModApp).Info(fmt.Sprintf("pending-%d", i))
	}
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.close()

	var messages []string
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, uiFile.name)) {
		if message, ok := record["msg"].(string); ok && strings.HasPrefix(message, "pending-") {
			messages = append(messages, message)
		}
	}
	if len(messages) != pendingLimit || messages[0] != "pending-5" || messages[len(messages)-1] != "pending-204" {
		t.Fatalf("pending message range = %d records, first=%q last=%q", len(messages), messages[0], messages[len(messages)-1])
	}
}

func TestFatalBeforeAttachWritesEarlyLog(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	var exitCode int
	logger.exit = func(code int) { exitCode = code }
	logger.fatal(logger.module(ModApp), "boom")

	early := filepath.Join(logger.dir, "ui-early.log")
	data, err := os.ReadFile(early)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"msg":"boom"`)) || exitCode != 1 {
		t.Fatalf("early log = %s, exit code = %d", data, exitCode)
	}
	if _, err := os.Stat(filepath.Join(logger.dir, uiFile.name)); !os.IsNotExist(err) {
		t.Fatalf("ui.log exists before attach: %v", err)
	}
}

func TestSecretsNeverReachTheFile(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.module(ModApp).Error("token=abc", "detail", "password: hunter2", "err", fmt.Errorf("cookie=c1"))
	logger.close()

	data, err := os.ReadFile(filepath.Join(logger.dir, uiFile.name))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"abc", "hunter2", "c1"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("log leaked %q: %s", secret, data)
		}
	}
	if !bytes.Contains(data, []byte("[REDACTED]")) {
		t.Fatalf("log does not show redaction: %s", data)
	}
}

func TestVerboseSwitchesLevelLive(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.module(ModApp).Debug("before")
	logger.setVerbose(true)
	logger.module(ModApp).Debug("enabled")
	logger.setVerbose(false)
	logger.module(ModApp).Debug("after")
	logger.close()

	var debugMessages []string
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, uiFile.name)) {
		if record["level"] == "DEBUG" {
			debugMessages = append(debugMessages, record["msg"].(string))
		}
	}
	if strings.Join(debugMessages, ",") != "enabled" {
		t.Fatalf("debug messages = %v, want [enabled]", debugMessages)
	}
}

func TestLogLevelEnvironmentOverridesVerbose(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", map[string]string{"DSH_WORK_LOG_LEVEL": "warn"}, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.setVerbose(true)
	logger.module(ModApp).Info("info")
	logger.module(ModApp).Warn("warn")
	logger.close()

	var messages []string
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, uiFile.name)) {
		messages = append(messages, record["msg"].(string))
	}
	if strings.Contains(strings.Join(messages, ","), "info") || !strings.Contains(strings.Join(messages, ","), "warn") {
		t.Fatalf("messages = %v", messages)
	}
}

func TestDesktopReportDefaultsToDebugAndStderr(t *testing.T) {
	var stderr bytes.Buffer
	logger := testLogger(t, ProcessUI, "", map[string]string{"DSH_WORK_DESKTOP_REPORT": "1"}, &stderr)
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.module(ModApp).Debug("probe detail")
	logger.close()
	if !bytes.Contains(stderr.Bytes(), []byte("probe detail")) {
		t.Fatalf("stderr = %q", stderr.String())
	}
	data, err := os.ReadFile(filepath.Join(logger.dir, uiFile.name))
	if err != nil || !bytes.Contains(data, []byte(`"msg":"probe detail"`)) {
		t.Fatalf("file = %s, error = %v", data, err)
	}
}

func TestWailsLoggerFloor(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	wails := logger.wailsLogger()
	wails.Info("hidden info")
	wails.Warn("visible warning")
	logger.setVerbose(true)
	wails.Info("verbose info")
	logger.close()

	var wailsMessages []string
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, uiFile.name)) {
		if record["mod"] == ModWails {
			wailsMessages = append(wailsMessages, record["msg"].(string))
		}
	}
	if strings.Join(wailsMessages, ",") != "visible warning,verbose info" {
		t.Fatalf("Wails messages = %v", wailsMessages)
	}
}

func TestEachProcessWritesItsOwnFile(t *testing.T) {
	dir := t.TempDir()
	daemon := testLogger(t, ProcessDaemon, dir, nil, &bytes.Buffer{})
	ui := testLogger(t, ProcessUI, dir, nil, &bytes.Buffer{})
	if err := daemon.attach(); err != nil {
		t.Fatal(err)
	}
	if err := ui.attach(); err != nil {
		t.Fatal(err)
	}
	daemon.module(ModApp).Info("daemon only")
	ui.module(ModApp).Info("ui only")
	daemon.close()
	ui.close()

	for _, check := range []struct {
		path, want, other string
	}{
		{filepath.Join(dir, daemonFile.name), "daemon only", "ui only"},
		{filepath.Join(dir, uiFile.name), "ui only", "daemon only"},
	} {
		data, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(check.want)) || bytes.Contains(data, []byte(check.other)) {
			t.Fatalf("%s contains wrong process records: %s", check.path, data)
		}
	}
}

func TestWorkerOutputGoesToWorkerLog(t *testing.T) {
	logger := testLogger(t, ProcessDaemon, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.workerOutput("g1", "stdout", "out line")
	logger.workerOutput("g1", "stderr", `Full diagnostics: C:\x\startup.log`)
	logger.close()

	workerRecords := readJSONLines(t, filepath.Join(logger.dir, workerFile.name))
	if len(workerRecords) != 2 || workerRecords[0]["level"] != "INFO" || workerRecords[1]["level"] != "WARN" {
		t.Fatalf("worker records = %#v", workerRecords)
	}
	for _, record := range workerRecords {
		if record["gen"] != "g1" || record["stream"] == nil {
			t.Fatalf("worker record missing context: %#v", record)
		}
	}
	var startup *map[string]any
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, daemonFile.name)) {
		if record["msg"] == "dsh startup diagnostics written" {
			copy := record
			startup = &copy
		}
	}
	if startup == nil || (*startup)["dshlog"] != `C:\x\startup.log` {
		t.Fatalf("startup log record = %#v", startup)
	}
}

func TestUIProcessHasNoWorkerLog(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.workerOutput("g1", "stdout", "ignored")
	logger.close()
	if _, err := os.Stat(filepath.Join(logger.dir, workerFile.name)); !os.IsNotExist(err) {
		t.Fatalf("UI process created worker.log: %v", err)
	}
}

func TestCrashFileIsRotatedAndReported(t *testing.T) {
	logger := testLogger(t, ProcessDaemon, "", nil, &bytes.Buffer{})
	crashDir := filepath.Join(logger.dir, "crash")
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crashDir, "daemon.current"), []byte("panic: boom"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.close()

	matches, err := filepath.Glob(filepath.Join(crashDir, "daemon-*.txt"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("crash files = %v, error = %v", matches, err)
	}
	if data, err := os.ReadFile(matches[0]); err != nil || string(data) != "panic: boom" {
		t.Fatalf("rotated crash = %q, error = %v", data, err)
	}
	if info, err := os.Stat(filepath.Join(crashDir, "daemon.current")); err != nil || info.Size() != 0 {
		t.Fatalf("current crash file = %#v, error = %v", info, err)
	}
	base := filepath.Base(matches[0])
	var reported bool
	for _, record := range readJSONLines(t, filepath.Join(logger.dir, daemonFile.name)) {
		if record["code"] == string(lifecycle.ErrorProcessCrashed) && record["prev"] == base {
			reported = true
		}
	}
	if !reported {
		t.Fatal("daemon.log did not report the previous crash")
	}
}

func TestCrashFilesArePruned(t *testing.T) {
	logger := testLogger(t, ProcessDaemon, "", nil, &bytes.Buffer{})
	crashDir := filepath.Join(logger.dir, "crash")
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i := 0; i < 12; i++ {
		path := filepath.Join(crashDir, fmt.Sprintf("daemon-recent-%02d.txt", i))
		if err := os.WriteFile(path, []byte("crash"), 0o600); err != nil {
			t.Fatal(err)
		}
		stamp := now.Add(-time.Duration(i+1) * time.Hour)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	old := filepath.Join(crashDir, "daemon-old.txt")
	if err := os.WriteFile(old, []byte("crash"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldStamp := now.Add(-40 * 24 * time.Hour)
	if err := os.Chtimes(old, oldStamp, oldStamp); err != nil {
		t.Fatal(err)
	}
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	logger.close()

	matches, err := filepath.Glob(filepath.Join(crashDir, "daemon-*.txt"))
	if err != nil || len(matches) != crashKeep {
		t.Fatalf("crash files = %d (%v), error = %v", len(matches), matches, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("old crash file remains: %v", err)
	}
}

func TestRotationKeepsConfiguredBackups(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	logger.specs[ProcessUI] = fileSpec{"ui.log", 1, 1, 14}
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	message := strings.Repeat("x", 900)
	for i := 0; i < 3500; i++ {
		logger.module(ModApp).Info(message)
	}
	logger.close()

	deadline := time.Now().Add(5 * time.Second)
	var backups []string
	for time.Now().Before(deadline) {
		backups, _ = filepath.Glob(filepath.Join(logger.dir, "ui-*"))
		if len(backups) == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(backups) != 1 {
		t.Fatalf("ui backups = %v, want exactly one", backups)
	}
}

func TestErrAttrs(t *testing.T) {
	for _, check := range []struct {
		name     string
		err      error
		fallback lifecycle.ErrorCode
		wantCode string
		wantNil  bool
	}{
		{name: "value failure", err: lifecycle.Failure{Code: "VALUE"}, wantCode: "VALUE"},
		{name: "pointer failure", err: &lifecycle.Failure{Code: "POINTER"}, wantCode: "POINTER"},
		{name: "fallback", err: errors.New("plain"), fallback: "FALLBACK", wantCode: "FALLBACK"},
		{name: "plain", err: errors.New("plain")},
		{name: "nil", wantNil: true},
	} {
		t.Run(check.name, func(t *testing.T) {
			attrs := ErrAttrs(check.err, check.fallback)
			if check.wantNil {
				if attrs != nil {
					t.Fatalf("ErrAttrs() = %#v, want nil", attrs)
				}
				return
			}
			values := map[string]any{}
			for i := 0; i < len(attrs); i += 2 {
				values[attrs[i].(string)] = attrs[i+1]
			}
			code, _ := values["code"].(string)
			if values["err"] != check.err.Error() || code != check.wantCode {
				t.Fatalf("ErrAttrs() = %#v", attrs)
			}
		})
	}
}

func TestWailsErrorHandlerDowngradesCancelledCalls(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	previous := current.Swap(logger)
	t.Cleanup(func() { current.Store(previous) })
	handler := WailsErrorHandler()

	handler(errors.New("Binding call failed: Bound method returned an error: CANCELLED: manager operation was cancelled"))
	logger.mu.Lock()
	cancelledRecords := len(logger.pending)
	logger.mu.Unlock()
	if cancelledRecords != 0 {
		t.Fatalf("cancelled call produced %d records, want none at the default level", cancelledRecords)
	}

	handler(errors.New("Binding call failed: Bound method returned an error: PLUGIN_INCOMPATIBLE: plugin cannot load"))
	logger.mu.Lock()
	records := append([]slog.Record(nil), logger.pending...)
	logger.mu.Unlock()
	if len(records) != 1 || records[0].Level != slog.LevelError {
		t.Fatalf("non-cancelled error records = %#v, want one ERROR", records)
	}
}

func TestHandlerGroupsAndAttributes(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	if err := logger.attach(); err != nil {
		t.Fatal(err)
	}
	slog.New(&core{l: logger, floor: &logger.level}).WithGroup("outer").WithGroup("inner").With("key", "value").Info("grouped")
	logger.close()
	records := readJSONLines(t, filepath.Join(logger.dir, uiFile.name))
	for _, record := range records {
		if record["msg"] != "grouped" {
			continue
		}
		outer, ok := record["outer"].(map[string]any)
		if !ok {
			t.Fatalf("outer group = %#v", record["outer"])
		}
		inner, ok := outer["inner"].(map[string]any)
		if !ok || inner["key"] != "value" {
			t.Fatalf("inner group = %#v", outer["inner"])
		}
		return
	}
	t.Fatal("grouped record not found")
}

func TestDispatchReturnsFileError(t *testing.T) {
	logger := testLogger(t, ProcessUI, "", nil, &bytes.Buffer{})
	logger.file = failingHandler{}
	if err := logger.module(ModApp).Handler().Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "failure", 0)); err == nil {
		t.Fatal("Handle() returned nil")
	}
}

type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool  { return true }
func (failingHandler) Handle(context.Context, slog.Record) error { return errors.New("write failed") }
func (f failingHandler) WithAttrs([]slog.Attr) slog.Handler      { return f }
func (f failingHandler) WithGroup(string) slog.Handler           { return f }
