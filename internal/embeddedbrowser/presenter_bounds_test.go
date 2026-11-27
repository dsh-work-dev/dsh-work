package embeddedbrowser

import "testing"

func TestClipPresenterBounds(t *testing.T) {
	slot := SlotBounds{X: -8, Y: 30.25, Width: 90.75, Height: 70.25}
	const scale = 1.5
	for _, tt := range []struct {
		name  string
		inset float64
		want  PixelBounds
	}{
		{"fixed inset", 40, PixelBounds{X: 0, Y: 46, Width: 110, Height: 94}},
		{"scaled inset", 60, PixelBounds{X: 0, Y: 60, Width: 110, Height: 80}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := clipPresenterBounds(slot, 110, 140, scale, tt.inset); got != tt.want {
				t.Fatalf("native bounds = %+v, want %+v", got, tt.want)
			}
		})
	}
	slot.X = -100
	if got := clipPresenterBounds(slot, 110, 140, scale, 40); got.Width != 0 {
		t.Fatalf("slot fully outside viewport has visible width: %+v", got)
	}
}
