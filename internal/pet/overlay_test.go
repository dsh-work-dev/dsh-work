package pet

import (
	"math"
	"testing"
)

func TestOverlayCapabilitiesAndPositionPolicy(t *testing.T) {
	if capabilities := CapabilitiesFor("windows"); capabilities.Level != OverlayFull || !capabilities.Transparent || !capabilities.ClickThrough || !capabilities.Drag {
		t.Fatalf("Windows capabilities = %+v", capabilities)
	}
	if capabilities := CapabilitiesFor("linux"); capabilities.Level != OverlayFallback {
		t.Fatalf("Linux capabilities = %+v", capabilities)
	}
	position := ResolvePosition(1, 1, 192, 208, 2, WorkArea{X: -1920, Y: -200, Width: 1920, Height: 1080}, 12)
	if position.X != -204 || position.Y != 660 || position.Width != 192 || position.Height != 208 || position.Scale != 2 {
		t.Fatalf("resolved position = %+v", position)
	}
	clamped := ClampPosition(OverlayPosition{X: -5000, Y: 5000, Width: 192, Height: 208, Scale: 2}, WorkArea{X: -1920, Y: -200, Width: 1920, Height: 1080}, 12)
	if clamped.X != -1908 || clamped.Y != 660 {
		t.Fatalf("clamped position = %+v", clamped)
	}
}

func TestHeadlessOverlayDefaultsToClickThrough(t *testing.T) {
	overlay := NewHeadlessOverlay(CapabilitiesFor("windows"))
	visible, closed, _, mode, _, _ := overlay.State()
	if visible || closed || mode != OverlayClickThrough {
		t.Fatalf("initial overlay state = visible=%v closed=%v mode=%q", visible, closed, mode)
	}
	if err := overlay.SetInteractionMode(OverlayInteractive); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Show(); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Hide(); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Close(); err != nil {
		t.Fatal(err)
	}
	if err := overlay.Show(); err == nil {
		t.Fatal("Show after Close() = nil, want error")
	}
}

func TestAnchorForPositionRoundTripsResolvedPosition(t *testing.T) {
	area := WorkArea{X: -300, Y: 40, Width: 1600, Height: 900}
	resolved := ResolvePosition(0.35, 0.8, 192, 208, 2, area, 12)
	anchorX, anchorY := AnchorForPosition(resolved.X, resolved.Y, resolved.Width, resolved.Height, area, 12)
	if math.Abs(anchorX-0.35) > 0.001 || math.Abs(anchorY-0.8) > 0.001 {
		t.Fatalf("anchor did not round-trip: got (%f, %f), want (0.35, 0.8)", anchorX, anchorY)
	}
}

func TestAnchorForPositionClampsToWorkArea(t *testing.T) {
	area := WorkArea{Width: 1000, Height: 800}
	anchorX, anchorY := AnchorForPosition(-100, 2000, 200, 100, area, 10)
	if anchorX != 0 || anchorY != 1 {
		t.Fatalf("unexpected clamped anchor: (%f, %f)", anchorX, anchorY)
	}
}
