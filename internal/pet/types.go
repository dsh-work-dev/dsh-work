// Package pet contains the Host-owned, format-independent Pet catalog,
// runtime, renderer and overlay policy contracts.
package pet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxManifestBytes       int64   = 256 * 1024
	MaxPackageBytes        int64   = 32 * 1024 * 1024
	MaxPackageFiles                = 64
	MaxImageWidth                  = 4096
	MaxImageHeight                 = 4096
	MaxDecodedPixels       int64   = 64 * 1024 * 1024
	MaxTrackFrames                 = 256
	MaxRendererFPS         float64 = 60
	MaxNonLoopDurationMS   int64   = 120 * 1000
	MaxIssueCount                  = 64
	MaxCatalogPackages             = 256
	MaxFolderIdentityRunes         = 48
	MaxSafeTextRunes               = 256
)

// ScanState describes the publication state of a catalog snapshot.
type ScanState string

const (
	ScanNeverScanned ScanState = "never-scanned"
	ScanScanning     ScanState = "scanning"
	ScanReady        ScanState = "ready"
	ScanFailed       ScanState = "failed"
)

// SourceKind is a safe source label. It is not a filesystem path.
type SourceKind string

const (
	SourceCodexPets SourceKind = "codex-pets"
	// SourceCodexAvatars is the Codex compatibility root. It uses the same
	// Codex manifest and atlas contract as SourceCodexPets.
	SourceCodexAvatars SourceKind = "codex-avatars"
	SourceDshPets      SourceKind = "dsh-pets"
	SourceCodexHome    SourceKind = "codex-home"
	SourceUnknown      SourceKind = "unknown"
)

// RootKind and RootStatus are the safe root projections exposed by a catalog
// snapshot.
type RootKind string

const (
	RootCodexPets    RootKind = "codex-pets"
	RootCodexAvatars RootKind = "codex-avatars"
)

type RootStatus string

const (
	RootAvailable  RootStatus = "available"
	RootMissing    RootStatus = "missing"
	RootUnreadable RootStatus = "unreadable"
)

// CatalogRoot is safe to expose to a caller. It intentionally contains no
// resolved or absolute filesystem path.
type CatalogRoot struct {
	Kind   RootKind   `json:"kind"`
	Status RootStatus `json:"status"`
}

type IssueSeverity string

const (
	SeverityInfo    IssueSeverity = "info"
	SeverityWarning IssueSeverity = "warning"
	SeverityError   IssueSeverity = "error"
)

const (
	IssueManifestMissing         = "codex.manifest-missing"
	IssueManifestInvalid         = "codex.manifest-invalid"
	IssuePathOutsideRoot         = "codex.path-outside-root"
	IssueVersionGeometryMismatch = "codex.version-geometry-mismatch"
	IssueInvalidSpritesheet      = "codex.invalid-spritesheet"
	IssueInvalidAnimation        = "codex.invalid-animation"
	IssueV2SemanticsUnverified   = "codex.v2-semantics-unverified"
	IssueDiscoveryConflict       = "codex.discovery-conflict"
	IssueHomeUnavailable         = "codex.home-unavailable"
	IssueRootUnreadable          = "codex.root-unreadable"
	IssuePackageTooLarge         = "pet.package-too-large"
	IssueManifestTooLarge        = "pet.manifest-too-large"
	IssueFileCountExceeded       = "pet.file-count-exceeded"
	IssueUnsafeResource          = "pet.unsafe-resource"
	IssueRemoteResource          = "pet.remote-resource"
	IssueSymlinkEscape           = "pet.symlink-escape"
	IssueDecodedImageTooLarge    = "pet.decoded-image-too-large"
	IssueDecodedPixelsExceeded   = "pet.decoded-pixels-exceeded"
	IssuePackageReadFailed       = "pet.package-read-failed"
	IssueCatalogItemNotFound     = "pet.catalog-item-not-found"
	IssueCatalogLimitExceeded    = "pet.catalog-limit-exceeded"
	IssueCancelled               = "pet.cancelled"
)

// Issue is a bounded, safe diagnostic. Args may contain only safe, truncated
// values such as a folder identity or resource kind.
type Issue struct {
	Code                string            `json:"code"`
	Args                map[string]string `json:"args,omitempty"`
	SourceKind          SourceKind        `json:"sourceKind"`
	Retryable           bool              `json:"retryable"`
	Severity            IssueSeverity     `json:"severity"`
	SafeFallbackSummary string            `json:"safeFallbackSummary,omitempty"`
}

