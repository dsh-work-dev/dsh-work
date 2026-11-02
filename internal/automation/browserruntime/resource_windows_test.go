//go:build windows

package browserruntime_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/automation"
	"github.com/local/dsh-work/internal/automation/browserruntime"
	win "github.com/local/dsh-work/internal/platform/windows"
	"github.com/local/dsh-work/internal/settings"
)

type fixtureRoute struct {
	messages chan json.RawMessage
	done     chan struct{}
	once     sync.Once
}

func (r *fixtureRoute) Send(context.Context, json.RawMessage) error { return nil }
func (r *fixtureRoute) Messages() <-chan json.RawMessage            { return r.messages }
func (r *fixtureRoute) Done() <-chan struct{}                       { return r.done }
func (r *fixtureRoute) Err() error                                  { return automation.ErrRevoked }
func (r *fixtureRoute) Close() error                                { r.once.Do(func() { close(r.done) }); return nil }

type fixturePeer struct{ route *fixtureRoute }

func (p fixturePeer) Open(context.Context, string, string) (automation.BrowserRoute, error) {
	p.route.messages <- json.RawMessage(`{"method":"chrome.tabs.onCreated","params":[{"id":1,"url":"about:blank","windowId":1,"groupId":-1}]}`)
	p.route.messages <- json.RawMessage(`{"method":"extension.initialized","params":[]}`)
	return p.route, nil
}

// Real pinned Node SDK/MCP, mutual HMAC pipe and existing Job Object adapter.
// Only schema discovery and pre-dispatch errors; no actual browser is launched.
func TestLiveOwnedMCPContractAndBoundaries(t *testing.T) {
	deps := os.Getenv("DSH_WORK_BROWSER_TEST_DEPENDENCIES")
	if deps == "" {
		t.Skip("set explicit isolated locked dependency tree for live adapter check")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	m, err := settings.New(settings.Config{Path: filepath.Join(t.TempDir(), "settings.json")})
	if err != nil {
		t.Fatal(err)
	}
	s := automation.New(m, nil)
	defer s.Close()
	id, err := s.BrowserConnected("chrome", automation.BrowserHello{Protocol: 1, WireProtocol: 2, ExtensionVersion: "0.2.0", ExtensionID: automation.DevelopmentExtensionID, Instance: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	candidate, _ := s.RequestBrowserBinding(id, "A")
	binding, err := s.ConfirmBrowserBinding(ctx, candidate.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SelectBrowserBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	route := &fixtureRoute{messages: make(chan json.RawMessage, 8), done: make(chan struct{})}
	if err = s.AttachBrowserPeer(id, fixturePeer{route}); err != nil {
		t.Fatal(err)
	}
	factory := browserruntime.NewFactory(browserruntime.Config{Service: s, Supervisor: win.NewJobObjectAdapter(), Root: t.TempDir(), Resolve: func(context.Context) (browserruntime.Runtime, error) {
		return browserruntime.Runtime{Node: node, Dependencies: deps}, nil
	}})
	r, err := factory(ctx, automation.Request{Session: "real-sdk-fixture", Lifetime: "only-this-run", Title: "Fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	largeFile, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"browser_evaluate", map[string]any{"function": "()=>1"}, "BROWSER_TOOL_UNAVAILABLE"},
		{"browser_type", map[string]any{"session": "override"}, "BROWSER_INVALID_ARGUMENT"},
		{"browser_type", map[string]any{"text": strings.Repeat("中", 90000)}, "BROWSER_INPUT_TOO_LARGE"},
		{"browser_drop", map[string]any{"target": "getByRole('textbox')", "paths": []string{largeFile}}, "BROWSER_UPLOAD_TOO_LARGE"},
	} {
		if _, err := r.Execute(ctx, tc.tool, tc.args); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: %v", tc.tool, err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Execute(ctx, "browser_snapshot", map[string]any{}); err == nil || !strings.Contains(err.Error(), "BROWSER_RESOURCE_CLOSED") {
		t.Fatalf("closed resource lost its terminal cause: %v", err)
	}
	select {
	case <-route.done:
	default:
		t.Fatal("resource close retained route")
	}
}
