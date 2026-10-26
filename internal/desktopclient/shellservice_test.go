package desktopclient

import (
	"context"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
	"github.com/local/dsh-work/internal/dshinteraction"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/pet"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type namedWindow struct {
	application.Window
	name string
}

func (w namedWindow) Name() string { return w.name }

func TestShellServiceOpensOnlyMenuSectionsFromTrustedWindows(t *testing.T) {
	opened := make(chan string, 4)
	s := &ShellService{Open: func(section string) { opened <- section }}
	trusted := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	if err := s.OpenSettings(context.Background(), "about"); err == nil {
		t.Fatal("opened Settings without a trusted window")
	}
	if err := s.OpenSettings(context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "worker"})), "about"); err == nil {
		t.Fatal("opened Settings from the Worker window")
	}
	if err := s.OpenSettings(trusted, "unknown"); err == nil {
		t.Fatal("opened a section outside the menu")
	}
	for _, section := range []string{"overview", "settings", "notifications", "pets", "runtimes", "profiles", "plugins", "data-directories", "diagnostics", "about"} {
		if err := s.OpenSettings(trusted, section); err != nil {
			t.Fatal(err)
		}
		select {
		case got := <-opened:
			if got != section {
				t.Fatalf("opened %q, want %q", got, section)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s was not opened", section)
		}
	}
	select {
	case got := <-opened:
		t.Fatalf("rejected request still opened %q", got)
	default:
	}
}

func TestShellServicePetStateFromTrustedWindows(t *testing.T) {
	var requested []*bool
	s := &ShellService{Pet: func(_ context.Context, visible *bool) (app.PetPanel, error) {
		requested = append(requested, visible)
		shown := visible != nil && *visible
		state := pet.VisibilityHidden
		if shown {
			state = pet.VisibilityVisible
		}
		return app.PetPanel{Runtime: pet.RuntimeState{SelectionStatus: pet.SelectionReady, EffectiveVisibility: state}}, nil
	}}
	if _, err := s.GetPet(context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "worker"}))); err == nil {
		t.Fatal("read pet state from the Worker window")
	}
	trusted := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	got, err := s.GetPet(trusted)
	if err != nil || got != (ShellPet{Ready: true, Visible: false}) {
		t.Fatalf("GetPet = %+v, %v", got, err)
	}
	got, err = s.SetPetVisible(trusted, true)
	if err != nil || got != (ShellPet{Ready: true, Visible: true}) {
		t.Fatalf("SetPetVisible = %+v, %v", got, err)
	}
	if len(requested) != 2 || requested[0] != nil || requested[1] == nil || !*requested[1] {
		t.Fatalf("requests = %v", requested)
	}
}

func TestShellServiceSavesZoomFromTrustedWindows(t *testing.T) {
	var saved []float64
	s := &ShellService{SaveZoomLevel: func(_ context.Context, zoom float64) error { saved = append(saved, zoom); return nil }}
	if err := s.SaveZoom(context.Background(), 1.1); err == nil {
		t.Fatal("saved zoom without a trusted window")
	}
	trusted := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	if err := s.SaveZoom(trusted, 1.1); err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0] != 1.1 {
		t.Fatalf("saved = %v", saved)
	}
}

func TestShellServiceReportsAndResetsInteractionsFromTrustedWindows(t *testing.T) {
	var reported dshinteraction.Report
	var reportSurface, resetSurface, resetInstance string
	reportCalls, resetCalls := 0, 0
	s := &ShellService{
		ReportPendingInteraction: func(_ context.Context, surface string, report dshinteraction.Report) error {
			reportCalls++
			reportSurface, reported = surface, report
			return nil
		},
		ResetPageInteractions: func(_ context.Context, surface, instance string) error {
			resetCalls++
			resetSurface, resetInstance = surface, instance
			return nil
		},
	}
	interaction := dshinteraction.Report{Instance: "page-1", SessionID: "session-1", Kind: "approval", Key: "approval:1", ToolName: "shell.exec", Current: "session-2", Visible: true}
	workspace := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace-2"}))
	worker := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "worker"}))
	if err := s.ReportInteraction(worker, interaction); err == nil {
		t.Fatal("accepted an interaction report from the Worker window")
	}
	if err := s.ResetInteractions(worker, "page-1"); err == nil {
		t.Fatal("accepted an interaction reset from the Worker window")
	}
	if reportCalls != 0 || resetCalls != 0 {
		t.Fatalf("untrusted callbacks were called: reports=%d resets=%d", reportCalls, resetCalls)
	}
	if err := s.ReportInteraction(workspace, interaction); err != nil {
		t.Fatal(err)
	}
	if reportCalls != 1 || reportSurface != "workspace-2" || reported != interaction {
		t.Fatalf("interaction report = %q %+v, calls=%d", reportSurface, reported, reportCalls)
	}
	if err := s.ResetInteractions(workspace, "page-1"); err != nil {
		t.Fatal(err)
	}
	if resetCalls != 1 || resetSurface != "workspace-2" || resetInstance != "page-1" {
		t.Fatalf("interaction reset = %q %q, calls=%d", resetSurface, resetInstance, resetCalls)
	}
}

