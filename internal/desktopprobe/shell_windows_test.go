//go:build windows

package desktopprobe_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/lifecycle"
)

// TestRealShellFrame runs the real Worker probe inside the R-50 shell: one
// frameless window whose shell document frames DSH on its own authority.
func TestRealShellFrame(t *testing.T) {
	if os.Getenv("DSH_WORK_SHELL_TEST") != "1" {
		t.Skip("opt-in native process/WebView test")
	}
	t.Chdir(filepath.Join("..", ".."))
	root, err := filepath.Abs(filepath.Join(".task", "shell-frame", fmt.Sprint(time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(root, "webview.json")
	t.Setenv("DSH_WORK_SHELL", "1")
	t.Setenv("DSH_WORK_DESKTOP_ROOT", root)
	t.Setenv("DSH_WORK_DESKTOP_REPORT", report)
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"version":2,"closeToTray":false,"locale":"zh-CN"}`), 0600); err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("DSH_WORK_TEST_BINARY")
	if binary == "" {
		binary = "bin/dsh-work.exe"
	}
	exe, _ := filepath.Abs(binary)
	logFile, err := os.Create(filepath.Join(root, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	background := exec.Command(exe, "--daemon")
	daemon.Detach(background)
	background.Stdout, background.Stderr = logFile, logFile
	if err := background.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- background.Wait() }()
	client := daemon.NewClient(root)
	defer client.Close()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_ = client.Call(ctx, "HostService", "Quit", "workspace", nil, nil)
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = background.Process.Kill()
		}
	})
	ready := func(previous string) daemon.Snapshot {
		var state daemon.Snapshot
		waitUntil(t, 120*time.Second, func() bool {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if client.JSON(ctx, "/snapshot", nil, &state) != nil {
				return false
			}
			if state.Status.State == lifecycle.StateFailed {
				t.Fatalf("startup failed: %+v", state.Status)
			}
			return state.Status.State == lifecycle.StateReady && state.Status.GenerationID != previous
		})
		return state
	}
	type probeResult struct {
		OK      bool     `json:"ok"`
		UIPID   int      `json:"uiPID"`
		Failure string   `json:"failure"`
		Checks  []string `json:"checks"`
		Framed  struct {
			Origin             string `json:"origin"`
			Embedder           string `json:"embedder"`
			PreviousGeneration string `json:"previousGeneration"`
		} `json:"framed"`
		Timings map[string]any `json:"timings"`
	}
	read := func(path string) probeResult {
		var result probeResult
		waitUntil(t, 160*time.Second, func() bool {
			data, err := os.ReadFile(path)
			return err == nil && json.Unmarshal(data, &result) == nil
		})
		if !result.OK {
			t.Fatalf("framed WebView: %s; report=%s", result.Failure, path)
		}
		if result.Framed.Origin != "http://wails.localhost:48217" || result.Framed.Embedder == "" || result.Framed.Embedder == result.Framed.Origin {
			t.Fatalf("Worker was not framed on its own authority: %+v", result.Framed)
		}
		return result
	}

	first := ready("")
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	err = client.JSON(ctx, "/open", "", nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	result := read(report)
	t.Logf("generation %s framed by %s: %v timings=%v", first.Status.GenerationID, result.Framed.Embedder, result.Checks, result.Timings)

	// A restart replaces only the frame: the next generation reports from the
	// same native window.
	if err := client.Call(context.Background(), "HostService", "Restart", "workspace", nil, nil); err != nil {
		t.Fatal(err)
	}
	second := ready(first.Status.GenerationID)
	again := read(report + ".2")
	if again.UIPID != result.UIPID {
		t.Fatalf("restart replaced the UI process: %d -> %d", result.UIPID, again.UIPID)
	}
	// The frame authority is fixed, so DSH's browser storage outlives a generation.
	if again.Framed.PreviousGeneration != first.Status.GenerationID {
		t.Fatalf("framed storage lost across restart: saw %q, want %q", again.Framed.PreviousGeneration, first.Status.GenerationID)
	}
	t.Logf("generation %s re-framed in UI %d: %d checks", second.Status.GenerationID, again.UIPID, len(again.Checks))
}
