package app

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image/png"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/local/dsh-work/internal/dshactivity"
	"github.com/local/dsh-work/internal/lifecycle"
	"github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/settings"
)

const (
	maxPetPreviewBytes    = 4 * 1024 * 1024
	maxPetPreviewRefs     = 256
	petPreviewRefLifetime = 10 * time.Minute
	minPetSizePercent     = 50
	maxPetSizePercent     = 200
)

type petPreviewRefEntry struct {
	Key      string
	IssuedAt time.Time
}

// PetRenderer aliases the format-independent renderer seam for callers that
// compose the Settings service. Keeping the alias in app avoids a second
// preflight-only contract that could drift from the runtime renderer.
type PetRenderer = pet.PetRenderer

// PetPanel is the safe Settings projection. PetCatalog already strips source
// paths from its snapshot; the service adds current-selection and opaque
// preview references without exposing a PetDefinition or package bytes.
type PetPanel struct {
	Snapshot      pet.CatalogSnapshot    `json:"snapshot"`
	Preference    settings.PetPreference `json:"preference"`
	Runtime       pet.RuntimeState       `json:"runtime"`
	SizePercent   int                    `json:"sizePercent"`
	SizeAvailable bool                   `json:"sizeAvailable"`
}

// PetPreview is a safe, one-shot preview response. PreviewRef is an opaque,
// short-lived Host handle; it is not a filesystem reference or resource URL.
type PetPreview struct {
	PreviewRef      string `json:"previewRef"`
	StableSourceKey string `json:"stableSourceKey"`
	DisplayName     string `json:"displayName"`
	Description     string `json:"description,omitempty"`
	AssetID         string `json:"assetId,omitempty"`
	Format          string `json:"format,omitempty"`
	Width           int    `json:"width,omitempty"`
	Height          int    `json:"height,omitempty"`
}

// PetOverlayState is the read-only projection consumed by the independent
// overlay page. The frame is a Host-rendered data URL; it is never a source
// path or a frontend-composed resource URL.
type PetOverlayState struct {
	PlaybackKey string                 `json:"playbackKey"`
	Runtime     pet.RuntimeState       `json:"runtime"`
	Snapshot    pet.PetRuntimeSnapshot `json:"snapshot"`
	Preview     PetPreview             `json:"preview"`
	DataURL     string                 `json:"dataUrl,omitempty"`
	Activity    dshactivity.Snapshot   `json:"activity"`
}

// PetOverlayHooks are composition-edge effects. They are attached by main
// after the Wails window exists; the Settings method below can request only
// the allow-listed size operation and never receives a native handle.
type PetOverlayHooks struct {
	AlwaysOnTop func(bool) error
	Show        func() error
	Hide        func() error
	Close       func() error
	Resize      func(width, height int) error
	// StateChanged must return quickly and must not call back into the service.
	StateChanged func()
}

// PetSettingsService is the trusted Settings-window boundary for Pet
// discovery and preference changes. Browse operations never call
// SetPetPreference. Selection is serialized here and commits only after the
// catalog has revalidated the source and the renderer has passed preflight.
type PetSettingsService struct {
	previewMedia       map[string]pet.Asset
	hitRegions         []PetHitRegion
	hitUpdated         time.Time
	playbackToken      string
	playbackDefinition *pet.PetDefinition
	activity           *dshactivity.Bridge
	activityRuntime    *pet.Runtime
	activityKey        string
	openWorkspace      func()
	mu                 sync.Mutex

	manager             *settings.Manager
	catalog             pet.PetCatalog
	renderer            pet.PetRenderer
	rendererFactory     func() pet.PetRenderer
	activeRenderer      pet.PetRenderer
	activeRuntime       *pet.Runtime
	activeDefinition    *pet.PetDefinition
	activeKey           string
	overlayFrameKey     string
	overlayDataURL      string
	overlayHooks        PetOverlayHooks
	overlayCapabilities pet.OverlayCapabilities
	lifecycleCancel     context.CancelFunc
	startupWG           sync.WaitGroup
	closed              bool
	overlayFailure      bool
	rendererFailure     bool
	reducedMotion       bool
	generation          string
	previewRefs         map[string]petPreviewRefEntry

	// trustedSurface is kept as a narrow seam for headless contract tests. The
	// production constructor always uses the existing Wails Settings-window
	// check.
	trustedSurface func(context.Context) bool
}

// SetPetActivity connects Host-owned conversation observation and navigation.
func SetPetActivity(s *PetSettingsService, bridge *dshactivity.Bridge, openWorkspace func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activity = bridge
	s.openWorkspace = openWorkspace
}

// OpenPetActivity returns to an existing DSH conversation from the pet surface.
func (s *PetSettingsService) OpenPetActivity(ctx context.Context, sessionID string) error {
	s.mu.Lock()
	operation, cancel, err := s.beginOperationForSurface(ctx, "pet")
	bridge, open := s.activity, s.openWorkspace
	s.mu.Unlock()
	if err != nil {
		return err
	}
	defer cancel()
	if bridge == nil {
		return errors.New("conversation activity is unavailable")
	}
	if err := bridge.Open(operation, sessionID); err != nil {
		return err
	}
	if open != nil {
		open()
	}
	return nil
}

// AttachPetOverlayHooks connects the app service to the independently-owned
// native window. The function is intentionally outside the Wails method set.
func AttachPetOverlayHooks(service *PetSettingsService, hooks PetOverlayHooks) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.overlayHooks = hooks
	visible := service.activeRenderer != nil && service.activeRuntime != nil && service.activeKey != ""
	var preference settings.PetPreference
	if service.manager != nil {
		if values, err := service.manager.Snapshot(context.Background()); err == nil {
			preference = values.Pet
		}
	}
	if preference.SelectedKey == nil || !visible || *preference.SelectedKey != service.activeKey || preference.VisibilityIntent != settings.PetVisibilityVisible || !service.overlayAllowedLocked() {
		visible = false
	}
	if service.activeRuntime != nil {
		service.activeRuntime.SetHidden(!visible)
	}
	service.mu.Unlock()
	if visible && hooks.Show != nil {
		_ = hooks.Show()
	} else if hooks.Hide != nil {
		_ = hooks.Hide()
	}
}

// SetPetRendererFactory supplies a fresh renderer for each selection
// transaction. It is a composition helper, not a frontend operation.
func SetPetRendererFactory(service *PetSettingsService, factory func() pet.PetRenderer) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.rendererFactory = factory
	service.mu.Unlock()
}

