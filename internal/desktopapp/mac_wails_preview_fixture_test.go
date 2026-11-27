//go:build darwin && cgo && e67presenter && e67nativefixture && !server

package desktopapp

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/settings"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The consumer launches the original built root application. It does not
// replace its Wails host, DSH Session model, registry, or browser owner.
func TestLiveMacWailsPreviewSlot(t *testing.T) {
	app := os.Getenv("DSH_WORK_MAC_PREVIEW_EXECUTABLE")
	publication := os.Getenv("DSH_WORK_CEF_MAC_PUBLICATION")
	cli := os.Getenv("DSH_WORK_EXECUTABLE")
	out := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE")
	if os.Getenv("CI") != "true" || !filepath.IsAbs(app) || !filepath.IsAbs(publication) || !filepath.IsAbs(cli) || !filepath.IsAbs(out) {
		t.Fatal("requires explicit isolated CI root app, publication, restricted DSH CLI and evidence directory")
	}
	requireIsolatedMacWailsFixture(t, app, true)
	if entries, err := os.ReadDir(out); err == nil {
		if len(entries) != 0 {
			t.Fatal("requires an empty evidence directory for this run")
		}
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(out, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "dsh-mac-preview-")
	if err != nil {
		t.Fatal(err)
	}
	complete, crashStageRemoved := false, false
	defer func() {
		if complete {
			_ = os.RemoveAll(root)
		} else if crashStageRemoved {
			t.Log("fixture profile owner record retained at", root)
		} else {
			t.Log("private failed preview evidence retained at", root)
		}
	}()
	disableMacFixtureNotifications(t, root)
	petGeometry := os.Getenv("DSH_WORK_MAC_PET_GEOMETRY") == "1"
	if petGeometry {
		seedMacPetGeometry(t, root)
	}
	workspace := filepath.Join(root, "workspace")
	if err = os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	// Seed only the saved workspace. The real DSH UI creates the session and
	// browser tab without needing to open a system directory picker.
	const workspaceID = "00000000-0000-4000-8000-000000000001"
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Format(time.RFC3339Nano)
	storage := map[string]any{
		"unit":   map[string]any{"name": "workspace", "version": 2},
		"global": map[string]any{"initialized": true, "workspaceIds": []string{workspaceID}, "archivedSessionIds": []string{}, "pinnedSessionIds": []string{}},
		"tables": map[string]any{"workspaces": map[string]any{workspaceID: map[string]any{
			"path": canonicalWorkspace, "title": "workspace", "createdAt": now, "updatedAt": now, "sessionIds": []string{},
		}}},
	}
	storageData, err := json.Marshal(storage)
	if err != nil {
		t.Fatal(err)
	}
	storageDir := filepath.Join(root, "data", "storages")
	if err = os.MkdirAll(storageDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(storageDir, "workspace.json"), storageData, 0600); err != nil {
		t.Fatal(err)
	}
	nonce := rand.Text()
	dockReopen := os.Getenv("DSH_WORK_MAC_DOCK_REOPEN") == "1"
	manualNotification := os.Getenv("DSH_WORK_MAC_MANUAL_NOTIFICATION") == "1"
	if manualNotification && !dockReopen {
		t.Fatal("manual notification requires the Dock and workbench restoration fixture")
	}
	manualFiles := os.Getenv("DSH_WORK_MAC_MANUAL_FILE_PICKER")
	if manualFiles == "1" {
		manualFiles = "both"
	}
	if manualFiles != "" {
		if err = os.WriteFile(filepath.Join(workspace, "dsh-parity-upload.txt"), []byte(nonce), 0600); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(workspace, "dsh-parity-artifact.html"), []byte("<title>DSH Work fixture artifact</title><h1>Fixture</h1>"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	type pageState struct {
		Nonce, Input, Prompt       string
		PopupLoads, ChooserCancels int
		PopupClicks                int
		PopupTargetAtPoint         string
		PopupClickTrusted          bool
		PopupClickActive           bool
		PromptStarts               int
		PromptDown, PromptUp       int
		PromptTargetAtPoint        string
		Clicks, Loads, Tick        int
		Composition                []string
		ScrollY                    float64
		FixtureFileRead            bool
		Focused                    bool
		IndicatorEvents            []string
	}
	var mu sync.Mutex
	var promptStarts atomic.Int32
	indicatorDependencies, indicatorMarkup := "", ""
	if os.Getenv("DSH_WORK_MAC_AGENT_INDICATOR") == "1" {
		indicatorDependencies = os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
		if !filepath.IsAbs(indicatorDependencies) {
			t.Fatal("indicator fixture requires absolute locked browser dependencies")
		}
		indicatorMarkup = `<button id="agent-target" style="position:fixed;left:112px;top:350px;width:260px">Agent feedback target</button>`
	}
	report := pageState{}
	loads, popupLoads := 0, 0
	failNavigation := os.Getenv("DSH_WORK_MAC_NAVIGATION_FAILURE") == "1"
	var failedNavigations atomic.Int32
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			t.Logf("fixture main document requests: %d, latest page report tick: %d", loads, report.Tick)
			mu.Unlock()
		}
	})
	downloadMode := os.Getenv("DSH_WORK_MAC_DOWNLOAD_FIXTURE")
	manualOverwrite := downloadMode == "manual-overwrite"
	manualDownload := downloadMode == "manual" || manualOverwrite
	if manualOverwrite {
		for name, contents := range map[string]string{"complete.txt": "previous preview download\n", "unrelated.txt": "keep\n"} {
			if err := os.WriteFile(filepath.Join(workspace, name), []byte(contents), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	nativeDownload := downloadMode == "native" || manualDownload
	crashDownload := downloadMode == "crash"
	invalidateDownload := downloadMode == "invalidate"
	resizeDownload := downloadMode == "resize"
	contentRisk, _ := strings.CutPrefix(downloadMode, "risk-")
	if contentRisk != "accept" && contentRisk != "decline" {
		contentRisk = ""
	}
	downloadQuit, _ := strings.CutPrefix(downloadMode, "quit-")
	stopChoice, downloadStopUI := strings.CutPrefix(downloadMode, "stop-")
	if downloadStopUI {
		downloadQuit = stopChoice
	}
	if downloadQuit != "wait" && downloadQuit != "cancel" && downloadQuit != "back" {
		downloadQuit = ""
	}
	downloadWindowClose := downloadMode == "close-window"
	download := downloadMode == "1" || nativeDownload || crashDownload || invalidateDownload || resizeDownload || contentRisk != "" || downloadQuit != "" || downloadWindowClose
	retirePicker := os.Getenv("DSH_WORK_MAC_RETIRE_PICKER") == "1"
	if retirePicker && (download || failNavigation || dockReopen || petGeometry || indicatorDependencies != "" || manualFiles != "") {
		t.Fatal("picker retirement requires a standalone workbench run")
	}
	if downloadMode != "" && !download {
		t.Fatalf("unknown Mac download fixture mode %q", downloadMode)
	}
	if indicatorDependencies != "" && (download || manualFiles != "") {
		t.Fatal("indicator fixture requires a standalone workbench run")
	}
	if dockReopen && (download || manualFiles != "" || indicatorDependencies != "") {
		t.Fatal("Dock fixture requires a standalone workbench run")
	}
	if petGeometry && (dockReopen || download || manualFiles != "" || indicatorDependencies != "") {
		t.Fatal("Pet geometry fixture requires a standalone daemon run")
	}
	var completedRequests, canceledRequests atomic.Int32
	defer func() {
		if download && t.Failed() {
			t.Logf("fixture download requests: complete=%d cancel=%d", completedRequests.Load(), canceledRequests.Load())
		}
	}()
	canceledTransfer := make(chan struct{}, 1)
	releaseDownload := make(chan struct{})
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if download && (r.URL.Path == "/complete.txt" || r.URL.Path == "/cancel.txt") {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Disposition", "attachment")
			if r.URL.Path == "/complete.txt" {
				completedRequests.Add(1)
				if contentRisk != "" {
					_, _ = io.WriteString(w, "\x89PNG\r\n\x1a\n")
				}
				_, _ = io.WriteString(w, nonce)
				return
			}
			request := canceledRequests.Add(1)
			chunk := make([]byte, 128<<10)
			for i := range chunk {
				chunk[i] = 'd'
			}
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
			if request == 1 {
				return
			}
			select {
			case <-r.Context().Done():
				canceledTransfer <- struct{}{}
			case <-releaseDownload:
				_, _ = io.WriteString(w, nonce)
			case <-time.After(30 * time.Second):
			}
			return
		}
		if r.URL.Path == "/popup" {
			mu.Lock()
			popupLoads++
			mu.Unlock()
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<title>Approved Mac popup</title><h1>Approved Mac popup</h1>")
			return
		}
		if r.URL.Path == "/prompt-start" && r.Method == http.MethodPost {
			body, err := io.ReadAll(io.LimitReader(r.Body, 128))
			if err != nil || string(body) != nonce {
				http.Error(w, "invalid fixture prompt report", http.StatusBadRequest)
				return
			}
			promptStarts.Add(1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/report" {
			mu.Lock()
			defer mu.Unlock()
			if r.Method == http.MethodPost {
				var value pageState
				if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&value) != nil || value.Nonce != nonce {
					http.Error(w, "invalid fixture report", 400)
					return
				}
				value.Loads = loads
				value.PopupLoads = popupLoads
				report = value
				w.WriteHeader(204)
				return
			}
			report.PopupLoads = popupLoads
			report.PromptStarts = int(promptStarts.Load())
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(report)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		loads++
		mu.Unlock()
		if failNavigation {
			if _, err := os.Stat(filepath.Join(out, "navigation-retry")); os.IsNotExist(err) {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					failedNavigations.Add(1)
					_ = conn.Close()
					return
				}
				http.Error(w, "fixture connection could not be closed", http.StatusInternalServerError)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Formal Mac Wails CEF</title><style>body{margin:0;min-height:220vh;background:#e6f4ec;color:#143322;font:20px system-ui}h1{font:22px system-ui;position:absolute;left:20px;top:0}input{position:absolute;left:20px;top:50px;width:200px;height:40px;font:20px system-ui;box-sizing:border-box}button{position:absolute;left:20px;top:120px;width:180px;height:40px;font:18px system-ui}#prompt,#popup{position:fixed;left:230px;top:50px}#popup{top:110px}#upload{position:fixed;left:230px;top:170px}#complete,#cancel{position:fixed;left:230px;top:240px}#cancel{top:300px}output{position:absolute;left:20px;top:180px}</style><h1>Formal Mac same-window page</h1><input aria-label="Fixture input" value=""><button>Click actual page</button><button id="prompt">Prompt</button><button id="popup">Popup</button><input id="upload" type="file" accept=".txt,image/*"><output>0 clicks</output><a id="complete" href="/complete.txt">Download</a><a id="cancel" href="/cancel.txt">Cancel fixture</a>%s<script>
const nonce=%q,input=document.querySelector('input'),button=document.querySelector('button'),output=document.querySelector('output');
const composition=[],indicatorEvents=[];let clicks=0,tick=0,promptResult='',promptDown=0,promptUp=0,popupClicks=0,popupClickTrusted=false,popupClickActive=false,chooserCancels=0,fixtureFileRead=false;
const upload=document.querySelector('#upload');
upload.addEventListener('cancel',()=>chooserCancels++);
upload.addEventListener('change',async()=>{const file=upload.files[0];fixtureFileRead=file?.name==='dsh-parity-upload.txt'&&file.size===nonce.length&&await file.text()===nonce});
const promptButton=document.querySelector('#prompt');
promptButton.onmousedown=()=>promptDown++;
promptButton.onmouseup=()=>promptUp++;
promptButton.onclick=()=>{navigator.sendBeacon('/prompt-start',nonce);promptResult=window.prompt('Fixture prompt','默认')??'<canceled>'};
document.querySelector('#popup').onclick=event=>{
 popupClicks++;popupClickTrusted=event.isTrusted;popupClickActive=navigator.userActivation.isActive;
 window.open('/popup','_blank');
};
for(const type of ['compositionstart','compositionupdate','compositionend']) input.addEventListener(type,event=>composition.push(type+':'+event.data));
button.onclick=()=>{output.textContent=++clicks+' clicks'};
const agentTarget=document.querySelector('#agent-target');
for(const type of ['mousedown','mouseup','click']) agentTarget?.addEventListener(type,()=>indicatorEvents.push(type+':'+!!document.querySelector('div[aria-hidden=true]')));
setInterval(()=>fetch('/report',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({nonce,input:input.value,clicks,composition,scrollY,prompt:promptResult,promptDown,promptUp,promptTargetAtPoint:document.elementFromPoint(260,70)?.id||'',popupClicks,popupClickTrusted,popupClickActive,popupTargetAtPoint:document.elementFromPoint(260,130)?.id||'',chooserCancels,fixtureFileRead,indicatorEvents,focused:document.hasFocus(),tick:++tick})}),250)
</script>`, indicatorMarkup, nonce)
	}))
	defer page.Close()
	cfg := map[string]any{"nonce": nonce, "url": page.URL + "/", "workspace": workspace, "evidence": out, "manualFiles": manualFiles, "download": download, "nativeDownload": nativeDownload, "manualDownload": manualDownload, "crashDownload": crashDownload}
	cfg["contentRisk"] = contentRisk
	cfg["manualOverwrite"] = manualOverwrite
	cfg["downloadQuit"] = downloadQuit
	cfg["downloadWindowClose"] = downloadWindowClose
	cfg["invalidateDownload"] = invalidateDownload
	cfg["resizeDownload"] = resizeDownload
	cfg["failNavigation"] = failNavigation
	cfg["indicatorDependencies"] = indicatorDependencies
	cfg["dockReopen"] = dockReopen
	cfg["retirePicker"] = retirePicker
	cfg["manualNotification"] = manualNotification
	cfg["petGeometry"] = petGeometry
	body, _ := json.Marshal(cfg)
	scenario := filepath.Join(root, "scenario.json")
	if err = os.WriteFile(scenario, body, 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "TMPDIR", "LANG", "LC_ALL"} {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
	env["CI"] = "true"
	env["DSH_WORK_EMBEDDED_CHECKPOINT"] = "1"
	env["DSH_WORK_EMBEDDED_ROOT"] = root
	env["DSH_WORK_EMBEDDED_UI_INSTANCE"] = "1"
	if !petGeometry {
		env["DSH_WORK_CEF_MAC_PUBLICATION"] = publication
		env["DSH_WORK_EXECUTABLE"] = cli
	}
	env["DSH_WORK_MAC_PREVIEW_SCENARIO"] = scenario
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		cmd  *exec.Cmd
		done chan struct{}
		err  error
	}
	start := func(name string, args ...string) *child {
		file, e := os.OpenFile(filepath.Join(root, name+".private.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		cmd := exec.Command(app, args...)
		cmd.Dir = repo
		for k, v := range env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		cmd.Stdout = file
		cmd.Stderr = file
		if e = cmd.Start(); e != nil {
			file.Close()
			t.Fatal(name, e)
		}
		c := &child{cmd: cmd, done: make(chan struct{})}
		go func() {
			e := cmd.Wait()
			file.Close()
			c.err = e
			close(c.done)
		}()
		return c
	}
	background := start("daemon", "--daemon")
	children := []*child{background}
	var cleanupClient *daemon.Client
	defer func() {
		if !complete && cleanupClient != nil {
			quit, stop := context.WithTimeout(context.Background(), 8*time.Second)
			_ = cleanupClient.Call(quit, "HostService", "RequestQuit", "workspace", nil, nil)
			select {
			case <-background.done:
			case <-quit.Done():
			}
			stop()
		}
		for i := len(children) - 1; i >= 0; i-- {
			c := children[i]
			select {
			case <-c.done:
				continue
			default:
			}
			{
				_ = c.cmd.Process.Kill()
				select {
				case <-c.done:
				case <-time.After(5 * time.Second):
					t.Error("owned app child did not join")
				}
			}
		}
	}()
	pid := uint32(background.cmd.Process.Pid)
	started, err := localipc.ProcessStarted(pid)
	if err != nil {
		t.Fatal("actual daemon instance", err)
	}
	client := daemon.NewProcessClient(root, daemon.ProcessPeer{PID: pid, Started: started})
	cleanupClient = client
	defer client.Close()
	timeout := 145 * time.Second
	if petGeometry {
		timeout = 60 * time.Second
	}
	if manualFiles != "" || manualDownload || manualNotification {
		timeout = 11 * time.Minute
	}
	parent := context.Background()
	if manualNotification || manualOverwrite {
		var stopSignal context.CancelFunc
		parent, stopSignal = signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
		defer stopSignal()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var snapshot daemon.Snapshot
	for {
		callCtx, stop := context.WithTimeout(ctx, time.Second)
		e := client.JSON(callCtx, "/snapshot", nil, &snapshot)
		stop()
		if e == nil && snapshot.Protocol == daemon.Protocol && snapshot.PID == int(pid) {
			break
		}
		select {
		case <-background.done:
			t.Fatal("actual daemon exited before IPC", background.err)
		case <-ctx.Done():
			t.Fatal("actual daemon IPC", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if snapshot.Preferences.Notifications.Enabled {
		t.Fatal("isolated workbench must not request system notification permission")
	}
	if petGeometry {
		first := waitMacPetFrame(t, ctx, client, nil)
		checkMacPetAnchor(t, first, 0.75, 0.65)
		input := checkMacPetPointer(t, ctx, client)
		checkMacPetClick(t, ctx, client)
		if err := client.Call(ctx, "MacPreviewPet", "Geometry", "workspace", []any{true}, nil); err != nil {
			t.Fatal("move the owned native Pet", err)
		}
		moved := waitMacPetFrame(t, ctx, client, func(f macPetFrame) bool {
			var state daemon.Snapshot
			if err := client.JSON(ctx, "/snapshot", nil, &state); err != nil {
				return false
			}
			position := state.Preferences.Pet.Position
			x, y := pet.AnchorForPosition(f.X, f.Y, f.Width, f.Height, f.workArea(), 12)
			return f.X == first.AreaX+73 && f.Y == first.AreaY+91 && position.MonitorID != "" &&
				position.AnchorX == x && position.AnchorY == y && position.Scale == f.Scale &&
				position.Width == 96 && position.Height == 104
		})
		var afterMove daemon.Snapshot
		if err := client.JSON(ctx, "/snapshot", nil, &afterMove); err != nil {
			t.Fatal(err)
		}
		displayEvents := []string{"screen", "backing", "topology"}
		for _, event := range displayEvents {
			if !t.Run("display-"+event, func(t *testing.T) {
				if err := client.Call(ctx, "MacPreviewPet", "DisplayChange", "workspace", []any{event}, nil); err != nil {
					t.Fatalf("deliver native Pet %s event: %v", event, err)
				}
				waitMacPetFrame(t, ctx, client, func(f macPetFrame) bool {
					var updated daemon.Snapshot
					return f == moved && client.JSON(ctx, "/snapshot", nil, &updated) == nil &&
						updated.Preferences.Pet.Position == afterMove.Preferences.Pet.Position
				})
			}) {
				return
			}
		}
		stopMacPetDaemon(t, ctx, client, background.done)
		if background.err != nil {
			t.Fatal("original Pet daemon exit", background.err)
		}
		restarted := start("daemon-restarted", "--daemon")
		children = append(children, restarted)
		newPID := uint32(restarted.cmd.Process.Pid)
		newStarted, err := localipc.ProcessStarted(newPID)
		if err != nil {
			t.Fatal("identify restarted Pet daemon", err)
		}
		restartClient := daemon.NewProcessClient(root, daemon.ProcessPeer{PID: newPID, Started: newStarted})
		defer restartClient.Close()
		restored := waitMacPetFrame(t, ctx, restartClient, func(f macPetFrame) bool { return f == moved })
		restartedInput := checkMacPetPointer(t, ctx, restartClient)
		checkMacPetClick(t, ctx, restartClient)
		stopMacPetDaemon(t, ctx, restartClient, restarted.done)
		if restarted.err != nil {
			t.Fatal("restarted Pet daemon exit", restarted.err)
		}
		evidence, err := json.MarshalIndent(map[string]any{
			"first": first, "moved": moved, "restored": restored, "daemonPID": pid, "restartedDaemonPID": newPID,
			"nativePointerInput": input, "restartedNativePointerInput": restartedInput,
			"trustedNativeWebViewClick": true, "restartedTrustedNativeWebViewClick": true,
			"nativeDisplayEvents": displayEvents,
		}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(out, "mac-preview-pet-geometry.json"), evidence, 0600); err != nil {
			t.Fatal(err)
		}
		complete = true
		return
	}
	if downloadWindowClose || dockReopen {
		if err := client.Call(ctx, "SettingsService", "AcknowledgeBackgroundNotice", "workspace", nil, nil); err != nil {
			t.Fatal("acknowledge the background notice in the isolated settings", err)
		}
	}
	ui := start("ui", "--ui", "", "--window", "workspace")
	children = append(children, ui)
	var uiClient *daemon.Client
	if downloadWindowClose || downloadStopUI {
		uiPID := uint32(ui.cmd.Process.Pid)
		uiStarted, err := localipc.ProcessStarted(uiPID)
		if err != nil {
			t.Fatal("actual workbench process identity", err)
		}
		uiClient = daemon.NewProcessClient(root+"-ui", daemon.ProcessPeer{PID: uiPID, Started: uiStarted})
		defer uiClient.Close()
	}
	var result struct {
		Passed, CloseInstanceReturned, PromptPassed, PopupPassed, ArtifactPickerCanceled, UploadPickerCanceled bool
		UploadFixtureFileRead, ArtifactFixtureOpened                                                           bool
		UploadPickerFileFilterPassed                                                                           bool
		DownloadsPassed, NavigationRetryPassed                                                                 bool
		CrashedSlotReopened, AgentIndicatorPassed                                                              bool
		AgentIndicatorClicks                                                                                   int
		DockReopenPassed, TrayActionsPassed, TrayMenuTrackingPassed                                            bool
		NotificationDefaultResponsePassed                                                                      bool
		ManualNotificationPassed                                                                               bool
		PromptStartedOnce                                                                                      bool
		OverlappingOverlaysBlockedInput                                                                        bool
		BrowserTabSwitchPassed                                                                                 bool
		BackingFramesAfterWindowResize                                                                         []json.RawMessage
		BrowserRetiredWithPicker                                                                               bool
		WorkspaceDirectoryPickerCanceled                                                                       bool
		DockDaemonWindows                                                                                      struct{ Total, Visible int }
		FailureStage, FailureCode                                                                              string
	}
	brokerKilled, downloadExitExercised := false, false
consumerResult:
	for {
		if (downloadQuit != "" || downloadWindowClose) && !downloadExitExercised {
			if ready, _ := os.ReadFile(filepath.Join(out, "mac-preview-download-quit-ready")); string(ready) == nonce {
				if downloadWindowClose {
					exerciseMacDownloadWindowClose(t, ctx, client, uiClient, out, nonce, releaseDownload, canceledTransfer, background.done, ui.done)
				} else {
					exerciseMacDownloadQuit(t, ctx, client, uiClient, out, nonce, downloadQuit, releaseDownload, canceledTransfer, background.done, ui.done)
				}
				downloadExitExercised = true
			}
		}
		if crashDownload && !brokerKilled {
			if ready, _ := os.ReadFile(filepath.Join(out, "mac-preview-download-crash-ready")); string(ready) == nonce {
				var broker localipc.ProcessTarget
				proof, err := os.ReadFile(filepath.Join(out, "mac-preview-download-broker.json"))
				if err != nil || json.Unmarshal(proof, &broker) != nil || broker.PID == 0 || broker.Started == 0 || broker.PID == uint32(pid) || broker.PID == uint32(ui.cmd.Process.Pid) {
					t.Fatal("missing isolated broker identity", err)
				}
				if err := localipc.SignalTarget(broker, syscall.SIGKILL); err != nil {
					t.Fatal("terminate fixture broker", err)
				}
				brokerKilled = true
			}
		}
		if data, e := os.ReadFile(filepath.Join(out, "mac-preview-consumer.json")); e == nil && json.Unmarshal(data, &result) == nil {
			if !result.Passed {
				t.Fatal("actual Mac UI consumer", result.FailureStage, result.FailureCode)
			}
			if (downloadQuit == "" && !downloadWindowClose) || downloadExitExercised {
				break
			}
		}
		callCtx, stop := context.WithTimeout(ctx, time.Second)
		e := client.JSON(callCtx, "/snapshot", nil, &snapshot)
		stop()
		if e == nil && snapshot.Status.State == lifecycle.StateFailed {
			t.Fatalf("actual DSH preview boot failed: %s, %+v", snapshot.Status.Phase, snapshot.Status.Error)
		}
		select {
		case <-ui.done:
			if data, e := os.ReadFile(filepath.Join(out, "mac-preview-consumer.json")); e == nil && json.Unmarshal(data, &result) == nil && result.Passed {
				break consumerResult
			}
			t.Fatal("actual UI exited without final consumer evidence", ui.err, result.FailureStage, result.FailureCode)
		case <-ctx.Done():
			t.Fatal("actual UI consumer", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
	if failNavigation && (!result.NavigationRetryPassed || failedNavigations.Load() == 0) {
		t.Fatal("browser did not recover from an actual failed main navigation")
	}
	if download {
		expectedCompleteRequests, expectedCancelRequests := int32(2), int32(2)
		if nativeDownload && !manualDownload {
			expectedCompleteRequests, expectedCancelRequests = 1, 0
			if _, err := os.Lstat(filepath.Join(workspace, "complete.txt")); !os.IsNotExist(err) {
				t.Fatal("canceling the real Save sheet must leave the destination absent")
			}
		}
		if manualDownload || contentRisk != "" {
			expectedCancelRequests = 0
		}
		if invalidateDownload {
			expectedCancelRequests = 1
		}
		if downloadQuit != "" || downloadWindowClose {
			expectedCompleteRequests = 0
		}
		if !result.DownloadsPassed || completedRequests.Load() != expectedCompleteRequests || canceledRequests.Load() != expectedCancelRequests {
			t.Fatalf("download evidence incomplete: passed=%t completed requests=%d canceled requests=%d", result.DownloadsPassed, completedRequests.Load(), canceledRequests.Load())
		}
		if manualOverwrite {
			if _, err := os.Stat(filepath.Join(out, "mac-preview-overwrite-confirmation")); err != nil {
				t.Fatal("missing native overwrite confirmation evidence", err)
			}
			content, err := os.ReadFile(filepath.Join(workspace, "unrelated.txt"))
			if err != nil || string(content) != "keep\n" {
				t.Fatal("unrelated fixture file changed", err)
			}
			stages, err := filepath.Glob(filepath.Join(workspace, ".dsh-download-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("stages remained after replacing the fixture file: %v (%v)", stages, err)
			}
		}
		if contentRisk == "accept" {
			if size, err := unix.Getxattr(filepath.Join(workspace, "complete.txt"), "com.apple.quarantine", nil); err != nil || size == 0 {
				t.Fatalf("accepted download is missing its quarantine metadata: size=%d (%v)", size, err)
			}
		}
		if !nativeDownload && !invalidateDownload && contentRisk == "" && !downloadWindowClose && (downloadQuit == "" || downloadQuit == "cancel") {
			select {
			case <-canceledTransfer:
			case <-time.After(5 * time.Second):
				t.Fatal("server did not observe cancellation of the approved GET")
			}
		}
	}
	if crashDownload {
		if !brokerKilled || !result.CrashedSlotReopened {
			t.Fatal("the active broker crash and workbench recovery were not both exercised")
		}
	} else if dockReopen {
		if !result.DockReopenPassed || !result.TrayActionsPassed || !result.TrayMenuTrackingPassed || !result.NotificationDefaultResponsePassed ||
			result.DockDaemonWindows.Total == 0 || result.DockDaemonWindows.Visible != 0 {
			t.Fatalf("missing Dock or tray reopen evidence: %+v", result.DockDaemonWindows)
		}
		if manualNotification && !result.ManualNotificationPassed {
			t.Fatal("missing real Notification Center response and workbench restoration evidence")
		}
	} else if indicatorDependencies != "" {
		if !result.AgentIndicatorPassed || result.AgentIndicatorClicks != 3 {
			t.Fatal("missing managed Agent indicator evidence")
		}
	} else if retirePicker {
		if !result.UploadPickerFileFilterPassed || !result.BrowserRetiredWithPicker {
			t.Fatal("detaching the browser did not drain the native chooser and retire its input lease")
		}
	} else if !download && !failNavigation {
		if !result.PromptPassed || !result.PromptStartedOnce || !result.PopupPassed {
			t.Fatal("missing native prompt or popup evidence")
		}
		if !result.ArtifactPickerCanceled {
			t.Fatal("missing native artifact picker cancellation evidence")
		}
		if !result.UploadPickerFileFilterPassed || !result.UploadPickerCanceled {
			t.Fatal("missing native webpage chooser filter or cancellation evidence")
		}
		if (manualFiles == "both" && !result.UploadFixtureFileRead) || (manualFiles != "" && !result.ArtifactFixtureOpened) {
			t.Fatal("missing confirmation for both native file selections")
		}
	}
	if !crashDownload && downloadQuit != "wait" && downloadQuit != "cancel" && !result.CloseInstanceReturned {
		t.Fatal("actual normal CEF CloseInstance missing")
	}
	select {
	case <-ui.done:
		if ui.err != nil {
			t.Fatal("actual normal UI exit", ui.err)
		}
	case <-ctx.Done():
		t.Fatal("actual UI join", ctx.Err())
	}
	screenshots := []string{"mac-preview-visible.png"}
	if dockReopen {
		screenshots = append(screenshots, "mac-preview-dock-reopened.png")
	} else if indicatorDependencies != "" {
		screenshots = append(screenshots, "mac-preview-indicator.png")
	} else if !download && !failNavigation {
		if !result.OverlappingOverlaysBlockedInput {
			t.Fatal("native CEF did not verify overlapping overlays and blocked input")
		}
		if !result.BrowserTabSwitchPassed {
			t.Fatal("native CEF did not verify hiding and restoration after switching tabs")
		}
		if !result.WorkspaceDirectoryPickerCanceled {
			t.Fatal("DSH did not verify cancellation of the native workspace folder picker")
		}
		if len(result.BackingFramesAfterWindowResize) != 2 {
			t.Fatal("native CEF backing pixels were not qualified after growing and restoring the window")
		}
		screenshots = append(screenshots, "mac-preview-cover.png", "mac-preview-hidden.png", "mac-preview-reopened.png")
	}
	for _, name := range screenshots {
		file, e := os.Open(filepath.Join(out, name))
		if e != nil {
			t.Fatal(e)
		}
		image, e := png.Decode(file)
		file.Close()
		if e != nil || image.Bounds().Dx() < 100 || image.Bounds().Dy() < 100 {
			t.Fatal("actual AppKit owned-view render", name, e)
		}
	}
	if crashDownload {
		crashStageRemoved = true
	}
	var quitErr error
	if downloadStopUI {
		var retained daemon.Snapshot
		if err := client.JSON(ctx, "/snapshot", nil, &retained); err != nil || retained.PID != int(pid) || retained.Status.State != lifecycle.StateReady {
			t.Fatalf("owned workbench stop changed the live daemon: PID=%d state=%s (%v)", retained.PID, retained.Status.State, err)
		}
	}
	if downloadQuit == "" || downloadQuit == "back" || downloadStopUI {
		quitCtx, stop := context.WithTimeout(ctx, 20*time.Second)
		quitErr = client.Call(quitCtx, "HostService", "RequestQuit", "workspace", nil, nil)
		stop()
	}
	select {
	case <-background.done:
		if background.err != nil {
			t.Fatal("actual normal daemon exit", background.err, quitErr)
		}
	case <-ctx.Done():
		t.Fatal("actual daemon join", ctx.Err(), quitErr)
	}
	proof := map[string]any{"entry": app, "daemonPID": pid, "daemonStarted": started, "actualUIExitedNormally": true, "actualDaemonExitedNormally": true, "normalCloseInstanceReturned": result.CloseInstanceReturned, "downloadBrokerKilled": brokerKilled, "sameWindowEvidence": "AppKit-offscreen-owned-view-tree; no global screenshot permission requested", "qualification": "single-normal-product-consumer; no fault or Agent permission claim"}
	if manualOverwrite {
		proof["nativeSaveAndOverwrite"] = true
	}
	if indicatorDependencies != "" {
		proof["managedAgentIndicator"] = result.AgentIndicatorPassed
		proof["managedAgentIndicatorClicks"] = result.AgentIndicatorClicks
		proof["qualification"] = "locked MCP and indicator in a managed native workbench frame"
	}
	if dockReopen {
		proof["macDockReopen"] = result.DockReopenPassed
		proof["macTrayActions"] = result.TrayActionsPassed
		proof["macTrayMenuTracking"] = result.TrayMenuTrackingPassed
		proof["macNotificationDefaultResponse"] = result.NotificationDefaultResponsePassed
		if manualNotification {
			proof["macNotificationCenterActivation"] = result.ManualNotificationPassed
		}
		proof["macDockDaemonWindows"] = result.DockDaemonWindows
		proof["qualification"] = "AppKit reopen and native tray actions restored the same workbench and browser without showing daemon windows"
	}
	if crashStageRemoved {
		proof["privateDownloadStageRemoved"] = true
		proof["crashedSlotReopened"] = result.CrashedSlotReopened
		proof["qualification"] = "download cleanup, same browser instance recovery and normal application exit after broker kill"
	}
	if contentRisk != "" {
		proof["contentRiskDecision"] = contentRisk
		proof["qualification"] = "native download content confirmation and normal application exit"
	}
	if downloadQuit != "" || downloadWindowClose {
		target := filepath.Join(workspace, "cancel.txt")
		if !downloadExitExercised {
			t.Fatal("fixture never requested close or quit during a live download")
		}
		if downloadQuit == "cancel" {
			if _, err := os.Lstat(target); !os.IsNotExist(err) {
				t.Fatalf("destination exists after cancel and exit: %v", err)
			}
		} else {
			data, err := os.ReadFile(target)
			if err != nil || string(data) != strings.Repeat("d", 128<<10)+nonce {
				t.Fatalf("download did not finish in scenario %s: %v", downloadMode, err)
			}
		}
		stages, err := filepath.Glob(filepath.Join(workspace, ".dsh-download-*"))
		if err != nil || len(stages) != 0 {
			t.Fatalf("private stage still exists in scenario %s: %v (%v)", downloadMode, stages, err)
		}
		if downloadWindowClose {
			proof["downloadCompletedWithWorkbenchHidden"] = true
			proof["qualification"] = "normal workbench close and reopen with an active loopback download"
		} else {
			proof["downloadQuitDecision"] = downloadQuit
			proof["qualification"] = "native quit decision with an active loopback download"
			if downloadStopUI {
				proof["daemonRetainedAfterUIExit"] = true
				proof["qualification"] = "native workbench stop decision and separately retained daemon"
			}
		}
	}
	data, _ := json.MarshalIndent(proof, "", "  ")
	if err = os.WriteFile(filepath.Join(out, "mac-preview-entry.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	complete = true
}

func seedMacPetGeometry(t *testing.T, root string) {
	t.Helper()
	appData := filepath.Join(root, "app")
	folder := filepath.Join(appData, "probe-pets", "pets", "geometry")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"pet.json":     `{"id":"geometry","displayName":"Geometry Pet"}`,
		"dsh-pet.json": `{"schema":"dsh.pet","schemaVersion":1,"id":"geometry","displayName":"Geometry Pet","assets":[{"id":"idle","renderer":"dsh-raster-v1","type":"image","path":"idle.png"}],"tracks":{"idle":{"asset":"idle","frames":[{"index":0,"durationMs":125}]}}}`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	sprite := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	for y := 4; y < 12; y++ {
		for x := 4; x < 12; x++ {
			sprite.SetNRGBA(x, y, color.NRGBA{R: 50, G: 180, B: 200, A: 255})
		}
	}
	file, err := os.Create(filepath.Join(folder, "idle.png"))
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(file, sprite)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	values := settings.DefaultValues()
	values.Notifications.Enabled = false
	selected := pet.StableSourceKey(pet.SourceDshPets, "geometry")
	values.Pet.SelectedKey = &selected
	values.Pet.VisibilityIntent = settings.PetVisibilityVisible
	values.Pet.Position.AnchorX, values.Pet.Position.AnchorY = 0.75, 0.65
	data, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appData, "settings.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}

type macPetFrame struct {
	X, Y, Width, Height                 int
	AreaX, AreaY, AreaWidth, AreaHeight int
	Scale                               float64
}

func (f macPetFrame) workArea() pet.WorkArea {
	return pet.WorkArea{X: f.AreaX, Y: f.AreaY, Width: f.AreaWidth, Height: f.AreaHeight}
}

func waitMacPetFrame(t *testing.T, ctx context.Context, client *daemon.Client, check func(macPetFrame) bool) macPetFrame {
	t.Helper()
	ctx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var frame macPetFrame
	for {
		err := client.Call(ctx, "MacPreviewPet", "Geometry", "workspace", []any{false}, &frame)
		if err == nil && frame.Width > 0 && frame.Height > 0 && (check == nil || check(frame)) {
			return frame
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Pet geometry did not settle: %+v (%v)", frame, err)
		case <-ticker.C:
		}
	}
}

func checkMacPetAnchor(t *testing.T, frame macPetFrame, anchorX, anchorY float64) {
	t.Helper()
	want := pet.ResolvePosition(anchorX, anchorY, frame.Width, frame.Height, frame.Scale, frame.workArea(), 12)
	if frame.X != want.X || frame.Y != want.Y || frame.Width != 240 || frame.Height != 168 {
		t.Fatalf("Pet position uses the wrong work area: frame=%+v want=%+v", frame, want)
	}
}

func checkMacPetPointer(t *testing.T, ctx context.Context, client *daemon.Client) map[string]bool {
	t.Helper()
	ctx, stop := context.WithTimeout(ctx, 12*time.Second)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var result map[string]bool
		err := client.Call(ctx, "MacPreviewPet", "Input", "workspace", nil, &result)
		if err == nil {
			for _, step := range []string{
				"transparentMargin", "outsidePressRetainsPassThrough", "bodyReceivesInput",
				"dragHandleReceivesInput", "dragKeepsCaptureOutsideHandle", "releaseRestoresPassThrough", "cleanupRestoresInput",
				"systemRoutesMarginToWindowBehindPet", "systemRoutesBodyToPet",
			} {
				if !result[step] {
					t.Errorf("native Pet input did not reach the expected state at %s", step)
				}
			}
			return result
		}
		select {
		case <-ctx.Done():
			t.Fatalf("Pet pointer fixture could not use the live hit map: %v", err)
		case <-ticker.C:
		}
	}
}

func checkMacPetClick(t *testing.T, ctx context.Context, client *daemon.Client) {
	t.Helper()
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var clicked bool
		if err := client.Call(ctx, "MacPreviewPet", "Click", "workspace", nil, &clicked); err != nil {
			t.Fatal("owned Pet WebView click", err)
		}
		if clicked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("owned Pet WebView click", ctx.Err())
		case <-ticker.C:
		}
	}
}

func stopMacPetDaemon(t *testing.T, ctx context.Context, client *daemon.Client, done <-chan struct{}) {
	t.Helper()
	ctx, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	if err := client.Call(ctx, "HostService", "RequestQuit", "workspace", nil, nil); err != nil {
		t.Fatal("request Pet daemon quit", err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("join Pet daemon", ctx.Err())
	}
}

func exerciseMacDownloadWindowClose(t *testing.T, ctx context.Context, client, uiClient *daemon.Client, out, nonce string, release chan struct{}, networkCanceled, daemonDone, uiDone <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := uiClient.JSON(ctx, "/close", "workspace", nil); err != nil {
		t.Fatal("close the actual workbench", err)
	}
	waitFor := func(name string) {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			if data, _ := os.ReadFile(filepath.Join(out, name)); string(data) == nonce {
				return
			}
			select {
			case <-daemonDone:
				t.Fatal("daemon exited while waiting for", name)
			case <-uiDone:
				t.Fatal("workbench exited while waiting for", name)
			case <-networkCanceled:
				t.Fatal("workbench close canceled the download")
			case <-ctx.Done():
				t.Fatal(name, ctx.Err())
			case <-ticker.C:
			}
		}
	}
	waitFor("mac-preview-download-window-hidden")
	var state daemon.Snapshot
	if err := client.JSON(ctx, "/snapshot", nil, &state); err != nil || state.Status.State != lifecycle.StateReady {
		t.Fatalf("daemon did not stay Ready during workbench close: %s (%v)", state.Status.State, err)
	}
	close(release)
	waitFor("mac-preview-download-window-complete")
	if _, err := os.Lstat(filepath.Join(out, "mac-preview-download-quit-decision")); !os.IsNotExist(err) {
		t.Fatal("a shutdown decision was made on window close", err)
	}
	var reopened uiOpenResult
	if err := uiClient.JSON(ctx, "/open", uiOpenRequest{Window: "workspace"}, &reopened); err != nil || reopened.Window != "workspace" {
		t.Fatalf("reopen the same workbench: %q (%v)", reopened.Window, err)
	}
}

func exerciseMacDownloadQuit(t *testing.T, ctx context.Context, client, uiClient *daemon.Client, out, nonce, choice string, release chan struct{}, networkCanceled, daemonDone, uiDone <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var result chan error
	if uiClient == nil {
		result = make(chan error, 1)
		go func() { result <- client.Call(ctx, "HostService", "Quit", "workspace", nil, nil) }()
	} else if err := uiClient.JSON(ctx, "/stop", nil, nil); err != nil {
		t.Fatal("request owned workbench stop", err)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	decision := func() bool {
		data, _ := os.ReadFile(filepath.Join(out, "mac-preview-download-quit-decision"))
		return string(data) == nonce
	}
decisionReady:
	for {
		if decision() {
			break
		}
		select {
		case err := <-result:
			if !decision() {
				t.Fatalf("quit returned before a native download choice: %v", err)
			}
			result <- err
			break decisionReady
		case <-ctx.Done():
			t.Fatal("native download choice did not appear", ctx.Err())
		case <-ticker.C:
		}
	}
	awaitQuit := func() error {
		select {
		case err := <-result:
			return err
		case <-ctx.Done():
			t.Fatal("quit request did not complete", ctx.Err())
			return ctx.Err()
		}
	}
	if choice == "back" {
		if result != nil {
			if err := awaitQuit(); err == nil || err.Error() != embeddedbrowser.ErrDownloadShutdownDeclined.Error() {
				t.Fatalf("Keep Working completed quit: %v", err)
			}
		}
		var current daemon.Snapshot
		if err := client.JSON(ctx, "/snapshot", nil, &current); err != nil || current.Status.State != lifecycle.StateReady {
			t.Fatalf("daemon did not remain ready after Keep Working: %s (%v)", current.Status.State, err)
		}
	}
	if choice != "cancel" {
		hold := time.NewTimer(250 * time.Millisecond)
		defer hold.Stop()
		select {
		case <-daemonDone:
			t.Fatal("daemon exited before the active download was released")
		case <-uiDone:
			t.Fatal("UI exited before the active download was released")
		case <-networkCanceled:
			t.Fatal("active download canceled while its completion was expected")
		case err := <-result:
			t.Fatalf("quit returned before the active download was released: %v", err)
		case <-hold.C:
		case <-ctx.Done():
			t.Fatal("download quit observation expired", ctx.Err())
		}
		close(release)
	}
	if choice != "back" && result != nil {
		if err := awaitQuit(); err != nil {
			t.Fatal("quit after native download choice", err)
		}
	}
}
