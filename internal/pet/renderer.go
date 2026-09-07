package pet

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"strings"
	"sync"

	"golang.org/x/image/webp"
)

const (
	RendererCodexAtlas = "codex-atlas"
	RendererRasterV1   = "dsh-raster-v1"
)

type RenderedFrame struct {
	Image      image.Image `json:"-"`
	TrackID    string      `json:"trackId"`
	FrameIndex int         `json:"frameIndex"`
	Width      int         `json:"width"`
	Height     int         `json:"height"`
}

type PetRenderer interface {
	Probe(context.Context, PetDefinition) error
	Load(context.Context, PetDefinition) error
	ApplySnapshot(context.Context, PetRuntimeSnapshot) (RenderedFrame, error)
	Unload(context.Context) error
}

type RasterRenderer struct {
	mu         sync.RWMutex
	definition *PetDefinition
	images     map[string]image.Image
}

var _ PetRenderer = (*RasterRenderer)(nil)

func NewRasterRenderer() *RasterRenderer {
	return &RasterRenderer{}
}

func (r *RasterRenderer) Probe(ctx context.Context, definition PetDefinition) error {
	if err := rendererContextError(ctx); err != nil {
		return err
	}
	if len(definition.Assets) == 0 {
		return rendererIssue(IssueInvalidSpritesheet, "asset")
	}
	images := make(map[string]image.Image, len(definition.Assets))
	decodedPixels := int64(0)
	for _, asset := range definition.Assets {
		if err := rendererContextError(ctx); err != nil {
			return err
		}
		if asset.ID == "" || len(asset.Data) == 0 {
			return rendererIssue(IssueInvalidSpritesheet, "asset")
		}
		if _, exists := images[asset.ID]; exists {
			return rendererIssue(IssueInvalidSpritesheet, "asset")
		}
		decoded, _, err := decodeRaster(asset.Data)
		if err != nil {
			return rendererIssue(IssueInvalidSpritesheet, "asset")
		}
		width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
		if width <= 0 || height <= 0 || width > MaxImageWidth || height > MaxImageHeight {
			return rendererIssue(IssueDecodedImageTooLarge, "asset")
		}
		decodedPixels += int64(width) * int64(height)
		if decodedPixels > MaxDecodedPixels {
			return rendererIssue(IssueDecodedPixelsExceeded, "pixels")
		}
		images[asset.ID] = decoded
	}
	if definition.Canvas.Width < 0 || definition.Canvas.Height < 0 {
		return rendererIssue(IssueInvalidSpritesheet, "canvas")
	}
	for _, track := range definition.Tracks {
		if len(track.Frames) == 0 || len(track.Frames) > MaxTrackFrames {
			return rendererIssue(IssueInvalidAnimation, "frames")
		}
		if track.RendererKind != "" && track.RendererKind != RendererCodexAtlas && track.RendererKind != RendererRasterV1 {
			return rendererIssue(IssueUnsupportedRender, "renderer")
		}
		if track.FPS < 0 || track.FPS > MaxRendererFPS {
			return rendererIssue(IssueInvalidAnimation, "fps")
		}
		for _, frame := range track.Frames {
			if frame.DurationMS <= 0 {
				return rendererIssue(IssueInvalidAnimation, "duration")
			}
			assetID := frame.AssetID
			if assetID == "" {
				assetID = definition.Assets[0].ID
			}
			img, ok := images[assetID]
			if !ok {
				return rendererIssue(IssueInvalidAnimation, "asset")
			}
			if err := validateFrameBounds(definition, track, frame, img.Bounds()); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *RasterRenderer) Load(ctx context.Context, definition PetDefinition) error {
	if err := r.Probe(ctx, definition); err != nil {
		return err
	}
	images := make(map[string]image.Image, len(definition.Assets))
	for _, asset := range definition.Assets {
		decoded, _, err := decodeRaster(asset.Data)
		if err != nil {
			return rendererIssue(IssueInvalidSpritesheet, "asset")
		}
		images[asset.ID] = decoded
	}
	copy := definition.clone()
	r.mu.Lock()
	r.definition = &copy
	r.images = images
	r.mu.Unlock()
	return nil
}

func (r *RasterRenderer) ApplySnapshot(ctx context.Context, snapshot PetRuntimeSnapshot) (RenderedFrame, error) {
	if err := rendererContextError(ctx); err != nil {
		return RenderedFrame{}, err
	}
	r.mu.RLock()
	definition := r.definition
	images := r.images
	if definition == nil {
		r.mu.RUnlock()
		return RenderedFrame{}, errors.New("pet renderer is not loaded")
	}
	track, ok := definition.Tracks[snapshot.TrackID]
	if !ok || len(track.Frames) == 0 {
		track, ok = definition.Tracks[definition.Fallback.Idle]
	}
	if !ok || len(track.Frames) == 0 {
		r.mu.RUnlock()
		return RenderedFrame{}, rendererIssue(IssueMissingTrack, "track")
	}
	frameIndex := snapshot.FrameIndex
	frame := track.Frames[0]
	foundFrame := false
	for _, candidate := range track.Frames {
		if candidate.Index == frameIndex {
			frame = candidate
			foundFrame = true
			break
		}
	}
	if !foundFrame && snapshot.LookTrackID == snapshot.TrackID && snapshot.LookFrameIndex == frameIndex && isDirectionFrame(definition.Directions, frameIndex) {
		// Codex v2 stores its directional cells in dedicated atlas rows rather
		// than a normal TrackSpec. The runtime keeps that source detail behind
		// DirectionProfile; the renderer only needs a validated atlas index here.
		frame = FrameRef{Index: frameIndex, DurationMS: 1}
	}
	assetID := frame.AssetID
	if assetID == "" {
		assetID = definition.Assets[0].ID
	}
	img, ok := images[assetID]
	if !ok {
		r.mu.RUnlock()
		return RenderedFrame{}, rendererIssue(IssueInvalidAnimation, "asset")
	}
	result, err := cropFrame(*definition, track, frame, img)
	r.mu.RUnlock()
	if err != nil {
		return RenderedFrame{}, err
	}
	return RenderedFrame{Image: result, TrackID: snapshot.TrackID, FrameIndex: frameIndex, Width: result.Bounds().Dx(), Height: result.Bounds().Dy()}, nil
}

func (r *RasterRenderer) Unload(ctx context.Context) error {
	if err := rendererContextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	r.definition = nil
	r.images = nil
	r.mu.Unlock()
	return nil
}

// PreviewDataURL renders the first idle frame through the same renderer path
// used by the overlay. The caller must only pass a definition that has already
// crossed the Host catalog validation boundary.
func PreviewDataURL(ctx context.Context, definition PetDefinition) (string, error) {
	renderer := NewRasterRenderer()
	if err := renderer.Load(ctx, definition); err != nil {
		return "", err
	}
	trackID := definition.Fallback.Idle
	if state, ok := definition.States[string(BaseIdle)]; ok && state.Track != "" {
		trackID = state.Track
	}
	frame, err := renderer.ApplySnapshot(ctx, PetRuntimeSnapshot{TrackID: trackID, FrameIndex: firstFrame(definition, trackID)})
	if err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, frame.Image); err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes()), nil
}

