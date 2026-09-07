package pet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/png"
	"math"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "golang.org/x/image/webp"
)

const (
	nativeSchema         = "dsh.pet"
	nativeSchemaVersion  = 1
	nativeRasterRenderer = "dsh-raster-v1"
	nativeProfile        = nativeRasterRenderer

	nativeIssueUnknownRenderer   = "dsh-native.unknown-renderer"
	nativeIssuePermissionsDenied = "dsh-native.permissions-denied"
	nativeIssueCanvasDenied      = "dsh-native.canvas-denied"
	nativeIssueIntegrityMismatch = "dsh-native.integrity-mismatch"
)

// DshNativeAdapter imports the host-owned dsh.pet package without carrying any
// Codex spritesheet assumptions across the adapter boundary. It only reads a
// package and returns a complete normalized definition.
type DshNativeAdapter struct{}

func NewDshNativeAdapter() DshNativeAdapter {
	return DshNativeAdapter{}
}

var _ PetAdapter = DshNativeAdapter{}

type dshNativeManifest struct {
	Schema        string          `json:"schema"`
	SchemaVersion *int            `json:"schemaVersion"`
	ID            string          `json:"id"`
	DisplayName   string          `json:"displayName"`
	Description   string          `json:"description"`
	Assets        json.RawMessage `json:"assets"`
	Tracks        json.RawMessage `json:"tracks"`
	States        json.RawMessage `json:"states"`
	Actions       json.RawMessage `json:"actions"`
	Fallback      json.RawMessage `json:"fallback"`
	Canvas        json.RawMessage `json:"canvas"`
	Permissions   json.RawMessage `json:"permissions"`
	Capabilities  []string        `json:"capabilities"`
	Inputs        json.RawMessage `json:"inputs"`
	Projection    json.RawMessage `json:"projection"`
	Presentation  json.RawMessage `json:"presentation"`

	canvas       CanvasSpec
	permissions  PermissionPolicy
	fallbackName string
	inputs       map[string]bool
	projection   map[string]string
	presentation PresentationHints
}

type dshNativeAssetManifest struct {
	ID           string   `json:"id"`
	Renderer     string   `json:"renderer"`
	RendererKind string   `json:"rendererKind"`
	Type         string   `json:"type"`
	Kind         string   `json:"kind"`
	Path         string   `json:"path"`
	Entry        string   `json:"entry"`
	Format       string   `json:"format"`
	Integrity    string   `json:"integrity"`
	Capabilities []string `json:"capabilities"`
	FrameWidth   *int     `json:"frameWidth"`
	FrameHeight  *int     `json:"frameHeight"`
	Columns      *int     `json:"columns"`
	Rows         *int     `json:"rows"`
}

type dshNativeTrackManifest struct {
	ID            string            `json:"id"`
	Renderer      string            `json:"renderer"`
	RendererKind  string            `json:"rendererKind"`
	Asset         string            `json:"asset"`
	AssetID       string            `json:"assetId"`
	Channel       string            `json:"channel"`
	Frames        []json.RawMessage `json:"frames"`
	FPS           *float64          `json:"fps"`
	Loop          *bool             `json:"loop"`
	Fallback      *string           `json:"fallback"`
	Priority      *int              `json:"priority"`
	Interruptible *bool             `json:"interruptible"`
	TimeoutMS     *int              `json:"timeoutMs"`
	Aliases       []string          `json:"aliases"`
}

type dshNativeBindingManifest struct {
	Track         string  `json:"track"`
	Loop          *bool   `json:"loop"`
	Fallback      *string `json:"fallback"`
	Priority      *int    `json:"priority"`
	Interruptible *bool   `json:"interruptible"`
	TimeoutMS     *int    `json:"timeoutMs"`
	Renderer      string  `json:"renderer"`
	RendererKind  string  `json:"rendererKind"`
}

type dshNativeFrameManifest struct {
	Index      *int   `json:"index"`
	Frame      *int   `json:"frame"`
	Asset      string `json:"asset"`
	AssetID    string `json:"assetId"`
	DurationMS *int   `json:"durationMs"`
}

type dshNativeAssetInfo struct {
	asset       Asset
	frameWidth  int
	frameHeight int
	columns     int
	rows        int
	frameCount  int
}

type dshNativeRasterInfo struct {
	width  int
	height int
	format string
	pixels int64
}

type dshNativeRasterDecodeError struct {
	code string
}

func (e *dshNativeRasterDecodeError) Error() string {
	return e.code
}

// Probe intentionally shares Load's validation boundary. A native manifest
// cannot be considered probeable if its declared raster or track references
// would not produce a complete definition.
func (DshNativeAdapter) Probe(ctx context.Context, source PackageSource) (ProbeResult, error) {
	definition, err := (DshNativeAdapter{}).Load(ctx, source)
	if err != nil {
		return ProbeResult{}, err
	}
	return ProbeResult{Source: definition.Source, Geometry: definition.Geometry}, nil
}

// Load validates and imports one dsh.pet package. Any validation error returns
// a zero definition; the source package is never written or executed.
func (DshNativeAdapter) Load(ctx context.Context, source PackageSource) (PetDefinition, error) {
	if err := contextErr(ctx); err != nil {
		return PetDefinition{}, err
	}
	inventory, err := inspectPackage(ctx, source)
	if err != nil {
		return PetDefinition{}, err
	}
	definition, _, err := (DshNativeAdapter{}).loadFromInventory(ctx, inventory)
	return definition, err
}

