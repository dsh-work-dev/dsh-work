package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunStableFromConfig(t *testing.T) {
	stdout, stderr, code := runInDir(t, []string{"--channel", "stable", "--config", repositoryConfigPath(t)}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	// The configured base is the single source of truth for a local build, so
	// this assertion tracks build/config.yml rather than a hard-coded version.
	if got := strings.TrimSpace(stdout); got != configuredBase(t) {
		t.Errorf("version = %q, want the configured base %q", got, configuredBase(t))
	}
}

// configuredBase reads info.version from the repository build/config.yml. The
// indented match skips the top-level Wails config schema version.
func configuredBase(t *testing.T) string {
	t.Helper()
	content, err := os.ReadFile(repositoryConfigPath(t))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	line := versionLine.FindSubmatch(content)
	if line == nil {
		t.Fatal("build/config.yml must contain info.version")
	}
	return string(line[1])
}

func TestRunBeta(t *testing.T) {
	stdout, stderr, code := runInDir(t, []string{"--channel", "beta", "--version", "v0.3.0-beta.2", "--base", "0.3.0"}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "0.3.0-beta.2" {
		t.Errorf("version = %q, want 0.3.0-beta.2", got)
	}
}

func TestRunNightlyFields(t *testing.T) {
	args := []string{"--channel", "nightly", "--base", "0.4.0", "--date", "2026-10-13", "--run-id", "123456789", "--source-sha", "abcdef"}
	stdout, stderr, code := runInDir(t, args, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "0.4.0-nightly.20261013.123456789" {
		t.Errorf("version = %q, want 0.4.0-nightly.20261013.123456789", got)
	}

	for field, want := range map[string]string{
		"version":     "0.4.0-nightly.20261013.123456789",
		"channel":     "nightly",
		"base":        "0.4.0",
		"date":        "20261013",
		"runId":       "123456789",
		"sourceSha":   "abcdef",
		"fileVersion": "0.4.0.0",
	} {
		stdout, stderr, code = runInDir(t, append(append([]string{}, args...), "--field", field), "")
		if code != exitOK {
			t.Fatalf("--field %s exit code = %d, stderr = %s", field, code, stderr)
		}
		if got := strings.TrimSpace(stdout); got != want {
			t.Errorf("--field %s = %q, want %q", field, got, want)
		}
	}
}

func TestRunNightlyJSON(t *testing.T) {
	stdout, stderr, code := runInDir(t, []string{
		"--channel", "nightly", "--base", "0.4.0", "--date", "2026-10-13", "--run-id", "42", "--source-sha", "deadbeef", "--json",
	}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	var payload struct {
		Version     string `json:"version"`
		Channel     string `json:"channel"`
		Base        string `json:"base"`
		SourceSHA   string `json:"sourceSha"`
		Date        string `json:"date"`
		RunID       string `json:"runId"`
		FileVersion string `json:"fileVersion"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("stdout is not JSON: %v (%s)", err, stdout)
	}
	if payload.Version != "0.4.0-nightly.20261013.42" || payload.Date != "20261013" || payload.RunID != "42" {
		t.Errorf("unexpected payload: %+v", payload)
	}
	if payload.SourceSHA != "deadbeef" || payload.FileVersion != "0.4.0.0" {
		t.Errorf("unexpected payload: %+v", payload)
	}
}

func TestRunRejectsNightlyWithoutRunID(t *testing.T) {
	_, stderr, code := runInDir(t, []string{"--channel", "nightly", "--base", "0.4.0", "--date", "2026-10-13"}, "")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr = %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "run id") {
		t.Errorf("stderr = %q, want the missing run id to be named", stderr)
	}
}

func TestRunMovesNightlyPastPublishedStable(t *testing.T) {
	stdout, stderr, code := runInDir(t, []string{
		"--channel", "nightly", "--base", "0.3.0", "--date", "2026-10-13", "--run-id", "1", "--last-stable", "0.3.0", "--field", "version",
	}, "")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (stderr = %s)", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "0.3.1-nightly.20261013.1" {
		t.Errorf("version = %q, want the patch after the published stable base", got)
	}
}

func TestRunRejectsOverflow(t *testing.T) {
	_, stderr, code := runInDir(t, []string{"--channel", "stable", "--base", "0.3.70000"}, "")
	if code != exitUsage {
		t.Fatalf("exit code = %d, want %d (stderr = %s)", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "Windows file version limit") {
		t.Errorf("stderr = %q, want the overflow to be named", stderr)
	}
}

func TestRunRejectsUnknownArgument(t *testing.T) {
	if _, _, code := runInDir(t, []string{"--nope"}, ""); code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestRunMissingConfig(t *testing.T) {
	_, stderr, code := runInDir(t, []string{"--channel", "stable"}, "")
	if code != exitError {
		t.Fatalf("exit code = %d, want %d (stderr = %s)", code, exitError, stderr)
	}
	if !strings.Contains(stderr, filepath.Join("build", "config.yml")) {
		t.Errorf("stderr = %q, want the config path to be named", stderr)
	}
}

// TestRunFindsConfigFromNestedDirectory covers the go test working directory:
// the config is not in the current directory, only in an ancestor.
func TestRunFindsConfigFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "build"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "build", "config.yml"), []byte("info:\n  version: \"0.7.0\"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	nested := filepath.Join(root, "internal", "release", "cmd", "release-version")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	stdout, stderr, code := runInWorkingDirectory(t, nested, []string{"--channel", "stable"})
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	if got := strings.TrimSpace(stdout); got != "0.7.0" {
		t.Errorf("version = %q, want 0.7.0", got)
	}
}

// runInDir runs the command in a scratch directory, so the working directory
// never picks up the real repository configuration. When configVersion is not
// empty the scratch directory also becomes a checkout root by gaining its own
// build/config.yml, which is what the default config lookup finds.
func runInDir(t *testing.T, args []string, configVersion string) (stdout, stderr string, code int) {
	t.Helper()
	dir := t.TempDir()
	if configVersion != "" {
		if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		content := "info:\n  version: \"" + configVersion + "\"\n"
		if err := os.WriteFile(filepath.Join(dir, "build", "config.yml"), []byte(content), 0o644); err != nil {
			t.Fatalf("write config: %v", err)
		}
	}
	return runInWorkingDirectory(t, dir, args)
}

func runInWorkingDirectory(t *testing.T, dir string, args []string) (stdout, stderr string, code int) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(previous); err != nil {
			t.Fatalf("chdir back: %v", err)
		}
	}()

	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return out.String(), errOut.String(), code
}

// repositoryConfigPath is the repository build/config.yml, located from this
// test file instead of the process working directory. Tests that assert the
// real configured base pass it explicitly with --config.
func repositoryConfigPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate this test file")
	}
	root := filepath.Dir(file)
	for {
		candidate := filepath.Join(root, "build", "config.yml")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(root)
		if parent == root {
			t.Fatal("cannot find build/config.yml above this test file")
		}
		root = parent
	}
}

func TestRunNightlyDefaultsToTodayUTC(t *testing.T) {
	stdout, stderr, code := runInDir(t, []string{"--channel", "nightly", "--base", "0.4.0", "--run-id", "5"}, "")
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr)
	}
	version := strings.TrimSpace(stdout)
	if !strings.HasPrefix(version, "0.4.0-nightly.") || !strings.HasSuffix(version, ".5") {
		t.Fatalf("version = %q, want 0.4.0-nightly.<date>.5", version)
	}
	if !strings.Contains(version, ".") || len(strings.Split(version, ".")) != 5 {
		t.Fatalf("version = %q, want the nightly.YYYYMMDD.RUNID shape", version)
	}
}