func (i Issue) clone() Issue {
	copy := i
	if i.Args != nil {
		copy.Args = make(map[string]string, len(i.Args))
		for key, value := range i.Args {
			copy.Args[key] = value
		}
	}
	if copy.SafeFallbackSummary == "" {
		copy.SafeFallbackSummary = defaultIssueSummary(copy.Code)
	}
	return copy
}

// DiagnosticError is returned when a package cannot produce a complete
// definition. The zero value of the accompanying PetDefinition is always
// safe to discard; no partial definition is returned.
type DiagnosticError struct {
	Issues []Issue
}

func (e *DiagnosticError) Error() string {
	if e == nil || len(e.Issues) == 0 {
		return "pet package validation failed"
	}
	return fmt.Sprintf("%s: pet package validation failed", e.Issues[0].Code)
}

func (e *DiagnosticError) clone() *DiagnosticError {
	if e == nil {
		return nil
	}
	copy := &DiagnosticError{Issues: make([]Issue, len(e.Issues))}
	for index, issue := range e.Issues {
		copy.Issues[index] = issue.clone()
	}
	return copy
}

// Diagnostics returns a defensive copy of package diagnostics carried by err.
func Diagnostics(err error) []Issue {
	var diagnostic *DiagnosticError
	if errors.As(err, &diagnostic) && diagnostic != nil {
		return boundedIssues(diagnostic.Issues)
	}
	return nil
}

type SourceBadge string

const (
	BadgeCodex     SourceBadge = "codex"
	BadgeDshNative SourceBadge = "dsh-native"
)

type Availability string

const (
	AvailabilityReady   Availability = "ready"
	AvailabilityInvalid Availability = "invalid"
)

// PetListItem is a safe selectable summary. A catalog never exposes the
// package root, manifest path, or raw resource URL here.
type PetListItem struct {
	StableSourceKey string       `json:"stableSourceKey"`
	DisplayName     string       `json:"displayName"`
	Description     string       `json:"description,omitempty"`
	SourceBadge     SourceBadge  `json:"sourceBadge"`
	Availability    Availability `json:"availability"`
	Current         bool         `json:"current"`
	PreviewRef      string       `json:"previewRef,omitempty"`
	Capabilities    []string     `json:"capabilities,omitempty"`
	IssueCodes      []string     `json:"issueCodes,omitempty"`
}

func (i PetListItem) clone() PetListItem {
	copy := i
	copy.Capabilities = append([]string(nil), i.Capabilities...)
	copy.IssueCodes = append([]string(nil), i.IssueCodes...)
	return copy
}

// CatalogSnapshot is an immutable read model. Snapshot() and every operation
// returning one create fresh slices, maps, and timestamps for the caller.
type CatalogSnapshot struct {
	Revision  uint64        `json:"revision"`
	ScanState ScanState     `json:"scanState"`
	Stale     bool          `json:"stale"`
	ScannedAt *time.Time    `json:"scannedAt,omitempty"`
	Roots     []CatalogRoot `json:"roots,omitempty"`
	Items     []PetListItem `json:"items,omitempty"`
	Issues    []Issue       `json:"issues,omitempty"`
}

func (s CatalogSnapshot) clone() CatalogSnapshot {
	copy := s
	if s.ScannedAt != nil {
		at := *s.ScannedAt
		copy.ScannedAt = &at
	}
	copy.Roots = append([]CatalogRoot(nil), s.Roots...)
	copy.Items = make([]PetListItem, len(s.Items))
	for index, item := range s.Items {
		copy.Items[index] = item.clone()
	}
	copy.Issues = make([]Issue, len(s.Issues))
	for index, issue := range s.Issues {
		copy.Issues[index] = issue.clone()
	}
	return copy
}

// PackageSource is the adapter-only source reference. Catalog callers see
// only StableSourceKey and normalized definitions; this type is useful for
// the Codex adapter and its contract tests.
type PackageSource struct {
	Root            string     `json:"-"`
	ManifestPath    string     `json:"-"`
	Entry           string     `json:"-"`
	Kind            SourceKind `json:"kind"`
	Folder          string     `json:"folder"`
	StableSourceKey string     `json:"stableSourceKey"`
}