func (DshNativeAdapter) loadFromInventory(ctx context.Context, inventory packageInventory) (PetDefinition, packageInventory, error) {
	if err := contextErr(ctx); err != nil {
		return PetDefinition{}, inventory, err
	}
	manifest, err := parseDshNativeManifest(inventory)
	if err != nil {
		return PetDefinition{}, inventory, err
	}
	assets, assetIndex, geometry, capabilities, err := loadDshNativeAssets(ctx, inventory, manifest)
	if err != nil {
		return PetDefinition{}, inventory, err
	}
	tracks, err := loadDshNativeTracks(ctx, inventory.Source, manifest, assets, assetIndex)
	if err != nil {
		return PetDefinition{}, inventory, err
	}
	states, err := loadDshNativeStates(inventory.Source, manifest, tracks)
	if err != nil {
		return PetDefinition{}, inventory, err
	}
	actions, err := loadDshNativeActions(inventory.Source, manifest, tracks)
	if err != nil {
		return PetDefinition{}, inventory, err
	}
	if err := contextErr(ctx); err != nil {
		return PetDefinition{}, inventory, err
	}

	definition := PetDefinition{
		Identity: Identity{
			ID:          manifest.ID,
			DisplayName: manifest.DisplayName,
			Description: manifest.Description,
		},
		Source: SourceInfo{
			Format:        "dsh-native",
			FormatVersion: strconv.Itoa(nativeSchemaVersion),
			Profile:       nativeProfile,
			Entry:         filepath.ToSlash(inventory.Source.ManifestPath),
			StableKey:     inventory.Source.StableSourceKey,
			Badge:         BadgeDshNative,
			Digest:        packageDigest(inventory),
		},
		Geometry:     geometry,
		Canvas:       manifest.canvas,
		Assets:       make([]Asset, len(assets)),
		Tracks:       tracks,
		States:       states,
		Actions:      actions,
		Permissions:  manifest.permissions,
		Fallback:     FallbackSpec{Idle: manifest.fallbackName},
		Capabilities: capabilities,
		Inputs:       cloneBoolMap(manifest.inputs),
		Projection:   cloneStringMap(manifest.projection),
		Presentation: manifest.presentation,
	}
	for index, info := range assets {
		definition.Assets[index] = cloneNativeAsset(info.asset)
	}
	return definition, inventory, nil
}

func cloneNativeAsset(asset Asset) Asset {
	asset.Data = append([]byte(nil), asset.Data...)
	return asset
}

