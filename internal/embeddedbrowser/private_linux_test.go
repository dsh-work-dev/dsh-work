//go:build linux && cgo && e67presenter && e67nativefixture

package embeddedbrowser

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	"github.com/local/dsh-work/internal/localipc"
	linuxplatform "github.com/local/dsh-work/internal/platform/linux"
	"github.com/local/dsh-work/internal/settings"
	"github.com/local/dsh-work/internal/supervisor"
)

func TestMain(m *testing.M) {
	if linuxplatform.RunBootstrap() {
		return
	}
	os.Exit(m.Run())
}

type linuxPublication struct{ deps string }

func (p linuxPublication) Prepare(context.Context, automation.Capability, func(automation.Progress)) error {
	for _, name := range []string{"@playwright/mcp", "@modelcontextprotocol/sdk", "ws"} {
		if _, e := os.Stat(filepath.Join(p.deps, "node_modules", name, "package.json")); e != nil {
			return e
		}
	}
	return nil
}

type linuxObserved struct {
	supervisor.Adapter
	worker supervisor.Worker
}

func (a *linuxObserved) Start(c context.Context, p supervisor.LaunchPlan, h supervisor.RawOutputHandler) (supervisor.Worker, error) {
	w, e := a.Adapter.Start(c, p, h)
	if e == nil {
		a.worker = w
	}
	return w, e
}

