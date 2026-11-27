//go:build darwin && cgo

package desktopapp

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/embeddedbrowser/distribution"
	"github.com/local/dsh-work/internal/embeddedbrowser/macowner"
	"github.com/local/dsh-work/internal/localipc"
)

func TestLiveMacDMGBrowserOwner(t *testing.T) {
	host := os.Getenv("DSH_WORK_TEST_DMG_EXECUTABLE")
	if !filepath.IsAbs(host) {
		t.Skip("requires the executable from a mounted DMG")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publication, err := macowner.OpenInstalledPublication(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(t.TempDir(), "profile")
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	owner, err := macowner.New(publication, profile)
	if err != nil {
		t.Fatal(err)
	}
	broker, err := embeddedbrowser.Start(ctx, embeddedbrowser.Config{
		Executable: publication.Executable, Profile: profile, Supervisor: owner,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	selection, err := broker.Select(ctx, automation.Request{Session: "dmg-browser", Lifetime: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	tab, err := selection.Open(ctx, "Packaged browser identity")
	if err != nil {
		t.Fatal(err)
	}
	waitForInstalledBrowserTab(t, ctx, tab.Messages())
	journals, err := filepath.Glob(filepath.Join(profile, ".dsh-cef-owner", "generation-*.json"))
	if err != nil || len(journals) != 1 {
		t.Fatalf("expected one browser owner generation, got %d: %v", len(journals), err)
	}
	data, err := os.ReadFile(journals[0])
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Body struct{ Owner macowner.Process }
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if canonical, err := filepath.EvalSymlinks(host); err != nil || record.Body.Owner.Path != canonical {
		t.Fatalf("browser did not launch under the packaged DSH Work executable: %q (%v)", record.Body.Owner.Path, err)
	}
	if err := tab.Close(); err != nil {
		t.Fatal(err)
	}
	if err := broker.Close(); err != nil {
		if state, readErr := os.ReadFile(journals[0]); readErr == nil {
			var status struct {
				Body struct {
					Phase        string
					NativeEvents []struct{ Name string }
				}
			}
			if json.Unmarshal(state, &status) == nil {
				t.Logf("owned browser shutdown phase=%s events=%v", status.Body.Phase, status.Body.NativeEvents)
			}
		}
		t.Fatal(err)
	}
	t.Log("Packaged DSH Work executable owned the browser, opened a CEF tab, and retired it")
}

func TestLiveMacInstalledPublication(t *testing.T) {
	want := distribution.State(os.Getenv("DSH_WORK_TEST_INSTALLED_CEF_STATE"))
	if want == "" {
		t.Skip("requires a signed application fixture or a deliberately damaged copy")
	}
	t.Setenv("DSH_WORK_EMBEDDED_CHECKPOINT", "")
	t.Setenv("DSH_WORK_EMBEDDED_ROOT", "")
	t.Setenv("DSH_WORK_CEF_MAC_PUBLICATION", "/missing/preview-override.json")
	result := embeddedPublication()
	if result.State != want {
		t.Fatalf("installed CEF state = %s, want %s (%v)", result.State, want, result.Reason)
	}
	if want != distribution.Ready {
		if embeddedExecutable() != "" || embeddedPlatformAvailable() {
			t.Fatal("damaged application publication remained available")
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	wantBrowser := filepath.Join(filepath.Dir(filepath.Dir(executable)), "Frameworks", "DSHWorkBrowser.app", "Contents", "MacOS", "DSHWorkBrowser")
	if !embeddedPlatformAvailable() || embeddedExecutable() != wantBrowser {
		t.Fatalf("browser was not located inside the running app: %q", embeddedExecutable())
	}
	if _, ok := embeddedBrowserSupervisor(nil, t.TempDir()).(*macBrowserComponentSupervisor); !ok {
		t.Fatal("installed browser did not select the Mac component owner")
	}
	service := &embeddedBrowserService{}
	availability, err := service.Availability(context.Background())
	if err != nil || availability.State != distribution.UnsupportedRuntime {
		t.Fatalf("ready publication skipped runtime admission: %+v (%v)", availability, err)
	}
	profile := filepath.Join(t.TempDir(), "profile")
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		journals, _ := filepath.Glob(filepath.Join(profile, ".dsh-cef-owner", "generation-*.json"))
		for _, journal := range journals {
			data, err := os.ReadFile(journal)
			if err != nil {
				continue
			}
			var record struct {
				Body struct {
					Phase        string
					ExitSignal   int
					NativeEvents []struct{ Name string }
				}
			}
			if json.Unmarshal(data, &record) == nil {
				t.Logf("browser owner phase=%s exit signal=%d native events=%v", record.Body.Phase, record.Body.ExitSignal, record.Body.NativeEvents)
			}
		}
	})
	// An interrupted first launch can leave an incomplete metadata stage
	// before the browser has opened the directory.
	if err := os.Mkdir(profile, 0700); err != nil {
		t.Fatal(err)
	}
	stage, err := os.CreateTemp(profile, ".dsh-cef-engine-*")
	if err != nil {
		t.Fatal(err)
	}
	_, err = stage.WriteString(`{"cefVersion":`)
	if err := errors.Join(err, stage.Close()); err != nil {
		t.Fatal(err)
	}
	if err = distribution.AdmitProfile(profile); err != nil {
		t.Fatal(err)
	}
	if appKeychain, err := distribution.UsesAppMacKeychain(profile); err != nil || !appKeychain {
		t.Fatalf("recovered installed profile did not select the app keychain: %t (%v)", appKeychain, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	broker, err := startEmbeddedBroker(ctx, embeddedbrowser.Config{Executable: result.Executable, Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = broker.Close() })
	selection, err := broker.Select(ctx, automation.Request{Session: "installed-mac-cef", Lifetime: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	route, err := selection.Open(ctx, "Installed Mac browser")
	if err != nil {
		t.Fatal(err)
	}
	waitForInstalledBrowserTab(t, ctx, route.Messages())
	if err = broker.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("verified installed browser at %s", wantBrowser)
}

func waitForInstalledBrowserTab(t *testing.T, ctx context.Context, messages <-chan json.RawMessage) {
	t.Helper()
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				t.Fatal("installed browser closed before creating a tab")
			}
			var event struct{ Method string }
			if json.Unmarshal(message, &event) == nil && event.Method == "tab.created" {
				return
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func TestLiveMacInstalledDaemonEmptyCatalog(t *testing.T) {
	executable := os.Getenv("DSH_WORK_TEST_INSTALLED_WAILS")
	if executable == "" {
		t.Skip("requires a signed Wails test app with a bundled CEF publication")
	}
	requireIsolatedMacWailsFixture(t, executable, false)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	disableMacFixtureNotifications(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	process := exec.CommandContext(ctx, executable, "--daemon")
	process.Env = append(os.Environ(), "PATH=/usr/bin:/bin", "DSH_WORK_EMBEDDED_ROOT="+root,
		"DSH_WORK_EMBEDDED_CHECKPOINT=1", "DSH_WORK_CEF_MAC_PUBLICATION=", "DSH_WORK_EXECUTABLE=",
		"DSH_WORK_DESKTOP_REPORT=", "DSH_WORK_DESKTOP_ROOT=", "DSH_WORK_EMBEDDED_UI_INSTANCE=",
		"DSH_WORK_UPDATE_FEED_URL=http://127.0.0.1:1")
	log, err := os.Create(filepath.Join(root, "daemon-output.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	process.Stdout, process.Stderr = log, log
	if err = process.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	retired := false
	defer func() {
		if !retired {
			_ = process.Process.Kill()
			<-done
		}
		if t.Failed() {
			if details, readErr := os.ReadFile(log.Name()); readErr == nil {
				t.Log(string(details))
			}
		}
	}()
	daemonPID := uint32(process.Process.Pid)
	daemonStarted, err := localipc.ProcessStarted(daemonPID)
	if err != nil {
		t.Fatal(err)
	}
	client := daemon.NewProcessClient(root, daemon.ProcessPeer{PID: daemonPID, Started: daemonStarted})
	defer client.Close()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var catalog dshmanager.Snapshot
	for {
		probe, stop := context.WithTimeout(ctx, time.Second)
		err = client.Call(probe, "ManagerService", "GetSnapshot", "workspace", nil, &catalog)
		stop()
		if err == nil {
			break
		}
		select {
		case <-ticker.C:
		case exit := <-done:
			retired = true
			t.Fatalf("installed daemon exited before opening its manager: %v (%v)", exit, err)
		case <-ctx.Done():
			t.Fatalf("empty manager catalog was unavailable: %v", err)
		}
	}
	if catalog.SystemNode != nil || len(catalog.Nodes) != 0 || len(catalog.Runtimes) != 0 || len(catalog.DataDirectories) != 1 {
		t.Fatalf("invalid installed first-run catalog: %+v", catalog)
	}
	var snapshot daemon.Snapshot
	if err = client.JSON(ctx, "/snapshot", nil, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Root != filepath.Join(root, "app") || catalog.DataDirectories[0].Path != filepath.Join(root, "data") {
		t.Fatal("installed fixture storage escaped its temporary root")
	}
	if snapshot.Preferences.Notifications.Enabled {
		t.Fatal("isolated daemon must not request system notification permission")
	}
	if snapshot.Update.Phase != daemon.UpdateUnconfigured {
		t.Fatalf("Mac daemon accepted the Windows updater feed: %+v", snapshot.Update)
	}

	// Keep the real workbench connected while the daemon takes the normal quit
	// path. A successor needs both processes to have exited before installation.
	ui := exec.CommandContext(ctx, executable, "--ui", "__boot")
	ui.Env = process.Env
	ui.Stdout, ui.Stderr = log, log
	if err = ui.Start(); err != nil {
		t.Fatal(err)
	}
	uiDone := make(chan error, 1)
	go func() { uiDone <- ui.Wait() }()
	uiRetired := false
	defer func() {
		if !uiRetired {
			_ = ui.Process.Kill()
			<-uiDone
		}
	}()
	uiPID := uint32(ui.Process.Pid)
	uiStarted, err := localipc.ProcessStarted(uiPID)
	if err != nil {
		t.Fatal(err)
	}
	uiClient := daemon.NewProcessClient(root+"-ui", daemon.ProcessPeer{PID: uiPID, Started: uiStarted})
	defer uiClient.Close()
	for {
		var status struct{ PID uint32 }
		probe, stop := context.WithTimeout(ctx, time.Second)
		err = uiClient.JSON(probe, "/status", nil, &status)
		stop()
		if err == nil && status.PID == uiPID {
			break
		}
		select {
		case <-ticker.C:
		case exit := <-uiDone:
			uiRetired = true
			t.Fatalf("installed workbench exited before opening its listener: %v (%v)", exit, err)
		case <-ctx.Done():
			t.Fatalf("installed workbench listener was unavailable: %v", err)
		}
	}
	if err = client.Call(ctx, "HostService", "Quit", "workspace", nil, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		retired = true
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("installed daemon did not exit after the normal shutdown request")
	}
	select {
	case err = <-uiDone:
		uiRetired = true
		if err != nil {
			t.Fatalf("installed workbench did not exit cleanly: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("installed workbench did not exit after the daemon shutdown")
	}
}

func requireIsolatedMacWailsFixture(t *testing.T, executable string, preview bool) {
	t.Helper()
	if !filepath.IsAbs(executable) {
		t.Fatal("Mac Wails fixture executable must be an absolute path")
	}
	info, err := buildinfo.ReadFile(executable)
	if err != nil {
		t.Fatalf("read Mac Wails fixture build settings: %v", err)
	}
	if err := checkIsolatedMacWailsBuild(info, preview); err != nil {
		t.Fatal(err)
	}
}

func checkIsolatedMacWailsBuild(info *debug.BuildInfo, preview bool) error {
	var tags []string
	var osName, cgo string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "GOOS":
			osName = setting.Value
		case "CGO_ENABLED":
			cgo = setting.Value
		case "-tags":
			tags = strings.Fields(strings.ReplaceAll(setting.Value, ",", " "))
		}
	}
	// The production and server builds ignore the fixture storage root.
	if info.Path != "github.com/local/dsh-work" || osName != "darwin" || cgo != "1" ||
		!slices.Contains(tags, "e67presenter") || slices.Contains(tags, "production") || slices.Contains(tags, "server") {
		return errors.New("Mac Wails fixture requires a Darwin cgo DSH Work build with isolated presenter storage")
	}
	if preview && !slices.Contains(tags, "e67previewfixture") {
		return errors.New("Mac Wails preview scenario requires the e67previewfixture build tag")
	}
	return nil
}

func TestCheckIsolatedMacWailsBuild(t *testing.T) {
	for _, tc := range []struct {
		name, path, goos, cgo, tags string
		preview, allowed            bool
	}{
		{"installed fixture", "github.com/local/dsh-work", "darwin", "1", "e67presenter", false, true},
		{"preview fixture", "github.com/local/dsh-work", "darwin", "1", "e67presenter,e67previewfixture", true, true},
		{"production", "github.com/local/dsh-work", "darwin", "1", "e67presenter,production", false, false},
		{"server", "github.com/local/dsh-work", "darwin", "1", "e67presenter,server", false, false},
		{"missing isolation", "github.com/local/dsh-work", "darwin", "1", "e67previewfixture", true, false},
		{"missing preview", "github.com/local/dsh-work", "darwin", "1", "e67presenter", true, false},
		{"cgo disabled", "github.com/local/dsh-work", "darwin", "0", "e67presenter", false, false},
		{"foreign program", "example.com/other", "darwin", "1", "e67presenter", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkIsolatedMacWailsBuild(&debug.BuildInfo{
				Path: tc.path,
				Settings: []debug.BuildSetting{
					{Key: "GOOS", Value: tc.goos},
					{Key: "CGO_ENABLED", Value: tc.cgo},
					{Key: "-tags", Value: tc.tags},
				},
			}, tc.preview)
			if (err == nil) != tc.allowed {
				t.Fatalf("fixture admission = %v, want allowed=%v", err, tc.allowed)
			}
		})
	}
}

func disableMacFixtureNotifications(t *testing.T, root string) {
	t.Helper()
	appData := filepath.Join(root, "app")
	if err := os.MkdirAll(appData, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appData, "settings.json"), []byte(`{"version":2,"notifications":{"enabled":false}}`), 0600); err != nil {
		t.Fatal(err)
	}
}