func TestBuiltinComponentHandshakeStatesAreSharedAndReadOnly(t *testing.T) {
	s := &ShellService{}
	workspace := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	worker := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "worker"}))
	ids := []string{"shell", "account", "pet"}

	initial, err := s.GetBuiltinComponents(workspace)
	if err != nil || len(initial) != len(ids) {
		t.Fatalf("initial = %+v, %v", initial, err)
	}
	for i, item := range initial {
		if item.ID != ids[i] || item.State != "inactive" {
			t.Fatalf("initial[%d] = %+v", i, item)
		}
	}
	if err := s.BeginBuiltinComponents(worker); err == nil {
		t.Fatal("began a component load from the Worker window")
	}
	if err := s.ReportBuiltinComponent(worker, "shell"); err == nil {
		t.Fatal("accepted a handshake from the Worker window")
	}
	if err := s.BeginBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}

	loading, err := s.GetBuiltinComponents(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range loading {
		if item.State != "loading" {
			t.Fatalf("loading state = %+v", loading)
		}
	}
	if err := s.ReportBuiltinComponent(workspace, "unknown"); err == nil {
		t.Fatal("accepted an unknown component")
	}
	if err := s.ReportBuiltinComponent(workspace, "account"); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.GetBuiltinComponents(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].State != "loading" || loaded[1].State != "loaded" || loaded[2].State != "loading" {
		t.Fatalf("after handshake = %+v", loaded)
	}

	loaded[1].State = "inactive"
	again, err := s.GetBuiltinComponents(workspace)
	if err != nil || again[1].State != "loaded" {
		t.Fatalf("caller mutated shared state: %+v, %v", again, err)
	}
	if err := s.ResetBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}
	reset, err := s.GetBuiltinComponents(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reset {
		if item.State != "inactive" {
			t.Fatalf("reset state = %+v", reset)
		}
	}
}

func TestBuiltinComponentHandshakeStatesAreIsolatedByWorkspace(t *testing.T) {
	s := &ShellService{}
	workspace := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	second := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace-2"}))
	if err := s.BeginBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}
	if err := s.BeginBuiltinComponents(second); err != nil {
		t.Fatal(err)
	}
	if err := s.ReportBuiltinComponent(second, "pet"); err != nil {
		t.Fatal(err)
	}
	firstState, _ := s.GetBuiltinComponents(workspace)
	secondState, _ := s.GetBuiltinComponents(second)
	if firstState[2].State != "loading" || secondState[2].State != "loaded" {
		t.Fatalf("pet state crossed windows: workspace=%+v workspace-2=%+v", firstState, secondState)
	}
	if err := s.ResetBuiltinComponents(second); err != nil {
		t.Fatal(err)
	}
	reset, _ := s.GetBuiltinComponents(second)
	firstState, _ = s.GetBuiltinComponents(workspace)
	if reset[2].State != "inactive" || firstState[2].State != "loading" {
		t.Fatalf("reset crossed windows: workspace=%+v workspace-2=%+v", firstState, reset)
	}
}

func TestBuiltinComponentsThatNeverReportAreMarkedFailed(t *testing.T) {
	published := make(chan []BuiltinComponentStatus, 8)
	s := &ShellService{BuiltinComponentTimeout: 20 * time.Millisecond, OnBuiltinComponentsChanged: func(_ string, statuses []BuiltinComponentStatus) { published <- statuses }}
	workspace := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	if err := s.BeginBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}
	if err := s.ReportBuiltinComponent(workspace, "shell"); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case statuses := <-published:
			if statuses[1].State != "failed" {
				continue
			}
			if statuses[0].State != "loaded" || statuses[2].State != "failed" {
				t.Fatalf("after timeout = %+v", statuses)
			}
			// A new frame starts over; the earlier deadline no longer applies.
			if err := s.BeginBuiltinComponents(workspace); err != nil {
				t.Fatal(err)
			}
			restarted, _ := s.GetBuiltinComponents(workspace)
			if restarted[1].State != "loading" {
				t.Fatalf("new load = %+v", restarted)
			}
			return
		case <-deadline:
			t.Fatal("components that never reported stayed loading")
		}
	}
}

func TestUnreportedBuiltinComponentsLogLoadFailure(t *testing.T) {
	captured := captureDefaultLogs(t)
	s := &ShellService{BuiltinComponentTimeout: 10 * time.Millisecond}
	workspace := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	if err := s.BeginBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}
	if err := s.ReportBuiltinComponent(workspace, "shell"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	found := make(map[string]bool)
	for _, record := range captured.snapshot() {
		if record.Message != "host plugin did not load" {
			continue
		}
		attrs := recordAttributes(record)
		plugin := attrs["plugin"].String()
		if attrs["code"].String() != string(lifecycle.ErrorHostPluginLoadFailed) {
			t.Errorf("%s code = %q", plugin, attrs["code"].String())
		}
		found[plugin] = true
	}
	for _, plugin := range []string{"@dsh-work/account", "@dsh-work/pet"} {
		if !found[plugin] {
			t.Errorf("missing load failure record for %s", plugin)
		}
	}
}