func decodeRaster(data []byte) (image.Image, string, error) {
	if len(data) == 0 {
		return nil, "", errors.New("empty raster")
	}
	reader := bytes.NewReader(data)
	if imageHeaderIsWebP(data) {
		decoded, err := webp.Decode(reader)
		return decoded, "webp", err
	}
	decoded, format, err := image.Decode(reader)
	if err == nil && format != "png" && format != "webp" {
		return nil, "", errors.New("unsupported raster format")
	}
	return decoded, format, err
}

func imageHeaderIsWebP(data []byte) bool {
	return len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP"
}

func validateFrameBounds(definition PetDefinition, track TrackSpec, frame FrameRef, bounds image.Rectangle) error {
	if frame.X != 0 || frame.Y != 0 || frame.Width != 0 || frame.Height != 0 {
		width, height := frame.Width, frame.Height
		if width <= 0 || height <= 0 || frame.X < 0 || frame.Y < 0 || frame.X+width > bounds.Dx() || frame.Y+height > bounds.Dy() {
			return rendererIssue(IssueInvalidAnimation, "frame")
		}
		return nil
	}
	if track.RendererKind == RendererRasterV1 && (definition.Geometry.Columns <= 0 || definition.Geometry.Rows <= 0) {
		return rendererIssue(IssueInvalidAnimation, "geometry")
	}
	if track.RendererKind == RendererCodexAtlas || (definition.Geometry.Columns > 1 && definition.Geometry.Rows > 1) {
		if frame.Index < 0 || frame.Index >= definition.Geometry.FrameCount {
			return rendererIssue(IssueInvalidAnimation, "frame")
		}
		x := (frame.Index % definition.Geometry.Columns) * definition.Geometry.CellWidth
		y := (frame.Index / definition.Geometry.Columns) * definition.Geometry.CellHeight
		if definition.Geometry.CellWidth <= 0 || definition.Geometry.CellHeight <= 0 || x+definition.Geometry.CellWidth > bounds.Dx() || y+definition.Geometry.CellHeight > bounds.Dy() {
			return rendererIssue(IssueInvalidAnimation, "geometry")
		}
	}
	return nil
}

