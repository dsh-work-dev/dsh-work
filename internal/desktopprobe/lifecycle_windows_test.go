//go:build windows

package desktopprobe_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/pet"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopprobe"
	"github.com/local/dsh-work/internal/lifecycle"
	"golang.org/x/sys/windows"
)

func TestRealDaemonLifecycle(t *testing.T) {
	if os.Getenv("DSH_WORK_DAEMON_TEST") != "1" {
		t.Skip("opt-in native process/WebView test")
	}
	t.Chdir(filepath.Join("..", ".."))
	root, err := filepath.Abs(filepath.Join(".task", "daemon-lifecycle", fmt.Sprint(time.Now().UnixNano())))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	var directTCPRequests atomic.Int32
	directTCPServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "content-type")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/__direct/ping" {
			directTCPRequests.Add(1)
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, "ok")
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/__direct/echo" {
			http.NotFound(w, r)
			return
		}
		directTCPRequests.Add(1)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, r.Body)
	}))
	defer directTCPServer.Close()
	t.Setenv("DSH_WORK_DIRECT_TCP_ORIGIN", directTCPServer.URL)
	report := filepath.Join(root, "webview.json")
	writeLifecyclePet(t, root)
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
	cli, _ := filepath.Abs("bin/dsh-work-cli.exe")
	if selected := os.Getenv("DSH_WORK_TEST_CLI_BINARY"); selected != "" {
		cli, _ = filepath.Abs(selected)
	}
	logFile, err := os.Create(filepath.Join(root, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	start := func(args ...string) *exec.Cmd {
		cmd := exec.Command(exe, args...)
		daemon.Detach(cmd)
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	background := start("--daemon")
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
	var state daemon.Snapshot
	waitUntil(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if client.JSON(ctx, "/snapshot", nil, &state) != nil {
			return false
		}
		if state.Status.State == lifecycle.StateFailed {
			t.Fatalf("production startup failed: %+v; diagnostics=%+v", state.Status, state.Diagnostics)
		}
		return state.Status.State == lifecycle.StateReady
	})
	baseline := state
	if baseline.PID != background.Process.Pid || baseline.Diagnostics.PID == 0 {
		t.Fatalf("ownership: %+v", baseline)
	}
	t.Logf("daemon=%d Worker=%d generation=%s root=%s", baseline.PID, baseline.Diagnostics.PID, baseline.Status.GenerationID, root)
	var panel app.PetPanel
	if err := client.Call(context.Background(), "PetSettingsService", "RefreshPetCatalog", "settings", nil, &panel); err != nil {
		t.Fatal(err)
	}
	var petKey string
	for _, item := range panel.Snapshot.Items {
		if item.DisplayName == "Lifecycle Pet" {
			petKey = item.StableSourceKey
		}
	}
	if petKey == "" {
		t.Fatal("lifecycle Pet fixture not discovered")
	}
	if err := client.Call(context.Background(), "PetSettingsService", "SelectPet", "settings", []any{petKey}, &panel); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(context.Background(), "PetSettingsService", "SetPetVisibility", "settings", []any{true}, &panel); err != nil {
		t.Fatal(err)
	}
	if err := client.Call(context.Background(), "SettingsService", "SetNotificationPreference", "settings", []any{"completed", false}, nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool { return nativeWindowVisible(baseline.PID, "dsh-work Pet") })
	workerRequest := func(action string) []byte {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		r, err := http.NewRequestWithContext(ctx, "GET", daemon.Origin+"/worker?action="+action, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-DSH-Path", "/__work/probe")
		r.Header.Set("X-DSH-Generation", baseline.Status.GenerationID)
		resp, err := client.HTTP.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("Worker %d: %s", resp.StatusCode, data)
		}
		return data
	}
	workerRequest("background")
	directBinarySamplesMs := directWorkerEcho(t, client, baseline.Status.GenerationID, 5)
	directBinaryMs := median(directBinarySamplesMs)
	t.Logf("direct daemon+Worker 2MiB echo: median %.1f ms samples=%v", directBinaryMs, directBinarySamplesMs)
	taskState := func() struct{ Ticks, PID, Child int } {
		var v struct{ Ticks, PID, Child int }
		if err := json.Unmarshal(workerRequest("background-status"), &v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	firstTask := taskState()
	if firstTask.Child == 0 {
		t.Fatal("owned child missing")
	}
	assertSame := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := client.JSON(ctx, "/snapshot", nil, &state); err != nil {
			t.Fatal(err)
		}
		if state.PID != baseline.PID || state.Diagnostics.PID != baseline.Diagnostics.PID || state.Status.GenerationID != baseline.Status.GenerationID {
			t.Fatal("UI replaced background or Worker")
		}
		if !nativeWindowVisible(baseline.PID, "dsh-work Pet") {
			t.Fatal("UI lifecycle hid background Pet")
		}
		if err := client.Call(ctx, "PetSettingsService", "GetPetPanel", "settings", nil, &panel); err != nil {
			t.Fatal(err)
		}
		if panel.Runtime.EffectiveVisibility != pet.VisibilityVisible || state.Preferences.Notifications.Completed {
			t.Fatal("Pet or notification preference lost")
		}

	}
	var uiPIDs []int
	var webViewTimings struct {
		BinaryRoundtripMs      float64   `json:"binaryRoundtripMs"`
		BinarySamplesMs        []float64 `json:"binarySamplesMs"`
		DirectTCPPingMs        float64   `json:"directTcpPingMs"`
		DirectTCPPingSamplesMs []float64 `json:"directTcpPingSamplesMs"`
		FirstChunkMs           float64   `json:"firstChunkMs"`
	}
	uiClient := daemon.NewClient(root + "-ui")
	defer uiClient.Close()
	for round := 0; round < 2; round++ {
		var uiPID int
		if round == 0 {
			// Startup may already have completed the probe in the hidden boot WebView.
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			err := client.JSON(ctx, "/open", "", nil)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				OK      bool   `json:"ok"`
				UIPID   int    `json:"uiPID"`
				Failure string `json:"failure"`
				Timings struct {
					BinaryRoundtripMs      float64   `json:"binaryRoundtripMs"`
					BinarySamplesMs        []float64 `json:"binarySamplesMs"`
					DirectTCPPingMs        float64   `json:"directTcpPingMs"`
					DirectTCPPingSamplesMs []float64 `json:"directTcpPingSamplesMs"`
					FirstChunkMs           float64   `json:"firstChunkMs"`
				} `json:"timings"`
			}
			waitUntil(t, 160*time.Second, func() bool {
				data, err := os.ReadFile(report)
				return err == nil && json.Unmarshal(data, &result) == nil
			})
			if !result.OK {
				t.Fatalf("WebView: %s; report=%s", result.Failure, report)
			}
			webViewTimings.BinaryRoundtripMs = result.Timings.BinaryRoundtripMs
			webViewTimings.BinarySamplesMs = result.Timings.BinarySamplesMs
			webViewTimings.DirectTCPPingMs = result.Timings.DirectTCPPingMs
			webViewTimings.DirectTCPPingSamplesMs = result.Timings.DirectTCPPingSamplesMs
			webViewTimings.FirstChunkMs = result.Timings.FirstChunkMs
			uiPID = result.UIPID
			if err := uiClient.JSON(context.Background(), "/close", "", nil); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, 15*time.Second, func() bool {
				return !nativeWindowVisible(uiPID, "dsh-work", "操作", "设置", "帮助")
			})
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			err := client.JSON(ctx, "/open", "", nil)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			var status struct{ PID int }
			waitUntil(t, 15*time.Second, func() bool {
				return uiClient.JSON(context.Background(), "/status", nil, &status) == nil && status.PID != 0
			})
			uiPID = status.PID
			waitUntil(t, 10*time.Second, func() bool {
				return nativeWindowVisible(uiPID, "dsh-work", "操作", "设置", "帮助")
			})
			if err := uiClient.JSON(context.Background(), "/close", "", nil); err != nil {
				t.Fatal(err)
			}
			waitUntil(t, 10*time.Second, func() bool {
				return !nativeWindowVisible(uiPID, "dsh-work", "操作", "设置", "帮助")
			})
		}
		if !processAlive(uiPID) {
			t.Fatalf("closing workbench exited UI client %d", uiPID)
		}
		uiPIDs = append(uiPIDs, uiPID)
		assertSame()
		t.Logf("round %d: native UI %d hidden and reusable; daemon and Worker unchanged", round, uiPID)
	}
	if uiPIDs[0] != uiPIDs[1] {
		t.Fatalf("reopen replaced hidden UI client: %v", uiPIDs)
	}
	// Reuse the hidden UI for a multi-window close. The UI singleton keeps this
	// process alive, so a second --ui invocation is not expected to replace it.
	uiPID := uiPIDs[0]
	if err := uiClient.JSON(context.Background(), "/open", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := uiClient.JSON(context.Background(), "/open", "settings", nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool {
		return nativeWindowVisible(uiPID, "dsh-work", "操作", "设置", "帮助") && nativeWindowVisible(uiPID, "设置")
	})
	if err := uiClient.JSON(context.Background(), "/close", "", nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool { return nativeWindowVisible(uiPID, "设置") })
	if !processAlive(uiPID) {
		t.Fatal("closing workbench also closed Settings")
	}
	if err := uiClient.JSON(context.Background(), "/close", "settings", nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 5*time.Second, func() bool { return !nativeWindowVisible(uiPID, "设置") })
	if !processAlive(uiPID) {
		t.Fatal("closing Settings exited the hidden UI client")
	}
	if err := uiClient.JSON(context.Background(), "/stop", nil, nil); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool { return !processAlive(uiPID) })
	assertSame()
	// Crash a new UI with real native windows, leaving the Worker task and its
	// subprocess running. A subsequent tray/open request reattaches.
	t.Setenv("DSH_WORK_UI_HOLD", "1")
	crash := start("--ui")
	crashClient := daemon.NewClient(root + "-ui")
	defer crashClient.Close()
	waitUntil(t, 30*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return crashClient.JSON(ctx, "/open", "", nil) == nil
	})
	if err := crash.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = crash.Wait()
	assertSame()
	if !processAlive(firstTask.Child) {
		t.Fatal("UI crash killed task child")
	}
	output, err := exec.Command(cli, "profile", "list", "--json").CombinedOutput()
	if err != nil || !json.Valid(output) {
		t.Fatalf("online CLI: %s %v", output, err)
	}
	after := taskState()
	if after.Ticks <= firstTask.Ticks || after.Child != firstTask.Child {
		t.Fatal("background task stopped across UI lifecycles")
	}
	t.Logf("UI crash survived; task ticks %d -> %d, child=%d; online CLI passed", firstTask.Ticks, after.Ticks, after.Child)
	// Reopen from the same background launcher after the crash, then stop it.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	err = client.JSON(ctx, "/open", "", nil)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	assertSame()
	var finalUI struct{ PID int }
	if err := crashClient.JSON(context.Background(), "/status", nil, &finalUI); err != nil {
		t.Fatal(err)
	}
	processSample := desktopprobe.Processes(baseline.Diagnostics.PID, baseline.PID, finalUI.PID)
	sampleData, _ := json.Marshal(processSample)
	var sample struct {
		Error                      string
		TCPListeners, UDPEndpoints []any
	}
	if json.Unmarshal(sampleData, &sample) != nil || sample.Error != "" || len(sample.TCPListeners) != 0 || len(sample.UDPEndpoints) != 0 {
		t.Fatalf("process/network sample: %s", sampleData)
	}
	// Restart belongs to the background owner and replaces exactly the Worker.
	if err := client.Call(context.Background(), "HostService", "Restart", "workspace", nil, nil); err != nil {
		t.Fatal(err)
	}
	var restarted daemon.Snapshot
	waitUntil(t, 120*time.Second, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return client.JSON(ctx, "/snapshot", nil, &restarted) == nil && restarted.Status.State == lifecycle.StateReady && restarted.Status.GenerationID != baseline.Status.GenerationID
	})
	if restarted.PID != baseline.PID || processAlive(int(baseline.Diagnostics.PID)) || processAlive(firstTask.Child) {
		t.Fatal("restart ownership or child cleanup failed")
	}
	if !nativeWindowVisible(baseline.PID, "dsh-work Pet") {
		t.Fatal("restart hid Pet")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Second)
	err = client.Call(ctx, "HostService", "Quit", "workspace", nil, nil)
	cancel()
	if err != nil {
		t.Logf("stop response closed during exit: %v", err)
	}
	waitUntil(t, 20*time.Second, func() bool {
		return !processAlive(baseline.PID) && !processAlive(int(baseline.Diagnostics.PID)) && !processAlive(firstTask.Child) && !processAlive(finalUI.PID) && !processAlive(int(restarted.Diagnostics.PID))
	})
	lock, err := app.AcquireManagerProcessLock(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatalf("manager lock retained: %v", err)
	}
	_ = lock.Close()
	data, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var prefs map[string]any
	_ = json.Unmarshal(data, &prefs)
	if _, exists := prefs["closeToTray"]; exists {
		t.Fatal("legacy close preference persisted again")
	}
	logData, err := os.ReadFile(filepath.Join(root, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(logData), "does not match registered data type") {
		t.Fatal("native UI rejected background events; see process.log")
	}
	if directTCPRequests.Load() < 5 {
		t.Fatalf("native WebView did not complete direct TCP samples: %d", directTCPRequests.Load())
	}
	fullWebViewSamples := webViewTimings.BinarySamplesMs
	fullWebViewColdMs := 0.0
	fullWebViewWarmMedianMs := webViewTimings.BinaryRoundtripMs
	if len(fullWebViewSamples) > 0 {
		fullWebViewColdMs = fullWebViewSamples[0]
	}
	if len(fullWebViewSamples) > 1 {
		fullWebViewWarmMedianMs = median(fullWebViewSamples[1:])
	}
	transportEvidence := map[string]any{
		"payloadBytes":                 2<<20 + 13,
		"directDaemonWorkerMs":         directBinaryMs,
		"directSamplesMs":              directBinarySamplesMs,
		"nativeDirectTCPPingMs":        webViewTimings.DirectTCPPingMs,
		"nativeDirectTCPPingSamplesMs": webViewTimings.DirectTCPPingSamplesMs,
		"fullWebViewMs":                webViewTimings.BinaryRoundtripMs,
		"fullWebViewSamplesMs":         webViewTimings.BinarySamplesMs,
		"fullWebViewColdMs":            fullWebViewColdMs,
		"fullWebViewWarmMedianMs":      fullWebViewWarmMedianMs,
		"firstChunkMs":                 webViewTimings.FirstChunkMs,
		"bridgeAndWebViewMs":           webViewTimings.BinaryRoundtripMs - directBinaryMs,
		"note":                         "direct path uses the daemon HTTP client and Worker named-pipe carrier; full path adds WebView/Wails 64 KiB framing, ACKs, and response credits",
	}
	transportRaw, _ := json.MarshalIndent(transportEvidence, "", "  ")
	_ = os.WriteFile(filepath.Join(root, "transport.json"), transportRaw, 0600)
	evidence := map[string]any{"ok": true, "daemonPID": baseline.PID, "processes": processSample, "restartedWorkerPID": restarted.Diagnostics.PID, "workerPID": baseline.Diagnostics.PID, "taskChildPID": firstTask.Child, "uiPIDs": uiPIDs, "ticksBefore": firstTask.Ticks, "ticksAfter": after.Ticks, "generation": baseline.Status.GenerationID, "transport": transportEvidence, "checks": []string{"native WebView binary/WS/cancellation", "native WebView direct TCP loopback", "native last-window close hides and reuses UI", "same daemon/Worker on reopen", "UI crash preserves task/subprocess", "online CLI with UI hidden", "explicit stop cleans Worker/task child and releases lock", "legacy closeToTray=false ignored", "Settings survives workbench hide", "native Pet stays visible in daemon", "notification preference retained", "no TCP/UDP listeners", "restart replaces Worker and preserves daemon"}}
	raw, _ := json.MarshalIndent(evidence, "", "  ")
	_ = os.WriteFile(filepath.Join(root, "lifecycle.json"), raw, 0600)
	t.Logf("evidence: %s", filepath.Join(root, "lifecycle.json"))
}