// NewCodexPackageSource creates the source reference used by the adapter. It
// does not read or mutate the source package.
func NewCodexPackageSource(root string, kind SourceKind, folder string) PackageSource {
	entry := "pet.json"
	if kind == SourceCodexAvatars {
		entry = "avatar.json"
	}
	return PackageSource{
		Root:            root,
		ManifestPath:    entry,
		Kind:            kind,
		Folder:          folder,
		StableSourceKey: StableSourceKey(kind, folder),
	}
}

// NewDshNativePackageSource creates the source reference for a dsh-native or
// dual-compatible package. The package remains an untrusted, read-only input.
func NewDshNativePackageSource(root, folder string) PackageSource {
	return PackageSource{
		Root:            root,
		ManifestPath:    "dsh-pet.json",
		Entry:           "dsh-pet.json",
		Kind:            SourceDshPets,
		Folder:          folder,
		StableSourceKey: StableSourceKey(SourceDshPets, folder),
	}
}

// StableSourceKey returns the path-independent key required by the catalog.
// Common folder names remain readable; unsafe or overlong identities are
// encoded and bounded without using an absolute path or display name.
func StableSourceKey(kind SourceKind, folder string) string {
	root := "pets"
	prefix := "codex"
	if kind == SourceCodexAvatars {
		root = "avatars"
	} else if kind == SourceDshPets {
		root = "native-pets"
		prefix = "dsh"
	}
	return prefix + ":" + root + ":" + sanitizeFolderIdentity(folder)
}

type Identity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
}

type SourceInfo struct {
	Format        string      `json:"format"`
	FormatVersion string      `json:"formatVersion"`
	Profile       string      `json:"profile"`
	Entry         string      `json:"entry"`
	StableKey     string      `json:"stableSourceKey"`
	Badge         SourceBadge `json:"badge"`
	Digest        string      `json:"digest,omitempty"`
}

type Geometry struct {
	CellWidth   int `json:"cellWidth"`
	CellHeight  int `json:"cellHeight"`
	Columns     int `json:"columns"`
	Rows        int `json:"rows"`
	ImageWidth  int `json:"imageWidth"`
	ImageHeight int `json:"imageHeight"`
	FrameCount  int `json:"frameCount"`
}

