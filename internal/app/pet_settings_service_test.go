package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/png"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dsh-work/internal/pet"
	"github.com/local/dsh-work/internal/settings"
)

func TestPetSettingsServiceRequiresTrustedSettingsSurface(t *testing.T) {
	manager := newPetServiceManager(t, settings.DefaultValues())
	service := NewPetSettingsService(manager, &petServiceCatalog{})
	if _, err := service.GetPetPanel(context.Background()); err == nil || !strings.Contains(err.Error(), "TRUSTED_SURFACE_REQUIRED") {
		t.Fatalf("GetPetPanel() error = %v, want trusted-surface failure", err)
	}
}

func TestPersistPetPositionSurvivesReloadWithoutWindowActions(t *testing.T) {
	store := &petServiceStore{}
	manager := newPetServiceManagerWithStore(t, store, settings.DefaultValues())
	service := newTrustedPetSettingsService(manager, &petServiceCatalog{}, nil)
	windowActions := 0
	service.overlayHooks = PetOverlayHooks{
		Show: func() error { windowActions++; return nil },
		Hide: func() error { windowActions++; return nil },
	}
	position := settings.PetPosition{MonitorID: "monitor-2", AnchorX: .65, AnchorY: .4, Width: 192, Height: 208, Scale: 2}
	if err := PersistPetPosition(context.Background(), service, position); err != nil {
		t.Fatal(err)
	}
	reloaded := newPetServiceManagerWithStore(t, store, settings.DefaultValues())
	values, err := reloaded.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if values.Pet.Position != position {
		t.Fatalf("reloaded position = %+v, want %+v", values.Pet.Position, position)
	}
	if windowActions != 0 {
		t.Fatalf("persisting a move triggered %d window actions", windowActions)
	}
}