func cropFrame(definition PetDefinition, track TrackSpec, frame FrameRef, source image.Image) (image.Image, error) {
	if frame.X != 0 || frame.Y != 0 || frame.Width != 0 || frame.Height != 0 {
		rect := image.Rect(frame.X, frame.Y, frame.X+frame.Width, frame.Y+frame.Height)
		if !rect.In(source.Bounds()) {
			return nil, rendererIssue(IssueInvalidAnimation, "frame")
		}
		return copyImage(source, rect), nil
	}
	if track.RendererKind == RendererRasterV1 && definition.Geometry.Columns <= 1 && definition.Geometry.Rows <= 1 {
		return source, nil
	}
	if definition.Geometry.Columns <= 0 || definition.Geometry.CellWidth <= 0 || definition.Geometry.CellHeight <= 0 {
		return nil, rendererIssue(IssueInvalidAnimation, "geometry")
	}
	rect := image.Rect(
		(frame.Index%definition.Geometry.Columns)*definition.Geometry.CellWidth,
		(frame.Index/definition.Geometry.Columns)*definition.Geometry.CellHeight,
		(frame.Index%definition.Geometry.Columns+1)*definition.Geometry.CellWidth,
		(frame.Index/definition.Geometry.Columns+1)*definition.Geometry.CellHeight,
	)
	if !rect.In(source.Bounds()) {
		return nil, rendererIssue(IssueInvalidAnimation, "geometry")
	}
	return copyImage(source, rect), nil
}

func copyImage(source image.Image, rect image.Rectangle) image.Image {
	result := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(result, result.Bounds(), source, rect.Min, draw.Src)
	return result
}

func firstFrame(definition PetDefinition, trackID string) int {
	if track, ok := definition.Tracks[trackID]; ok && len(track.Frames) > 0 {
		return track.Frames[0].Index
	}
	return 0
}

func isDirectionFrame(profile *DirectionProfile, index int) bool {
	if profile == nil {
		return false
	}
	for _, direction := range profile.Directions {
		if direction.FrameIndex == index {
			return true
		}
	}
	return false
}

func rendererIssue(code, resource string) error {
	return &DiagnosticError{Issues: []Issue{{Code: code, Args: map[string]string{"resource": safeRendererResource(resource)}, Severity: SeverityError, Retryable: false, SafeFallbackSummary: defaultIssueSummary(code)}}}
}

func safeRendererResource(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 48 {
		return "asset"
	}
	return value
}

func rendererContextError(ctx context.Context) error {
	if ctx == nil || ctx.Err() == nil {
		return nil
	}
	return ctx.Err()
}
