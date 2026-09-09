package pet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/png"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/image/webp"
)

const (
	profileCodexV1      = "codex-v1"
	profileCodexV2      = "codex-v2"
	neutralDeadzoneIdle = "deadzone-to-idle"
	rendererCodexAtlas  = "codex-atlas"
)

// CodexAdapter recognizes and imports pure Codex packages. It never writes to
// a source package and emits only normalized PetDefinition values.
type CodexAdapter struct{}

func NewCodexAdapter() CodexAdapter {
	return CodexAdapter{}
}

type codexManifest struct {
	ID                  string          `json:"id"`
	DisplayName         string          `json:"displayName"`
	Description         string          `json:"description"`
	SpriteVersionNumber *int            `json:"spriteVersionNumber"`
	SpritesheetPath     string          `json:"spritesheetPath"`
	Frame               json.RawMessage `json:"frame"`
	Animations          json.RawMessage `json:"animations"`
}

type frameManifest struct {
	Width   *int `json:"width"`
	Height  *int `json:"height"`
	Columns *int `json:"columns"`
	Rows    *int `json:"rows"`
}

type animationManifest struct {
	Frames   []int    `json:"frames"`
	FPS      *float64 `json:"fps"`
	Loop     *bool    `json:"loop"`
	Fallback *string  `json:"fallback"`
	Aliases  []string `json:"aliases"`
}

// Probe performs bounded manifest identification. Resource geometry is
// checked by Load, because a manifest alone cannot establish a valid profile.
func (CodexAdapter) Probe(ctx context.Context, source PackageSource) (ProbeResult, error) {
	inventory, err := inspectPackage(ctx, source)
	if err != nil {
		return ProbeResult{}, err
	}
	manifest, err := parseCodexManifest(inventory)
	if err != nil {
		return ProbeResult{}, err
	}
	if _, err := manifestSpritePath(inventory.Source, manifest); err != nil {
		return ProbeResult{}, err
	}
	profile, geometry, version, err := profileGeometry(inventory.Source, manifest, Geometry{})
	if err != nil {
		return ProbeResult{}, err
	}
	info := sourceInfo(inventory.Source, profile, version)
	info.Digest = packageDigest(inventory)
	return ProbeResult{
		Source:   info,
		Geometry: geometry,
	}, nil
}

// Load validates the complete package and returns a fully normalized
// definition. The zero definition is returned for every validation failure.
func (CodexAdapter) Load(ctx context.Context, source PackageSource) (PetDefinition, error) {
	inventory, err := inspectPackage(ctx, source)
	if err != nil {
		return PetDefinition{}, err
	}
	definition, _, err := (CodexAdapter{}).loadFromInventory(ctx, inventory)
	return definition, err
}