func parseDshNativeManifest(inventory packageInventory) (dshNativeManifest, error) {
	entry := filepath.ToSlash(filepath.Clean(inventory.Source.ManifestPath))
	if !strings.EqualFold(entry, "dsh-pet.json") {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	file, ok := inventory.Files[filepath.Clean(inventory.Source.ManifestPath)]
	if !ok || int64(len(file.Data)) > MaxManifestBytes {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	var manifest dshNativeManifest
	if err := decodeJSONDocument(file.Data, &manifest); err != nil {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	if manifest.Schema != nativeSchema || manifest.SchemaVersion == nil || *manifest.SchemaVersion != nativeSchemaVersion {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	if !nativePackageIdentifier(manifest.ID) {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	if _, ok := safeString(manifest.DisplayName, MaxSafeTextRunes); !ok || strings.TrimSpace(manifest.DisplayName) == "" {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	if manifest.Description != "" {
		if _, ok := safeString(manifest.Description, MaxSafeTextRunes); !ok {
			return dshNativeManifest{}, nativeManifestError(inventory.Source)
		}
	}
	capabilities, err := validateNativeCapabilities(inventory.Source, manifest.Capabilities)
	if err != nil {
		return dshNativeManifest{}, err
	}
	manifest.Capabilities = capabilities
	canvas, err := parseDshNativeCanvas(inventory.Source, manifest.Canvas)
	if err != nil {
		return dshNativeManifest{}, err
	}
	permissions, err := parseDshNativePermissions(inventory.Source, manifest.Permissions)
	if err != nil {
		return dshNativeManifest{}, err
	}
	inputs, err := parseDshNativeInputs(inventory.Source, manifest.Inputs)
	if err != nil {
		return dshNativeManifest{}, err
	}
	projection, err := parseDshNativeProjection(inventory.Source, manifest.Projection)
	if err != nil {
		return dshNativeManifest{}, err
	}
	presentation, err := parseDshNativePresentation(inventory.Source, manifest.Presentation)
	if err != nil {
		return dshNativeManifest{}, err
	}
	fallback, err := parseDshNativeFallback(inventory.Source, manifest.Fallback)
	if err != nil {
		return dshNativeManifest{}, err
	}
	if !nativeIdentifier(fallback) {
		return dshNativeManifest{}, nativeAnimationError(inventory.Source)
	}
	if len(manifest.Assets) == 0 || isJSONNull(manifest.Assets) {
		return dshNativeManifest{}, nativeManifestError(inventory.Source)
	}
	if len(manifest.Tracks) == 0 || isJSONNull(manifest.Tracks) {
		return dshNativeManifest{}, nativeAnimationError(inventory.Source)
	}
	if _, err := decodeDshNativeObjectMap(manifest.Tracks); err != nil {
		return dshNativeManifest{}, nativeAnimationError(inventory.Source)
	}
	if len(manifest.States) > 0 && !isJSONNull(manifest.States) {
		if _, err := decodeDshNativeObjectMap(manifest.States); err != nil {
			return dshNativeManifest{}, nativeAnimationError(inventory.Source)
		}
	}
	if len(manifest.Actions) > 0 && !isJSONNull(manifest.Actions) {
		if _, err := decodeDshNativeObjectMap(manifest.Actions); err != nil {
			return dshNativeManifest{}, nativeAnimationError(inventory.Source)
		}
	}
	manifest.canvas = canvas
	manifest.permissions = permissions
	manifest.fallbackName = fallback
	manifest.inputs = inputs
	manifest.projection = projection
	manifest.presentation = presentation
	return manifest, nil
}

func nativeManifestError(source PackageSource) error {
	return packageError(source, IssueManifestInvalid, SeverityError, false, nil)
}

func nativeAnimationError(source PackageSource) error {
	return packageError(source, IssueInvalidAnimation, SeverityError, false, nil)
}

func nativeResourceError(source PackageSource, code string) error {
	return packageError(source, code, SeverityError, false, map[string]string{"resource": "asset"})
}

func nativeRasterError(source PackageSource, code string) error {
	return packageError(source, code, SeverityError, false, map[string]string{"resource": "raster"})
}

func decodeDshNativeObjectMap(raw json.RawMessage) (map[string]json.RawMessage, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	var values map[string]json.RawMessage
	if err := decodeJSONDocument(raw, &values); err != nil || values == nil {
		return nil, err
	}
	return values, nil
}

func decodeDshNativeAssets(raw json.RawMessage) ([]dshNativeAssetManifest, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, strconv.ErrSyntax
	}
	if trimmed[0] == '[' {
		var values []json.RawMessage
		if err := decodeJSONDocument(raw, &values); err != nil {
			return nil, err
		}
		assets := make([]dshNativeAssetManifest, len(values))
		for index, value := range values {
			if err := decodeJSONDocument(value, &assets[index]); err != nil {
				return nil, err
			}
		}
		return assets, nil
	}
	if trimmed[0] != '{' {
		return nil, strconv.ErrSyntax
	}
	values, err := decodeDshNativeObjectMap(raw)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	assets := make([]dshNativeAssetManifest, 0, len(keys))
	for _, key := range keys {
		var asset dshNativeAssetManifest
		if err := decodeJSONDocument(values[key], &asset); err != nil {
			return nil, err
		}
		if asset.ID == "" {
			asset.ID = key
		} else if asset.ID != key {
			return nil, strconv.ErrSyntax
		}
		assets = append(assets, asset)
	}
	return assets, nil
}

func parseDshNativeCanvas(source PackageSource, raw json.RawMessage) (CanvasSpec, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return CanvasSpec{}, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("false")) || bytes.Equal(trimmed, []byte(`"deny"`)) {
		return CanvasSpec{}, nil
	}
	if bytes.Equal(trimmed, []byte("true")) {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	values, err := decodeDshNativeObjectMap(raw)
	if err != nil {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	for key := range values {
		switch key {
		case "allow", "enabled", "width", "height", "pivot", "pivotX", "pivotY", "scaleMode", "safePadding":
		default:
			return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
		}
	}
	var canvas struct {
		Allow   *bool    `json:"allow"`
		Enabled *bool    `json:"enabled"`
		Width   *int     `json:"width"`
		Height  *int     `json:"height"`
		PivotX  *float64 `json:"pivotX"`
		PivotY  *float64 `json:"pivotY"`
		Pivot   *struct {
			X *float64 `json:"x"`
			Y *float64 `json:"y"`
		} `json:"pivot"`
		ScaleMode   string `json:"scaleMode"`
		SafePadding *int   `json:"safePadding"`
	}
	if err := decodeJSONDocument(raw, &canvas); err != nil {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	if (canvas.Allow != nil && *canvas.Allow) || (canvas.Enabled != nil && *canvas.Enabled) {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	if (canvas.Width != nil) != (canvas.Height != nil) || (canvas.Width != nil && (*canvas.Width <= 0 || *canvas.Height <= 0 || *canvas.Width > MaxImageWidth || *canvas.Height > MaxImageHeight)) {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	if canvas.PivotX != nil && (math.IsNaN(*canvas.PivotX) || math.IsInf(*canvas.PivotX, 0) || *canvas.PivotX < 0 || *canvas.PivotX > 1) {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	if canvas.PivotY != nil && (math.IsNaN(*canvas.PivotY) || math.IsInf(*canvas.PivotY, 0) || *canvas.PivotY < 0 || *canvas.PivotY > 1) {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	if canvas.Pivot != nil {
		if canvas.Pivot.X != nil && (math.IsNaN(*canvas.Pivot.X) || math.IsInf(*canvas.Pivot.X, 0) || *canvas.Pivot.X < 0 || *canvas.Pivot.X > 1) {
			return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
		}
		if canvas.Pivot.Y != nil && (math.IsNaN(*canvas.Pivot.Y) || math.IsInf(*canvas.Pivot.Y, 0) || *canvas.Pivot.Y < 0 || *canvas.Pivot.Y > 1) {
			return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
		}
	}
	if canvas.SafePadding != nil && (*canvas.SafePadding < 0 || *canvas.SafePadding > MaxImageWidth) {
		return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
	}
	if canvas.ScaleMode != "" {
		mode := strings.ToLower(strings.TrimSpace(canvas.ScaleMode))
		if mode != "none" && mode != "contain" && mode != "cover" && mode != "stretch" {
			return CanvasSpec{}, packageError(source, nativeIssueCanvasDenied, SeverityError, false, nil)
		}
		canvas.ScaleMode = mode
	}
	result := CanvasSpec{ScaleMode: canvas.ScaleMode}
	if canvas.Width != nil {
		result.Width = *canvas.Width
		result.Height = *canvas.Height
	}
	if canvas.PivotX != nil {
		result.PivotX = *canvas.PivotX
	}
	if canvas.PivotY != nil {
		result.PivotY = *canvas.PivotY
	}
	if canvas.Pivot != nil {
		if canvas.Pivot.X != nil {
			result.PivotX = *canvas.Pivot.X
		}
		if canvas.Pivot.Y != nil {
			result.PivotY = *canvas.Pivot.Y
		}
	}
	if canvas.SafePadding != nil {
		result.SafePadding = *canvas.SafePadding
	}
	return result, nil
}

func validateNativeCapabilities(source PackageSource, values []string) ([]string, error) {
	if len(values) > MaxIssueCount {
		return nil, packageError(source, IssueManifestInvalid, SeverityError, false, nil)
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, `/\\`) || strings.Contains(value, "://") {
			return nil, packageError(source, IssueManifestInvalid, SeverityError, false, nil)
		}
		if _, ok := safeString(value, MaxSafeTextRunes); !ok {
			return nil, packageError(source, IssueManifestInvalid, SeverityError, false, nil)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func parseDshNativeInputs(source PackageSource, raw json.RawMessage) (map[string]bool, error) {
	values, err := decodeDshNativeObjectMap(raw)
	if err != nil || len(values) > MaxIssueCount {
		return nil, nativeManifestError(source)
	}
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]bool, len(values))
	for key, rawValue := range values {
		if !nativeIdentifier(key) {
			return nil, nativeManifestError(source)
		}
		var enabled bool
		if err := decodeJSONDocument(rawValue, &enabled); err != nil {
			return nil, nativeManifestError(source)
		}
		result[key] = enabled
	}
	return result, nil
}

func parseDshNativeProjection(source PackageSource, raw json.RawMessage) (map[string]string, error) {
	values, err := decodeDshNativeObjectMap(raw)
	if err != nil || len(values) > MaxIssueCount {
		return nil, nativeManifestError(source)
	}
	if len(values) == 0 {
		return nil, nil
	}
	result := make(map[string]string, len(values))
	for key, rawValue := range values {
		if !nativeIdentifier(key) {
			return nil, nativeManifestError(source)
		}
		var target string
		if err := decodeJSONDocument(rawValue, &target); err != nil || !nativeIdentifier(target) {
			return nil, nativeManifestError(source)
		}
		result[key] = target
	}
	return result, nil
}

func parseDshNativePresentation(source PackageSource, raw json.RawMessage) (PresentationHints, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return PresentationHints{}, nil
	}
	values, err := decodeDshNativeObjectMap(raw)
	if err != nil {
		return PresentationHints{}, nativeManifestError(source)
	}
	for key := range values {
		switch key {
		case "anchorX", "anchorY", "scale":
		default:
			return PresentationHints{}, nativeManifestError(source)
		}
	}
	var presentation struct {
		AnchorX *float64 `json:"anchorX"`
		AnchorY *float64 `json:"anchorY"`
		Scale   *float64 `json:"scale"`
	}
	if err := decodeJSONDocument(raw, &presentation); err != nil {
		return PresentationHints{}, nativeManifestError(source)
	}
	if presentation.AnchorX != nil && (*presentation.AnchorX < 0 || *presentation.AnchorX > 1 || math.IsNaN(*presentation.AnchorX) || math.IsInf(*presentation.AnchorX, 0)) {
		return PresentationHints{}, nativeManifestError(source)
	}
	if presentation.AnchorY != nil && (*presentation.AnchorY < 0 || *presentation.AnchorY > 1 || math.IsNaN(*presentation.AnchorY) || math.IsInf(*presentation.AnchorY, 0)) {
		return PresentationHints{}, nativeManifestError(source)
	}
	if presentation.Scale != nil && (*presentation.Scale <= 0 || *presentation.Scale > 8 || math.IsNaN(*presentation.Scale) || math.IsInf(*presentation.Scale, 0)) {
		return PresentationHints{}, nativeManifestError(source)
	}
	result := PresentationHints{}
	if presentation.AnchorX != nil {
		result.AnchorX = *presentation.AnchorX
	}
	if presentation.AnchorY != nil {
		result.AnchorY = *presentation.AnchorY
	}
	if presentation.Scale != nil {
		result.Scale = *presentation.Scale
	}
	return result, nil
}

func parseDshNativePermissions(source PackageSource, raw json.RawMessage) (PermissionPolicy, error) {
	deny := PermissionPolicy{Network: "deny", Process: "deny", Scripts: "deny", Audio: "deny"}
	if len(raw) == 0 || isJSONNull(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("false")) || bytes.Equal(bytes.TrimSpace(raw), []byte(`"deny"`)) {
		return deny, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("true")) {
		return PermissionPolicy{}, packageError(source, nativeIssuePermissionsDenied, SeverityError, false, nil)
	}
	values, err := decodeDshNativeObjectMap(raw)
	if err != nil {
		return PermissionPolicy{}, packageError(source, nativeIssuePermissionsDenied, SeverityError, false, nil)
	}
	for key, value := range values {
		if key != "network" && key != "process" && key != "scripts" && key != "audio" {
			return PermissionPolicy{}, packageError(source, nativeIssuePermissionsDenied, SeverityError, false, nil)
		}
		if !nativeDenyValue(value) {
			return PermissionPolicy{}, packageError(source, nativeIssuePermissionsDenied, SeverityError, false, nil)
		}
	}
	return deny, nil
}

func nativeDenyValue(raw json.RawMessage) bool {
	if len(raw) == 0 || isJSONNull(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte("false")) {
		return true
	}
	var value string
	if decodeJSONDocument(raw, &value) == nil {
		value = strings.ToLower(strings.TrimSpace(value))
		return value == "" || value == "deny"
	}
	return false
}

func parseDshNativeFallback(source PackageSource, raw json.RawMessage) (string, error) {
	if len(raw) == 0 || isJSONNull(raw) {
		return "idle", nil
	}
	var name string
	if decodeJSONDocument(raw, &name) == nil {
		if nativeIdentifier(name) {
			return name, nil
		}
		return "", nativeAnimationError(source)
	}
	var object struct {
		Idle string `json:"idle"`
	}
	if decodeJSONDocument(raw, &object) == nil && nativeIdentifier(object.Idle) {
		return object.Idle, nil
	}
	return "", nativeAnimationError(source)
}

func loadDshNativeAssets(ctx context.Context, inventory packageInventory, manifest dshNativeManifest) ([]dshNativeAssetInfo, map[string]int, Geometry, []string, error) {
	declared, err := decodeDshNativeAssets(manifest.Assets)
	if err != nil || len(declared) == 0 || len(declared) > MaxTrackFrames {
		return nil, nil, Geometry{}, nil, nativeManifestError(inventory.Source)
	}
	assets := make([]dshNativeAssetInfo, 0, len(declared))
	assetIndex := make(map[string]int, len(declared))
	decoded := make(map[string]dshNativeRasterInfo, len(declared))
	capabilities := append([]string(nil), manifest.Capabilities...)
	var totalPixels int64
	totalFrames := 0
	for _, declaredAsset := range declared {
		if err := contextErr(ctx); err != nil {
			return nil, nil, Geometry{}, nil, err
		}
		if !nativeIdentifier(declaredAsset.ID) || declaredAsset.ID == "" {
			return nil, nil, Geometry{}, nil, nativeManifestError(inventory.Source)
		}
		if _, exists := assetIndex[declaredAsset.ID]; exists {
			return nil, nil, Geometry{}, nil, nativeManifestError(inventory.Source)
		}
		renderer, ok := nativeRendererName(declaredAsset.Renderer, declaredAsset.RendererKind)
		if !ok || renderer != nativeRasterRenderer {
			return nil, nil, Geometry{}, nil, packageError(inventory.Source, nativeIssueUnknownRenderer, SeverityError, false, nil)
		}
		resourcePath := strings.TrimSpace(declaredAsset.Path)
		if declaredAsset.Entry != "" {
			entryPath := strings.TrimSpace(declaredAsset.Entry)
			if resourcePath != "" && resourcePath != entryPath {
				return nil, nil, Geometry{}, nil, nativeManifestError(inventory.Source)
			}
			resourcePath = entryPath
		}
		relative, pathCode := safeRelativePath(resourcePath)
		if pathCode != "" {
			return nil, nil, Geometry{}, nil, nativeResourceError(inventory.Source, pathCode)
		}
		file, exists := inventory.Files[relative]
		if !exists {
			return nil, nil, Geometry{}, nil, nativeResourceError(inventory.Source, IssueManifestMissing)
		}
		if _, exists := decoded[relative]; exists {
			return nil, nil, Geometry{}, nil, nativeManifestError(inventory.Source)
		}
		if ext := strings.ToLower(filepath.Ext(relative)); ext != ".png" && ext != ".webp" {
			return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
		}
		raster, err := decodeDshNativeRaster(file.Data)
		if err != nil {
			return nil, nil, Geometry{}, nil, nativeRasterDecodeIssue(inventory.Source, err)
		}
		if declaredAsset.Format != "" && strings.ToLower(strings.TrimSpace(declaredAsset.Format)) != raster.format {
			return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
		}
		if declaredAsset.Integrity != "" && !nativeIntegrityMatches(declaredAsset.Integrity, file.Data) {
			return nil, nil, Geometry{}, nil, packageError(inventory.Source, nativeIssueIntegrityMismatch, SeverityError, false, map[string]string{"resource": "asset"})
		}
		assetCapabilities, err := validateNativeCapabilities(inventory.Source, declaredAsset.Capabilities)
		if err != nil {
			return nil, nil, Geometry{}, nil, err
		}
		capabilities = append(capabilities, assetCapabilities...)
		if totalPixels > MaxDecodedPixels-raster.pixels {
			return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueDecodedPixelsExceeded)
		}
		totalPixels += raster.pixels
		decoded[relative] = raster

		kind := nativeAssetKind(declaredAsset)
		if kind == "" {
			return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
		}
		frameWidth, frameHeight := raster.width, raster.height
		columns, rows, frameCount := 1, 1, 1
		if kind == "atlas" {
			if declaredAsset.FrameWidth == nil || declaredAsset.FrameHeight == nil || declaredAsset.Columns == nil || declaredAsset.Rows == nil ||
				*declaredAsset.FrameWidth <= 0 || *declaredAsset.FrameHeight <= 0 || *declaredAsset.Columns <= 0 || *declaredAsset.Rows <= 0 {
				return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
			}
			frameWidth, frameHeight = *declaredAsset.FrameWidth, *declaredAsset.FrameHeight
			columns, rows = *declaredAsset.Columns, *declaredAsset.Rows
			if int64(frameWidth)*int64(columns) != int64(raster.width) || int64(frameHeight)*int64(rows) != int64(raster.height) {
				return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
			}
			frames := int64(columns) * int64(rows)
			if frames <= 0 || frames > MaxTrackFrames || frames > int64(^uint(0)>>1) {
				return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
			}
			frameCount = int(frames)
		}
		if totalFrames > MaxTrackFrames-frameCount {
			return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueInvalidSpritesheet)
		}
		totalFrames += frameCount
		assetIndex[declaredAsset.ID] = len(assets)
		assets = append(assets, dshNativeAssetInfo{
			asset: Asset{
				ID:     declaredAsset.ID,
				Kind:   kind,
				Path:   filepath.ToSlash(relative),
				Format: raster.format,
				Size:   int64(len(file.Data)),
				Width:  raster.width,
				Height: raster.height,
				SHA256: HashBytes(file.Data),
				Data:   append([]byte(nil), file.Data...),
			},
			frameWidth: frameWidth, frameHeight: frameHeight,
			columns: columns, rows: rows, frameCount: frameCount,
		})
	}
	for _, relative := range inventory.Ordered {
		if err := contextErr(ctx); err != nil {
			return nil, nil, Geometry{}, nil, err
		}
		if !looksLikeImagePath(relative) {
			continue
		}
		if _, exists := decoded[relative]; exists {
			continue
		}
		raster, err := decodeDshNativeRaster(inventory.Files[relative].Data)
		if err != nil {
			return nil, nil, Geometry{}, nil, nativeRasterDecodeIssue(inventory.Source, err)
		}
		if totalPixels > MaxDecodedPixels-raster.pixels {
			return nil, nil, Geometry{}, nil, nativeRasterError(inventory.Source, IssueDecodedPixelsExceeded)
		}
		totalPixels += raster.pixels
	}
	capabilities, err = validateNativeCapabilities(inventory.Source, capabilities)
	if err != nil {
		return nil, nil, Geometry{}, nil, err
	}
	return assets, assetIndex, nativeGeometry(assets, totalFrames), capabilities, nil
}

func nativeRendererName(first, second string) (string, bool) {
	first = strings.TrimSpace(first)
	second = strings.TrimSpace(second)
	if first != "" && second != "" && first != second {
		return "", false
	}
	if first != "" {
		return first, true
	}
	return second, second != ""
}

func nativeIntegrityMatches(value string, data []byte) bool {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(strings.ToLower(value), "sha256:") {
		return false
	}
	digest := value[len("sha256:"):]
	if len(digest) != 64 {
		return false
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return false
	}
	return strings.EqualFold(digest, HashBytes(data))
}

func nativeAssetKind(asset dshNativeAssetManifest) string {
	kind := strings.ToLower(strings.TrimSpace(asset.Type))
	declaredKind := strings.ToLower(strings.TrimSpace(asset.Kind))
	if kind != "" && declaredKind != "" && kind != declaredKind {
		return ""
	}
	if kind == "" {
		kind = declaredKind
	}
	if kind == "" {
		if asset.FrameWidth != nil || asset.FrameHeight != nil || asset.Columns != nil || asset.Rows != nil {
			kind = "atlas"
		} else {
			kind = "image"
		}
	}
	switch kind {
	case "image", "independent", "single":
		if asset.FrameWidth != nil || asset.FrameHeight != nil || asset.Columns != nil || asset.Rows != nil {
			return ""
		}
		return "image"
	case "atlas":
		return "atlas"
	default:
		return ""
	}
}

func decodeDshNativeRaster(data []byte) (dshNativeRasterInfo, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "webp") {
		if err == nil {
			err = errors.New("unsupported raster format")
		}
		return dshNativeRasterInfo{}, err
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxImageWidth || config.Height > MaxImageHeight {
		return dshNativeRasterInfo{}, &dshNativeRasterDecodeError{code: IssueDecodedImageTooLarge}
	}
	pixels := int64(config.Width) * int64(config.Height)
	if pixels <= 0 || pixels > MaxDecodedPixels {
		return dshNativeRasterInfo{}, &dshNativeRasterDecodeError{code: IssueDecodedPixelsExceeded}
	}
	_, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		if err == nil {
			err = errors.New("raster format mismatch")
		}
		return dshNativeRasterInfo{}, err
	}
	return dshNativeRasterInfo{width: config.Width, height: config.Height, format: format, pixels: pixels}, nil
}

func nativeRasterDecodeIssue(source PackageSource, err error) error {
	code := IssueInvalidSpritesheet
	var validationErr *dshNativeRasterDecodeError
	if errors.As(err, &validationErr) {
		code = validationErr.code
	}
	return nativeRasterError(source, code)
}

func nativeGeometry(assets []dshNativeAssetInfo, totalFrames int) Geometry {
	if len(assets) == 1 {
		asset := assets[0]
		return Geometry{
			CellWidth: asset.frameWidth, CellHeight: asset.frameHeight,
			Columns: asset.columns, Rows: asset.rows,
			ImageWidth: asset.asset.Width, ImageHeight: asset.asset.Height,
			FrameCount: asset.frameCount,
		}
	}
	return Geometry{FrameCount: totalFrames}
}

func loadDshNativeTracks(ctx context.Context, source PackageSource, manifest dshNativeManifest, assets []dshNativeAssetInfo, assetIndex map[string]int) (map[string]TrackSpec, error) {
	values, err := decodeDshNativeObjectMap(manifest.Tracks)
	if err != nil || len(values) == 0 {
		return nil, nativeAnimationError(source)
	}
	names := sortedNativeKeys(values)
	tracks := make(map[string]TrackSpec, len(names))
	usedAliases := make(map[string]string)
	for _, name := range names {
		if err := contextErr(ctx); err != nil {
			return nil, err
		}
		if !nativeIdentifier(name) {
			return nil, nativeAnimationError(source)
		}
		var declared dshNativeTrackManifest
		if err := decodeJSONDocument(values[name], &declared); err != nil {
			return nil, nativeAnimationError(source)
		}
		if declared.ID != "" && declared.ID != name {
			return nil, nativeAnimationError(source)
		}
		if renderer, ok := nativeRendererName(declared.Renderer, declared.RendererKind); (!ok && (declared.Renderer != "" || declared.RendererKind != "")) || (ok && renderer != "" && renderer != nativeRasterRenderer) {
			return nil, packageError(source, nativeIssueUnknownRenderer, SeverityError, false, nil)
		}
		if len(declared.Frames) == 0 || len(declared.Frames) > MaxTrackFrames {
			return nil, nativeAnimationError(source)
		}
		fps := 8.0
		if declared.FPS != nil {
			fps = *declared.FPS
		}
		if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > MaxRendererFPS {
			return nil, nativeAnimationError(source)
		}
		loop := true
		if declared.Loop != nil {
			loop = *declared.Loop
		}
		fallback := manifest.fallbackName
		if declared.Fallback != nil {
			fallback = *declared.Fallback
		}
		if !nativeIdentifier(fallback) {
			return nil, nativeAnimationError(source)
		}
		priority := 0
		if declared.Priority != nil {
			priority = *declared.Priority
		}
		interruptible := true
		if declared.Interruptible != nil {
			interruptible = *declared.Interruptible
		}
		timeout := 0
		if declared.TimeoutMS != nil {
			timeout = *declared.TimeoutMS
			if timeout < 0 || int64(timeout) > MaxNonLoopDurationMS {
				return nil, nativeAnimationError(source)
			}
		}
		channel, ok := nativeTrackChannel(declared.Channel)
		if !ok {
			return nil, nativeAnimationError(source)
		}
		assetID := declared.Asset
		if declared.AssetID != "" {
			if assetID != "" && assetID != declared.AssetID {
				return nil, nativeAnimationError(source)
			}
			assetID = declared.AssetID
		}
		if assetID != "" && !nativeIdentifier(assetID) {
			return nil, nativeAnimationError(source)
		}
		durationDefault := int(math.Round(1000 / fps))
		if durationDefault < 1 {
			durationDefault = 1
		}
		frames := make([]FrameRef, len(declared.Frames))
		var durationTotal int64
		for index, rawFrame := range declared.Frames {
			frame, err := decodeDshNativeFrame(rawFrame)
			if err != nil {
				return nil, nativeAnimationError(source)
			}
			frameAssetID := assetID
			frameDeclaredAsset := frame.Asset
			if frame.AssetID != "" {
				if frameDeclaredAsset != "" && frameDeclaredAsset != frame.AssetID {
					return nil, nativeAnimationError(source)
				}
				frameDeclaredAsset = frame.AssetID
			}
			if frameDeclaredAsset != "" {
				if frameAssetID != "" && frameAssetID != frameDeclaredAsset {
					return nil, nativeAnimationError(source)
				}
				frameAssetID = frameDeclaredAsset
			}
			if frameAssetID == "" {
				if len(assetIndex) != 1 {
					return nil, nativeAnimationError(source)
				}
				for candidate := range assetIndex {
					frameAssetID = candidate
				}
			}
			if !nativeIdentifier(frameAssetID) {
				return nil, nativeAnimationError(source)
			}
			assetPosition, exists := assetIndex[frameAssetID]
			if !exists || assetPosition < 0 || assetPosition >= len(assets) || frame.Index == nil || *frame.Index < 0 || *frame.Index >= assets[assetPosition].frameCount {
				return nil, nativeAnimationError(source)
			}
			duration := durationDefault
			if frame.DurationMS != nil {
				duration = *frame.DurationMS
			}
			if duration <= 0 || int64(duration) > MaxNonLoopDurationMS || (!loop && durationTotal > MaxNonLoopDurationMS-int64(duration)) {
				return nil, nativeAnimationError(source)
			}
			durationTotal += int64(duration)
			frames[index] = nativeFrameForAsset(assets[assetPosition], *frame.Index, duration)
		}
		if !loop && durationTotal > MaxNonLoopDurationMS {
			return nil, nativeAnimationError(source)
		}
		aliases := append([]string(nil), declared.Aliases...)
		for _, alias := range aliases {
			if !nativeIdentifier(alias) || alias == name {
				return nil, nativeAnimationError(source)
			}
			if previous, exists := usedAliases[alias]; exists && previous != name {
				return nil, nativeAnimationError(source)
			}
			usedAliases[alias] = name
		}
		tracks[name] = TrackSpec{
			ID:            name,
			Channel:       channel,
			RendererKind:  nativeRasterRenderer,
			Frames:        frames,
			FPS:           fps,
			Loop:          loop,
			Priority:      priority,
			Interruptible: interruptible,
			Fallback:      fallback,
			TimeoutMS:     timeout,
			Aliases:       aliases,
		}
	}
	if err := validateTrackFallbacks(source, tracks); err != nil {
		return nil, err
	}
	return tracks, nil
}

func decodeDshNativeFrame(raw json.RawMessage) (dshNativeFrameManifest, error) {
	var index int
	if decodeJSONDocument(raw, &index) == nil {
		return dshNativeFrameManifest{Index: &index}, nil
	}
	var frame dshNativeFrameManifest
	if err := decodeJSONDocument(raw, &frame); err != nil {
		return dshNativeFrameManifest{}, err
	}
	if frame.Index != nil && frame.Frame != nil && *frame.Index != *frame.Frame {
		return dshNativeFrameManifest{}, strconv.ErrSyntax
	}
	if frame.Index == nil {
		frame.Index = frame.Frame
	}
	if frame.Index == nil {
		return dshNativeFrameManifest{}, strconv.ErrSyntax
	}
	return frame, nil
}

func nativeTrackChannel(value string) (TrackChannel, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(TrackBase):
		return TrackBase, true
	case string(TrackAction):
		return TrackAction, true
	case string(TrackLook):
		return TrackLook, true
	default:
		return "", false
	}
}

func sortedNativeKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func nativeIdentifier(value string) bool {
	if value == "" || strings.TrimSpace(value) != value {
		return false
	}
	if _, ok := safeString(value, MaxSafeTextRunes); !ok {
		return false
	}
	return !strings.ContainsAny(value, `/\:`) && value != "." && value != ".."
}

func nativePackageIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	first := value[0]
	if !((first >= 'a' && first <= 'z') || (first >= '0' && first <= '9')) {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func loadDshNativeStates(source PackageSource, manifest dshNativeManifest, tracks map[string]TrackSpec) (map[string]StateSpec, error) {
	values, err := decodeDshNativeObjectMap(manifest.States)
	if err != nil {
		return nil, nativeAnimationError(source)
	}
	if len(values) == 0 {
		track, ok := tracks[manifest.fallbackName]
		if !ok {
			return nil, nativeAnimationError(source)
		}
		return map[string]StateSpec{"idle": {Track: manifest.fallbackName, Loop: track.Loop, Fallback: track.Fallback}}, nil
	}
	states := make(map[string]StateSpec, len(values))
	for _, name := range sortedNativeKeys(values) {
		if !nativeIdentifier(name) {
			return nil, nativeAnimationError(source)
		}
		binding, err := decodeDshNativeBinding(values[name])
		if err != nil || !nativeIdentifier(binding.Track) {
			return nil, nativeAnimationError(source)
		}
		track, ok := tracks[binding.Track]
		if !ok {
			return nil, nativeAnimationError(source)
		}
		loop := track.Loop
		if binding.Loop != nil {
			loop = *binding.Loop
		}
		fallback := track.Fallback
		if fallback == "" {
			fallback = manifest.fallbackName
		}
		if binding.Fallback != nil {
			fallback = *binding.Fallback
		}
		if !nativeIdentifier(fallback) {
			return nil, nativeAnimationError(source)
		}
		if _, ok := tracks[fallback]; !ok {
			return nil, nativeAnimationError(source)
		}
		states[name] = StateSpec{Track: binding.Track, Loop: loop, Fallback: fallback}
	}
	return states, nil
}

func loadDshNativeActions(source PackageSource, manifest dshNativeManifest, tracks map[string]TrackSpec) (map[string]ActionSpec, error) {
	values, err := decodeDshNativeObjectMap(manifest.Actions)
	if err != nil {
		return nil, nativeAnimationError(source)
	}
	actions := make(map[string]ActionSpec, len(values))
	for _, name := range sortedNativeKeys(values) {
		if !nativeIdentifier(name) {
			return nil, nativeAnimationError(source)
		}
		binding, err := decodeDshNativeBinding(values[name])
		if err != nil || !nativeIdentifier(binding.Track) {
			return nil, nativeAnimationError(source)
		}
		track, ok := tracks[binding.Track]
		if !ok {
			return nil, nativeAnimationError(source)
		}
		if renderer, ok := nativeRendererName(binding.Renderer, binding.RendererKind); (!ok && (binding.Renderer != "" || binding.RendererKind != "")) || (ok && renderer != "" && renderer != nativeRasterRenderer) {
			return nil, packageError(source, nativeIssueUnknownRenderer, SeverityError, false, nil)
		}
		priority := track.Priority
		if binding.Priority != nil {
			priority = *binding.Priority
		}
		interruptible := track.Interruptible
		if binding.Interruptible != nil {
			interruptible = *binding.Interruptible
		}
		fallback := track.Fallback
		if fallback == "" {
			fallback = manifest.fallbackName
		}
		if binding.Fallback != nil {
			fallback = *binding.Fallback
		}
		if !nativeIdentifier(fallback) {
			return nil, nativeAnimationError(source)
		}
		if _, ok := tracks[fallback]; !ok {
			return nil, nativeAnimationError(source)
		}
		if binding.TimeoutMS != nil {
			timeout := *binding.TimeoutMS
			if timeout < 0 || int64(timeout) > MaxNonLoopDurationMS {
				return nil, nativeAnimationError(source)
			}
			if track.TimeoutMS != 0 && track.TimeoutMS != timeout {
				return nil, nativeAnimationError(source)
			}
			if timeout != 0 {
				track.TimeoutMS = timeout
				tracks[binding.Track] = track
			}
		}
		actions[name] = ActionSpec{Track: binding.Track, Priority: priority, Interruptible: interruptible, Fallback: fallback}
	}
	return actions, nil
}

func decodeDshNativeBinding(raw json.RawMessage) (dshNativeBindingManifest, error) {
	var track string
	if decodeJSONDocument(raw, &track) == nil {
		return dshNativeBindingManifest{Track: track}, nil
	}
	var binding dshNativeBindingManifest
	if err := decodeJSONDocument(raw, &binding); err != nil || binding.Track == "" {
		return dshNativeBindingManifest{}, strconv.ErrSyntax
	}
	return binding, nil
}

func nativeFrameForAsset(asset dshNativeAssetInfo, index, duration int) FrameRef {
	frame := FrameRef{Index: index, DurationMS: duration, AssetID: asset.asset.ID, Width: asset.frameWidth, Height: asset.frameHeight}
	if asset.asset.Kind == "atlas" {
		frame.X = (index % asset.columns) * asset.frameWidth
		frame.Y = (index / asset.columns) * asset.frameHeight
	}
	return frame
}