func waitUntil(t *testing.T, limit time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if predicate() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatal("native lifecycle condition timed out")
}

func directWorkerEcho(t *testing.T, client *daemon.Client, generation string, samples int) []float64 {
	t.Helper()
	payload := bytes.Repeat([]byte("0123456789abcdef"), (2<<20+13+15)/16)
	payload = payload[:2<<20+13]
	results := make([]float64, 0, samples)
	for i := 0; i < samples; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, daemon.Origin+"/worker?action=echo", bytes.NewReader(payload))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		request.Header.Set("X-DSH-Path", "/__work/probe")
		request.Header.Set("X-DSH-Generation", generation)
		request.Header.Set("Content-Type", "application/octet-stream")
		started := time.Now()
		response, err := client.HTTP.Do(request)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		output, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		cancel()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if response.StatusCode != http.StatusOK || !bytes.Equal(output, payload) {
			t.Fatalf("direct Worker echo: status=%d outputBytes=%d", response.StatusCode, len(output))
		}
		results = append(results, float64(time.Since(started).Microseconds())/1000)
	}
	return results
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	middle := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[middle-1] + sorted[middle]) / 2
	}
	return sorted[middle]
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	status, _ := windows.WaitForSingleObject(h, 0)
	return status == uint32(windows.WAIT_TIMEOUT)
}