// This qualifies the formal native/registry/Service slice, not a DSH UI or
// BrowserLab host. All data/settings/dependencies are explicit isolated inputs.
func TestLiveLinuxManagedPrivateCEFService(t *testing.T) {
	exe, deps := os.Getenv("DSH_WORK_CEF_TEST_EXECUTABLE"), os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if exe == "" || deps == "" {
		t.Skip("requires formal sandboxed CEF component and locked publication")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	parent, closeParent := fixtureParent()
	defer closeParent()
	if parent == 0 {
		t.Fatal("realized GTK X11 parent unavailable")
	}
	started, e := localipc.ProcessStarted(uint32(os.Getpid()))
	if e != nil {
		t.Fatal(e)
	}
	peer := PresenterPeer{PID: uint32(os.Getpid()), Started: started}
	root := t.TempDir()
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><title>Formal Linux managed fixture</title><input aria-label="Unsubmitted input" value="linux-original"><p>Formal managed snapshot</p>`)
	}))
	defer fixture.Close()
	acquire := func(dir string) (io.Closer, error) {
		return app.AcquireManagerProcessLock(filepath.Join(dir, "instance.json"))
	}
	store, e := OpenInstanceStore(filepath.Join(root, "instances"), acquire)
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	nativeAdapter := &linuxObserved{Adapter: linuxplatform.NewSystemdAdapter()}
	var broker *Broker
	registry, e := NewInstancePresenterRegistry(InstanceRegistryConfig{Store: store, AcquireProfile: acquire, StartBroker: func(c context.Context, i InstanceConfig) (*Broker, error) {
		var err error
		broker, err = Start(c, Config{Executable: exe, Profile: i.Profile, Supervisor: nativeAdapter})
		return broker, err
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := registry.Close(); e != nil {
			t.Error("formal registry cleanup", e)
		}
		if broker != nil {
			t.Logf("formal native final exit=%+v pending=%t", nativeAdapter.worker.ExitResult(), broker.CleanupPending())
			if broker.CleanupPending() {
				t.Error("formal native tree not joined")
			}
		}
	}()
	window, e := registry.RegisterInstanceWindow(ctx, peer, "linux-fixture-instance", "window", parent)
	if e != nil {
		t.Fatal(e)
	}
	view, e := registry.Update(ctx, peer, "window", parent, window.Lease, "slot", fixture.URL, SlotBounds{Width: 700, Height: 450}, 1, true)
	if e != nil {
		t.Fatal(e)
	}
	for view.State != "ready" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
		view, e = registry.State(ctx, peer, "window", "slot")
		if e != nil {
			t.Fatal(e)
		}
	}
	target, e := registry.OwnedWindowViewTarget(peer, "window", parent, window.Lease, "slot", view.Lease)
	if e != nil {
		t.Fatal(e)
	}
	identity, e := target.OwnedIdentity(ctx)
	if e != nil || !identity.ViewVisible || !identity.FrameScopeReady {
		t.Fatal("actual native identity", identity, e)
	}
	t.Logf("actual GTK peer=%d startTicks=%d XID=%d brokerPID=%d target=%s document=%s frame=%s", peer.PID, peer.Started, parent, nativeAdapter.worker.Diagnostics().PID, identity.Target, identity.Document, identity.Frame)
	m, e := settings.New(settings.Config{Path: filepath.Join(root, "settings.json")})
	if e != nil {
		t.Fatal(e)
	}
	s := automation.New(m, linuxPublication{deps})
	defer s.Close()
	prompts := make(chan automation.BrowserConsent, 8)
	s.EnableBrowserGateway(automation.BrowserGatewayConfig{Notify: func(p automation.BrowserConsent) { prompts <- p }})
	node, e := exec.LookPath("node")
	if e != nil {
		t.Fatal(e)
	}
	images := make(chan []byte, 1)
	s.ConfigureBrowserBackend(browserruntime.NewFactory(browserruntime.Config{Service: s, Supervisor: linuxplatform.NewSystemdAdapter(), Root: filepath.Join(root, "automation"), Observe: func(_ automation.Request, o browserruntime.Observation) {
		if o.Tool != "browser_take_screenshot" {
			return
		}
		var result struct{ Content []struct{ Type, Data string } }
		if json.Unmarshal(o.RawResult, &result) == nil {
			for _, part := range result.Content {
				if part.Type == "image" {
					if data, err := base64.StdEncoding.DecodeString(part.Data); err == nil {
						images <- data
						return
					}
				}
			}
		}
	}, Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}}), browserruntime.ToolSchemas())
	s.Worker("linux-owned", true, true)
	s.Gateway("linux-owned", true)
	if _, e = s.SetEnabled(ctx, automation.Browser, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetDefaultBrowser(ctx, true); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Retry(automation.Browser); e != nil {
		t.Fatal(e)
	}
	for s.Snapshot().Browser.Dependency != "prepared" {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	request := automation.Request{Generation: "linux-owned", Epoch: s.Snapshot().Browser.Epoch, Capability: automation.Browser, Operation: "browser", Session: "linux-native-fixture", Lifetime: "one", Turn: "one", Tool: "browser_snapshot", Arguments: map[string]any{}}
	if _, e = s.BrowserTurn(request); e != nil {
		t.Fatal(e)
	}
	if e = s.AdmitOwnedBrowser(ctx, automation.OwnedBrowserScope{Generation: request.Generation, Session: request.Session, Lifetime: request.Lifetime}, target); e != nil {
		t.Fatal(e)
	}
	type decision struct {
		value automation.BrowserDecision
		err   error
	}
	ready := make(chan decision, 1)
	go func() { d, e := s.AuthorizeBrowser(ctx, request); ready <- decision{d, e} }()
	for {
		select {
		case p := <-prompts:
			if p.Session != request.Session || (p.Kind == "site" && p.Site != fixture.URL) {
				t.Fatal("isolated consent escaped fixture", p)
			}
			if _, e = s.AnswerBrowserConsent(ctx, p.ID, "allow"); e != nil {
				t.Fatal(e)
			}
		case d := <-ready:
			if d.err != nil {
				t.Fatal(d.err)
			}
			request.Permit = d.value.Permit
			request.Epoch = d.value.Epoch
			goto execute
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
execute:
	value, e := s.Execute(ctx, request)
	data, _ := json.Marshal(value)
	if e != nil || !strings.Contains(string(data), "Formal managed snapshot") || strings.Contains(string(data), `"isError":true`) {
		t.Fatal("actual locked MCP snapshot", string(data), e)
	}
	t.Log("formal locked listTools schema comparison + managed Service snapshot succeeded", string(data))
	request.Tool = "browser_take_screenshot"
	request.Arguments = map[string]any{"type": "png", "scale": "css"}
	request.Permit = ""
	d, e := s.AuthorizeBrowser(ctx, request)
	if e != nil {
		t.Fatal("authorize managed render evidence", e)
	}
	request.Permit, request.Epoch = d.Permit, d.Epoch
	if _, e = s.Execute(ctx, request); e != nil {
		t.Fatal("actual managed screenshot", e)
	}
	select {
	case data := <-images:
		image, err := png.Decode(bytes.NewReader(data))
		if err != nil || image.Bounds().Dx() < 100 || image.Bounds().Dy() < 100 {
			t.Fatal("invalid actual CEF image", err)
		}
		if dir := os.Getenv("DSH_WORK_CEF_TEST_EVIDENCE"); dir != "" {
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(dir, "formal-linux-managed.png")
			if e = os.WriteFile(path, data, 0600); e != nil {
				t.Fatal(e)
			}
			t.Logf("actual managed CEF render %s %dx%d", path, image.Bounds().Dx(), image.Bounds().Dy())
		}
	case <-ctx.Done():
		t.Fatal("missing actual MCP image", ctx.Err())
	}
	if e = registry.Hide(ctx, peer, "window"); e != nil {
		t.Fatal(e)
	}
	after, e := target.OwnedIdentity(ctx)
	if e != nil || after.ViewVisible || after.Document != identity.Document {
		t.Fatal("actual native hide ACK/document", after, e)
	}
	if _, e = s.AuthorizeBrowser(ctx, request); e == nil {
		t.Fatal("hidden view admitted")
	}
	t.Log("actual hidden view rejected without losing document")
	// The Service owns its observer/MCP executor. Release that owner while its
	// broker can still acknowledge revocation/detach, before destroying pages.
	if e = s.StopBrowserUse(request.Session, request.Lifetime); e != nil {
		t.Fatal("managed automation resource cleanup before native close", e)
	}
	if e = registry.CloseInstance(ctx, peer, "window"); e != nil {
		t.Fatal("normal native/frame/tree barrier", e)
	}
	if nativeAdapter.worker.ExitResult().Code != 0 || broker.CleanupPending() {
		t.Fatal("formal CEF did not close normally", nativeAdapter.worker.ExitResult())
	}
}
