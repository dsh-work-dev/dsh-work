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
	if err := s.OpenSettings(trusted, "runtimes"); err == nil {
		t.Fatal("opened a section outside the menu")
	}
	for _, section := range []string{"settings", "about"} {
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
