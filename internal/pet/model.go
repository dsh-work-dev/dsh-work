package pet

import "context"

// PetAdapter is the format boundary used by the Host. Implementations inspect
// a package directory and normalize it into a format-independent definition.
// The package path is input only; adapters never write to it.
type PetAdapter interface {
	Probe(ctx context.Context, packageRoot string) (SourceProfile, error)
	Load(ctx context.Context, packageRoot string) (PetDefinition, error)
}

type SourceFormat string

const SourceFormatCodex SourceFormat = "codex"

type SourceProfile struct {
	Format      SourceFormat `json:"format"`
	Version     int          `json:"version"`
	ProfileID   string       `json:"profileId"`
	Entry       string       `json:"entry"`
	Legacy      bool         `json:"legacy,omitempty"`
	Look        LookProfile  `json:"look"`
	Diagnostics []Diagnostic `json:"diagnostics,omitempty"`
}

type Identity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
}

type Canvas struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	PivotX      float64 `json:"pivotX"`
	PivotY      float64 `json:"pivotY"`
	ScaleMode   string  `json:"scaleMode"`
	SafePadding int     `json:"safePadding"`
}

type Asset struct {
	ID           string `json:"id"`
	RendererKind string `json:"rendererKind"`
	Resource     string `json:"resource"`
	Format       string `json:"format"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
}

type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Frame is a renderer-neutral reference to one visual region. Source-format
// row, column and sprite indexes are intentionally not exposed here.
type Frame struct {
	AssetID    string `json:"assetId"`
	Region     Rect   `json:"region"`
	DurationMS int    `json:"durationMs"`
}

type TrackChannel string

const (
	TrackChannelBase   TrackChannel = "base"
	TrackChannelAction TrackChannel = "action"
	TrackChannelLook   TrackChannel = "look"
)

type Track struct {
	ID            string       `json:"id"`
	Channel       TrackChannel `json:"channel"`
	Frames        []Frame      `json:"frames"`
	FPS           float64      `json:"fps,omitempty"`
	Loop          bool         `json:"loop"`
	RepeatCount   int          `json:"repeatCount,omitempty"`
	Priority      int          `json:"priority,omitempty"`
	Interruptible bool         `json:"interruptible,omitempty"`
	Fallback      string       `json:"fallback"`
	TimeoutMS     int          `json:"timeoutMs,omitempty"`
}

// TrackSpec is kept as a descriptive alias for callers that use the standard
// document's name.
type TrackSpec = Track

type Capability string

const (
	CapabilityVisualStatic        Capability = "visual.static"
	CapabilityVisualFrameSequence Capability = "visual.frame-sequence"
	CapabilityLookDirectional16   Capability = "look.directional16"
	CapabilityProjectionTaskState Capability = "projection.task-state"
	CapabilityAnimationOneShot    Capability = "animation.one-shot"
)

type Permission string

const (
	PermissionNetwork Permission = "network"
	PermissionProcess Permission = "process"
	PermissionScripts Permission = "scripts"
	PermissionAudio   Permission = "audio"
)

type PermissionState string

const PermissionStateDeny PermissionState = "deny"

type PermissionSet map[Permission]PermissionState

type LookMode string

const (
	LookModeNone          LookMode = "none"
	LookModeDirectional16 LookMode = "directional16"
)

type NeutralPolicy string

const NeutralPolicyDeadzoneToIdle NeutralPolicy = "deadzone-to-idle"

type LookDirection struct {
	Degrees float64 `json:"degrees"`
	Frame   Frame   `json:"frame"`
}

type LookProfile struct {
	Mode          LookMode        `json:"mode"`
	NeutralPolicy NeutralPolicy   `json:"neutralPolicy"`
	Directions    []LookDirection `json:"directions,omitempty"`
}

type Fallback struct {
	Track       string `json:"track"`
	Placeholder bool   `json:"placeholder"`
}

type PetDefinition struct {
	Identity     Identity          `json:"identity"`
	Source       SourceProfile     `json:"source"`
	Assets       []Asset           `json:"assets"`
	Canvas       Canvas            `json:"canvas"`
	Capabilities []Capability      `json:"capabilities"`
	Tracks       []Track           `json:"tracks"`
	States       map[string]string `json:"states"`
	Actions      map[string]string `json:"actions"`
	Aliases      map[string]string `json:"aliases"`
	Look         LookProfile       `json:"look"`
	Permissions  PermissionSet     `json:"permissions"`
	Fallback     Fallback          `json:"fallback"`
	Diagnostics  []Diagnostic      `json:"diagnostics,omitempty"`
}

func (d PetDefinition) Track(id string) (Track, bool) {
	for _, track := range d.Tracks {
		if track.ID == id {
			return track, true
		}
	}
	return Track{}, false
}
