package pet

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "golang.org/x/image/webp"
)

const (
	manifestName       = "pet.json"
	legacyManifestName = "avatar.json"
	defaultSpritesheet = "spritesheet.webp"
	cellWidth          = 192
	cellHeight         = 208
	maxManifestBytes   = 256 * 1024
	maxPackageBytes    = 32 * 1024 * 1024
	maxFrameCount      = 256
)

// CodexAdapter imports Codex source packages without granting them host
// authority. Legacy avatar loading is an explicitly enabled compatibility
// profile and is disabled by default.
type CodexAdapter struct {
	LegacyProfileEnabled bool
}

type CodexAdapterOption func(*CodexAdapter)

func NewCodexAdapter(options ...CodexAdapterOption) *CodexAdapter {
	adapter := &CodexAdapter{}
	for _, option := range options {
		if option != nil {
			option(adapter)
		}
	}
	return adapter
}

func WithLegacyAvatars(enabled bool) CodexAdapterOption {
	return func(adapter *CodexAdapter) { adapter.LegacyProfileEnabled = enabled }
}

func (a *CodexAdapter) Probe(ctx context.Context, packageRoot string) (SourceProfile, error) {
	inspection, err := a.inspectCodex(ctx, packageRoot)
	if err != nil {
		return SourceProfile{}, err
	}
	if err := contextDiagnostic(ctx); err != nil {
		return SourceProfile{}, err
	}
	return inspection.profile, nil
}

func (a *CodexAdapter) Load(ctx context.Context, packageRoot string) (PetDefinition, error) {
	inspection, err := a.inspectCodex(ctx, packageRoot)
	if err != nil {
		return PetDefinition{}, err
	}
	definition, err := normalizeCodex(inspection.manifest, inspection.profile, inspection.geometry)
	if contextErr := contextDiagnostic(ctx); contextErr != nil {
		return PetDefinition{}, contextErr
	}
	if err != nil {
		return PetDefinition{}, err
	}
	return definition, nil
}

type codexInspection struct {
	location packageLocation
	manifest codexManifest
	profile  SourceProfile
	geometry atlasGeometry
}

func (a *CodexAdapter) inspectCodex(ctx context.Context, input string) (codexInspection, error) {
	if err := contextDiagnostic(ctx); err != nil {
		return codexInspection{}, err
	}
	location, manifest, err := a.readManifest(ctx, input)
	if err != nil {
		return codexInspection{}, err
	}
	if err := contextDiagnostic(ctx); err != nil {
		return codexInspection{}, err
	}
	profile, geometry, err := validateManifest(location, manifest)
	if err != nil {
		return codexInspection{}, err
	}
	if err := validateSpritesheet(ctx, location.root, manifest.SpritesheetPath, geometry); err != nil {
		return codexInspection{}, err
	}
	if err := contextDiagnostic(ctx); err != nil {
		return codexInspection{}, err
	}
	return codexInspection{location: location, manifest: manifest, profile: profile, geometry: geometry}, nil
}

type packageLocation struct {
	root     string
	manifest string
	entry    string
	legacy   bool
}

type codexManifest struct {
	ID                  string                    `json:"id"`
	DisplayName         string                    `json:"displayName"`
	Description         string                    `json:"description"`
	SpriteVersionNumber *int                      `json:"spriteVersionNumber"`
	SpritesheetPath     string                    `json:"spritesheetPath"`
	Frame               *codexFrame               `json:"frame"`
	Animations          map[string]codexAnimation `json:"animations"`
	Aliases             map[string]string         `json:"aliases"`
}