// SetPetOverlayCapabilities supplies the platform capability report at the
// composition edge. A fallback platform keeps settings and selection intact
// but never claims that the desktop overlay is visible.
func SetPetOverlayCapabilities(service *PetSettingsService, capabilities pet.OverlayCapabilities) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.overlayCapabilities = capabilities
	if service.overlayCapabilities.Level == "" {
		service.overlayCapabilities = pet.CurrentOverlayCapabilities()
	}
	var preference settings.PetPreference
	if service.manager != nil {
		if values, err := service.manager.Snapshot(context.Background()); err == nil {
			preference = values.Pet
		}
	}
	service.applyOverlayVisibilityLocked(preference)
	service.mu.Unlock()
}

// SetPetGeneration binds future Pet events to the current Host generation.
// Generation changes reset the runtime's event ordering boundary and are
// owned by the Host composition edge rather than the frontend.
func SetPetGeneration(service *PetSettingsService, generation string) {
	if service == nil {
		return
	}
	service.mu.Lock()
	service.setGenerationLocked(generation)
	service.mu.Unlock()
}

// SetPetReducedMotion records the operating-system accessibility signal sent
// by the app-owned overlay surface. It is not a package-controlled setting;
// the Host applies it to the active Runtime and carries it across activation.
func (s *PetSettingsService) SetPetReducedMotion(ctx context.Context, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, cancel, err := s.beginOperationForSurface(ctx, "pet")
	if err != nil {
		return err
	}
	defer cancel()
	if err := operation.Err(); err != nil {
		return err
	}
	s.reducedMotion = enabled
	if s.activeRuntime != nil {
		s.activeRuntime.SetReducedMotion(enabled)
	}
	return nil
}