type Asset struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Format string `json:"format"`
	Size   int64  `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	SHA256 string `json:"sha256"`
	Data   []byte `json:"-"`
}

type FrameRef struct {
	Index      int    `json:"index"`
	DurationMS int    `json:"durationMs"`
	AssetID    string `json:"assetId,omitempty"`
	X          int    `json:"x,omitempty"`
	Y          int    `json:"y,omitempty"`
	Width      int    `json:"width,omitempty"`
	Height     int    `json:"height,omitempty"`
}

type TrackChannel string

const (
	TrackBase   TrackChannel = "base"
	TrackAction TrackChannel = "action"
	TrackLook   TrackChannel = "look"
)

type TrackSpec struct {
	ID            string       `json:"id"`
	Channel       TrackChannel `json:"channel"`
	RendererKind  string       `json:"rendererKind"`
	Frames        []FrameRef   `json:"frames"`
	FPS           float64      `json:"fps,omitempty"`
	Loop          bool         `json:"loop"`
	Priority      int          `json:"priority,omitempty"`
	Interruptible bool         `json:"interruptible"`
	Fallback      string       `json:"fallback"`
	TimeoutMS     int          `json:"timeoutMs,omitempty"`
	Aliases       []string     `json:"aliases,omitempty"`
	RepeatCount   int          `json:"repeatCount,omitempty"`
}

func (t TrackSpec) clone() TrackSpec {
	copy := t
	copy.Frames = append([]FrameRef(nil), t.Frames...)
	copy.Aliases = append([]string(nil), t.Aliases...)
	return copy
}

type StateSpec struct {
	Track    string `json:"track"`
	Loop     bool   `json:"loop"`
	Fallback string `json:"fallback,omitempty"`
}

type ActionSpec struct {
	Track         string `json:"track"`
	Priority      int    `json:"priority"`
	Interruptible bool   `json:"interruptible"`
	Fallback      string `json:"fallback"`
}

type FallbackSpec struct {
	Idle string `json:"idle"`
}

type Direction struct {
	Angle      float64 `json:"angle"`
	FrameIndex int     `json:"frameIndex"`
}

type DirectionProfile struct {
	Name                     string      `json:"name"`
	Directions               []Direction `json:"directions"`
	NeutralPolicy            string      `json:"neutralPolicy"`
	DesktopSemanticsVerified bool        `json:"desktopSemanticsVerified"`
}

// CanvasSpec describes the logical drawing surface without binding callers to
// a particular renderer or native window handle.
type CanvasSpec struct {
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	PivotX      float64 `json:"pivotX"`
	PivotY      float64 `json:"pivotY"`
	ScaleMode   string  `json:"scaleMode"`
	SafePadding int     `json:"safePadding"`
}

// PresentationHints are package preferences only. Host policy and platform
// capabilities always take precedence.
type PresentationHints struct {
	AnchorX float64 `json:"anchorX,omitempty"`
	AnchorY float64 `json:"anchorY,omitempty"`
	Scale   float64 `json:"scale,omitempty"`
}

// PermissionPolicy is intentionally deny-by-default. It records the result
// of validation, not a grant to access an operating-system capability.
type PermissionPolicy struct {
	Network string `json:"network"`
	Process string `json:"process"`
	Scripts string `json:"scripts"`
	Audio   string `json:"audio"`
}

func (p *DirectionProfile) clone() *DirectionProfile {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Directions = append([]Direction(nil), p.Directions...)
	return &copy
}

// PetDefinition is the normalized, format-independent adapter result.
// Codex row/column knowledge is converted to frame indexes and does not cross
// this boundary.
type PetDefinition struct {
	Identity     Identity              `json:"identity"`
	Source       SourceInfo            `json:"source"`
	Geometry     Geometry              `json:"geometry"`
	Canvas       CanvasSpec            `json:"canvas"`
	Capabilities []string              `json:"capabilities,omitempty"`
	Assets       []Asset               `json:"assets"`
	Tracks       map[string]TrackSpec  `json:"tracks"`
	States       map[string]StateSpec  `json:"states"`
	Actions      map[string]ActionSpec `json:"actions"`
	Inputs       map[string]bool       `json:"inputs,omitempty"`
	Projection   map[string]string     `json:"projection,omitempty"`
	Presentation PresentationHints     `json:"presentation,omitempty"`
	Permissions  PermissionPolicy      `json:"permissions"`
	Fallback     FallbackSpec          `json:"fallback"`
	Directions   *DirectionProfile     `json:"directions,omitempty"`
	Diagnostics  []Issue               `json:"diagnostics,omitempty"`
}

func (d PetDefinition) clone() PetDefinition {
	copy := d
	copy.Capabilities = append([]string(nil), d.Capabilities...)
	copy.Assets = make([]Asset, len(d.Assets))
	for index, asset := range d.Assets {
		copy.Assets[index] = asset
		copy.Assets[index].Data = append([]byte(nil), asset.Data...)
	}
	copy.Tracks = make(map[string]TrackSpec, len(d.Tracks))
	for key, track := range d.Tracks {
		copy.Tracks[key] = track.clone()
	}
	copy.States = make(map[string]StateSpec, len(d.States))
	for key, state := range d.States {
		copy.States[key] = state
	}
	copy.Actions = make(map[string]ActionSpec, len(d.Actions))
	for key, action := range d.Actions {
		copy.Actions[key] = action
	}
	copy.Inputs = make(map[string]bool, len(d.Inputs))
	for key, enabled := range d.Inputs {
		copy.Inputs[key] = enabled
	}
	copy.Projection = make(map[string]string, len(d.Projection))
	for key, value := range d.Projection {
		copy.Projection[key] = value
	}
	copy.Directions = d.Directions.clone()
	copy.Diagnostics = make([]Issue, len(d.Diagnostics))
	for index, issue := range d.Diagnostics {
		copy.Diagnostics[index] = issue.clone()
	}
	return copy
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	copy := make(map[string]bool, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copy := make(map[string]string, len(values))
	for key, value := range values {
		copy[key] = value
	}
	return copy
}

// Clone returns a defensive copy for Host-owned runtime and renderer state.
// Adapters and the catalog use the unexported helper internally; application
// composition needs the same guarantee when retaining an active definition.
func (d PetDefinition) Clone() PetDefinition {
	return d.clone()
}

// ProbeResult is a bounded manifest-only identification result. Load performs
// the complete resource and geometry validation.
type ProbeResult struct {
	Source   SourceInfo `json:"source"`
	Geometry Geometry   `json:"geometry"`
}

type Inspection struct {
	Item       PetListItem   `json:"item"`
	Definition PetDefinition `json:"definition"`
	Issues     []Issue       `json:"issues,omitempty"`
}

func (i Inspection) clone() Inspection {
	copy := i
	copy.Item = i.Item.clone()
	copy.Definition = i.Definition.clone()
	copy.Issues = make([]Issue, len(i.Issues))
	for index, issue := range i.Issues {
		copy.Issues[index] = issue.clone()
	}
	return copy
}

type PetAdapter interface {
	Probe(ctx context.Context, source PackageSource) (ProbeResult, error)
	Load(ctx context.Context, source PackageSource) (PetDefinition, error)
}

// HashBytes is kept small and deterministic for adapter materialization and
// contract tests.
func HashBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func cloneIssues(issues []Issue) []Issue {
	copy := make([]Issue, len(issues))
	for index, issue := range issues {
		copy[index] = issue.clone()
	}
	return copy
}

func boundedIssues(issues []Issue) []Issue {
	if len(issues) <= MaxIssueCount {
		return cloneIssues(issues)
	}
	return cloneIssues(issues[:MaxIssueCount])
}

func appendIssue(issues []Issue, issue Issue) []Issue {
	if len(issues) >= MaxIssueCount {
		return issues
	}
	return append(issues, issue.clone())
}

func sortIssues(issues []Issue) {
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].SourceKind != issues[j].SourceKind {
			return issues[i].SourceKind < issues[j].SourceKind
		}
		if issues[i].Code != issues[j].Code {
			return issues[i].Code < issues[j].Code
		}
		return issueFolder(issues[i]) < issueFolder(issues[j])
	})
}

func issueFolder(issue Issue) string {
	return issue.Args["folder"]
}

func defaultIssueSummary(code string) string {
	switch code {
	case IssueDiscoveryConflict:
		return "another Pet source with this folder takes precedence"
	case IssueV2SemanticsUnverified:
		return "v2 desktop direction semantics are not verified"
	case IssueManifestMissing:
		return "the Pet manifest is missing"
	case IssueManifestInvalid, IssueManifestTooLarge:
		return "the Pet manifest is invalid"
	case IssuePathOutsideRoot, IssueRemoteResource, IssueSymlinkEscape:
		return "the Pet resource path is unsafe"
	case IssueInvalidAnimation:
		return "the Pet animation definition is invalid"
	case IssueInvalidSpritesheet, IssueDecodedImageTooLarge, IssueDecodedPixelsExceeded:
		return "the Pet spritesheet is invalid"
	case IssuePackageTooLarge, IssueFileCountExceeded:
		return "the Pet package exceeds the supported limits"
	case IssueCatalogLimitExceeded:
		return "the Codex Pet directory has too many entries"
	case IssueMissingGeneration:
		return "the Pet event has no active generation"
	case IssueUnsafeEvent:
		return "the Pet event is invalid"
	case IssueRendererApply:
		return "the Pet frame could not be rendered"
	case IssueRootUnreadable, IssueHomeUnavailable:
		return "the Codex Pet directory could not be read"
	default:
		return "the Pet package could not be used"
	}
}

func safeString(value string, maxRunes int) (string, bool) {
	if value == "" || !utf8.ValidString(value) || len([]rune(value)) > maxRunes {
		return "", false
	}
	for _, r := range value {
		if r == '\x00' || r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return value, true
}

func safeSummary(value string) (string, bool) {
	if strings.ContainsAny(value, `/\\`) || strings.Contains(value, ":") {
		return "", false
	}
	return safeString(value, MaxSafeTextRunes)
}

func sanitizeFolderIdentity(folder string) string {
	if folder == "" {
		folder = "folder"
	}
	var builder strings.Builder
	for _, r := range folder {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteString("~")
			builder.WriteString(fmt.Sprintf("%X", r))
		}
		if builder.Len() > MaxFolderIdentityRunes*4 {
			break
		}
	}
	value := builder.String()
	if value == "" {
		value = "folder"
	}
	if len([]rune(value)) > MaxFolderIdentityRunes {
		suffix := HashBytes([]byte(folder))[:8]
		prefix := value
		if len(prefix) > MaxFolderIdentityRunes-9 {
			prefix = prefix[:MaxFolderIdentityRunes-9]
		}
		value = prefix + "-" + suffix
	}
	return value
}
