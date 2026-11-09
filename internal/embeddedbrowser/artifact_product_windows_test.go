//go:build windows

package embeddedbrowser_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
)

// Explicit independent fixture: actual owned system selection, registry slot,
// Service authorization and locked MCP. It never uses a model or live app.
func TestLiveArtifactProductSelection(t *testing.T) {
	exe, deps, script := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES"), os.Getenv("DSH_WORK_ARTIFACT_PICKER_SCRIPT")
	if exe == "" || deps == "" || script == "" {
		t.Skip("requires explicit native component, locked MCP and scoped self-fixture helper")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("", "dsh-e76-artifact-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	for name, body := range map[string]string{
		"index.html": `<!doctype html><title>Own artifact product</title><link rel="stylesheet" href="style.css"><label>Message<input aria-label="Message"></label><button onclick="document.querySelector('output').textContent=document.querySelector('input').value">Mark</button><output>ready</output><script src="main.js"></script>`,
		"style.css":  `output{color:rgb(12,34,56)}`, "main.js": `window.bundleLoaded=true;document.querySelector('output').textContent='relative-js-ready'`,
	} {
		if err = os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	parent, peer, closeParent := policyParent(t)
	defer closeParent()
	acquire := func(dir string) (io.Closer, error) {
		return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
	}
	store, err := embeddedbrowser.OpenInstanceStore(filepath.Join(root, "instances"), acquire)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var broker *embeddedbrowser.Broker
	adapter := &policyAdapter{Adapter: win.NewJobObjectAdapter()}
	reg, err := embeddedbrowser.NewInstancePresenterRegistry(embeddedbrowser.InstanceRegistryConfig{Store: store, AcquireProfile: acquire, StartBroker: func(c context.Context, i embeddedbrowser.InstanceConfig) (*embeddedbrowser.Broker, error) {
		var e error
		broker, e = embeddedbrowser.Start(c, embeddedbrowser.Config{Executable: exe, Profile: i.Profile, Supervisor: adapter, ObserveProtocol: func(direction, method string) {
			if direction == "renderer-census" {
				t.Log(direction, method)
			}
		}})
		return broker, e
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := reg.Close(); e != nil {
			t.Error(e)
		}
		if broker != nil {
			t.Logf("native exit=%+v cleanupPending=%v", adapter.worker.ExitResult(), broker.CleanupPending())
		}
	}()
	admission, err := reg.RegisterInstanceWindow(ctx, peer, "artifact-fixture", "window", parent)
	if err != nil {
		t.Fatal(err)
	}
	selectFile := func() embeddedbrowser.ViewState {
		t.Helper()
		type answer struct {
			state embeddedbrowser.ViewState
			err   error
		}
		done := make(chan answer, 1)
		go func() {
			v, e := reg.OpenArtifact(ctx, peer, "window", parent, admission.Lease, "session/artifact/tab")
			done <- answer{v, e}
		}()
		command := exec.CommandContext(ctx, `C:/Program Files/PowerShell/7/pwsh.exe`, "-NoProfile", "-File", script, "-FixturePid", fmt.Sprint(peer.PID), "-Started", fmt.Sprint(peer.Started), "-Owner", fmt.Sprint(parent), "-Action", "select", "-OwnTempFile", filepath.Join(root, "index.html"), "-OwnTempRoot", root, "-ExpectedTitle", "打开 HTML（允许读取所在文件夹内的资源）")
		command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, e := command.CombinedOutput()
		t.Log(string(out))
		if e != nil {
			cancel()
			t.Fatal(e)
		}
		result := <-done
		if result.err != nil {
			t.Logf("graph after open failure: %+v", broker.FrameGraph())
			t.Fatal(result.err)
		}
		return result.state
	}
	state := selectFile()
	if state.Visible || state.ArtifactName != "index.html" {
		t.Fatal("selection prematurely visible or missing local source", state)
	}
	state, err = reg.Update(ctx, peer, "window", parent, admission.Lease, "session/artifact/tab", "", embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	for state.State != "ready" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
		state, err = reg.State(ctx, peer, "window", "session/artifact/tab")
		if err != nil {
			t.Fatal(err)
		}
	}
	target, err := reg.OwnedWindowViewTarget(peer, "window", parent, admission.Lease, "session/artifact/tab", state.Lease)
	if err != nil {
		t.Fatal(err)
	}
	before, err := target.OwnedIdentity(ctx)
	if err != nil || before.ArtifactLease == "" || !before.ViewVisible {
		t.Fatal(before, err)
	}
	manager, err := settings.New(settings.Config{Path: filepath.Join(root, "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	service := automation.New(manager, installedBrowser{deps: deps})
	defer service.Close()
	prompts := make(chan automation.BrowserConsent, 8)
	service.EnableBrowserGateway(automation.BrowserGatewayConfig{ExtensionSource: "not-an-extension", Notify: func(p automation.BrowserConsent) { prompts <- p }})
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	service.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{Service: service, Supervisor: win.NewJobObjectAdapter(), Root: filepath.Join(root, "runtime"), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}}), browserruntime.ToolSchemas())
	service.Worker("artifact-worker", true, true)
	service.Gateway("artifact-worker", true)
	if _, err = service.SetEnabled(ctx, automation.Browser, true); err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetDefaultBrowser(ctx, true); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Retry(automation.Browser); err != nil {
		t.Fatal(err)
	}
	for service.Snapshot().Browser.Dependency != "prepared" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	request := automation.Request{Generation: "artifact-worker", Epoch: service.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: "artifact-session", Lifetime: "one", Turn: "one", Title: "Own artifact fixture"}
	if _, err = service.BrowserTurn(request); err != nil {
		t.Fatal(err)
	}
	if err = service.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: request.Generation, Session: request.Session, Lifetime: request.Lifetime}, target); err != nil {
		t.Fatal(err)
	}
	call := func(tool string, args map[string]any) any {
		t.Helper()
		q := request
		q.Tool = tool
		q.Arguments = args
		type result struct {
			d automation.BrowserDecision
			e error
		}
		ready := make(chan result, 1)
		go func() { d, e := service.AuthorizeBrowser(ctx, q); ready <- result{d, e} }()
		for {
			select {
			case p := <-prompts:
				if p.Session != request.Session || p.AllowAlways {
					t.Fatal("artifact consent scope", p)
				}
				if _, e := service.AnswerBrowserConsent(ctx, p.ID, "allow"); e != nil {
					t.Fatal(e)
				}
			case r := <-ready:
				if r.e != nil {
					t.Fatal(r.e)
				}
				q.Epoch = r.d.Epoch
				q.Permit = r.d.Permit
				value, e := service.Execute(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				return value
			case <-ctx.Done():
				t.Fatal(ctx.Err())
				return nil
			}
		}
	}
	initial, _ := json.Marshal(call("browser_snapshot", map[string]any{}))
	if !strings.Contains(string(initial), "relative-js-ready") {
		t.Fatal("relative JS unavailable", string(initial))
	}
	call("browser_type", map[string]any{"target": "getByRole('textbox', {name:'Message'})", "text": "same-artifact-document"})
	call("browser_click", map[string]any{"target": "getByRole('button', {name:'Mark',exact:true})"})
	after, _ := json.Marshal(call("browser_snapshot", map[string]any{}))
	if !strings.Contains(string(after), "same-artifact-document") {
		t.Fatal(string(after))
	}
	identity, err := target.OwnedIdentity(ctx)
	if err != nil || identity.Document != before.Document {
		t.Fatal("not same artifact document", identity, err)
	}
	t.Log("actual Host picker -> managed instance slot -> explicit Service artifact principal -> locked MCP modified same document; relative JS loaded")
	if err = reg.Detach(ctx, peer, "window", "session/artifact/tab"); err != nil {
		t.Fatal(err)
	}
	if broker.ArtifactCount() != 0 {
		t.Fatal("artifact lease/root retained after tab close")
	}
	if _, err = target.OwnedIdentity(ctx); err == nil {
		t.Fatal("old artifact identity survived close")
	}
	t.Log("actual tab close drained artifact context/resources; old identity rejected")
	t.Logf("graph after close: %+v", broker.FrameGraph())
	fresh := selectFile()
	fresh, err = reg.Update(ctx, peer, "window", parent, admission.Lease, "session/artifact/tab", "", embeddedbrowser.SlotBounds{Width: 700, Height: 450}, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	for fresh.State != "ready" {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
		fresh, err = reg.State(ctx, peer, "window", "session/artifact/tab")
		if err != nil {
			t.Fatal(err)
		}
	}
	freshTarget, err := reg.OwnedWindowViewTarget(peer, "window", parent, admission.Lease, "session/artifact/tab", fresh.Lease)
	if err != nil {
		t.Fatal(err)
	}
	freshID, err := freshTarget.OwnedIdentity(ctx)
	if err != nil || freshID.ArtifactLease == before.ArtifactLease || freshID.ArtifactOrigin == before.ArtifactOrigin || freshID.Target == before.Target {
		t.Fatal("reopen reused artifact identity", freshID, err)
	}
	if err = reg.RetireDocument(ctx, peer, "window", parent, admission.Lease); err != nil {
		t.Fatal(err)
	}
	if broker.ArtifactCount() != 0 {
		t.Fatal("document retirement retained root")
	}
	saved, err := reg.State(ctx, peer, "window", "session/artifact/tab")
	if err != nil || saved.State != "NeedsReload" {
		t.Fatal(saved, err)
	}
	if _, err = reg.Recover(ctx, peer, "window", parent, admission.Lease, "session/artifact/tab", ""); err == nil || !strings.Contains(err.Error(), "ARTIFACT_RESELECT_REQUIRED") {
		t.Fatal("descriptor revived old file authority", err)
	}
	t.Log("independent reopen used new lease/origin/document; shell document retirement revoked root; generic Recover cannot resurrect file authority")
	pickerCtx, stopPicker := context.WithCancel(ctx)
	pickerDone := make(chan error, 1)
	go func() {
		_, e := reg.OpenArtifact(pickerCtx, peer, "window", parent, admission.Lease, "canceled/artifact/tab")
		pickerDone <- e
	}()
	command := exec.CommandContext(ctx, `C:/Program Files/PowerShell/7/pwsh.exe`, "-NoProfile", "-File", script, "-FixturePid", fmt.Sprint(peer.PID), "-Started", fmt.Sprint(peer.Started), "-Owner", fmt.Sprint(parent), "-Action", "observe", "-OwnTempFile", filepath.Join(root, "index.html"), "-OwnTempRoot", root, "-ExpectedTitle", "打开 HTML（允许读取所在文件夹内的资源）")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, e := command.CombinedOutput()
	t.Log(string(out))
	stopPicker()
	if e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-pickerDone:
		if e == nil {
			t.Fatal("canceled picker admitted file")
		}
	case <-ctx.Done():
		t.Fatal("picker did not drain", ctx.Err())
	}
	if broker.ArtifactCount() != 0 {
		t.Fatal("canceled picker created lease")
	}
	t.Log("actual pending Host file selection canceled and STA drained without a root/partition lease")
}
