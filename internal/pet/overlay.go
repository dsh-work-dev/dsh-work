package pet

import (
	"errors"
	"math"
	"runtime"
	"sync"
)

type OverlayMode string

const (
	OverlayClickThrough OverlayMode = "click-through"
	OverlayInteractive  OverlayMode = "interactive"
)

type OverlayLevel string

const (
	OverlayFull           OverlayLevel = "full-overlay"
	OverlayCapabilityGate OverlayLevel = "capability-gated"
	OverlayFallback       OverlayLevel = "fallback"
)

type OverlayCapabilities struct {
	Level        OverlayLevel `json:"level"`
	Transparent  bool         `json:"transparent"`
	AlwaysOnTop  bool         `json:"alwaysOnTop"`
	ClickThrough bool         `json:"clickThrough"`
	Interaction  bool         `json:"interaction"`
	Drag         bool         `json:"drag"`
	DPI          bool         `json:"dpi"`
	MonitorAware bool         `json:"monitorAware"`
	Reason       string       `json:"reason,omitempty"`
}

// CapabilitiesFor reports the conservative platform contract. Runtime.GOOS
// is used only by the production default; tests can pass an explicit target.
func CapabilitiesFor(goos string) OverlayCapabilities {
	switch goos {
	case "windows":
		return OverlayCapabilities{Level: OverlayFull, Transparent: true, AlwaysOnTop: true, ClickThrough: true, Interaction: true, Drag: true, DPI: true, MonitorAware: true}
	case "darwin":
		return OverlayCapabilities{Level: OverlayCapabilityGate, Transparent: true, AlwaysOnTop: true, ClickThrough: true, DPI: true, MonitorAware: true, Reason: "interactive input and drag behavior is platform-dependent"}
	default:
		return OverlayCapabilities{Level: OverlayFallback, Reason: "transparent desktop overlay is not guaranteed on this window manager"}
	}
}

func CurrentOverlayCapabilities() OverlayCapabilities {
	return CapabilitiesFor(runtime.GOOS)
}

type WorkArea struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

type OverlayPosition struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

// ResolvePosition translates a normalized anchor into work-area coordinates
// and clamps it with a safe margin. Width and height are logical window units;
// the host platform applies the returned scale according to its window API.
// This keeps Wails DIP coordinates and persisted logical sizes from being
// scaled twice while still preserving the active DPI in the result.
func ResolvePosition(anchorX, anchorY float64, width, height int, scale float64, area WorkArea, margin int) OverlayPosition {
	if width <= 0 {
		width = 192
	}
	if height <= 0 {
		height = 208
	}
	if scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) {
		scale = 1
	}
	anchorX = clampUnit(anchorX)
	anchorY = clampUnit(anchorY)
	logicalWidth := maxDimension(width, 1)
	logicalHeight := maxDimension(height, 1)
	minX := area.X + margin
	minY := area.Y + margin
	maxX := area.X + area.Width - logicalWidth - margin
	maxY := area.Y + area.Height - logicalHeight - margin
	if maxX < minX {
		maxX = minX
	}
	if maxY < minY {
		maxY = minY
	}
	return OverlayPosition{
		X:      minX + int(math.Round(float64(maxX-minX)*anchorX)),
		Y:      minY + int(math.Round(float64(maxY-minY)*anchorY)),
		Width:  logicalWidth,
		Height: logicalHeight,
		Scale:  scale,
	}
}

func ClampPosition(position OverlayPosition, area WorkArea, margin int) OverlayPosition {
	return ResolvePosition(float64(position.X-area.X-margin)/float64(maxDimension(area.Width-position.Width-2*margin, 1)), float64(position.Y-area.Y-margin)/float64(maxDimension(area.Height-position.Height-2*margin, 1)), position.Width, position.Height, position.Scale, area, margin)
}

// AnchorForPosition converts a host-reported logical window position back to
// the normalized anchor persisted by Settings. The calculation intentionally
// stays in work-area/DIP coordinates: Wails reports and accepts those units
// on Windows, so multiplying by scale here would make a drag jump after a DPI
// change.
func AnchorForPosition(x, y, width, height int, area WorkArea, margin int) (float64, float64) {
	if width <= 0 {
		width = 192
	}
	if height <= 0 {
		height = 208
	}
	denominatorX := maxDimension(area.Width-width-2*margin, 1)
	denominatorY := maxDimension(area.Height-height-2*margin, 1)
	return clampUnit(float64(x-area.X-margin) / float64(denominatorX)), clampUnit(float64(y-area.Y-margin) / float64(denominatorY))
}

// OverlayWindow is the host-owned window seam. Its implementation may wrap
// Wails or a native handle, while policy tests use HeadlessOverlay.
type OverlayWindow interface {
	Show() error
	Hide() error
	Move(OverlayPosition) error
	SetInteractionMode(OverlayMode) error
	Close() error
}

type HeadlessOverlay struct {
	mu         sync.Mutex
	visible    bool
	closed     bool
	position   OverlayPosition
	mode       OverlayMode
	showCount  int
	hideCount  int
	capability OverlayCapabilities
}

func NewHeadlessOverlay(capability OverlayCapabilities) *HeadlessOverlay {
	if capability.Level == "" {
		capability = CapabilitiesFor("windows")
	}
	return &HeadlessOverlay{capability: capability, mode: OverlayClickThrough}
}

func (o *HeadlessOverlay) Show() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("overlay is closed")
	}
	o.visible = true
	o.showCount++
	return nil
}

func (o *HeadlessOverlay) Hide() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.visible = false
	o.hideCount++
	return nil
}

func (o *HeadlessOverlay) Move(position OverlayPosition) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("overlay is closed")
	}
	o.position = position
	return nil
}

func (o *HeadlessOverlay) SetInteractionMode(mode OverlayMode) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return errors.New("overlay is closed")
	}
	if mode == OverlayInteractive && !o.capability.Interaction {
		return errors.New("overlay interaction is unavailable")
	}
	if mode != OverlayClickThrough && mode != OverlayInteractive {
		return errors.New("overlay interaction mode is invalid")
	}
	o.mode = mode
	return nil
}

func (o *HeadlessOverlay) Close() error {
	o.mu.Lock()
	o.closed = true
	o.visible = false
	o.mu.Unlock()
	return nil
}

func (o *HeadlessOverlay) State() (visible, closed bool, position OverlayPosition, mode OverlayMode, showCount, hideCount int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.visible, o.closed, o.position, o.mode, o.showCount, o.hideCount
}

func clampUnit(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func maxDimension(value, fallback int) int {
	if value < fallback {
		return fallback
	}
	return value
}