// PersistPetPosition stores a validated logical position from the Host-owned
// drag/display adapter. It is deliberately a composition helper rather than a
// frontend method, so arbitrary package data cannot move native windows.
func PersistPetPosition(ctx context.Context, service *PetSettingsService, position settings.PetPosition) error {
	if service == nil || service.manager == nil {
		return petSettingsUnavailable()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed {
		return petSettingsUnavailable()
	}
	values, err := service.manager.Snapshot(ctx)
	if err != nil {
		return err
	}
	next := copyPetPreference(values.Pet)
	next.Position = position
	// This records a native move. Reapplying visibility would call Show and
	// reposition the window again, feeding more events back into persistence.
	_, err = service.manager.SetPetPreference(ctx, next)
	return err
}

// StartupPetService performs the background scan and restores the persisted
// selected package without changing its visibility intent. It is safe to call
// before Wails creates the overlay window.
func StartupPetService(service *PetSettingsService) {
	if service == nil || service.manager == nil || service.catalog == nil {
		return
	}
	service.mu.Lock()
	if service.closed || service.lifecycleCancel != nil {
		service.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	service.lifecycleCancel = cancel
	service.startupWG.Add(1)
	service.mu.Unlock()
	go func() {
		defer service.startupWG.Done()
		service.startupScan(ctx)
	}()
}

// ShutdownPetService is the idempotent application-quit cleanup seam.
func ShutdownPetService(ctx context.Context, service *PetSettingsService) error {
	if service == nil {
		return nil
	}
	return service.shutdown(ctx)
}

// PublishPetHostStatus projects the trusted Host lifecycle into the Pet's
// allow-listed event port. It does not parse workspace DOM, logs or process
// output, and is a composition helper rather than a frontend binding.
func PublishPetHostStatus(service *PetSettingsService, status lifecycle.Status) {
	if service == nil {
		return
	}
	eventType := "host.offline"
	switch status.State {
	case lifecycle.StateStarting:
		eventType = "host.starting"
	case lifecycle.StateReady:
		eventType = "host.ready"
	case lifecycle.StateStopping, lifecycle.StateStopped:
		eventType = "host.offline"
	case lifecycle.StateFailed:
		eventType = "host.offline"
	}
	eventID := status.CorrelationID
	if eventID == "" {
		eventID = status.GenerationID
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	service.setGenerationLocked(status.GenerationID)
	if service.activeRuntime == nil {
		return
	}
	_ = service.activeRuntime.Dispatch(pet.PetInputEvent{
		EventID:    "host-status:" + eventID + ":" + eventType,
		Generation: status.GenerationID,
		Type:       eventType,
		Source:     "host",
	})
}

// PublishPetEvent is the Host-owned structured-event bridge for DSH/task
// integrations. Callers provide an already-approved event; this helper never
// parses workspace DOM, logs, process output or package data.
func PublishPetEvent(service *PetSettingsService, event pet.PetInputEvent) error {
	if service == nil {
		return petSettingsUnavailable()
	}
	if strings.TrimSpace(event.Source) == "" {
		event.Source = "host"
	}
	if event.Source != "host" && event.Source != "dsh" && event.Source != "overlay" {
		return errors.New("pet event source is not allow-listed")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.closed || service.activeRuntime == nil {
		return nil
	}
	if event.Generation == "" {
		event.Generation = service.generation
	} else if service.generation == "" {
		service.setGenerationLocked(event.Generation)
	}
	return service.activeRuntime.Dispatch(event)
}

// NewPetSettingsService constructs the trusted Pet Settings service. The
// renderer is optional while the native renderer/overlay is not composed; a
// nil renderer means that selection is accepted after catalog validation.
func NewPetSettingsService(manager *settings.Manager, catalog pet.PetCatalog, renderers ...pet.PetRenderer) *PetSettingsService {
	var renderer pet.PetRenderer
	if len(renderers) > 0 {
		renderer = renderers[0]
	}
	return &PetSettingsService{
		manager:             manager,
		catalog:             catalog,
		renderer:            renderer,
		previewRefs:         make(map[string]petPreviewRefEntry),
		overlayCapabilities: pet.CurrentOverlayCapabilities(),
		trustedSurface: func(ctx context.Context) bool {
			return isTrustedWindow(ctx, "settings")
		},
	}
}

// GetPetPanel returns the current catalog snapshot, persisted preference and
// derived runtime state without refreshing or persisting anything.
func (s *PetSettingsService) GetPetPanel(ctx context.Context) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()
	return s.panel(operation)
}

// GetPetPanelFromHost returns the same safe Pet projection for trusted Host
// composition code, such as the native application menu. It intentionally
// bypasses the WebView surface check because the caller is already inside the
// Host process and cannot be reached through an untrusted WebView.
func GetPetPanelFromHost(ctx context.Context, service *PetSettingsService) (PetPanel, error) {
	if service == nil {
		return PetPanel{}, petSettingsUnavailable()
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	operation, cancel, err := service.beginHostOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()
	return service.panel(operation)
}

// GetPetOverlay returns the latest normalized runtime frame for the separate
// Pet window. The overlay surface is read-only; state changes still enter
// through the Host-owned lifecycle and Settings seams.
func (s *PetSettingsService) GetPetOverlay(ctx context.Context) (PetOverlayState, error) {
	return s.getPetOverlay(ctx, true)
}

// GetPetPresentation returns the timeline without encoding or transferring frames.
func (s *PetSettingsService) GetPetPresentation(ctx context.Context) (PetOverlayState, error) {
	return s.getPetOverlay(ctx, false)
}

func (s *PetSettingsService) getPetOverlay(ctx context.Context, render bool) (PetOverlayState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperationForSurface(ctx, "pet")
	if err != nil {
		return PetOverlayState{}, err
	}
	defer cancel()

	snapshot, err := s.catalog.Snapshot(operation)
	if err != nil {
		return PetOverlayState{}, safePetCatalogError(err)
	}
	preference, err := s.manager.Snapshot(operation)
	if err != nil {
		return PetOverlayState{}, err
	}
	panel := s.projectPanel(snapshot, preference.Pet)
	state := PetOverlayState{Runtime: panel.Runtime}
	if s.activity != nil {
		state.Activity = s.activity.Snapshot()
		if s.activeRuntime != nil && state.Activity.Generation == s.generation {
			kind, key := dshactivity.AnimationIntent(state.Activity)
			key = s.generation + ":" + key
			if s.activityRuntime != s.activeRuntime || s.activityKey != key {
				_ = s.activeRuntime.Dispatch(pet.PetInputEvent{Type: kind, Generation: s.generation, Source: "dsh"})
				s.activityRuntime, s.activityKey = s.activeRuntime, key
			}
		}
	}
	if panel.Runtime.EffectiveVisibility != pet.VisibilityVisible && !s.rendererFailure {
		return state, nil
	}
	if s.activeRuntime == nil || s.activeRenderer == nil || s.activeDefinition == nil || s.activeKey == "" {
		return state, nil
	}

	runtimeSnapshot := s.activeRuntime.Snapshot(time.Now())
	state.Snapshot = runtimeSnapshot
	state.PlaybackKey = s.playbackKeyLocked()
	if !render {
		return state, nil
	}
	state.Runtime = panel.Runtime
	preview := s.projectPetPreviewLocked(s.activeKey, *s.activeDefinition)
	state.Preview = preview
	frameKey := s.activeKey + "\x00" + runtimeSnapshot.TrackID + "\x00" + strconv.Itoa(runtimeSnapshot.FrameIndex)
	if frameKey != s.overlayFrameKey {
		frame, err := s.activeRenderer.ApplySnapshot(operation, runtimeSnapshot)
		if err != nil {
			s.markRendererFailureLocked(&state)
			return state, nil
		}
		dataURL := renderedFrameDataURL(frame)
		if dataURL == "" {
			s.markRendererFailureLocked(&state)
			return state, nil
		}
		s.overlayDataURL = dataURL
		s.overlayFrameKey = frameKey
	}
	if s.rendererFailure {
		s.rendererFailure = false
		s.overlayFailure = false
		state.Runtime = projectPetRuntime(snapshot, preference.Pet)
	}
	state.DataURL = s.overlayDataURL
	return state, nil
}

// RefreshPetCatalog refreshes discovery and returns the resulting safe panel.
// A failed refresh may still return a stale panel because PetCatalog retains
// the last usable snapshot; the refresh error remains visible to the caller.
func (s *PetSettingsService) RefreshPetCatalog(ctx context.Context) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()

	snapshot, refreshErr := s.catalog.Refresh(operation)
	if refreshErr != nil && snapshot.Revision == 0 && snapshot.ScanState == pet.ScanNeverScanned {
		return PetPanel{}, safePetCatalogError(refreshErr)
	}
	s.revokePreviewRefsLocked()
	preference, preferenceErr := s.manager.Snapshot(operation)
	if preferenceErr != nil {
		return PetPanel{}, preferenceErr
	}
	s.reconcileActiveLocked(operation, snapshot, preference.Pet)
	panel := s.projectPanel(snapshot, preference.Pet)
	if refreshErr != nil {
		return panel, safePetCatalogError(refreshErr)
	}
	return panel, nil
}

// PreviewPet resolves the selected key through PetCatalog and returns only
// bounded metadata plus an opaque Host-owned preview handle. It deliberately
// does not change the persisted selection or visibility intent.
func (s *PetSettingsService) PreviewPet(ctx context.Context, stableSourceKey string) (PetPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPreview{}, err
	}
	defer cancel()

	key, err := validatePetKey(stableSourceKey)
	if err != nil {
		return PetPreview{}, err
	}
	// Browse previews are intentionally served from the last complete,
	// materialized catalog snapshot. Only SelectPet performs the explicit
	// commit-time revalidation; using it here could return metadata from a
	// newly edited source while GetPetPreview resolves the older snapshot.
	definition, err := s.catalog.Resolve(operation, key)
	if err != nil {
		return PetPreview{}, safePetCatalogError(err)
	}
	return s.projectPetPreviewLocked(key, definition), nil
}

// GetPetPreview resolves an opaque preview handle and renders only the
// validated idle frame. The handle is the sole frontend capability: callers
// cannot turn a stable key or package path into a resource URL themselves.
func (s *PetSettingsService) GetPetPreview(ctx context.Context, previewRef string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return "", err
	}
	defer cancel()

	key, ok := s.previewKeyLocked(previewRef, time.Now())
	if !ok {
		return "", petSelectionInvalid()
	}
	definition, err := s.catalog.Resolve(operation, key)
	if err != nil {
		return "", safePetCatalogError(err)
	}
	if definition.Source.Profile == pet.RendererWebM {
		t := definition.Tracks[definition.Fallback.Idle]
		if len(t.Frames) == 0 {
			return "", petRendererFailure()
		}
		for _, a := range definition.Assets {
			if a.ID == t.Frames[0].AssetID {
				if s.previewMedia == nil {
					s.previewMedia = map[string]pet.Asset{}
				}
				if len(s.previewMedia) >= 8 {
					for old := range s.previewMedia {
						delete(s.previewMedia, old)
						break
					}
				}
				s.previewMedia[previewRef] = a
				return "/__pet_media/" + previewRef + "/preview", nil
			}
		}
		return "", petRendererFailure()
	}
	dataURL, err := pet.PreviewDataURL(operation, definition)
	if err != nil {
		return "", petRendererFailure()
	}
	return dataURL, nil
}

func resolvePetForCommit(ctx context.Context, catalog pet.PetCatalog, key string) (pet.PetDefinition, error) {
	if revalidating, ok := catalog.(pet.RevalidatingCatalog); ok {
		return revalidating.Revalidate(ctx, key)
	}
	return catalog.Resolve(ctx, key)
}

// SelectPet performs the explicit selection transaction. Resolve is the
// commit-time source revalidation seam; renderer preflight and persistence
// happen only after it succeeds. No failure mutates the old preference.
func (s *PetSettingsService) SelectPet(ctx context.Context, stableSourceKey string) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()

	key, err := validatePetKey(stableSourceKey)
	if err != nil {
		return PetPanel{}, err
	}
	snapshot, err := s.catalog.Snapshot(operation)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	current, err := s.manager.Snapshot(operation)
	if err != nil {
		return PetPanel{}, err
	}
	definition, err := resolvePetForCommit(operation, s.catalog, key)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	canActivate := s.rendererFactory != nil && s.overlayAllowedLocked()
	candidateRenderer := s.renderer
	if s.rendererFactory != nil {
		if canActivate {
			candidateRenderer = s.rendererFactory()
		} else {
			candidateRenderer = nil
		}
	}
	if canActivate && candidateRenderer == nil {
		return PetPanel{}, petRendererFailure()
	}
	if candidateRenderer != nil {
		if err := candidateRenderer.Probe(operation, definition); err != nil {
			if s.rendererFactory != nil {
				_ = candidateRenderer.Unload(context.Background())
			}
			return PetPanel{}, petRendererFailure()
		}
		if canActivate {
			if err := candidateRenderer.Load(operation, definition); err != nil {
				_ = candidateRenderer.Unload(context.Background())
				return PetPanel{}, petRendererFailure()
			}
		}
	}
	var candidateRuntime *pet.Runtime
	if canActivate {
		candidateRuntime = pet.NewRuntime(pet.RuntimeConfig{Generation: s.generation})
		if err := candidateRuntime.Start(operation, definition); err != nil {
			_ = candidateRenderer.Unload(context.Background())
			return PetPanel{}, petRendererFailure()
		}
		candidateRuntime.SetReducedMotion(s.reducedMotion)
	}

	nextPreference := copyPetPreference(current.Pet)
	nextPreference.SelectedKey = stringPointer(key)
	values, err := s.manager.SetPetPreference(operation, nextPreference)
	if err != nil {
		if candidateRuntime != nil {
			_ = candidateRuntime.Stop(context.Background())
		}
		if candidateRenderer != nil && canActivate {
			_ = candidateRenderer.Unload(context.Background())
		}
		return PetPanel{}, err
	}
	if canActivate {
		oldRenderer, oldRuntime := s.activeRenderer, s.activeRuntime
		s.activeRenderer = candidateRenderer
		s.activeRuntime = candidateRuntime
		definitionCopy := definition.Clone()
		s.activeDefinition = &definitionCopy
		s.activeKey = key
		s.overlayFrameKey = ""
		s.overlayDataURL = ""
		s.overlayFailure = false
		s.rendererFailure = false
		if cleanupErr := cleanupPetResources(oldRenderer, oldRuntime); cleanupErr != nil {
			s.overlayFailure = true
			return s.projectPanel(snapshot, values.Pet), petRendererFailure()
		}
		s.applyOverlayVisibilityLocked(values.Pet)
	} else if s.rendererFactory != nil {
		// A capability fallback still persists the user's selection, but it
		// must not retain a renderer/runtime that could be mistaken for a
		// working desktop overlay.
		if cleanupErr := s.clearActiveLocked(); cleanupErr != nil {
			return s.projectPanel(snapshot, values.Pet), petRendererFailure()
		}
	}
	panel := s.projectPanel(snapshot, values.Pet)
	if s.overlayFailure {
		return panel, petRendererFailure()
	}
	return panel, nil
}

