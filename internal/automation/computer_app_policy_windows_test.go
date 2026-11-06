//go:build windows

package automation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/settings"
)

func TestWindowsComputerAppPolicyUsesProcessImagePaths(t *testing.T) {
	for _, tc := range []struct {
		path  string
		kind  computerAppKind
		level string
	}{
		{`C:\Program Files\Google\Chrome\Application\chrome.exe`, computerAppBrowser, "view"},
		{`C:\Program Files (x86)\Microsoft\Edge\Application\MSEDGE.EXE`, computerAppBrowser, "view"},
		{`\\server\apps\Mozilla Firefox\firefox.exe`, computerAppBrowser, "view"},
		{`\\?\C:\Users\User\Apps\Brave\brave.exe`, computerAppBrowser, "view"},
		{`C:/Apps/Chromium/chromium.exe`, computerAppBrowser, "view"},
		{`C:\Windows\System32\cmd.exe`, computerAppTerminal, "click"},
		{`C:\Program Files\PowerShell\7\pwsh.exe`, computerAppTerminal, "click"},
		{`C:\Users\User\Apps\Code - Insiders.exe`, computerAppOrdinary, "click"},
		{`C:\Apps\1Password.exe`, computerAppOrdinary, "blocked"},
		{`C:\Windows\System32\CredentialUIBroker.exe`, computerAppOrdinary, "blocked"},
		{`C:\Apps\Editor\editor.exe`, computerAppOrdinary, "full"},
		{`C:\chrome.exe\editor.exe`, computerAppOrdinary, "full"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			kind, level := windowsComputerAppPolicy(tc.path)
			if kind != tc.kind || level != tc.level {
				t.Fatalf("got kind=%v level=%q, want kind=%v level=%q", kind, level, tc.kind, tc.level)
			}
		})
	}
}

// Exercise the adapter's real Windows path classification through authorization,
// including a full-access app rule: it must not lift the native view ceiling,
// and browser content must still be refused.
func TestWindowsBrowserClassificationReachesGateway(t *testing.T) {
	s, f, target := computerTestService(t)
	target.Executable = `C:\Program Files\Google\Chrome\Application\chrome.exe`
	target.kind, target.Level = windowsComputerAppPolicy(target.Executable)
	target.Identity = "fixture-browser"
	s.computer.policy.Apps = append(s.computer.policy.Apps,
		settings.ComputerAppRule{ID: target.Identity, Level: "full"})
	inspected, err := s.inspectComputerTarget(target.PID, target.WindowID)
	if err != nil || inspected.Level != "view" || inspected.kind != computerAppBrowser {
		t.Fatalf("app rule changed native browser classification/ceiling: %+v, %v", inspected, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := s.AuthorizeComputer(ctx, computerRequest(s, "computer_observe")); err == nil || !strings.Contains(err.Error(), "BROWSER_TOOLS_REQUIRED") {
		t.Fatalf("browser path was not refused: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 || s.Snapshot().ComputerAccess.Use != nil {
		t.Fatal("browser denial read content or retained desktop access")
	}
}