func (CodexAdapter) loadFromInventory(ctx context.Context, inventory packageInventory) (PetDefinition, packageInventory, error) {
	manifest, err := parseCodexManifest(inventory)
	if err != nil {
		return PetDefinition{}, packageInventory{}, err
	}

	spriteRelative, err := manifestSpritePath(inventory.Source, manifest)
	if err != nil {
		return PetDefinition{}, packageInventory{}, err
	}
	spriteFile, ok := inventory.Files[spriteRelative]
	if !ok {
		return PetDefinition{}, packageInventory{}, packageError(inventory.Source, IssueInvalidSpritesheet, SeverityError, true, map[string]string{"resource": "spritesheet"})
	}

	decoded, imageFormat, err := decodeSprite(spriteFile.Data)
	if err != nil {
		code := IssueInvalidSpritesheet
		var validationErr *spriteDecodeError
		if errors.As(err, &validationErr) {
			code = validationErr.Code
		}
		return PetDefinition{}, packageInventory{}, packageError(inventory.Source, code, SeverityError, false, map[string]string{"resource": "spritesheet"})
	}
	profile, geometry, version, err := profileGeometry(inventory.Source, manifest, Geometry{
		ImageWidth:  decoded.Bounds().Dx(),
		ImageHeight: decoded.Bounds().Dy(),
	})
	if err != nil {
		return PetDefinition{}, packageInventory{}, err
	}
	if geometry.ImageWidth != decoded.Bounds().Dx() || geometry.ImageHeight != decoded.Bounds().Dy() {
		return PetDefinition{}, packageInventory{}, packageError(inventory.Source, IssueVersionGeometryMismatch, SeverityError, false, nil)
	}
	if geometry.FrameCount <= 0 || geometry.FrameCount > MaxTrackFrames {
		return PetDefinition{}, packageInventory{}, packageError(inventory.Source, IssueInvalidSpritesheet, SeverityError, false, map[string]string{"resource": "frame-count"})
	}
	if !hasTransparency(decoded) {
		return PetDefinition{}, packageInventory{}, packageError(inventory.Source, IssueInvalidSpritesheet, SeverityError, false, map[string]string{"resource": "transparency"})
	}

	if err := validateAllImages(inventory, spriteRelative, int64(geometry.ImageWidth)*int64(geometry.ImageHeight)); err != nil {
		return PetDefinition{}, packageInventory{}, err
	}

	tracks, err := defaultTracks(geometry)
	if err != nil {
		return PetDefinition{}, packageInventory{}, err
	}
	customTracks, err := parseCustomAnimations(inventory.Source, manifest.Animations, geometry.FrameCount, tracks)
	if err != nil {
		return PetDefinition{}, packageInventory{}, err
	}
	for name, track := range customTracks {
		tracks[name] = track
	}
	if err := validateTrackFallbacks(inventory.Source, tracks); err != nil {
		return PetDefinition{}, packageInventory{}, err
	}

	definition := PetDefinition{
		Identity: Identity{
			ID:          manifest.ID,
			DisplayName: manifest.DisplayName,
			Description: manifest.Description,
		},
		Source:   sourceInfo(inventory.Source, profile, version),
		Geometry: geometry,
		Canvas: CanvasSpec{
			Width: geometry.CellWidth, Height: geometry.CellHeight,
			PivotX: 0.5, PivotY: 1, ScaleMode: "contain",
		},
		Capabilities: []string{"visual.frame-sequence", "projection.task-state"},
		Assets: []Asset{{
			ID:     "spritesheet",
			Kind:   "spritesheet",
			Path:   filepath.ToSlash(spriteRelative),
			Format: imageFormat,
			Size:   int64(len(spriteFile.Data)),
			Width:  geometry.ImageWidth,
			Height: geometry.ImageHeight,
			SHA256: HashBytes(spriteFile.Data),
			Data:   append([]byte(nil), spriteFile.Data...),
		}},
		Tracks:      tracks,
		States:      defaultStates(tracks),
		Actions:     actionAliases(tracks),
		Inputs:      map[string]bool{"look.directional16": strings.HasPrefix(profile, profileCodexV2)},
		Permissions: PermissionPolicy{Network: "deny", Process: "deny", Scripts: "deny", Audio: "deny"},
		Fallback:    FallbackSpec{Idle: "idle"},
	}
	if strings.HasPrefix(profile, profileCodexV2) {
		definition.Directions = v2Directions(geometry)
		definition.Diagnostics = []Issue{packageIssue(inventory.Source, IssueV2SemanticsUnverified, SeverityWarning, false, map[string]string{"profile": "v2"})}
	}
	definition.Source.Digest = packageDigest(inventory)
	return definition, inventory, nil
}

func manifestSpritePath(source PackageSource, manifest codexManifest) (string, error) {
	spritePath := manifest.SpritesheetPath
	if spritePath == "" {
		spritePath = "spritesheet.webp"
	}
	relative, pathCode := safeRelativePath(spritePath)
	if pathCode != "" {
		return "", packageError(source, pathCode, SeverityError, false, map[string]string{"resource": "spritesheet"})
	}
	return relative, nil
}

