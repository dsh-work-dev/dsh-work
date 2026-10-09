package desktopclient

import (
	"context"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/app"
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
	for _, section := range []string{"overview", "settings", "notifications", "pets", "runtimes", "profiles", "plugins", "data-directories", "about"} {
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

func TestBuiltinComponentHandshakeStatesAreSharedAndReadOnly(t *testing.T) {
	s := &ShellService{}
	workspace := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "workspace"}))
	settings := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "settings"}))
	worker := context.WithValue(context.Background(), application.WindowKey, application.Window(namedWindow{name: "worker"}))
	ids := []string{"shell", "account", "pet"}

	initial, err := s.GetBuiltinComponents(settings)
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
	if err := s.ReportBuiltinComponent(settings, "shell"); err == nil {
		t.Fatal("accepted a handshake from Settings")
	}
	if err := s.BeginBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}

	loading, err := s.GetBuiltinComponents(settings)
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
	loaded, err := s.GetBuiltinComponents(settings)
	if err != nil {
		t.Fatal(err)
	}
	if loaded[0].State != "loading" || loaded[1].State != "loaded" || loaded[2].State != "loading" {
		t.Fatalf("after handshake = %+v", loaded)
	}

	loaded[1].State = "inactive"
	again, err := s.GetBuiltinComponents(settings)
	if err != nil || again[1].State != "loaded" {
		t.Fatalf("caller mutated shared state: %+v, %v", again, err)
	}
	if err := s.ResetBuiltinComponents(workspace); err != nil {
		t.Fatal(err)
	}
	reset, err := s.GetBuiltinComponents(settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range reset {
		if item.State != "inactive" {
			t.Fatalf("reset state = %+v", reset)
		}
	}
}

func TestBuiltinComponentsThatNeverReportAreMarkedFailed(t *testing.T) {
	published := make(chan []BuiltinComponentStatus, 8)
	s := &ShellService{BuiltinComponentTimeout: 20 * time.Millisecond, OnBuiltinComponentsChanged: func(statuses []BuiltinComponentStatus) { published <- statuses }}
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