func TestPetSettingsServiceSetPetSizePersistsAndResizes(t *testing.T) {
	key := "codex:pets:size"
	store := &petServiceStore{}
	manager := newPetServiceManagerWithStore(t, store, petServiceValues(key, settings.PetVisibilityHidden))
	catalog := &petServiceCatalog{
		snapshot:    pet.CatalogSnapshot{Revision: 1, ScanState: pet.ScanReady, Items: []pet.PetListItem{{StableSourceKey: key, DisplayName: "Size", Availability: pet.AvailabilityReady}}},
		definitions: map[string]pet.PetDefinition{key: testPetDefinition(key, "Size", "C:\\pet\\sprite.png", []byte("ok"))},
	}
	service := newTrustedPetSettingsService(manager, catalog, nil)
	var resized [][2]int
	AttachPetOverlayHooks(service, PetOverlayHooks{Resize: func(width, height int) error {
		resized = append(resized, [2]int{width, height})
		return nil
	}})

	panel, err := service.SetPetSize(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	if panel.SizePercent != 200 || !panel.SizeAvailable {
		t.Fatalf("size projection = %+v, want 200%% and available", panel)
	}
	if panel.Preference.Position.Width != 288 || panel.Preference.Position.Height != 312 {
		t.Fatalf("persisted size = %+v, want 288x312", panel.Preference.Position)
	}
	if len(resized) != 1 || resized[0] != [2]int{288, 312} {
		t.Fatalf("resize calls = %v, want 288x312", resized)
	}
	if store.saves != 1 {
		t.Fatalf("size persisted %d times, want once", store.saves)
	}
}

func TestPetSettingsServiceSetPetSizeRejectsOutOfRange(t *testing.T) {
	store := &petServiceStore{}
	manager := newPetServiceManagerWithStore(t, store, settings.DefaultValues())
	service := newTrustedPetSettingsService(manager, &petServiceCatalog{}, nil)
	resizes := 0
	AttachPetOverlayHooks(service, PetOverlayHooks{Resize: func(int, int) error {
		resizes++
		return nil
	}})
	for _, percent := range []int{201, 51} {
		if _, err := service.SetPetSize(context.Background(), percent); err == nil || !strings.Contains(err.Error(), "percent is invalid") {
			t.Fatalf("invalid size %d error = %v", percent, err)
		}
	}
	if store.saves != 0 || resizes != 0 {
		t.Fatalf("invalid size changed state: saves=%d resizes=%d", store.saves, resizes)
	}
}

func TestPetSettingsServiceProjectsSafelyAndBrowseDoesNotPersist(t *testing.T) {
	key := "codex:pets:alpha"
	definition := testPetDefinition(key, "Alpha", "C:\\private\\spritesheet.png", previewPNG())
	catalog := &petServiceCatalog{
		snapshot: pet.CatalogSnapshot{
			Revision:  4,
			ScanState: pet.ScanReady,
			Items:     []pet.PetListItem{{StableSourceKey: key, DisplayName: "Alpha", Availability: pet.AvailabilityReady}},
		},
		definitions: map[string]pet.PetDefinition{key: definition},
	}
	store := &petServiceStore{}
	manager := newPetServiceManagerWithStore(t, store, settings.DefaultValues())
	service := newTrustedPetSettingsService(manager, catalog, nil)

	panel, err := service.GetPetPanel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if selectedKeyForTest(panel.Preference) != "" {
		t.Fatalf("initial preference = %+v, want no selected key", panel.Preference)
	}
	if len(panel.Snapshot.Items) != 1 || !strings.HasPrefix(panel.Snapshot.Items[0].PreviewRef, "pet-preview-") {
		t.Fatalf("projected items = %+v, want opaque preview ref", panel.Snapshot.Items)
	}
	if panel.Runtime.SelectionStatus != pet.SelectionNone || panel.Runtime.EffectiveVisibility != pet.VisibilityHidden {
		t.Fatalf("initial runtime = %+v", panel.Runtime)
	}

	preview, err := service.PreviewPet(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if preview.PreviewRef != panel.Snapshot.Items[0].PreviewRef || !strings.HasPrefix(preview.PreviewRef, "pet-preview-") {
		t.Fatalf("preview = %+v", preview)
	}
	dataURL, err := service.GetPetPreview(context.Background(), preview.PreviewRef)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(dataURL, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("preview data could not be decoded: %v", err)
	}
	imageData, err := png.Decode(bytes.NewReader(decoded))
	if err != nil || imageData.Bounds().Dx() != 1 || imageData.Bounds().Dy() != 1 {
		t.Fatalf("preview image = %v, error = %v", imageData.Bounds(), err)
	}
	if strings.Contains(dataURL, definition.Assets[0].Path) || strings.Contains(preview.PreviewRef, definition.Assets[0].Path) {
		t.Fatalf("preview leaked source path: ref=%+v data=%q", preview, dataURL)
	}
	if store.saves != 0 {
		t.Fatalf("browse operations saved %d times, want zero", store.saves)
	}
	service.mu.Lock()
	service.previewRefs[preview.PreviewRef] = petPreviewRefEntry{Key: key, IssuedAt: time.Now().Add(-petPreviewRefLifetime - time.Second)}
	service.mu.Unlock()
	if _, err := service.GetPetPreview(context.Background(), preview.PreviewRef); err == nil {
		t.Fatal("expired preview reference resolved successfully")
	}
}

func TestPetSettingsServiceSelectPreflightsBeforePersisting(t *testing.T) {
	key := "codex:pets:alpha"
	events := make([]string, 0, 3)
	store := &petServiceStore{onSave: func() { events = append(events, "save") }}
	manager := newPetServiceManagerWithStore(t, store, petServiceValues(key, settings.PetVisibilityVisible))
	catalog := &petServiceCatalog{
		snapshot:    pet.CatalogSnapshot{Revision: 2, ScanState: pet.ScanReady, Items: []pet.PetListItem{{StableSourceKey: key, DisplayName: "Alpha", Availability: pet.AvailabilityReady}}},
		definitions: map[string]pet.PetDefinition{key: testPetDefinition(key, "Alpha", "D:\\pet\\sprite.png", []byte("ok"))},
		onResolve:   func() { events = append(events, "resolve") },
	}
	renderer := &petServiceRenderer{onPreflight: func() { events = append(events, "preflight") }}
	service := newTrustedPetSettingsService(manager, catalog, renderer)

	panel, err := service.SelectPet(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"resolve", "preflight", "save"}) {
		t.Fatalf("transaction events = %v, want resolve -> preflight -> save", events)
	}
	if panel.Preference.SelectedKey == nil || *panel.Preference.SelectedKey != key || panel.Preference.VisibilityIntent != settings.PetVisibilityVisible {
		t.Fatalf("selected preference = %+v", panel.Preference)
	}
	if panel.Runtime.SelectionStatus != pet.SelectionReady || panel.Runtime.EffectiveVisibility != pet.VisibilityVisible {
		t.Fatalf("selected runtime = %+v", panel.Runtime)
	}
}

func TestPetSettingsServiceSelectFailureKeepsOldPreference(t *testing.T) {
	oldKey := "codex:pets:old"
	newKey := "codex:pets:new"
	values := petServiceValues(oldKey, settings.PetVisibilityVisible)
	store := &petServiceStore{}
	manager := newPetServiceManagerWithStore(t, store, values)
	catalog := &petServiceCatalog{
		snapshot:    pet.CatalogSnapshot{Revision: 3, ScanState: pet.ScanReady, Items: []pet.PetListItem{{StableSourceKey: newKey, DisplayName: "New", Availability: pet.AvailabilityReady}}},
		definitions: map[string]pet.PetDefinition{newKey: testPetDefinition(newKey, "New", "E:\\old\\new.png", []byte("new"))},
	}
	renderer := &petServiceRenderer{err: errors.New("preflight failed")}
	service := newTrustedPetSettingsService(manager, catalog, renderer)
	if _, err := service.SelectPet(context.Background(), newKey); err == nil {
		t.Fatal("SelectPet() error = nil, want renderer preflight failure")
	}
	if store.saves != 0 {
		t.Fatalf("renderer failure saved %d times, want zero", store.saves)
	}
	got, err := manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Pet.SelectedKey == nil || *got.Pet.SelectedKey != oldKey || got.Pet.VisibilityIntent != settings.PetVisibilityVisible {
		t.Fatalf("preference after renderer failure = %+v", got.Pet)
	}

	store.err = errors.New("save failed")
	renderer.err = nil
	if _, err := service.SelectPet(context.Background(), newKey); err == nil {
		t.Fatal("SelectPet() error = nil, want persistence failure")
	}
	got, err = manager.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Pet.SelectedKey == nil || *got.Pet.SelectedKey != oldKey {
		t.Fatalf("preference after persistence failure = %+v", got.Pet)
	}
}

func TestPetSettingsServiceVisibilityClearAndUnavailableProjection(t *testing.T) {
	missingKey := "codex:pets:missing"
	manager := newPetServiceManager(t, petServiceValues(missingKey, settings.PetVisibilityVisible))
	catalog := &petServiceCatalog{snapshot: pet.CatalogSnapshot{Revision: 1, ScanState: pet.ScanReady}}
	service := newTrustedPetSettingsService(manager, catalog, nil)

	panel, err := service.GetPetPanel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if panel.Runtime.SelectionStatus != pet.SelectionUnavailable || panel.Runtime.EffectiveVisibility != pet.VisibilityPaused {
		t.Fatalf("unavailable visible runtime = %+v", panel.Runtime)
	}
	panel, err = service.SetPetVisibility(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if panel.Preference.SelectedKey == nil || *panel.Preference.SelectedKey != missingKey || panel.Preference.VisibilityIntent != settings.PetVisibilityHidden || panel.Runtime.EffectiveVisibility != pet.VisibilityHidden {
		t.Fatalf("hidden unavailable panel = %+v", panel)
	}
	panel, err = service.ClearPetSelection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if panel.Preference.SelectedKey != nil || panel.Preference.VisibilityIntent != settings.PetVisibilityHidden || panel.Runtime.SelectionStatus != pet.SelectionNone {
		t.Fatalf("cleared panel = %+v", panel)
	}
}

func TestPetSettingsServiceFallbackPersistsSelectionWithoutActivatingOverlay(t *testing.T) {
	key := "codex:pets:fallback"
	manager := newPetServiceManager(t, petServiceValues(key, settings.PetVisibilityVisible))
	catalog := &petServiceCatalog{
		snapshot:    pet.CatalogSnapshot{Revision: 1, ScanState: pet.ScanReady, Items: []pet.PetListItem{{StableSourceKey: key, DisplayName: "Fallback", Availability: pet.AvailabilityReady}}},
		definitions: map[string]pet.PetDefinition{key: testPetDefinition(key, "Fallback", "C:\\pet\\sprite.png", []byte("ok"))},
	}
	renderer := &petServiceRenderer{}
	service := newTrustedPetSettingsService(manager, catalog, nil)
	SetPetRendererFactory(service, func() pet.PetRenderer { return renderer })
	SetPetOverlayCapabilities(service, pet.CapabilitiesFor("linux"))

	panel, err := service.SelectPet(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if panel.Preference.SelectedKey == nil || *panel.Preference.SelectedKey != key {
		t.Fatalf("selection = %+v, want persisted key", panel.Preference)
	}
	if panel.Runtime.EffectiveVisibility != pet.VisibilityPaused || panel.Runtime.OverlayStatus != pet.OverlayFailed {
		t.Fatalf("fallback runtime = %+v", panel.Runtime)
	}
	if renderer.err != nil {
		t.Fatalf("unexpected renderer state: %+v", renderer)
	}
}

type petServiceStore struct {
	mu     sync.Mutex
	values *settings.Values
	saves  int
	err    error
	onSave func()
}

func (s *petServiceStore) Load(context.Context, string) (*settings.Values, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		return nil, nil
	}
	copy := *s.values
	copy.Pet.SelectedKey = cloneStringPointer(s.values.Pet.SelectedKey)
	return &copy, nil
}

func (s *petServiceStore) Save(_ context.Context, _ string, values settings.Values) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	if s.onSave != nil {
		s.onSave()
	}
	if s.err != nil {
		return s.err
	}
	copy := values
	copy.Pet.SelectedKey = cloneStringPointer(values.Pet.SelectedKey)
	s.values = &copy
	return nil
}

type petServiceCatalog struct {
	mu          sync.Mutex
	snapshot    pet.CatalogSnapshot
	definitions map[string]pet.PetDefinition
	refreshErr  error
	snapshotErr error
	resolveErr  error
	onResolve   func()
}

func (c *petServiceCatalog) Snapshot(context.Context) (pet.CatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot, c.snapshotErr
}

func (c *petServiceCatalog) Refresh(context.Context) (pet.CatalogSnapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.snapshot, c.refreshErr
}

func (c *petServiceCatalog) Inspect(context.Context, pet.PackageSource) (pet.Inspection, error) {
	return pet.Inspection{}, errors.New("not used by PetSettingsService")
}

func (c *petServiceCatalog) Resolve(_ context.Context, key string) (pet.PetDefinition, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onResolve != nil {
		c.onResolve()
	}
	if c.resolveErr != nil {
		return pet.PetDefinition{}, c.resolveErr
	}
	definition, ok := c.definitions[key]
	if !ok {
		return pet.PetDefinition{}, &pet.DiagnosticError{Issues: []pet.Issue{{Code: pet.IssueCatalogItemNotFound, Retryable: true, Severity: pet.SeverityError}}}
	}
	return definition, nil
}

type petServiceRenderer struct {
	err         error
	onPreflight func()
}

func (r *petServiceRenderer) Probe(context.Context, pet.PetDefinition) error {
	if r.onPreflight != nil {
		r.onPreflight()
	}
	return r.err
}

func (r *petServiceRenderer) Load(context.Context, pet.PetDefinition) error { return r.err }

func (r *petServiceRenderer) ApplySnapshot(context.Context, pet.PetRuntimeSnapshot) (pet.RenderedFrame, error) {
	return pet.RenderedFrame{}, r.err
}

func (r *petServiceRenderer) Unload(context.Context) error { return r.err }

func newTrustedPetSettingsService(manager *settings.Manager, catalog pet.PetCatalog, renderer pet.PetRenderer) *PetSettingsService {
	service := NewPetSettingsService(manager, catalog, renderer)
	service.trustedSurface = func(context.Context) bool { return true }
	return service
}

func newPetServiceManager(t *testing.T, values settings.Values) *settings.Manager {
	t.Helper()
	return newPetServiceManagerWithStore(t, &petServiceStore{values: &values}, values)
}

func newPetServiceManagerWithStore(t *testing.T, store *petServiceStore, values settings.Values) *settings.Manager {
	t.Helper()
	if store.values == nil {
		store.values = &values
	}
	manager, err := settings.New(settings.Config{Path: "pet-service-test.json", Store: store})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func petServiceValues(key string, visibility settings.PetVisibilityIntent) settings.Values {
	values := settings.DefaultValues()
	values.Pet.SelectedKey = stringPointer(key)
	values.Pet.VisibilityIntent = visibility
	return values
}

func testPetDefinition(key, name, path string, data []byte) pet.PetDefinition {
	return pet.PetDefinition{
		Identity: pet.Identity{ID: key, DisplayName: name},
		Source:   pet.SourceInfo{StableKey: key},
		Geometry: pet.Geometry{CellWidth: 1, CellHeight: 1, Columns: 1, Rows: 1, ImageWidth: 1, ImageHeight: 1, FrameCount: 1},
		Assets:   []pet.Asset{{ID: "spritesheet", Kind: "spritesheet", Path: path, Format: "png", Width: 1, Height: 1, Data: data}},
		Tracks:   map[string]pet.TrackSpec{"idle": {ID: "idle", RendererKind: pet.RendererRasterV1, Frames: []pet.FrameRef{{Index: 0, DurationMS: 1000}}, Loop: true}},
		Fallback: pet.FallbackSpec{Idle: "idle"},
	}
}

func previewPNG() []byte {
	var buffer bytes.Buffer
	imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
	imageData.Set(0, 0, color.RGBA{R: 0x40, G: 0x90, B: 0xff, A: 0xff})
	if err := png.Encode(&buffer, imageData); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func selectedKeyForTest(p settings.PetPreference) string {
	if p.SelectedKey == nil {
		return ""
	}
	return *p.SelectedKey
}
