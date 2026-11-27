package nativeui

import (
	"runtime"
	"testing"

	"github.com/local/dsh-work/internal/pet"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestPetScreenWorkAreaMatchesWindowCoordinates(t *testing.T) {
	screen := &application.Screen{
		ScaleFactor:      2,
		WorkArea:         application.Rect{X: -1500, Y: 24, Width: 1440, Height: 876},
		PhysicalWorkArea: application.Rect{X: -2880, Y: -150, Width: 2880, Height: 1752},
	}
	want := pet.WorkArea{X: -1500, Y: 24, Width: 1440, Height: 876}
	if runtime.GOOS == "darwin" {
		want = pet.WorkArea{X: -1440, Y: -75, Width: 1440, Height: 876}
	}
	if got := PetScreenWorkArea(screen); got != want {
		t.Fatalf("Pet screen work area = %+v, want %+v", got, want)
	}
}

func TestPetWindowOptionsUseTransparentWindowAndSliderResize(t *testing.T) {
	options := PetWindowOptions()
	if options.AlwaysOnTop {
		t.Fatal("pet must not be always on top by default")
	}
	if options.BackgroundType != application.BackgroundTypeTransparent {
		t.Fatalf("BackgroundType = %v, want transparent", options.BackgroundType)
	}
	if options.BackgroundColour != application.NewRGBA(0, 0, 0, 0) {
		t.Fatalf("BackgroundColour = %+v, want fully transparent", options.BackgroundColour)
	}
	if options.Mac.Backdrop != application.MacBackdropTransparent {
		t.Fatalf("Mac.Backdrop = %v, want transparent", options.Mac.Backdrop)
	}
	if !options.DisableResize {
		t.Fatal("DisableResize must be true so the size slider is the only resize control")
	}
	if options.IgnoreMouseEvents {
		t.Fatal("the overlay must receive hover input for its drag handle")
	}
}