type codexFrame struct {
	Width   int `json:"width"`
	Height  int `json:"height"`
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// atlasGeometry is adapter-local. Host callers receive only renderer-neutral
// frame regions, never Codex's row/column grid.
type atlasGeometry struct {
	imageWidth  int
	imageHeight int
	cellWidth   int
	cellHeight  int
	columns     int
	rows        int
}

type codexAnimation struct {
	Frames   []int    `json:"frames"`
	FPS      *float64 `json:"fps"`
	Loop     *bool    `json:"loop"`
	Fallback *string  `json:"fallback"`
}

func (a *CodexAdapter) readManifest(ctx context.Context, input string) (packageLocation, codexManifest, error) {
	if err := contextDiagnostic(ctx); err != nil {
		return packageLocation{}, codexManifest{}, err
	}
	if strings.TrimSpace(input) == "" {
		return packageLocation{}, codexManifest{}, diagnostic(CodeManifestMissing, "The Codex package manifest was not found.", false)
	}
	absolute, err := filepath.Abs(input)
	if err != nil {
		return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return packageLocation{}, codexManifest{}, diagnostic(CodeManifestMissing, "The Codex package manifest was not found.", false)
		}
		return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	location := packageLocation{}
	switch {
	case info.IsDir():
		location.root = absolute
		manifestPath := filepath.Join(absolute, manifestName)
		manifestInfo, statErr := os.Lstat(manifestPath)
		switch {
		case statErr == nil && manifestInfo.Mode()&os.ModeSymlink != 0:
			return packageLocation{}, codexManifest{}, diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
		case statErr == nil && manifestInfo.Mode().IsRegular():
			location.manifest = manifestPath
			location.entry = manifestName
		case statErr == nil:
			return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
		case !errors.Is(statErr, os.ErrNotExist):
			return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
		default:
			legacyPath := filepath.Join(absolute, legacyManifestName)
			legacyInfo, legacyErr := os.Lstat(legacyPath)
			switch {
			case legacyErr == nil && legacyInfo.Mode()&os.ModeSymlink != 0:
				return packageLocation{}, codexManifest{}, diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
			case legacyErr == nil && legacyInfo.Mode().IsRegular():
				if !a.legacyEnabled() {
					return packageLocation{}, codexManifest{}, diagnostic(CodeLegacyProfileDisabled, "The Codex legacy avatar profile is disabled.", false)
				}
				location.manifest = legacyPath
				location.entry = legacyManifestName
				location.legacy = true
			case legacyErr == nil:
				return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
			case !errors.Is(legacyErr, os.ErrNotExist):
				return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
			default:
				return packageLocation{}, codexManifest{}, diagnostic(CodeManifestMissing, "The Codex package manifest was not found.", false)
			}
		}
	case info.Mode().IsRegular():
		base := strings.ToLower(filepath.Base(absolute))
		switch base {
		case manifestName:
			location.manifest = absolute
			location.root = filepath.Dir(absolute)
			location.entry = filepath.Base(absolute)
		case legacyManifestName:
			if !a.legacyEnabled() {
				return packageLocation{}, codexManifest{}, diagnostic(CodeLegacyProfileDisabled, "The Codex legacy avatar profile is disabled.", false)
			}
			location.manifest = absolute
			location.root = filepath.Dir(absolute)
			location.entry = filepath.Base(absolute)
			location.legacy = true
		default:
			return packageLocation{}, codexManifest{}, diagnostic(CodeManifestMissing, "The Codex package manifest was not found.", false)
		}
	default:
		return packageLocation{}, codexManifest{}, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if err := validateCanonicalPath(location.root, location.manifest); err != nil {
		return packageLocation{}, codexManifest{}, err
	}

	data, err := readManifestBytes(ctx, location.manifest, maxManifestBytes)
	if err != nil {
		return packageLocation{}, codexManifest{}, err
	}
	var manifest codexManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return packageLocation{}, codexManifest{}, diagnostic(CodeManifestInvalid, "The Codex package manifest is invalid.", false)
	}
	return location, manifest, nil
}

type contextAwareReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextAwareReader) Read(p []byte) (int, error) {
	if err := contextDiagnostic(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func readManifestBytes(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	if err := contextDiagnostic(ctx); err != nil {
		return nil, err
	}
	lstatInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, diagnostic(CodeManifestMissing, "The Codex package manifest was not found.", false)
		}
		return nil, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if lstatInfo.Mode()&os.ModeSymlink != 0 {
		return nil, diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	if !lstatInfo.Mode().IsRegular() {
		return nil, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, diagnostic(CodeManifestMissing, "The Codex package manifest was not found.", false)
		}
		return nil, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	defer file.Close()

	handleInfo, err := file.Stat()
	if err != nil {
		return nil, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if !os.SameFile(lstatInfo, handleInfo) {
		return nil, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if !handleInfo.Mode().IsRegular() || handleInfo.Size() > maxBytes {
		return nil, diagnostic(CodeManifestInvalid, "The Codex package manifest is invalid.", false)
	}

	data, err := io.ReadAll(io.LimitReader(contextAwareReader{ctx: ctx, reader: file}, maxBytes+1))
	if err != nil {
		var diagnosticErr Diagnostic
		if errors.As(err, &diagnosticErr) && diagnosticErr.Code == CodeLoadCanceled {
			return nil, err
		}
		return nil, diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if err := contextDiagnostic(ctx); err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, diagnostic(CodeManifestInvalid, "The Codex package manifest is invalid.", false)
	}
	return data, nil
}

func (a *CodexAdapter) legacyEnabled() bool {
	return a != nil && a.LegacyProfileEnabled
}

func contextDiagnostic(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return diagnostic(CodeLoadCanceled, "The Codex package load was canceled.", true)
	}
	return nil
}

func validateManifest(location packageLocation, manifest codexManifest) (SourceProfile, atlasGeometry, error) {
	if !validText(manifest.ID, 64) || !validText(manifest.DisplayName, 256) || !validText(manifest.Description, 4096) {
		return SourceProfile{}, atlasGeometry{}, diagnostic(CodeManifestInvalid, "The Codex package manifest is invalid.", false)
	}
	if manifest.ID == "" || manifest.DisplayName == "" {
		return SourceProfile{}, atlasGeometry{}, diagnostic(CodeManifestInvalid, "The Codex package manifest is invalid.", false)
	}
	version := 1
	if manifest.SpriteVersionNumber != nil {
		version = *manifest.SpriteVersionNumber
	}
	if version != 1 && version != 2 {
		return SourceProfile{}, atlasGeometry{}, diagnostic(CodeVersionGeometryMismatch, "The Codex sprite version is not supported by this adapter.", false)
	}
	geometry := atlasGeometry{
		cellWidth:  cellWidth,
		cellHeight: cellHeight,
		columns:    8,
		rows:       9,
	}
	if version == 2 {
		geometry.rows = 11
	}
	geometry.imageWidth = geometry.cellWidth * geometry.columns
	geometry.imageHeight = geometry.cellHeight * geometry.rows
	if manifest.Frame != nil {
		if manifest.Frame.Width != geometry.cellWidth || manifest.Frame.Height != geometry.cellHeight || manifest.Frame.Columns != geometry.columns || manifest.Frame.Rows != geometry.rows {
			return SourceProfile{}, atlasGeometry{}, diagnostic(CodeVersionGeometryMismatch, "The Codex frame grid does not match its selected sprite version.", false)
		}
	}
	profile := SourceProfile{
		Format:    SourceFormatCodex,
		Version:   version,
		ProfileID: "codex-v1",
		Entry:     location.entry,
		Legacy:    location.legacy,
		Look: LookProfile{
			Mode:          LookModeNone,
			NeutralPolicy: NeutralPolicyDeadzoneToIdle,
		},
	}
	if location.legacy {
		profile.ProfileID = "codex-legacy-v1"
	}
	if version == 2 {
		profile.ProfileID = "codex-v2"
		profile.Look.Mode = LookModeDirectional16
		profile.Look.Directions = directionProfiles(geometry)
		profile.Diagnostics = append(profile.Diagnostics, diagnostic(CodeV2SemanticsUnverified, "Codex v2 desktop look semantics are not fully verified by a source fixture.", false))
	}
	return profile, geometry, nil
}

func validateSpritesheet(ctx context.Context, root, resource string, geometry atlasGeometry) error {
	if err := contextDiagnostic(ctx); err != nil {
		return err
	}
	path, err := safeResourcePath(root, resource)
	if err != nil {
		return err
	}
	extension := strings.ToLower(filepath.Ext(path))
	if extension != ".png" && extension != ".webp" {
		return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet format is not supported.", false)
	}
	lstatInfo, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet is invalid.", false)
		}
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if lstatInfo.Mode()&os.ModeSymlink != 0 {
		return diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	if !lstatInfo.Mode().IsRegular() {
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet is invalid.", false)
		}
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	defer file.Close()
	if err := contextDiagnostic(ctx); err != nil {
		return err
	}
	handleInfo, err := file.Stat()
	if err != nil {
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if !os.SameFile(lstatInfo, handleInfo) {
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if !handleInfo.Mode().IsRegular() {
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if handleInfo.Size() > maxPackageBytes {
		return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet is invalid.", false)
	}
	reader := contextAwareReader{ctx: ctx, reader: file}
	config, format, decodeConfigErr := image.DecodeConfig(reader)
	if err := contextDiagnostic(ctx); err != nil {
		return err
	}
	if decodeConfigErr != nil || (extension == ".png" && format != "png") || (extension == ".webp" && format != "webp") {
		return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet is invalid.", false)
	}
	if config.Width != geometry.imageWidth || config.Height != geometry.imageHeight {
		return diagnostic(CodeVersionGeometryMismatch, "The Codex spritesheet geometry does not match its selected sprite version.", false)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 64*1024*1024 {
		return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet exceeds the supported image budget.", false)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return diagnostic(CodePackageUnreadable, "The Codex package could not be read.", false)
	}
	if err := contextDiagnostic(ctx); err != nil {
		return err
	}
	decoded, format, decodeErr := image.Decode(reader)
	if err := contextDiagnostic(ctx); err != nil {
		return err
	}
	if decodeErr != nil || format != extension[1:] {
		return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet is invalid.", false)
	}
	if bounds := decoded.Bounds(); bounds.Dx() != geometry.imageWidth || bounds.Dy() != geometry.imageHeight || !hasTransparentPixel(decoded) {
		return diagnostic(CodeInvalidSpritesheet, "The Codex spritesheet must contain transparency.", false)
	}
	return nil
}

func hasTransparentPixel(img image.Image) bool {
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

func safeResourcePath(root, resource string) (string, error) {
	resource = strings.TrimSpace(resource)
	if resource == "" {
		resource = defaultSpritesheet
	}
	if strings.IndexByte(resource, 0) >= 0 || filepath.IsAbs(resource) || filepath.VolumeName(resource) != "" || strings.Contains(resource, "://") {
		return "", diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	for _, part := range strings.FieldsFunc(resource, func(r rune) bool { return r == '/' || r == '\\' }) {
		if part == ".." || part == "." || part == "" {
			if part == ".." || part == "." {
				return "", diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
			}
		}
	}
	clean := filepath.Clean(filepath.FromSlash(resource))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	rootAbs = filepath.Clean(rootAbs)
	path := filepath.Join(rootAbs, clean)
	rel, err := filepath.Rel(rootAbs, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	if err := validateCanonicalPath(rootAbs, path); err != nil {
		return "", err
	}
	return path, nil
}

func validateCanonicalPath(root, path string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	pathAbs, err := filepath.Abs(path)
	if err != nil || !pathWithinRoot(rootAbs, pathAbs) {
		return diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	if _, err := os.Stat(pathAbs); err != nil {
		return nil
	}
	realRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	realPath, err := filepath.EvalSymlinks(pathAbs)
	if err != nil || !pathWithinRoot(realRoot, realPath) {
		return diagnostic(CodePathOutsideRoot, "The Codex resource path must stay inside its package.", false)
	}
	return nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func normalizeCodex(manifest codexManifest, profile SourceProfile, geometry atlasGeometry) (PetDefinition, error) {
	durations := defaultDurations()
	rowColumns := []int{6, 8, 8, 4, 5, 8, 6, 6, 6}
	trackIDs := []string{"idle", "running-right", "running-left", "waving", "jumping", "failed", "waiting", "running", "review"}
	tracks := make([]Track, 0, len(trackIDs)+len(manifest.Animations))
	trackIndex := make(map[string]int, len(trackIDs)+len(manifest.Animations))
	for row, id := range trackIDs {
		channel := TrackChannelBase
		if id == "waving" || id == "jumping" {
			channel = TrackChannelAction
		}
		track := Track{
			ID:            id,
			Channel:       channel,
			Frames:        makeFrames("main", geometry, row, rowColumns[row], durations[row]),
			Loop:          id == "idle",
			RepeatCount:   1,
			Interruptible: id == "idle",
			Fallback:      "idle",
		}
		if id != "idle" {
			track.RepeatCount = 3
		}
		trackIndex[id] = len(tracks)
		tracks = append(tracks, track)
	}

	customNames := make([]string, 0, len(manifest.Animations))
	for name := range manifest.Animations {
		customNames = append(customNames, name)
	}
	sort.Strings(customNames)
	knownNames := make(map[string]bool, len(trackIDs)+len(customNames))
	for _, id := range trackIDs {
		knownNames[id] = true
	}
	for _, name := range customNames {
		if !validTrackID(name) {
			return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation declaration is invalid.", false)
		}
		knownNames[name] = true
	}
	for _, name := range customNames {
		animation := manifest.Animations[name]
		if len(animation.Frames) == 0 || len(animation.Frames) > maxFrameCount {
			return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation declaration is invalid.", false)
		}
		fps := 8.0
		if animation.FPS != nil {
			fps = *animation.FPS
		}
		if math.IsNaN(fps) || math.IsInf(fps, 0) || fps <= 0 || fps > 60 {
			return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation declaration is invalid.", false)
		}
		loop := true
		if animation.Loop != nil {
			loop = *animation.Loop
		}
		fallback := "idle"
		if animation.Fallback != nil {
			fallback = strings.TrimSpace(*animation.Fallback)
			if fallback == "" {
				return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation declaration is invalid.", false)
			}
		}
		if !knownNames[fallback] && !defaultAliasTarget(fallback) {
			return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation fallback is not available.", false)
		}
		frames := make([]Frame, len(animation.Frames))
		for index, sourceIndex := range animation.Frames {
			if sourceIndex < 0 || sourceIndex >= geometry.columns*geometry.rows {
				return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation frame index is invalid.", false)
			}
			frames[index] = frameAt("main", geometry, sourceIndex, int(math.Round(1000/fps)))
		}
		if _, exists := trackIndex[name]; exists {
			tracks[trackIndex[name]] = Track{ID: name, Channel: animationChannel(name), Frames: frames, FPS: fps, Loop: loop, RepeatCount: 1, Interruptible: loop, Fallback: resolveTrackReference(fallback, knownNames)}
			continue
		}
		trackIndex[name] = len(tracks)
		tracks = append(tracks, Track{ID: name, Channel: animationChannel(name), Frames: frames, FPS: fps, Loop: loop, RepeatCount: 1, Interruptible: loop, Fallback: resolveTrackReference(fallback, knownNames)})
	}

	aliases := defaultAliases()
	for alias, target := range manifest.Aliases {
		if !validTrackID(alias) || (!knownNames[target] && !defaultAliasTarget(target)) {
			return PetDefinition{}, diagnostic(CodeInvalidAnimation, "The Codex animation alias is invalid.", false)
		}
		aliases[alias] = resolveTrackReference(target, knownNames)
	}
	for _, alias := range []string{"move_right", "move_left", "wave", "bounce", "sad"} {
		if _, custom := manifest.Animations[alias]; custom {
			aliases[alias] = alias
		}
	}
	states := map[string]string{
		"starting":  "idle",
		"idle":      "idle",
		"working":   "running",
		"waiting":   "waiting",
		"reviewing": "review",
		"failed":    "failed",
		"offline":   "idle",
	}
	actions := map[string]string{"completed": "wave"}
	if _, ok := trackIndex["wave"]; !ok {
		actions["completed"] = aliases["wave"]
	}
	look := profile.Look
	if profile.Version == 2 {
		look = LookProfile{Mode: LookModeDirectional16, NeutralPolicy: NeutralPolicyDeadzoneToIdle, Directions: directionProfiles(geometry)}
	}
	diagnostics := append([]Diagnostic(nil), profile.Diagnostics...)
	capabilities := []Capability{CapabilityVisualStatic, CapabilityVisualFrameSequence, CapabilityProjectionTaskState}
	if profile.Version == 2 {
		capabilities = appendCapability(capabilities, CapabilityLookDirectional16)
	}
	for _, track := range tracks {
		if track.Channel == TrackChannelAction && !track.Loop {
			capabilities = appendCapability(capabilities, CapabilityAnimationOneShot)
			break
		}
	}
	return PetDefinition{
		Identity:     manifestIdentity(manifest),
		Source:       profile,
		Assets:       []Asset{{ID: "main", RendererKind: "raster-atlas", Resource: normalizedResource(manifest.SpritesheetPath), Format: strings.TrimPrefix(strings.ToLower(filepath.Ext(normalizedResource(manifest.SpritesheetPath))), "."), Width: geometry.imageWidth, Height: geometry.imageHeight}},
		Canvas:       Canvas{Width: geometry.cellWidth, Height: geometry.cellHeight, PivotX: 0.5, PivotY: 1, ScaleMode: "contain"},
		Capabilities: capabilities,
		Tracks:       tracks,
		States:       states,
		Actions:      actions,
		Aliases:      aliases,
		Look:         look,
		Permissions:  PermissionSet{},
		Fallback:     Fallback{Track: "idle", Placeholder: true},
		Diagnostics:  diagnostics,
	}, nil
}

func appendCapability(capabilities []Capability, capability Capability) []Capability {
	for _, existing := range capabilities {
		if existing == capability {
			return capabilities
		}
	}
	return append(capabilities, capability)
}

func manifestIdentity(manifest codexManifest) Identity {
	return Identity{ID: manifest.ID, DisplayName: manifest.DisplayName, Description: manifest.Description}
}

func normalizedResource(resource string) string {
	if strings.TrimSpace(resource) == "" {
		return defaultSpritesheet
	}
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(resource)))
}

func defaultDurations() [][]int {
	return [][]int{{1680, 660, 660, 840, 840, 1920}, {120, 120, 120, 120, 120, 120, 120, 220}, {120, 120, 120, 120, 120, 120, 120, 220}, {140, 140, 140, 280}, {140, 140, 140, 140, 280}, {140, 140, 140, 140, 140, 140, 140, 240}, {150, 150, 150, 150, 150, 260}, {120, 120, 120, 120, 120, 220}, {150, 150, 150, 150, 150, 280}}
}

func makeFrames(assetID string, geometry atlasGeometry, row, count int, durations []int) []Frame {
	frames := make([]Frame, count)
	for column := 0; column < count; column++ {
		frames[column] = Frame{AssetID: assetID, Region: Rect{X: column * geometry.cellWidth, Y: row * geometry.cellHeight, Width: geometry.cellWidth, Height: geometry.cellHeight}, DurationMS: durations[column]}
	}
	return frames
}

func frameAt(assetID string, geometry atlasGeometry, index, durationMS int) Frame {
	return Frame{AssetID: assetID, Region: Rect{X: (index % geometry.columns) * geometry.cellWidth, Y: (index / geometry.columns) * geometry.cellHeight, Width: geometry.cellWidth, Height: geometry.cellHeight}, DurationMS: durationMS}
}

func directionProfiles(geometry atlasGeometry) []LookDirection {
	directions := make([]LookDirection, 16)
	for index := range directions {
		directions[index] = LookDirection{Degrees: float64(index) * 22.5, Frame: frameAt("main", geometry, (9+index/8)*geometry.columns+index%8, 0)}
	}
	return directions
}

func defaultAliases() map[string]string {
	return map[string]string{"move_right": "running-right", "move_left": "running-left", "wave": "waving", "bounce": "jumping", "sad": "failed"}
}

func defaultAliasTarget(value string) bool {
	_, ok := defaultAliases()[value]
	return ok
}

func resolveDefaultAlias(value string) string {
	if target, ok := defaultAliases()[value]; ok {
		return target
	}
	return value
}

func resolveTrackReference(value string, knownNames map[string]bool) string {
	if knownNames[value] {
		return value
	}
	return resolveDefaultAlias(value)
}

func animationChannel(name string) TrackChannel {
	if name == "waving" || name == "jumping" || name == "wave" || name == "bounce" || name == "celebrate" || name == "attention" {
		return TrackChannelAction
	}
	return TrackChannelBase
}

func validTrackID(value string) bool {
	return validText(value, 64) && value != "" && !strings.ContainsAny(value, "/\\")
}

func validText(value string, max int) bool {
	if len(value) > max || strings.IndexByte(value, 0) >= 0 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