func parseCodexManifest(inventory packageInventory) (codexManifest, error) {
	manifestPath := filepath.Clean(inventory.Source.ManifestPath)
	file, ok := inventory.Files[manifestPath]
	if !ok {
		return codexManifest{}, packageError(inventory.Source, IssueManifestMissing, SeverityError, true, nil)
	}
	if int64(len(file.Data)) > MaxManifestBytes {
		return codexManifest{}, packageError(inventory.Source, IssueManifestTooLarge, SeverityError, false, nil)
	}
	var manifest codexManifest
	if err := decodeJSONDocument(file.Data, &manifest); err != nil {
		return codexManifest{}, packageError(inventory.Source, IssueManifestInvalid, SeverityError, false, nil)
	}
	if manifest.ID == "" || manifest.DisplayName == "" {
		return codexManifest{}, packageError(inventory.Source, IssueManifestInvalid, SeverityError, false, nil)
	}
	if _, ok := safeString(manifest.ID, MaxSafeTextRunes); !ok {
		return codexManifest{}, packageError(inventory.Source, IssueManifestInvalid, SeverityError, false, nil)
	}
	if _, ok := safeString(manifest.DisplayName, MaxSafeTextRunes); !ok {
		return codexManifest{}, packageError(inventory.Source, IssueManifestInvalid, SeverityError, false, nil)
	}
	if manifest.Description != "" {
		if _, ok := safeString(manifest.Description, MaxSafeTextRunes); !ok {
			return codexManifest{}, packageError(inventory.Source, IssueManifestInvalid, SeverityError, false, nil)
		}
	}
	return manifest, nil
}

func decodeJSONDocument(data []byte, target any) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("null JSON document")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple json documents")
		}
		return err
	}
	return nil
}

func profileGeometry(source PackageSource, manifest codexManifest, actual Geometry) (string, Geometry, int, error) {
	version := 1
	if manifest.SpriteVersionNumber != nil {
		version = *manifest.SpriteVersionNumber
	}
	if version != 1 && version != 2 {
		return "", Geometry{}, 0, packageError(source, IssueVersionGeometryMismatch, SeverityError, false, nil)
	}
	geometry := Geometry{CellWidth: 192, CellHeight: 208, Columns: 8, Rows: 9, ImageWidth: 1536, ImageHeight: 1872}
	profile := profileCodexV1
	if version == 2 {
		geometry.Rows = 11
		geometry.ImageHeight = 2288
		profile = profileCodexV2
	}
	geometry.FrameCount = geometry.Columns * geometry.Rows
	if manifest.Frame != nil && !isJSONNull(manifest.Frame) {
		candidate, err := decodeFrame(manifest.Frame)
		if err != nil || candidate.Width == nil || candidate.Height == nil || candidate.Columns == nil || candidate.Rows == nil || candidate.WidthValue() != geometry.CellWidth || candidate.HeightValue() != geometry.CellHeight || candidate.ColumnsValue() != geometry.Columns || candidate.RowsValue() != geometry.Rows {
			return "", Geometry{}, 0, packageError(source, IssueVersionGeometryMismatch, SeverityError, false, nil)
		}
	} else if manifest.Frame != nil {
		return "", Geometry{}, 0, packageError(source, IssueVersionGeometryMismatch, SeverityError, false, nil)
	}
	if actual.ImageWidth != 0 && (actual.ImageWidth != geometry.ImageWidth || actual.ImageHeight != geometry.ImageHeight) {
		return "", Geometry{}, 0, packageError(source, IssueVersionGeometryMismatch, SeverityError, false, nil)
	}
	return profile, geometry, version, nil
}

func decodeFrame(raw json.RawMessage) (frameManifest, error) {
	var frame frameManifest
	if err := decodeJSONDocument(raw, &frame); err != nil {
		return frameManifest{}, err
	}
	return frame, nil
}

func (f frameManifest) WidthValue() int {
	if f.Width == nil {
		return 0
	}
	return *f.Width
}