func writeLifecyclePet(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, "probe-pets", "pets", "lifecycle")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	// Codex discovery requires its entry marker before selecting a native
	// companion manifest. Rendering is provided by dsh-pet.json below.
	if err := os.WriteFile(filepath.Join(dir, "pet.json"), []byte(`{"id":"lifecycle","displayName":"Lifecycle Pet"}`), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := `{"schema":"dsh.pet","schemaVersion":1,"id":"lifecycle","displayName":"Lifecycle Pet","assets":[{"id":"idle","renderer":"dsh-raster-v1","type":"image","path":"idle.png"}],"tracks":{"idle":{"asset":"idle","frames":[{"index":0,"durationMs":125}]}}}`
	if err := os.WriteFile(filepath.Join(dir, "dsh-pet.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "idle.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sprite := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			sprite.SetNRGBA(x, y, color.NRGBA{R: 50, G: 180, B: 200, A: 255})
		}
	}
	if err := png.Encode(f, sprite); err != nil {
		t.Fatal(err)
	}
}
func nativeWindowVisible(pid int, title string, menuLabels ...string) bool {
	dll := windows.NewLazySystemDLL("user32.dll")
	enum := dll.NewProc("EnumWindows")
	owner := dll.NewProc("GetWindowThreadProcessId")
	text := dll.NewProc("GetWindowTextW")
	visible := dll.NewProc("IsWindowVisible")
	found := false
	callback := windows.NewCallback(func(hwnd, unused uintptr) uintptr {
		var id uint32
		owner.Call(hwnd, uintptr(unsafe.Pointer(&id)))
		if id != uint32(pid) {
			return 1
		}
		var buf [256]uint16
		text.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
		shown, _, _ := visible.Call(hwnd)
		if windows.UTF16ToString(buf[:]) == title && shown != 0 {
			if len(menuLabels) > 0 {
				menu, _, _ := dll.NewProc("GetMenu").Call(hwnd)
				count, _, _ := dll.NewProc("GetMenuItemCount").Call(menu)
				if menu == 0 || int(count) != len(menuLabels) {
					return 1
				}
				for i, label := range menuLabels {
					var item [128]uint16
					dll.NewProc("GetMenuStringW").Call(menu, uintptr(i), uintptr(unsafe.Pointer(&item[0])), 128, 0x400)
					if windows.UTF16ToString(item[:]) != label {
						return 1
					}
				}
			}
			found = true
			return 0
		}
		return 1
	})
	enum.Call(callback, 0)
	return found
}
