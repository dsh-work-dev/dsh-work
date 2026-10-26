package desktopapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/local/dsh-work/internal/daemon"
)

func TestUIOpenRequestSelectsAndReturnsTheActualWindow(t *testing.T) {
	var section, window string
	handler := uiHandler(func(gotSection, gotWindow string) string {
		section, window = gotSection, gotWindow
		return "workspace"
	}, nil, nil)
	request := httptest.NewRequest(http.MethodPost, "/open", strings.NewReader(`{"section":"","window":"workspace-2"}`))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || section != "" || window != "workspace-2" {
		t.Fatalf("open request = %d, %q, %q", recorder.Code, section, window)
	}
	var result uiOpenResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Window != "workspace" {
		t.Fatalf("open result = %q, %v", recorder.Body.String(), err)
	}

	legacy := httptest.NewRequest(http.MethodPost, "/open", strings.NewReader(`"settings"`))
	legacyResult := httptest.NewRecorder()
	handler.ServeHTTP(legacyResult, legacy)
	if legacyResult.Code != http.StatusBadRequest {
		t.Fatalf("legacy string /open body status = %d", legacyResult.Code)
	}
}

func TestUILauncherAllowsForegroundActivationOnceBeforeOpening(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("native daemon IPC is available on Windows")
	}
	identity := "ui-launcher-" + t.TempDir()
	listener, err := daemon.Listen(identity + "-ui")
	if err != nil {
		t.Fatal(err)
	}
	order := make(chan string, 2)
	server := &http.Server{Handler: uiHandler(func(_, window string) string {
		order <- "open"
		return window
	}, nil, nil)}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })

	activationCalls := 0
	launcher := &uiLauncher{identity: identity, allowForegroundActivation: func() {
		activationCalls++
		order <- "activation"
	}}
	window, err := launcher.Open("", "workspace-2")
	if err != nil || window != "workspace-2" {
		t.Fatalf("Open() = %q, %v", window, err)
	}
	if activationCalls != 1 {
		t.Fatalf("foreground activation calls = %d, want 1", activationCalls)
	}
	if first, second := <-order, <-order; first != "activation" || second != "open" {
		t.Fatalf("operation order = %q, %q, want activation before open", first, second)
	}
}