func (f frameManifest) HeightValue() int {
	if f.Height == nil {
		return 0
	}
	return *f.Height
}

func (f frameManifest) ColumnsValue() int {
	if f.Columns == nil {
		return 0
	}
	return *f.Columns
}

func (f frameManifest) RowsValue() int {
	if f.Rows == nil {
		return 0
	}
	return *f.Rows
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.EqualFold(strings.TrimSpace(string(raw)), "null")
}

func sourceInfo(source PackageSource, profile string, version int) SourceInfo {
	formatVersion := strconv.Itoa(version)
	return SourceInfo{
		Format:        "codex",
		FormatVersion: formatVersion,
		Profile:       profile,
		Entry:         filepath.ToSlash(source.ManifestPath),
		StableKey:     source.StableSourceKey,
		Badge:         BadgeCodex,
	}
}

func decodeSprite(data []byte) (image.Image, string, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "webp") {
		return nil, "", errors.New("unsupported sprite image")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxImageWidth || config.Height > MaxImageHeight {
		return nil, "", &spriteDecodeError{Code: IssueDecodedImageTooLarge, Reason: "sprite image exceeds dimensions"}
	}
	if int64(config.Width) > MaxDecodedPixels/int64(config.Height) {
		return nil, "", &spriteDecodeError{Code: IssueDecodedPixelsExceeded, Reason: "sprite image exceeds the decoded pixel budget"}
	}
	decoded, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		return nil, "", errors.New("sprite image could not be decoded")
	}
	return decoded, format, nil
}

type spriteDecodeError struct {
	Code   string
	Reason string
}

func (e *spriteDecodeError) Error() string { return e.Reason }

func hasTransparency(img image.Image) bool {
	if img == nil {
		return false
	}
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := img.At(x, y).RGBA()
			if alpha < 0xffff {
				return true
			}
		}
	}
	return false
}

func validateAllImages(inventory packageInventory, selected string, selectedPixels int64) error {
	totalPixels := selectedPixels
	for _, relative := range inventory.Ordered {
		if relative == selected || !looksLikeImagePath(relative) {
			continue
		}
		file := inventory.Files[relative]
		config, format, err := image.DecodeConfig(bytes.NewReader(file.Data))
		if err != nil || (format != "png" && format != "webp") {
			return packageError(inventory.Source, IssueUnsafeResource, SeverityError, false, map[string]string{"resource": "image"})
		}
		if config.Width <= 0 || config.Height <= 0 || config.Width > MaxImageWidth || config.Height > MaxImageHeight {
			return packageError(inventory.Source, IssueDecodedImageTooLarge, SeverityError, false, map[string]string{"resource": "image"})
		}
		pixels := int64(config.Width) * int64(config.Height)
		if pixels > MaxDecodedPixels || totalPixels > MaxDecodedPixels-pixels {
			return packageError(inventory.Source, IssueDecodedPixelsExceeded, SeverityError, false, map[string]string{"resource": "image"})
		}
		totalPixels += pixels
	}
	if selectedPixels > MaxDecodedPixels {
		return packageError(inventory.Source, IssueDecodedPixelsExceeded, SeverityError, false, map[string]string{"resource": "spritesheet"})
	}
	return nil
}

func looksLikeImagePath(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".png" || extension == ".webp"
}

type codexAnimation struct {
	name string
	data animationManifest
}