// SetPetAlwaysOnTop applies and persists the Settings-owned window preference.
func (s *PetSettingsService) SetPetAlwaysOnTop(ctx context.Context, enabled bool) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()
	snapshot, err := s.catalog.Snapshot(operation)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	current, err := s.manager.Snapshot(operation)
	if err != nil {
		return PetPanel{}, err
	}
	next := copyPetPreference(current.Pet)
	next.AlwaysOnTop = enabled
	if s.overlayHooks.AlwaysOnTop != nil {
		if err := s.overlayHooks.AlwaysOnTop(enabled); err != nil {
			return PetPanel{}, petRendererFailure()
		}
	}
	values, err := s.manager.SetPetPreference(operation, next)
	if err != nil {
		if s.overlayHooks.AlwaysOnTop != nil {
			_ = s.overlayHooks.AlwaysOnTop(current.Pet.AlwaysOnTop)
		}
		return PetPanel{}, err
	}
	return s.projectPanel(snapshot, values.Pet), nil
}

// SetPetVisibility persists visibility intent, including while a pet is unavailable.
func (s *PetSettingsService) SetPetVisibility(ctx context.Context, visible bool) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()
	return s.setPetVisibilityLocked(operation, visible)
}

// SetPetVisibilityFromHost applies the same visibility transaction for trusted
// native Host controls. It keeps menu actions on the Host side while sharing
// the persistence and overlay lifecycle with Settings.
func SetPetVisibilityFromHost(ctx context.Context, service *PetSettingsService, visible bool) (PetPanel, error) {
	if service == nil {
		return PetPanel{}, petSettingsUnavailable()
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	operation, cancel, err := service.beginHostOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()
	return service.setPetVisibilityLocked(operation, visible)
}

func (s *PetSettingsService) setPetVisibilityLocked(operation context.Context, visible bool) (PetPanel, error) {
	snapshot, err := s.catalog.Snapshot(operation)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	current, err := s.manager.Snapshot(operation)
	if err != nil {
		return PetPanel{}, err
	}
	if current.Pet.SelectedKey == nil {
		// The settings module normalizes this invariant too; avoid a needless
		// write and never create a visible preference without a selection.
		return s.projectPanel(snapshot, current.Pet), nil
	}
	nextPreference := copyPetPreference(current.Pet)
	if visible {
		nextPreference.VisibilityIntent = settings.PetVisibilityVisible
	} else {
		nextPreference.VisibilityIntent = settings.PetVisibilityHidden
	}
	values, err := s.manager.SetPetPreference(operation, nextPreference)
	if err != nil {
		return PetPanel{}, err
	}
	s.applyOverlayVisibilityLocked(values.Pet)
	panel := s.projectPanel(snapshot, values.Pet)
	if s.overlayFailure {
		return panel, petRendererFailure()
	}
	return panel, nil
}

// SetPetSize changes the persisted Pet dimensions through the Settings
// surface. The slider is intentionally the only size control: native window
// border resizing is disabled, while the Host-owned Resize hook applies the
// same validated dimensions to the desktop overlay.
func (s *PetSettingsService) SetPetSize(ctx context.Context, percent int) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()
	if percent < minPetSizePercent || percent > maxPetSizePercent || (percent-minPetSizePercent)%5 != 0 {
		return PetPanel{}, errors.New("pet size percent is invalid")
	}
	if !s.petSizeAvailableLocked() {
		return PetPanel{}, errors.New("pet size is unavailable")
	}

	snapshot, err := s.catalog.Snapshot(operation)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	current, err := s.manager.Snapshot(operation)
	if err != nil {
		return PetPanel{}, err
	}
	nextPreference := copyPetPreference(current.Pet)
	defaults := settings.DefaultPetPosition()
	nextPreference.Position.Width = petSizeDimension(defaults.Width, percent)
	nextPreference.Position.Height = petSizeDimension(defaults.Height, percent)
	previousWidth := current.Pet.Position.Width
	previousHeight := current.Pet.Position.Height
	resize := s.overlayHooks.Resize
	values, err := s.manager.SetPetPreference(operation, nextPreference)
	if err != nil {
		return PetPanel{}, err
	}
	if resize != nil && (previousWidth != nextPreference.Position.Width || previousHeight != nextPreference.Position.Height) {
		if err := resize(nextPreference.Position.Width, nextPreference.Position.Height); err != nil {
			_, _ = s.manager.SetPetPreference(operation, copyPetPreference(current.Pet))
			return PetPanel{}, err
		}
	}
	s.applyOverlayVisibilityLocked(values.Pet)
	panel := s.projectPanel(snapshot, values.Pet)
	if s.overlayFailure {
		return panel, petRendererFailure()
	}
	return panel, nil
}

