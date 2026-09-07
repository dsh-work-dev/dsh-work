package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestPetWindowOptionsUseTransparentWindowAndSliderResize(t *testing.T) {
	options := petWindowOptions()
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
