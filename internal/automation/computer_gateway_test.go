package automation

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type computerFixture struct {
	mu     sync.Mutex
	calls  []string
	closed bool
	block  chan struct{}
}

func (f *computerFixture) Execute(ctx context.Context, tool string, args map[string]any) (any, error) {
	f.mu.Lock()
	f.calls = append(f.calls, tool)
	f.mu.Unlock()
	switch tool {
	case "list_windows":
		return map[string]any{"structuredContent": map[string]any{"windows": []any{map[string]any{"pid": float64(10), "window_id": float64(20)}, map[string]any{"pid": float64(11), "window_id": float64(21)}}}}, nil
	case "get_window_state":
		return map[string]any{"structuredContent": map[string]any{"snapshot_id": "s00000001", "elements": []any{map[string]any{"element_index": float64(0), "label": "Editor", "role": "Document", "frame": map[string]any{"x": 10.0, "y": 20.0}}}}}, nil
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return map[string]any{"structuredContent": map[string]any{"effect": "confirmed"}}, nil
}
func (f *computerFixture) Close() error { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }
func computerTestService(t *testing.T) (*Service, *computerFixture, *ComputerTarget) {
	t.Helper()
	s := New(nil, nil)
	s.Worker("g", true, true)
	s.Gateway("g", true)
	s.slots[Computer].state.Enabled = true
	s.slots[Computer].state.Dependency = "prepared"
	f := &computerFixture{}
	target := &ComputerTarget{PID: 10, WindowID: 20, Executable: `C:\fixture.exe`, Created: 1, WindowIdentity: "unique", App: "Fixture", Title: "Owned", Level: "full"}
	s.ConfigureComputerGateway(ComputerGatewayConfig{Probe: func(context.Context, ComputerTarget, map[string]any) (ComputerElementIdentity, error) {
		return ComputerElementIdentity{RuntimeID: "fixture"}, nil
	}, Factory: func(context.Context, string) (Resource, error) { return f, nil }, Inspect: func(pid uint32, hwnd uint64) (ComputerTarget, error) {
		if pid == 11 {
			return ComputerTarget{Level: "blocked"}, nil
		}
		if pid != 10 || hwnd != 20 {
			return ComputerTarget{}, errors.New("missing")
		}
		return *target, nil
	}})
	t.Cleanup(s.Close)
	return s, f, target
}
func computerRequest(s *Service, tool string) Request {
	args := map[string]any{}
	if tool != "computer_windows" && tool != "computer_release" {
		args["pid"] = float64(10)
		args["window_id"] = float64(20)
	}
	return Request{Generation: "g", Epoch: s.Snapshot().Computer.Epoch, Capability: Computer, Session: "a", Lifetime: "life", Turn: "turn", Operation: "computer", Tool: tool, Arguments: args, Title: "Fixture conversation"}
}
func computerAuthorize(t *testing.T, s *Service, r Request) BrowserDecision {
	t.Helper()
	d, e := s.AuthorizeComputer(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func computerExecute(t *testing.T, s *Service, r Request, d BrowserDecision) any {
	t.Helper()
	r.Permit = d.Permit
	v, e := s.Execute(context.Background(), r)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func computerGrant(t *testing.T, s *Service, r Request) BrowserDecision {
	t.Helper()
	out := make(chan BrowserDecision, 1)
	errs := make(chan error, 1)
	go func() { d, e := s.AuthorizeComputer(context.Background(), r); out <- d; errs <- e }()
	deadline := time.After(time.Second)
	for {
		v := s.Snapshot()
		if len(v.ComputerAccess.Requests) > 0 {
			if _, e := s.AnswerComputerConsent(context.Background(), v.ComputerAccess.Requests[0].ID, "allow"); e != nil {
				t.Fatal(e)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("missing app consent")
		case <-time.After(time.Millisecond):
		}
	}
	d := <-out
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	return d
}
func TestComputerWindowFilteringAndConsentBeforeContent(t *testing.T) {
	s, f, _ := computerTestService(t)
	r := computerRequest(s, "computer_windows")
	v := computerExecute(t, s, r, computerAuthorize(t, s, r))
	content, _ := computerContent(v)
	if len(content["windows"].([]any)) != 1 {
		t.Fatal(v)
	}
	r = computerRequest(s, "computer_observe")
	d := computerGrant(t, s, r)
	f.mu.Lock()
	if len(f.calls) != 1 {
		t.Fatal("content read before consent", f.calls)
	}
	f.mu.Unlock()
	computerExecute(t, s, r, d)
	if s.computer.observation == nil {
		t.Fatal("no observation")
	}
}
func TestComputerPermitTargetBindingAndUnknownClick(t *testing.T) {
	s, _, target := computerTestService(t)
	r := computerRequest(s, "computer_observe")
	computerExecute(t, s, r, computerGrant(t, s, r))
	click := computerRequest(s, "computer_click")
	click.Arguments["snapshot_id"] = "s00000001"
	click.Arguments["element_index"] = float64(0)
	d := computerAuthorize(t, s, click)
	if d.Kind != "ask" {
		t.Fatal("unknown click was not confirmed", d)
	}
	target.Created = 2
	click.Permit = d.Permit
	if _, e := s.Execute(context.Background(), click); e == nil {
		t.Fatal("recycled process accepted")
	}
	target.Created = 1
	d = computerAuthorize(t, s, click)
	click.Permit = d.Permit
	click.Arguments["element_index"] = float64(1)
	if _, e := s.Execute(context.Background(), click); e == nil {
		t.Fatal("changed permit args accepted")
	}
}
func TestComputerActionConsumesObservationAndStopRevokes(t *testing.T) {
	s, f, _ := computerTestService(t)
	observe := computerRequest(s, "computer_observe")
	computerExecute(t, s, observe, computerGrant(t, s, observe))
	r := computerRequest(s, "computer_type")
	r.Arguments["snapshot_id"] = "s00000001"
	r.Arguments["element_index"] = float64(0)
	r.Arguments["text"] = "marker"
	d := computerAuthorize(t, s, r)
	computerExecute(t, s, r, d)
	if _, e := s.AuthorizeComputer(context.Background(), r); e == nil {
		t.Fatal("stale observation accepted")
	}
	r.Permit = d.Permit
	if _, e := s.Execute(context.Background(), r); e == nil {
		t.Fatal("reused permit")
	}
	s.StopSession("g", "a", "life")
	if _, e := s.AuthorizeComputer(context.Background(), computerRequest(s, "computer_windows")); !errors.Is(e, ErrRevoked) {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		t.Fatal("engine not closed")
	}
}
func TestComputerDesktopLeaseAndStrictArguments(t *testing.T) {
	s, _, _ := computerTestService(t)
	r := computerRequest(s, "computer_windows")
	computerExecute(t, s, r, computerAuthorize(t, s, r))
	other := r
	other.Session = "b"
	if _, e := s.AuthorizeComputer(context.Background(), other); e == nil {
		t.Fatal("two desktop owners")
	}
	bad := computerRequest(s, "computer_type")
	bad.Arguments["snapshot_id"] = "s00000001"
	bad.Arguments["element_index"] = float64(0)
	bad.Arguments["text"] = "marker"
	bad.Arguments["x"] = float64(1)
	if e := validateComputerArguments(bad); e == nil {
		t.Fatal("coordinate injection accepted")
	}
	s.StopSession("g", "a", "life")
	computerExecute(t, s, other, computerAuthorize(t, s, other))
}
func TestComputerStopAndNewTurnAreIndependentOfBrowser(t *testing.T) {
	s, _, _ := computerTestService(t)
	r := computerRequest(s, "computer_windows")
	if _, e := s.ComputerTurn(r); e != nil {
		t.Fatal(e)
	}
	computerExecute(t, s, r, computerAuthorize(t, s, r))
	browser := &computerFixture{}
	ctx, cancel := context.WithCancel(s.root)
	ready := make(chan struct{})
	close(ready)
	entry := &entry{ctx: ctx, cancel: cancel, ready: ready, closed: make(chan struct{}), resource: browser}
	s.mu.Lock()
	s.entries["browser/browser/a/browser-life"] = entry
	s.mu.Unlock()
	if e := s.StopComputerUse("a", "life"); e != nil {
		t.Fatal(e)
	}
	browser.mu.Lock()
	closed := browser.closed
	browser.mu.Unlock()
	if closed {
		t.Fatal("computer stop closed browser")
	}
	if _, e := s.AuthorizeComputer(context.Background(), r); !errors.Is(e, ErrRevoked) {
		t.Fatal("stopped turn accepted", e)
	}
	same, e := s.ComputerTurn(r)
	if e != nil || same["lifetime"] != "life" {
		t.Fatal(same, e)
	}
	r.Turn = "next"
	if _, e := s.ComputerTurn(r); !errors.Is(e, ErrRevoked) {
		t.Fatal("a synthetic/new Agent round revived stopped use", e)
	}
	r.UserTurn = true
	next, e := s.ComputerTurn(r)
	if e != nil || next["lifetime"] == "life" {
		t.Fatal("new actual message did not renew", next, e)
	}
	r.Lifetime = next["lifetime"]
	computerExecute(t, s, r, computerAuthorize(t, s, r))
}
func TestComputerReleaseDropsTemporaryGrantWithoutStoppingConversation(t *testing.T) {
	s, _, _ := computerTestService(t)
	observe := computerRequest(s, "computer_observe")
	computerExecute(t, s, observe, computerGrant(t, s, observe))
	release := computerRequest(s, "computer_release")
	computerExecute(t, s, release, computerAuthorize(t, s, release))
	if len(s.computer.grants) != 0 || s.Snapshot().ComputerAccess.Use != nil {
		t.Fatal("release retained temporary access")
	}
	computerExecute(t, s, observe, computerGrant(t, s, observe))
}
func TestComputerBrowserReadCannotBypassBrowserPolicy(t *testing.T) {
	s, f, target := computerTestService(t)
	target.Executable = filepath.Join("fixture", "chrome.exe")
	r := computerRequest(s, "computer_observe")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, e := s.AuthorizeComputer(ctx, r); e == nil || !strings.Contains(e.Error(), "BROWSER_TOOLS_REQUIRED") {
		t.Fatal(e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 0 || s.Snapshot().ComputerAccess.Use != nil {
		t.Fatal("browser content read or desktop retained")
	}
}
func TestComputerSensitiveKeysAndCaptureBounds(t *testing.T) {
	o := &computerObservation{snapshot: "s", width: 100, height: 80, elements: []map[string]any{{"element_index": float64(0), "label": "Editor"}}}
	r := Request{Tool: "computer_key", Arguments: map[string]any{"element_index": float64(0), "key": "v", "modifiers": []any{"ctrl"}}}
	if _, e := computerCategory(r, ComputerTarget{}, o); e == nil {
		t.Fatal("clipboard shortcut accepted")
	}
	r.Arguments["key"] = "s"
	if category, e := computerCategory(r, ComputerTarget{}, o); e != nil || category != "save" {
		t.Fatal(category, e)
	}
	r.Tool = "computer_click"
	r.Arguments = map[string]any{"x": float64(100), "y": float64(10)}
	if _, e := computerCategory(r, ComputerTarget{}, o); e == nil {
		t.Fatal("outside screenshot accepted")
	}
}
func TestComputerStopCancelsAndWaitsForAllProbeJobs(t *testing.T) {
	for _, cleanupFails := range []bool{false, true} {
		t.Run(fmt.Sprint(cleanupFails), func(t *testing.T) {
			s, _, _ := computerTestService(t)
			released := make(chan struct{})
			s.computer.config.Acquire = func() (func() error, error) {
				var once sync.Once
				return func() error { once.Do(func() { close(released) }); return nil }, nil
			}
			observe := computerRequest(s, "computer_observe")
			computerExecute(t, s, observe, computerGrant(t, s, observe))
			started, cancelled, cleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			finish := func() { once.Do(func() { close(cleanup) }) }
			defer finish()
			s.computer.config.Probe = func(ctx context.Context, _ ComputerTarget, _ map[string]any) (ComputerElementIdentity, error) {
				close(started)
				<-ctx.Done()
				close(cancelled)
				<-cleanup
				if cleanupFails {
					return ComputerElementIdentity{}, errors.New("COMPUTER_HELPER_CLEANUP_FAILED: owned probe job remains")
				}
				return ComputerElementIdentity{}, ctx.Err()
			}
			r := computerRequest(s, "computer_type")
			r.Arguments["snapshot_id"] = "s00000001"
			r.Arguments["element_index"] = float64(0)
			r.Arguments["text"] = "marker"
			authorized := make(chan error, 1)
			go func() { _, e := s.AuthorizeComputer(context.Background(), r); authorized <- e }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("probe did not start")
			}
			stopped := make(chan error, 1)
			go func() { stopped <- s.StopComputerUse("a", "life") }()
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("Stop did not cancel probe")
			}
			select {
			case <-released:
				t.Fatal("desktop released before probe cleanup")
			default:
			}
			if use := s.Snapshot().ComputerAccess.Use; use == nil || !use.Stopping {
				t.Fatal("stopping state missing")
			}
			finish()
			if e := <-authorized; e == nil {
				t.Fatal("cancelled probe approved action")
			}
			e := <-stopped
			if cleanupFails {
				if e == nil || s.Snapshot().ComputerAccess.Use == nil || s.Snapshot().ComputerAccess.Failure != "cleanup" {
					t.Fatal("cleanup failure hidden", e, s.Snapshot().ComputerAccess)
				}
				select {
				case <-released:
					t.Fatal("failed cleanup released desktop")
				default:
				}
			} else {
				if e != nil {
					t.Fatal(e)
				}
				select {
				case <-released:
				default:
					t.Fatal("desktop not released after cleanup")
				}
				if s.Snapshot().ComputerAccess.Use != nil {
					t.Fatal("clean stop retained bar")
				}
			}
		})
	}
}

func TestComputerPermissionDoesNotJumpAnOlderBrowserPrompt(t *testing.T) {
	now := time.Now()
	v := Snapshot{BrowserAccess: &BrowserGatewaySnapshot{Requests: []BrowserConsent{{ID: "browser", Expires: now}}}, ComputerAccess: &ComputerSnapshot{Requests: []ComputerConsent{{ID: "computer", Expires: now.Add(time.Minute)}}}}
	browser, computer := FirstAutomationPermission(v)
	if browser == nil || computer != nil {
		t.Fatal("computer jumped older browser")
	}
	v.ComputerAccess.Requests[0].Expires = now.Add(-time.Minute)
	browser, computer = FirstAutomationPermission(v)
	if computer == nil || browser != nil {
		t.Fatal("older computer prompt not selected")
	}
}