// ClearPetSelection clears both the selected key and its visibility intent.
func (s *PetSettingsService) ClearPetSelection(ctx context.Context) (PetPanel, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	operation, cancel, err := s.beginOperation(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	defer cancel()

	snapshot, err := s.catalog.Snapshot(operation)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	current, err := s.manager.Snapshot(operation)
	if err != nil {
		return PetPanel{}, err
	}
	if current.Pet.SelectedKey == nil && current.Pet.VisibilityIntent == settings.PetVisibilityHidden {
		return s.projectPanel(snapshot, current.Pet), nil
	}
	nextPreference := copyPetPreference(current.Pet)
	nextPreference.SelectedKey = nil
	nextPreference.VisibilityIntent = settings.PetVisibilityHidden
	values, err := s.manager.SetPetPreference(operation, nextPreference)
	if err != nil {
		return PetPanel{}, err
	}
	s.revokePreviewRefsLocked()
	cleanupErr := s.clearActiveLocked()
	s.applyOverlayVisibilityLocked(values.Pet)
	panel := s.projectPanel(snapshot, values.Pet)
	if cleanupErr != nil || s.overlayFailure {
		return panel, petRendererFailure()
	}
	return panel, nil
}

func (s *PetSettingsService) panel(ctx context.Context) (PetPanel, error) {
	snapshot, err := s.catalog.Snapshot(ctx)
	if err != nil {
		return PetPanel{}, safePetCatalogError(err)
	}
	preference, err := s.manager.Snapshot(ctx)
	if err != nil {
		return PetPanel{}, err
	}
	return s.projectPanel(snapshot, preference.Pet), nil
}

func (s *PetSettingsService) startupScan(ctx context.Context) {
	if s == nil || s.manager == nil || s.catalog == nil {
		return
	}
	operation, cancel := managerContext(ctx)
	defer cancel()
	snapshot, err := s.catalog.Refresh(operation)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.overlayCapabilities.Level == pet.OverlayFallback || operation.Err() != nil {
		return
	}
	values, err := s.manager.Snapshot(operation)
	if err != nil || values.Pet.SelectedKey == nil {
		return
	}
	item, ok := findPetItem(snapshot, *values.Pet.SelectedKey)
	if !ok || item.Availability != pet.AvailabilityReady {
		return
	}
	if err := s.activateLocked(operation, *values.Pet.SelectedKey, false); err == nil {
		s.applyOverlayVisibilityLocked(values.Pet)
	} else {
		s.overlayFailure = true
	}
}

func (s *PetSettingsService) shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	if s.closed && s.activeRenderer == nil && s.activeRuntime == nil {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.lifecycleCancel != nil {
		s.lifecycleCancel()
		s.lifecycleCancel = nil
	}
	s.mu.Unlock()
	s.startupWG.Wait()
	s.mu.Lock()
	hooks := s.overlayHooks
	renderer := s.activeRenderer
	runtimeState := s.activeRuntime
	s.activeRenderer = nil
	s.activeRuntime = nil
	s.activeDefinition = nil
	s.playbackDefinition = nil
	s.playbackToken = ""
	s.activeKey = ""
	s.overlayFrameKey = ""
	s.overlayDataURL = ""
	s.overlayFailure = false
	s.rendererFailure = false
	s.revokePreviewRefsLocked()
	s.mu.Unlock()

	var firstErr error
	if hooks.Hide != nil {
		if err := hooks.Hide(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if runtimeState != nil {
		if err := runtimeState.Stop(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if renderer != nil {
		if err := renderer.Unload(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if hooks.Close != nil {
		if err := hooks.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *PetSettingsService) applyOverlayVisibilityLocked(preference settings.PetPreference) {
	defer s.notifyStateChangedLocked()
	if s.overlayHooks.AlwaysOnTop != nil {
		if err := s.overlayHooks.AlwaysOnTop(preference.AlwaysOnTop); err != nil {
			s.overlayFailure = true
			return
		}
	}
	hadFailure := s.overlayFailure
	if s.overlayHooks.Show == nil && s.overlayHooks.Hide == nil {
		if s.activeRuntime != nil {
			s.activeRuntime.SetHidden(true)
		}
		return
	}
	visible := preference.SelectedKey != nil && preference.VisibilityIntent == settings.PetVisibilityVisible && s.overlayAllowedLocked() && s.activeKey != "" && s.activeRenderer != nil && s.activeRuntime != nil && *preference.SelectedKey == s.activeKey
	if visible {
		s.activeRuntime.SetHidden(false)
		if s.overlayHooks.Show != nil {
			if err := s.overlayHooks.Show(); err != nil {
				s.overlayFailure = true
				s.activeRuntime.SetHidden(true)
				return
			}
		}
		if !hadFailure {
			s.overlayFailure = false
		}
		return
	}
	if s.activeRuntime != nil {
		s.activeRuntime.SetHidden(true)
	}
	if s.overlayHooks.Hide != nil {
		if err := s.overlayHooks.Hide(); err != nil {
			s.overlayFailure = true
			return
		}
	}
	if !hadFailure {
		s.overlayFailure = false
	}
}

func (s *PetSettingsService) notifyStateChangedLocked() {
	if s.overlayHooks.StateChanged != nil {
		s.overlayHooks.StateChanged()
	}
}

func (s *PetSettingsService) overlayAllowedLocked() bool {
	capabilities := s.overlayCapabilities
	return capabilities.Level != pet.OverlayFallback && capabilities.Transparent && capabilities.AlwaysOnTop && capabilities.Interaction && capabilities.Drag && capabilities.DPI && capabilities.MonitorAware
}

func cleanupPetResources(renderer pet.PetRenderer, runtimeState *pet.Runtime) error {
	var firstErr error
	if runtimeState != nil {
		if err := runtimeState.Stop(context.Background()); err != nil {
			firstErr = err
		}
	}
	if renderer != nil {
		if err := renderer.Unload(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (s *PetSettingsService) clearActiveLocked() error {
	renderer, runtimeState := s.activeRenderer, s.activeRuntime
	s.activeRenderer = nil
	s.activeRuntime = nil
	s.activeDefinition = nil
	s.activeKey = ""
	s.overlayFrameKey = ""
	s.overlayDataURL = ""
	s.overlayFailure = false
	s.rendererFailure = false
	if err := cleanupPetResources(renderer, runtimeState); err != nil {
		s.overlayFailure = true
		return err
	}
	return nil
}

func (s *PetSettingsService) activateLocked(ctx context.Context, key string, unloadPrevious bool) error {
	if s.rendererFactory == nil {
		return nil
	}
	definition, err := resolvePetForCommit(ctx, s.catalog, key)
	if err != nil {
		return err
	}
	candidate := s.rendererFactory()
	if candidate == nil {
		return petRendererFailure()
	}
	if err := candidate.Probe(ctx, definition); err != nil {
		_ = candidate.Unload(context.Background())
		return err
	}
	if err := candidate.Load(ctx, definition); err != nil {
		_ = candidate.Unload(context.Background())
		return err
	}
	runtimeState := pet.NewRuntime(pet.RuntimeConfig{Generation: s.generation})
	if err := runtimeState.Start(ctx, definition); err != nil {
		_ = candidate.Unload(context.Background())
		return err
	}
	runtimeState.SetReducedMotion(s.reducedMotion)
	oldRenderer, oldRuntime := s.activeRenderer, s.activeRuntime
	s.activeRenderer = candidate
	s.activeRuntime = runtimeState
	copy := definition.Clone()
	s.activeDefinition = &copy
	s.activeKey = key
	s.overlayFrameKey = ""
	s.overlayDataURL = ""
	s.overlayFailure = false
	s.rendererFailure = false
	if unloadPrevious {
		if cleanupErr := cleanupPetResources(oldRenderer, oldRuntime); cleanupErr != nil {
			s.overlayFailure = true
			return cleanupErr
		}
	}
	return nil
}

func (s *PetSettingsService) beginOperationForSurface(ctx context.Context, surface string) (context.Context, context.CancelFunc, error) {
	if s == nil || s.manager == nil || s.catalog == nil {
		return nil, nil, petSettingsUnavailable()
	}
	trusted := s.trustedSurface
	if surface == "pet" {
		trusted = func(candidate context.Context) bool { return isTrustedWindow(candidate, "pet") }
	}
	if trusted == nil || !trusted(ctx) {
		return nil, nil, trustedSurfaceRequired("dsh-work Pet controls are available only on a trusted surface.")
	}
	if s.closed {
		return nil, nil, petSettingsUnavailable()
	}
	operation, cancel := managerContext(ctx)
	return operation, cancel, nil
}

func (s *PetSettingsService) setGenerationLocked(generation string) {
	generation = strings.TrimSpace(generation)
	if generation == "" || !utf8.ValidString(generation) || len([]rune(generation)) > 128 {
		return
	}
	for _, character := range generation {
		if character < 0x20 || character == 0x7f {
			return
		}
	}
	if s.generation == generation {
		return
	}
	s.generation = generation
	if s.activeRuntime != nil {
		s.activeRuntime.SetGeneration(generation)
	}
}

func (s *PetSettingsService) beginOperation(ctx context.Context) (context.Context, context.CancelFunc, error) {
	return s.beginOperationForSurface(ctx, "settings")
}

func (s *PetSettingsService) beginHostOperation(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if s == nil || s.manager == nil || s.catalog == nil {
		return nil, nil, petSettingsUnavailable()
	}
	if s.closed {
		return nil, nil, petSettingsUnavailable()
	}
	operation, cancel := managerContext(ctx)
	return operation, cancel, nil
}

func (s *PetSettingsService) projectPanel(snapshot pet.CatalogSnapshot, preference settings.PetPreference) PetPanel {
	panel := projectPetPanel(snapshot, preference)
	panel.SizePercent = petSizePercent(preference.Position)
	panel.SizeAvailable = s.petSizeAvailableLocked()
	now := time.Now()
	for index := range panel.Snapshot.Items {
		if key, err := validatePetKey(panel.Snapshot.Items[index].StableSourceKey); err == nil {
			panel.Snapshot.Items[index].PreviewRef = s.previewRefLocked(key, now)
		}
	}
	if preference.SelectedKey != nil && preference.VisibilityIntent == settings.PetVisibilityVisible && !s.overlayAllowedLocked() {
		panel.Runtime.EffectiveVisibility = pet.VisibilityPaused
		panel.Runtime.OverlayStatus = pet.OverlayFailed
	}
	if s.overlayFailure && preference.SelectedKey != nil && preference.VisibilityIntent == settings.PetVisibilityVisible {
		panel.Runtime.EffectiveVisibility = pet.VisibilityPaused
		panel.Runtime.OverlayStatus = pet.OverlayFailed
	}
	if preference.SelectedKey != nil && s.rendererFactory != nil && s.overlayAllowedLocked() && s.activeKey != *preference.SelectedKey {
		if preference.VisibilityIntent == settings.PetVisibilityVisible {
			panel.Runtime.EffectiveVisibility = pet.VisibilityPaused
			panel.Runtime.OverlayStatus = pet.OverlayPreparing
		} else {
			panel.Runtime.EffectiveVisibility = pet.VisibilityHidden
			panel.Runtime.OverlayStatus = pet.OverlayHidden
		}
	}
	return panel
}

func (s *PetSettingsService) petSizeAvailableLocked() bool {
	return s.overlayHooks.Resize != nil && s.overlayCapabilities.Level != pet.OverlayFallback
}

func (s *PetSettingsService) reconcileActiveLocked(ctx context.Context, snapshot pet.CatalogSnapshot, preference settings.PetPreference) {
	if preference.SelectedKey == nil {
		s.clearActiveLocked()
		s.applyOverlayVisibilityLocked(preference)
		return
	}
	key := *preference.SelectedKey
	item, found := findPetItem(snapshot, key)
	if !found || item.Availability != pet.AvailabilityReady {
		if s.activeKey == key {
			s.clearActiveLocked()
		}
		s.applyOverlayVisibilityLocked(preference)
		return
	}
	if s.rendererFactory != nil && s.overlayAllowedLocked() && s.activeKey != key {
		if err := s.activateLocked(ctx, key, true); err != nil {
			s.clearActiveLocked()
			s.overlayFailure = true
		}
	}
	s.applyOverlayVisibilityLocked(preference)
}

func renderedFrameDataURL(frame pet.RenderedFrame) string {
	if frame.Image == nil {
		return ""
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, frame.Image); err != nil || len(buffer.Bytes()) > maxPetPreviewBytes {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func (s *PetSettingsService) markRendererFailureLocked(state *PetOverlayState) {
	s.rendererFailure = true
	s.overlayFailure = true
	s.overlayDataURL = ""
	s.overlayFrameKey = ""
	state.Runtime.EffectiveVisibility = pet.VisibilityPaused
	state.Runtime.OverlayStatus = pet.OverlayFailed
	state.Runtime.FailureCode = pet.IssueRendererApply
	if len(state.Snapshot.Diagnostics) < pet.MaxIssueCount {
		state.Snapshot.Diagnostics = append(state.Snapshot.Diagnostics, pet.Issue{
			Code:                pet.IssueRendererApply,
			Severity:            pet.SeverityError,
			Retryable:           true,
			SafeFallbackSummary: "the Pet frame could not be rendered",
		})
	}
}

func projectPetPanel(snapshot pet.CatalogSnapshot, preference settings.PetPreference) PetPanel {
	preference = copyPetPreference(preference)
	for index := range snapshot.Items {
		item := &snapshot.Items[index]
		item.Current = preference.SelectedKey != nil && item.StableSourceKey == *preference.SelectedKey
		if key, err := validatePetKey(item.StableSourceKey); err == nil {
			item.PreviewRef = petPreviewRef(key)
		}
	}
	return PetPanel{
		Snapshot:   snapshot,
		Preference: preference,
		Runtime:    projectPetRuntime(snapshot, preference),
	}
}

func projectPetRuntime(snapshot pet.CatalogSnapshot, preference settings.PetPreference) pet.RuntimeState {
	state := pet.RuntimeState{
		SelectedKey:               cloneStringPointer(preference.SelectedKey),
		PersistedVisibilityIntent: string(preference.VisibilityIntent),
		EffectiveVisibility:       pet.VisibilityHidden,
		SelectionStatus:           pet.SelectionNone,
		OverlayStatus:             pet.OverlayStopped,
	}
	if preference.SelectedKey == nil {
		state.PersistedVisibilityIntent = string(settings.PetVisibilityHidden)
		return state
	}

	selected, found := findPetItem(snapshot, *preference.SelectedKey)
	if !found {
		state.SelectionStatus = pet.SelectionUnavailable
		state.FailureCode = pet.IssueCatalogItemNotFound
		if preference.VisibilityIntent == settings.PetVisibilityVisible {
			state.EffectiveVisibility = pet.VisibilityPaused
			state.OverlayStatus = pet.OverlayFailed
		} else {
			state.EffectiveVisibility = pet.VisibilityHidden
			state.OverlayStatus = pet.OverlayHidden
		}
		return state
	}
	if selected.Availability != pet.AvailabilityReady {
		state.SelectionStatus = pet.SelectionInvalid
		if len(selected.IssueCodes) > 0 {
			state.FailureCode = selected.IssueCodes[0]
		}
		if preference.VisibilityIntent == settings.PetVisibilityVisible {
			state.EffectiveVisibility = pet.VisibilityPaused
			state.OverlayStatus = pet.OverlayFailed
		} else {
			state.OverlayStatus = pet.OverlayHidden
		}
		return state
	}

	state.SelectionStatus = pet.SelectionReady
	if preference.VisibilityIntent == settings.PetVisibilityVisible {
		state.EffectiveVisibility = pet.VisibilityVisible
		state.OverlayStatus = pet.OverlayVisible
	} else {
		state.EffectiveVisibility = pet.VisibilityHidden
		state.OverlayStatus = pet.OverlayHidden
	}
	return state
}

func findPetItem(snapshot pet.CatalogSnapshot, key string) (pet.PetListItem, bool) {
	for _, item := range snapshot.Items {
		if item.StableSourceKey == key {
			return item, true
		}
	}
	return pet.PetListItem{}, false
}

func (s *PetSettingsService) projectPetPreviewLocked(key string, definition pet.PetDefinition) PetPreview {
	preview := projectPetPreview(key, definition)
	preview.PreviewRef = s.previewRefLocked(key, time.Now())
	return preview
}

func projectPetPreview(key string, definition pet.PetDefinition) PetPreview {
	preview := PetPreview{
		PreviewRef:      petPreviewRef(key),
		StableSourceKey: key,
		DisplayName:     safePetText(definition.Identity.DisplayName, "Pet"),
		Description:     safePetDescription(definition.Identity.Description),
	}
	for _, asset := range definition.Assets {
		mimeType := previewMIME(asset.Format, asset.Data)
		if mimeType == "" {
			continue
		}
		preview.AssetID = safePetText(asset.ID, "preview")
		preview.Format = strings.TrimPrefix(mimeType, "image/")
		preview.Width = asset.Width
		preview.Height = asset.Height
		break
	}
	return preview
}

func (s *PetSettingsService) previewRefLocked(key string, now time.Time) string {
	if s.previewRefs == nil {
		s.previewRefs = make(map[string]petPreviewRefEntry)
	}
	s.prunePreviewRefsLocked(now)
	for ref, entry := range s.previewRefs {
		if entry.Key == key {
			return ref
		}
	}
	for len(s.previewRefs) >= maxPetPreviewRefs {
		oldestRef := ""
		var oldest time.Time
		for ref, entry := range s.previewRefs {
			if oldestRef == "" || entry.IssuedAt.Before(oldest) {
				oldestRef = ref
				oldest = entry.IssuedAt
			}
		}
		if oldestRef == "" {
			break
		}
		delete(s.previewRefs, oldestRef)
	}
	ref := newPetPreviewRef(key, now)
	s.previewRefs[ref] = petPreviewRefEntry{Key: key, IssuedAt: now}
	return ref
}

func (s *PetSettingsService) previewKeyLocked(ref string, now time.Time) (string, bool) {
	if ref == "" || len(ref) > 128 || strings.TrimSpace(ref) != ref {
		return "", false
	}
	entry, ok := s.previewRefs[ref]
	if !ok {
		return "", false
	}
	if now.Before(entry.IssuedAt) || now.Sub(entry.IssuedAt) <= petPreviewRefLifetime {
		return entry.Key, true
	}
	delete(s.previewRefs, ref)
	return "", false
}

func (s *PetSettingsService) prunePreviewRefsLocked(now time.Time) {
	for ref, entry := range s.previewRefs {
		if !now.Before(entry.IssuedAt) && now.Sub(entry.IssuedAt) > petPreviewRefLifetime {
			delete(s.previewRefs, ref)
		}
	}
}

func (s *PetSettingsService) revokePreviewRefsLocked() {
	s.previewMedia = nil
	s.previewRefs = make(map[string]petPreviewRefEntry)
}

func newPetPreviewRef(key string, now time.Time) string {
	var random [16]byte
	if _, err := cryptorand.Read(random[:]); err != nil {
		fallback := sha256.Sum256([]byte(key + "\x00" + strconv.FormatInt(now.UnixNano(), 10)))
		copy(random[:], fallback[:16])
	}
	return "pet-preview-" + hex.EncodeToString(random[:])
}

func previewMIME(format string, data []byte) string {
	format = strings.ToLower(strings.TrimSpace(format))
	if strings.HasPrefix(format, "image/") {
		format = strings.TrimPrefix(format, "image/")
	}
	switch format {
	case "png":
		return "image/png"
	case "webp":
		return "image/webp"
	case "jpeg", "jpg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	}
	switch {
	case bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "image/png"
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp"
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return "image/jpeg"
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		return "image/gif"
	default:
		return ""
	}
}

func petPreviewRef(key string) string {
	digest := sha256.Sum256([]byte(key))
	return "pet-preview-" + hex.EncodeToString(digest[:12])
}

func validatePetKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 256 || !utf8.ValidString(value) || strings.ContainsAny(value, `/\\`) || strings.Contains(value, "://") {
		return "", petSelectionInvalid()
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return "", petSelectionInvalid()
		}
	}
	return value, nil
}

func safePetText(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]rune(value)) > pet.MaxSafeTextRunes || strings.ContainsAny(value, `/\\`) || strings.Contains(value, "://") {
		return fallback
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fallback
		}
	}
	return value
}

func safePetDescription(value string) string {
	if value == "" {
		return ""
	}
	if safe := safePetText(value, ""); safe != "" {
		return safe
	}
	return ""
}

func copyPetPreference(preference settings.PetPreference) settings.PetPreference {
	copy := preference
	copy.SelectedKey = cloneStringPointer(preference.SelectedKey)
	return copy
}

func petSizePercent(position settings.PetPosition) int {
	base := settings.DefaultPetPosition().Width
	if base <= 0 || position.Width <= 0 {
		return 100
	}
	percent := int(math.Round(float64(position.Width) * 100 / float64(base)))
	if percent < minPetSizePercent {
		return minPetSizePercent
	}
	if percent > maxPetSizePercent {
		return maxPetSizePercent
	}
	return percent
}

func petSizeDimension(base, percent int) int {
	if base <= 0 {
		return 1
	}
	dimension := int(math.Round(float64(base) * float64(percent) / 100))
	if dimension < 1 {
		return 1
	}
	return dimension
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func stringPointer(value string) *string {
	return &value
}

func petSettingsUnavailable() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorSettingsUnavailable,
		Summary:       "dsh-work Pet settings are unavailable.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Restart dsh-work and try again.",
	}
}

func petSelectionInvalid() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorSettingsStateInvalid,
		Summary:       "The Pet selection is invalid.",
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "Choose a Pet from the catalog.",
	}
}

func petRendererFailure() error {
	return lifecycle.Failure{
		Code:          lifecycle.ErrorSettingsStateInvalid,
		Summary:       "The selected Pet could not be prepared.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "The selection was not changed.",
	}
}

func safePetCatalogError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var failure lifecycle.Failure
	if errors.As(err, &failure) {
		return err
	}
	if len(pet.Diagnostics(err)) > 0 {
		return err
	}
	return lifecycle.Failure{
		Code:          lifecycle.ErrorSettingsStateInvalid,
		Summary:       "The Pet catalog operation failed.",
		Retryable:     true,
		CorrelationID: lifecycle.NewCorrelationID(),
		Detail:        "The Pet catalog could not complete the request.",
	}
}