func parseCustomAnimations(source PackageSource, raw json.RawMessage, frameCount int, existing map[string]TrackSpec) (map[string]TrackSpec, error) {
	if len(raw) == 0 {
		return map[string]TrackSpec{}, nil
	}
	if isJSONNull(raw) {
		return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
	}
	var values map[string]json.RawMessage
	if err := decodeJSONDocument(raw, &values); err != nil {
		return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	animations := make([]codexAnimation, 0, len(names))
	for _, name := range names {
		if _, ok := safeString(name, MaxSafeTextRunes); !ok {
			return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
		}
		var animation animationManifest
		if err := decodeJSONDocument(values[name], &animation); err != nil || len(animation.Frames) == 0 || len(animation.Frames) > MaxTrackFrames {
			return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
		}
		fps := 8.0
		if animation.FPS != nil {
			fps = *animation.FPS
		}
		if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > MaxRendererFPS {
			return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
		}
		loop := true
		if animation.Loop != nil {
			loop = *animation.Loop
		}
		fallback := "idle"
		if animation.Fallback != nil {
			fallback = *animation.Fallback
		}
		if _, ok := safeString(fallback, MaxSafeTextRunes); !ok {
			return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
		}
		duration := int(math.Round(1000 / fps))
		if duration < 1 {
			duration = 1
		}
		if !loop && float64(len(animation.Frames))*1000/fps > float64(MaxNonLoopDurationMS) {
			return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
		}
		frames := make([]FrameRef, len(animation.Frames))
		for index, frame := range animation.Frames {
			if frame < 0 || frame >= frameCount {
				return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
			}
			frames[index] = FrameRef{Index: frame, DurationMS: duration}
		}
		aliases := append([]string(nil), animation.Aliases...)
		for _, alias := range aliases {
			if _, ok := safeString(alias, MaxSafeTextRunes); !ok {
				return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
			}
		}
		animations = append(animations, codexAnimation{name: name, data: animationManifest{Frames: framesToIndexes(frames), FPS: &fps, Loop: &loop, Fallback: &fallback, Aliases: aliases}})
	}
	tracks := make(map[string]TrackSpec, len(animations))
	usedAliases := make(map[string]string)
	for name, track := range existing {
		for _, alias := range track.Aliases {
			usedAliases[alias] = name
		}
	}
	for _, animation := range animations {
		fps := *animation.data.FPS
		loop := *animation.data.Loop
		fallback := *animation.data.Fallback
		frames := make([]FrameRef, len(animation.data.Frames))
		duration := int(math.Round(1000 / fps))
		if duration < 1 {
			duration = 1
		}
		for index, frame := range animation.data.Frames {
			frames[index] = FrameRef{Index: frame, DurationMS: duration}
		}
		for _, alias := range animation.data.Aliases {
			if previous, ok := usedAliases[alias]; ok && previous != animation.name {
				return nil, packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
			}
			usedAliases[alias] = animation.name
		}
		tracks[animation.name] = TrackSpec{
			ID:            animation.name,
			Channel:       TrackAction,
			RendererKind:  rendererCodexAtlas,
			Frames:        frames,
			FPS:           fps,
			Loop:          loop,
			Interruptible: true,
			Fallback:      fallback,
			Aliases:       append([]string(nil), animation.data.Aliases...),
			RepeatCount:   0,
		}
	}
	return tracks, nil
}

func framesToIndexes(frames []FrameRef) []int {
	indexes := make([]int, len(frames))
	for index, frame := range frames {
		indexes[index] = frame.Index
	}
	return indexes
}

func validateTrackFallbacks(source PackageSource, tracks map[string]TrackSpec) error {
	for _, track := range tracks {
		if track.Fallback == "" {
			continue
		}
		if _, ok := tracks[track.Fallback]; !ok {
			return packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
		}
	}
	for trackID := range tracks {
		visited := make(map[string]struct{})
		current := trackID
		for current != "" {
			if current == "idle" {
				break
			}
			track, ok := tracks[current]
			if !ok || track.Loop {
				break
			}
			if _, seen := visited[current]; seen {
				return packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
			}
			visited[current] = struct{}{}
			current = track.Fallback
		}
	}
	return nil
}

func defaultTracks(geometry Geometry) (map[string]TrackSpec, error) {
	if geometry.Columns != 8 || (geometry.Rows != 9 && geometry.Rows != 11) {
		return nil, errors.New("unsupported Codex geometry")
	}
	type rowDefinition struct {
		id        string
		row       int
		count     int
		durations []int
		aliases   []string
		channel   TrackChannel
	}
	definitions := []rowDefinition{
		{id: "idle", row: 0, count: 6, durations: []int{1680, 660, 660, 840, 840, 1920}, channel: TrackBase},
		{id: "running-right", row: 1, count: 8, durations: []int{120, 120, 120, 120, 120, 120, 120, 220}, aliases: []string{"move_right"}, channel: TrackBase},
		{id: "running-left", row: 2, count: 8, durations: []int{120, 120, 120, 120, 120, 120, 120, 220}, aliases: []string{"move_left"}, channel: TrackBase},
		{id: "waving", row: 3, count: 4, durations: []int{140, 140, 140, 280}, aliases: []string{"wave"}, channel: TrackAction},
		{id: "jumping", row: 4, count: 5, durations: []int{140, 140, 140, 140, 280}, aliases: []string{"bounce"}, channel: TrackAction},
		{id: "failed", row: 5, count: 8, durations: []int{140, 140, 140, 140, 140, 140, 140, 240}, aliases: []string{"sad"}, channel: TrackBase},
		{id: "waiting", row: 6, count: 6, durations: []int{150, 150, 150, 150, 150, 260}, channel: TrackBase},
		{id: "running", row: 7, count: 6, durations: []int{120, 120, 120, 120, 120, 220}, channel: TrackBase},
		{id: "review", row: 8, count: 6, durations: []int{150, 150, 150, 150, 150, 280}, channel: TrackBase},
	}
	tracks := make(map[string]TrackSpec, len(definitions))
	for _, definition := range definitions {
		frames := make([]FrameRef, definition.count)
		for index := range frames {
			frames[index] = FrameRef{Index: definition.row*geometry.Columns + index, DurationMS: definition.durations[index]}
		}
		track := TrackSpec{
			ID:            definition.id,
			Channel:       definition.channel,
			RendererKind:  rendererCodexAtlas,
			Frames:        frames,
			Loop:          definition.id == "idle",
			Interruptible: definition.id == "idle",
			Fallback:      "",
			Aliases:       append([]string(nil), definition.aliases...),
			RepeatCount:   0,
		}
		if definition.id != "idle" {
			track.Fallback = "idle"
			track.RepeatCount = 3
		}
		tracks[definition.id] = track
	}
	return tracks, nil
}

func defaultStates(tracks map[string]TrackSpec) map[string]StateSpec {
	stateTracks := map[string]string{
		"starting":  "idle",
		"idle":      "idle",
		"working":   "running",
		"waiting":   "waiting",
		"reviewing": "review",
		"failed":    "failed",
	}
	states := make(map[string]StateSpec, len(stateTracks))
	for state, track := range stateTracks {
		if _, ok := tracks[track]; !ok {
			track = "idle"
		}
		states[state] = StateSpec{Track: track, Loop: tracks[track].Loop || state == "working" || state == "waiting" || state == "reviewing", Fallback: tracks[track].Fallback}
	}
	return states
}

func actionAliases(tracks map[string]TrackSpec) map[string]ActionSpec {
	actions := make(map[string]ActionSpec)
	for trackID, track := range tracks {
		for _, alias := range track.Aliases {
			actions[alias] = ActionSpec{Track: trackID, Priority: track.Priority, Interruptible: track.Interruptible, Fallback: track.Fallback}
		}
	}
	return actions
}

func v2Directions(geometry Geometry) *DirectionProfile {
	directions := make([]Direction, 16)
	for index := range directions {
		row := 9
		column := index
		if index >= 8 {
			row = 10
			column = index - 8
		}
		directions[index] = Direction{Angle: float64(index) * 22.5, FrameIndex: row*geometry.Columns + column}
	}
	return &DirectionProfile{
		Name:                     "directional16",
		Directions:               directions,
		NeutralPolicy:            neutralDeadzoneIdle,
		DesktopSemanticsVerified: false,
	}
}

// Keep the WebP decoder import close to the adapter's supported formats. The
// package registers itself with image.Decode; no custom codec is used.
var _ = webp.Decode
