package pet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/at-wat/ebml-go"
	"github.com/at-wat/ebml-go/webm"
	"github.com/tailscale/hujson"
	"math"
	"path/filepath"
	"sort"
	"strings"
)

const RendererWebM = "dsh-webm"
const MaxCommunityBytes int64 = 256 * 1024 * 1024
const MaxCommunityFiles = 1024

type WeightedAction struct {
	Track  string  `json:"track"`
	Weight float64 `json:"weight"`
}
type Behavior struct {
	Idle   []WeightedAction `json:"idle"`
	Clicks []string         `json:"clicks"`
}
type communityConfig struct {
	Pets []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"pets"`
	Animations struct {
		Idle       []string `json:"idle"`
		Turn       []string `json:"turn"`
		Drag       []string `json:"drag"`
		Clicks     []string `json:"clicks"`
		Categories []struct {
			Weight  float64  `json:"weight"`
			Actions []string `json:"actions"`
		} `json:"categories"`
		Events map[string][]string `json:"events"`
	} `json:"animations"`
	Weights struct {
		Idle float64 `json:"idle"`
		Turn float64 `json:"turn"`
		Move float64 `json:"move"`
	} `json:"animationWeights"`
}

// loadCommunity adapts the data-only installed config.jsonc + webm directory.
// hujson handles comments/trailing commas; ebml-go bounds and parses media headers.
func loadCommunity(ctx context.Context, in packageInventory) (PetDefinition, packageInventory, error) {
	fail := func() (PetDefinition, packageInventory, error) {
		return PetDefinition{}, packageInventory{}, packageError(in.Source, IssueManifestInvalid, SeverityError, false, nil)
	}
	raw, err := hujson.Standardize(in.Files[in.Source.ManifestPath].Data)
	if err != nil {
		return fail()
	}
	var c communityConfig
	if json.Unmarshal(raw, &c) != nil || len(c.Animations.Idle) == 0 {
		return fail()
	}
	name := in.Source.Folder
	if len(c.Pets) > 0 && c.Pets[0].Name != "" {
		name = c.Pets[0].Name
	}
	name, ok := safeString(name, MaxSafeTextRunes)
	if !ok {
		return fail()
	}
	d := PetDefinition{Identity: Identity{ID: in.Source.Folder, DisplayName: name}, Source: SourceInfo{Format: "dsh-pet-community", FormatVersion: "1", Profile: RendererWebM, Entry: in.Source.Entry, StableKey: in.Source.StableSourceKey, Badge: BadgeDshNative}, Tracks: map[string]TrackSpec{}, States: map[string]StateSpec{}, Actions: map[string]ActionSpec{}, Permissions: PermissionPolicy{Network: "deny", Process: "deny", Scripts: "deny", Audio: "deny"}}
	names := map[string]bool{}
	add := func(pool []string) {
		for _, n := range pool {
			names[n] = true
		}
	}
	add(c.Animations.Idle)
	add(c.Animations.Turn)
	add(c.Animations.Drag)
	add(c.Animations.Clicks)
	for _, cat := range c.Animations.Categories {
		add(cat.Actions)
	}
	for _, pool := range c.Animations.Events {
		add(pool)
	}
	if len(names) > 256 {
		return fail()
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	// Cache only the configuration and referenced media, not unused previews.
	used := packageInventory{Source: in.Source, Root: in.Root, CanonicalRoot: in.CanonicalRoot, Files: map[string]packageFile{}, Ordered: []string{in.Source.ManifestPath}}
	used.Files[in.Source.ManifestPath] = in.Files[in.Source.ManifestPath]
	for _, n := range ordered {
		if contextErr(ctx) != nil {
			return PetDefinition{}, in, ctx.Err()
		}
		if n == "" || strings.ContainsAny(n, "/\\:") || n == "." || n == ".." {
			return fail()
		}
		path := filepath.Join("webm", n+".webm")
		f, exists := in.Files[path]
		if !exists {
			return fail()
		}
		width, height, duration, err := webmMetadata(f.Data)
		if err != nil {
			return fail()
		}
		d.Assets = append(d.Assets, Asset{ID: n, Kind: "video", Path: path, Format: "webm", Size: f.Size, Width: width, Height: height, SHA256: HashBytes(f.Data), Data: f.Data})
		d.Tracks[n] = TrackSpec{ID: n, RendererKind: RendererWebM, Frames: []FrameRef{{Index: 0, DurationMS: duration, AssetID: n, Width: width, Height: height}}, Interruptible: true}
		d.Actions[n] = ActionSpec{Track: n, Interruptible: true}
		used.Files[path] = f
		used.Ordered = append(used.Ordered, path)
	}
	idle := c.Animations.Idle[0]
	d.Fallback.Idle = idle
	d.Canvas = CanvasSpec{Width: d.Assets[0].Width, Height: d.Assets[0].Height, PivotX: .5, PivotY: 1, ScaleMode: "contain"}
	d.Geometry = Geometry{CellWidth: d.Canvas.Width, CellHeight: d.Canvas.Height, Columns: 1, Rows: 1, ImageWidth: d.Canvas.Width, ImageHeight: d.Canvas.Height, FrameCount: 1}
	for _, n := range c.Animations.Idle {
		t := d.Tracks[n]
		t.Loop = true
		d.Tracks[n] = t
	}
	d.States["idle"] = StateSpec{Track: idle, Loop: true}
	phases := []string{"thinking", "working", "result", "waiting", "completed", "failed"}
	for i, phase := range phases {
		track := idle
		if pool := c.Animations.Events["workStatus"]; len(pool) > i {
			track = pool[i]
		}
		d.States[phase] = StateSpec{Track: track, Loop: i < 4, Fallback: idle}
	}
	d.States["reviewing"] = d.States["result"]
	behavior := &Behavior{Clicks: append([]string(nil), c.Animations.Clicks...)}
	validWeight := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 && v <= 100 }
	if !validWeight(c.Weights.Idle) || !validWeight(c.Weights.Turn) || !validWeight(c.Weights.Move) || c.Weights.Idle+c.Weights.Turn+c.Weights.Move > 100 {
		return fail()
	}
	poolWeight := func(pool []string, weight float64) {
		if len(pool) > 0 {
			for _, n := range pool {
				behavior.Idle = append(behavior.Idle, WeightedAction{n, weight / float64(len(pool))})
			}
		}
	}
	// Movement probability remains stationary until explicit roaming is supported.
	poolWeight(c.Animations.Idle, c.Weights.Idle+c.Weights.Move)
	poolWeight(c.Animations.Turn, c.Weights.Turn)
	total := 0.0
	for _, cat := range c.Animations.Categories {
		if !validWeight(cat.Weight) {
			return fail()
		}
		if len(cat.Actions) > 0 {
			total += cat.Weight
		}
	}
	if total > 0 {
		for _, cat := range c.Animations.Categories {
			poolWeight(cat.Actions, (100-c.Weights.Idle-c.Weights.Turn-c.Weights.Move)*cat.Weight/total)
		}
	}
	d.Behavior = behavior
	return d, used, nil
}

func webmMetadata(data []byte) (int, int, int, error) {
	var document struct {
		EBML    webm.EBMLHeader
		Segment struct {
			Info   webm.Info
			Tracks webm.Tracks `ebml:"Tracks,stop"`
		}
	}
	if len(data) < 4 || !bytes.Equal(data[:4], []byte{0x1a, 0x45, 0xdf, 0xa3}) {
		return 0, 0, 0, errors.New("invalid WebM")
	}
	err := ebml.Unmarshal(bytes.NewReader(data), &document, ebml.WithMaxLeafElementSize(1024*1024))
	if err != nil && !errors.Is(err, ebml.ErrReadStopped) {
		return 0, 0, 0, err
	}
	tracks := document.Segment.Tracks.TrackEntry
	if document.EBML.DocType != "webm" || len(tracks) != 1 || tracks[0].Video == nil || (tracks[0].CodecID != "V_VP9" && tracks[0].CodecID != "V_VP8") {
		return 0, 0, 0, errors.New("unsupported WebM tracks")
	}
	v := tracks[0].Video
	scale := document.Segment.Info.TimecodeScale
	if scale == 0 {
		scale = 1000000
	}
	duration := document.Segment.Info.Duration * float64(scale) / 1e6
	if v.PixelWidth == 0 || v.PixelWidth > MaxImageWidth || v.PixelHeight == 0 || v.PixelHeight > MaxImageHeight || math.IsNaN(duration) || math.IsInf(duration, 0) || duration <= 0 || duration > float64(MaxNonLoopDurationMS) {
		return 0, 0, 0, errors.New("invalid WebM dimensions/duration")
	}
	return int(v.PixelWidth), int(v.PixelHeight), int(math.Ceil(duration)), nil
}

func probeVideoDefinition(ctx context.Context, d PetDefinition) error {
	if contextErr(ctx) != nil {
		return ctx.Err()
	}
	if len(d.Assets) == 0 {
		return rendererIssue(IssueInvalidAnimation, "video")
	}
	for _, a := range d.Assets {
		if a.Format != "webm" || a.Width <= 0 || a.Height <= 0 || len(a.Data) == 0 || HashBytes(a.Data) != a.SHA256 {
			return rendererIssue(IssueInvalidAnimation, "video")
		}
	}
	return nil
}
