//go:build darwin && cgo && e67presenter && e67previewfixture && !production && !server

package desktopapp

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/localipc"
	"github.com/wailsapp/wails/v3/pkg/application"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed embedded_preview_consumer.js
var macPreviewConsumerJS string

//go:embed embedded_preview_shell.js
var macPreviewShellJS string

type macPreviewScenario struct {
	Nonce     string `json:"nonce"`
	URL       string `json:"url"`
	Workspace string `json:"workspace"`
	Evidence  string `json:"evidence"`
}

var macPreviewScenarioOnce sync.Once
var macPreviewScenarioValue *macPreviewScenario
var macPreviewScenarioError error

func macPreviewConfig() (*macPreviewScenario, error) {
	macPreviewScenarioOnce.Do(func() {
		path := os.Getenv("DSH_WORK_MAC_PREVIEW_SCENARIO")
		if path == "" {
			return
		}
		root := embeddedDevelopmentRoot()
		if os.Getenv("CI") != "true" || root == "" || filepath.Clean(path) != filepath.Join(root, "scenario.json") {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 8192 {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		data, err := os.ReadFile(path)
		var cfg macPreviewScenario
		if err != nil || json.Unmarshal(data, &cfg) != nil || len(cfg.Nonce) < 16 || len(cfg.Nonce) > 128 || filepath.Clean(cfg.Workspace) != filepath.Join(root, "workspace") || !filepath.IsAbs(cfg.Evidence) {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		parsed, err := url.Parse(cfg.URL)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil || parsed.Path != "/" || parsed.RawQuery != "" || parsed.Fragment != "" {
			macPreviewScenarioError = errors.New("MAC_PREVIEW_SCENARIO_REJECTED")
			return
		}
		macPreviewScenarioValue = &cfg
	})
	return macPreviewScenarioValue, macPreviewScenarioError
}
func previewScript(source string, cfg *macPreviewScenario) string {
	data, _ := json.Marshal(struct {
		Nonce     string `json:"nonce"`
		URL       string `json:"url"`
		Workspace string `json:"workspace"`
	}{cfg.Nonce, cfg.URL, cfg.Workspace})
	return strings.Replace(source, "__MAC_PREVIEW_CONFIG__", string(data), 1)
}
func configureEmbeddedPreviewAssets(surface *desktopbridge.Surface) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return
	}
	surface.Assets = func(bridge *desktopbridge.Bridge) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				bridge.Assets(w, r)
				return
			}
			response := httptest.NewRecorder()
			bridge.Assets(response, r)
			for k, values := range response.Header() {
				for _, value := range values {
					w.Header().Add(k, value)
				}
			}
			body := strings.Replace(response.Body.String(), "</body>", "<script type=\"module\">"+previewScript(macPreviewConsumerJS, cfg)+"</script></body>", 1)
			w.Header().Del("Content-Length")
			w.WriteHeader(response.Code)
			_, _ = io.WriteString(w, body)
		})
	}
}
func installEmbeddedPreviewConsumer(app *application.App, window application.Window, service *desktopclient.EmbeddedBrowserService) {
	cfg, err := macPreviewConfig()
	if err != nil || cfg == nil {
		return
	}
	type report struct{ Stage, Nonce, Slot, Session, Step, WorkerOrigin, ShellOrigin string }
	reports := make(chan report, 16)
	connections := make(chan *application.StreamConn, 1)
	var accept sync.Once
	app.HandleStream("mac-preview-consumer", func(c *application.StreamConn) {
		if c.Window() == nil || c.Window().ID() != window.ID() {
			return
		}
		accepted := false
		accept.Do(func() { accepted = true; connections <- c })
		if !accepted {
			return
		}
		for {
			data, err := c.Receive()
			if err != nil {
				return
			}
			if len(data) > 8192 {
				return
			}
			var value report
			if json.Unmarshal(data, &value) != nil || value.Nonce != cfg.Nonce {
				return
			}
			select {
			case reports <- value:
			default:
				return
			}
		}
	})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
		defer cancel()
		ctx = context.WithValue(ctx, application.WindowKey, window)
		result := map[string]any{"qualification": "actual-Wails/DSH-native-view-normal-consumer", "screenshotSource": "AppKit-offscreen-actual-owned-NSView-tree-plus-CEF-CALayer", "globalScreenCapture": "not-requested", "passed": false, "closeInstanceReturned": false}
		save := func(name string, value any) error {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(cfg.Evidence, name+".json"), append(data, '\n'), 0600)
		}
		fail := func(stage string, err error) {
			result["failureStage"] = stage
			result["failureCode"] = fmt.Sprint(err)
			_ = save("mac-preview-consumer", result)
			app.Quit()
		}
		if err := os.MkdirAll(cfg.Evidence, 0700); err != nil {
			return
		}
		script := previewScript(macPreviewShellJS, cfg)
		var connection *application.StreamConn
		for connection == nil {
			window.ExecJS(script)
			select {
			case connection = <-connections:
			case <-ctx.Done():
				fail("shell-runtime", ctx.Err())
				return
			case <-time.After(250 * time.Millisecond):
			}
		}
		defer connection.Close()
		await := func(stage string) (report, error) {
			for {
				select {
				case value := <-reports:
					if value.Stage == "shell-ready" {
						if err := save("mac-preview-shell-boot", map[string]string{"shellOrigin": value.ShellOrigin, "entry": "actual-Wails-Stream-window"}); err != nil {
							return value, err
						}
						if value.ShellOrigin == "" || value.ShellOrigin == "null" {
							return value, errors.New("MAC_PREVIEW_SHELL_ORIGIN_OPAQUE")
						}
					}
					if value.Stage == "failure" {
						return value, fmt.Errorf("MAC_PREVIEW_DOM_FAILED_%s", value.Step)
					}
					if value.Stage == stage {
						return value, nil
					}
				case <-ctx.Done():
					return report{}, ctx.Err()
				}
			}
		}
		requested, err := await("slot-requested")
		if err != nil {
			fail("real-DSH-slot", err)
			return
		}
		if !strings.HasPrefix(requested.Session, "session:session-") || !strings.HasPrefix(requested.Slot, strings.TrimPrefix(requested.Session, "session:")+"/") || requested.WorkerOrigin == "" || requested.WorkerOrigin == "null" || requested.ShellOrigin == "" || requested.ShellOrigin == "null" || requested.WorkerOrigin == requested.ShellOrigin {
			fail("actual-session-origin", errors.New("MAC_PREVIEW_REAL_SESSION_REQUIRED"))
			return
		}
		result["actualSession"] = requested.Session
		result["slot"] = requested.Slot
		result["workerOrigin"] = requested.WorkerOrigin
		result["shellOrigin"] = requested.ShellOrigin
		var state embeddedbrowser.ViewState
		wait := func(check func() bool) error {
			for {
				if check() {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
		err = wait(func() bool {
			var e error
			state, e = service.State(ctx, requested.Slot)
			return e == nil && state.State == "ready" && state.Visible && state.URL == cfg.URL && state.Document != "" && state.Frame != "" && state.Lease != ""
		})
		if err != nil {
			fail("native-slot-ready", err)
			return
		}
		before := state
		result["before"] = before
		var native map[string]any
		err = wait(func() bool {
			var e error
			native, e = desktopclient.MacPreviewObserve(window, before.Lease)
			return e == nil && native["visible"] == true && native["nativeHitTestIsCEF"] == true && native["paintSequence"].(float64) > 0
		})
		if err != nil {
			fail("actual-native-view", err)
			return
		}
		native["pid"] = os.Getpid()
		started, e := localipc.ProcessStarted(uint32(os.Getpid()))
		if e != nil {
			fail("actual-native-process-instance", e)
			return
		}
		native["started"] = started
		if err = save("mac-preview-native-before", native); err != nil {
			fail("native-before-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewInteract(window, before.Lease); err != nil {
			fail("actual-NSEvent-input", err)
			return
		}
		type pageState struct {
			Nonce, Input        string
			Clicks, Loads, Tick int
		}
		httpClient := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		defer httpClient.CloseIdleConnections()
		readPage := func() (pageState, error) {
			var value pageState
			request, e := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL+"report", nil)
			if e != nil {
				return value, e
			}
			response, e := httpClient.Do(request)
			if e != nil {
				return value, e
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				return value, errors.New("MAC_PREVIEW_PAGE_OBSERVATION_FAILED")
			}
			e = json.NewDecoder(io.LimitReader(response.Body, 8192)).Decode(&value)
			return value, e
		}
		var changed pageState
		err = wait(func() bool {
			var e error
			changed, e = readPage()
			return e == nil && changed.Nonce == cfg.Nonce && changed.Input == "mac-ui-dirty" && changed.Clicks == 1 && changed.Loads == 1
		})
		if err != nil {
			fail("input-page-readback", err)
			return
		}
		result["afterPhysicalInput"] = changed
		originalPaint := native["paintSequence"].(float64)
		err = wait(func() bool {
			value, e := desktopclient.MacPreviewObserve(window, before.Lease)
			if e == nil && value["visible"] == true && value["paintSequence"].(float64) > originalPaint {
				native = value
				return true
			}
			return false
		})
		if err != nil {
			fail("physical-input-new-paint", err)
			return
		}
		if err = save("mac-preview-native-after-input", native); err != nil {
			fail("native-input-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-visible.png")); err != nil {
			fail("AppKit-visible-render", err)
			return
		}
		command := func(value string) error {
			data, _ := json.Marshal(map[string]string{"nonce": cfg.Nonce, "command": value})
			return connection.TrySend(data)
		}
		if err = command("hide"); err != nil {
			fail("collapse-command", err)
			return
		}
		if _, err = await("hidden-requested"); err != nil {
			fail("collapse-real-sidebar", err)
			return
		}
		err = wait(func() bool {
			var e error
			state, e = service.State(ctx, requested.Slot)
			return e == nil && !state.Visible && state.Document == before.Document && state.Target == before.Target
		})
		if err != nil {
			fail("native-hide-ACK", err)
			return
		}
		result["hidden"] = state
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != false {
			fail("actual-hidden-native-view", errors.New("MAC_PREVIEW_NATIVE_HIDE_NOT_OBSERVED"))
			return
		}
		if err = save("mac-preview-native-hidden", native); err != nil {
			fail("native-hidden-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-hidden.png")); err != nil {
			fail("AppKit-hidden-render", err)
			return
		}
		if err = command("show"); err != nil {
			fail("reopen-command", err)
			return
		}
		if _, err = await("reopened-requested"); err != nil {
			fail("reopen-real-sidebar", err)
			return
		}
		err = wait(func() bool {
			var e error
			state, e = service.State(ctx, requested.Slot)
			return e == nil && state.State == "ready" && state.Visible && state.BrokerGeneration == before.BrokerGeneration && state.Target == before.Target && state.Frame == before.Frame && state.Document == before.Document && state.Lease == before.Lease
		})
		if err != nil {
			fail("same-document-reopen", err)
			return
		}
		result["after"] = state
		err = wait(func() bool {
			value, e := readPage()
			if e == nil && value.Nonce == cfg.Nonce && value.Input == changed.Input && value.Clicks == changed.Clicks && value.Loads == 1 && value.Tick > changed.Tick {
				result["afterReopen"] = value
				return true
			}
			return false
		})
		if err != nil {
			fail("same-live-page-readback", err)
			return
		}
		native, err = desktopclient.MacPreviewObserve(window, before.Lease)
		if err != nil || native["visible"] != true || native["nativeHitTestIsCEF"] != true {
			fail("actual-reopened-native-view", errors.New("MAC_PREVIEW_NATIVE_REOPEN_NOT_OBSERVED"))
			return
		}
		if err = save("mac-preview-native-reopened", native); err != nil {
			fail("native-reopen-evidence", err)
			return
		}
		if err = desktopclient.MacPreviewRender(window, before.Lease, filepath.Join(cfg.Evidence, "mac-preview-reopened.png")); err != nil {
			fail("AppKit-reopened-render", err)
			return
		}
		// The same normal CloseInstance operation used by ExitUI completes while
		// Cocoa still pumps, before this process is asked to quit.
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		err = service.Client.Call(cleanup, "EmbeddedBrowserService", "CloseInstance", window.Name(), nil, nil)
		stop()
		if err != nil {
			fail("normal-instance-close", err)
			return
		}
		result["closeInstanceReturned"] = true
		result["passed"] = true
		if err = save("mac-preview-consumer", result); err != nil {
			fail("evidence-write", err)
			return
		}
		app.Quit()
	}()
}
