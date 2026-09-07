package pet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var spritePNGCache sync.Map

func TestCodexV1PreservesGeometryTracksAndAliases(t *testing.T) {
	root := t.TempDir()
	packageRoot := writeCodexPackage(t, root, SourceCodexPets, "alpha", map[string]any{
		"id":              "alpha",
		"displayName":     "Alpha",
		"description":     "A test pet",
		"spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")

	definition, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "alpha"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if definition.Source.Profile != profileCodexV1 || definition.Source.FormatVersion != "1" {
		t.Fatalf("source = %#v, want Codex v1", definition.Source)
	}
	if definition.Geometry != (Geometry{CellWidth: 192, CellHeight: 208, Columns: 8, Rows: 9, ImageWidth: 1536, ImageHeight: 1872, FrameCount: 72}) {
		t.Fatalf("geometry = %#v", definition.Geometry)
	}
	idle := definition.Tracks["idle"]
	if got := frameDurations(idle); !equalInts(got, []int{1680, 660, 660, 840, 840, 1920}) || !idle.Loop {
		t.Fatalf("idle = %#v", idle)
	}
	running := definition.Tracks["running-right"]
	if running.RepeatCount != 3 || running.Fallback != "idle" || !equalStrings(running.Aliases, []string{"move_right"}) {
		t.Fatalf("running-right = %#v", running)
	}
	if got := frameIndexes(definition.Tracks["review"]); !equalInts(got, []int{64, 65, 66, 67, 68, 69}) {
		t.Fatalf("review frames = %v", got)
	}
	if definition.Actions["wave"].Track != "waving" || definition.Actions["sad"].Track != "failed" {
		t.Fatalf("actions = %#v", definition.Actions)
	}
}

func TestCodexV2UsesExplicitProfileAndRecordsUnverifiedSemantics(t *testing.T) {
	root := t.TempDir()
	packageRoot := writeCodexPackage(t, root, SourceCodexPets, "beta", map[string]any{
		"id":                  "beta",
		"displayName":         "Beta",
		"spriteVersionNumber": 2,
		"spritesheetPath":     "spritesheet.png",
	}, 1536, 2288, "spritesheet.png")

	definition, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "beta"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if definition.Source.Profile != profileCodexV2 || definition.Geometry.Rows != 11 || definition.Geometry.FrameCount != 88 {
		t.Fatalf("v2 definition = %#v", definition)
	}
	if definition.Directions == nil || len(definition.Directions.Directions) != 16 || definition.Directions.NeutralPolicy != neutralDeadzoneIdle || definition.Directions.DesktopSemanticsVerified {
		t.Fatalf("v2 directions = %#v", definition.Directions)
	}
	if len(definition.Diagnostics) != 1 || definition.Diagnostics[0].Code != IssueV2SemanticsUnverified {
		t.Fatalf("v2 diagnostics = %#v", definition.Diagnostics)
	}
	if got := frameIndexes(definition.Tracks["idle"]); !equalInts(got, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("v2 idle frames = %v", got)
	}
	if got := frameIndexes(definition.Tracks["review"]); !equalInts(got, []int{64, 65, 66, 67, 68, 69}) {
		t.Fatalf("v2 review frames = %v", got)
	}
	if definition.Directions.Directions[0].FrameIndex != 72 || definition.Directions.Directions[15].FrameIndex != 87 {
		t.Fatalf("v2 direction frames = %#v", definition.Directions.Directions)
	}
}

func TestCodexVersionIsNotInferredFromImageHeight(t *testing.T) {
	root := t.TempDir()
	packageRoot := writeCodexPackage(t, root, SourceCodexPets, "unversioned-v2", map[string]any{
		"id":              "unversioned-v2",
		"displayName":     "Unversioned",
		"spritesheetPath": "spritesheet.png",
	}, 1536, 2288, "spritesheet.png")

	definition, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "unversioned-v2"))
	if err == nil || !isIssue(err, IssueVersionGeometryMismatch) || len(definition.Assets) != 0 || len(definition.Tracks) != 0 {
		t.Fatalf("Load() = definition %#v, error %v; want version/geometry rejection with no definition", definition, err)
	}
}

func TestCodexCustomAnimationValidationAndFallback(t *testing.T) {
	root := t.TempDir()
	packageRoot := writeCodexPackage(t, root, SourceCodexPets, "custom", map[string]any{
		"id":              "custom",
		"displayName":     "Custom",
		"spritesheetPath": "spritesheet.png",
		"animations": map[string]any{
			"blink": map[string]any{
				"frames":   []int{3, 4},
				"fps":      8,
				"loop":     false,
				"fallback": "idle",
				"aliases":  []string{"blink-now"},
			},
		},
	}, 1536, 1872, "spritesheet.png")

	definition, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "custom"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	custom := definition.Tracks["blink"]
	if !equalInts(frameIndexes(custom), []int{3, 4}) || !equalInts(frameDurations(custom), []int{125, 125}) || custom.Loop || custom.Fallback != "idle" || !equalStrings(custom.Aliases, []string{"blink-now"}) {
		t.Fatalf("custom track = %#v", custom)
	}

	cases := []struct {
		name      string
		animation map[string]any
	}{
		{name: "empty frames", animation: map[string]any{"frames": []int{}}},
		{name: "out of range", animation: map[string]any{"frames": []int{72}}},
		{name: "zero fps", animation: map[string]any{"frames": []int{0}, "fps": 0}},
		{name: "high fps", animation: map[string]any{"frames": []int{0}, "fps": 61}},
		{name: "missing fallback", animation: map[string]any{"frames": []int{0}, "fallback": "missing"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			manifest := map[string]any{
				"id":              "invalid-" + testCase.name,
				"displayName":     "Invalid",
				"spritesheetPath": "spritesheet.png",
				"animations":      map[string]any{"bad": testCase.animation},
			}
			writeJSON(t, filepath.Join(packageRoot, "pet.json"), manifest)
			definition, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "custom"))
			if err == nil || !isIssue(err, IssueInvalidAnimation) || len(definition.Assets) != 0 || len(definition.Tracks) != 0 {
				t.Fatalf("Load() = definition %#v, error %v; want invalid animation with no definition", definition, err)
			}
		})
	}
}

func TestCodexAvatarCompatibilityPathUsesStandardContract(t *testing.T) {
	root := t.TempDir()
	packageRoot := writeCodexPackage(t, root, SourceCodexAvatars, "avatar", map[string]any{
		"id": "avatar", "displayName": "Avatar", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")

	definition, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexAvatars, "avatar"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if definition.Source.Profile != profileCodexV1 || definition.Source.Badge != BadgeCodex || definition.Source.FormatVersion != "1" {
		t.Fatalf("avatar source = %#v", definition.Source)
	}
	if definition.Identity.DisplayName != "Avatar" || definition.Geometry.FrameCount != 72 || frameIndexes(definition.Tracks["idle"])[0] != 0 {
		t.Fatalf("avatar definition = %#v", definition)
	}
}

func TestCodexRejectsUnsafePathsAndExecutableResources(t *testing.T) {
	root := t.TempDir()
	packageRoot := writeCodexPackage(t, root, SourceCodexPets, "unsafe", map[string]any{
		"id":              "unsafe",
		"displayName":     "Unsafe",
		"spritesheetPath": "../outside.png",
	}, 1536, 1872, "unused.png")
	if _, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "unsafe")); !isIssue(err, IssuePathOutsideRoot) {
		t.Fatalf("path traversal error = %v, want %s", err, IssuePathOutsideRoot)
	}

	writeJSON(t, filepath.Join(packageRoot, "pet.json"), map[string]any{
		"id":              "unsafe",
		"displayName":     "Unsafe",
		"spritesheetPath": "spritesheet.png",
	})
	if err := os.WriteFile(filepath.Join(packageRoot, "payload.js"), []byte("alert(1)"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "unsafe")); !isIssue(err, IssueUnsafeResource) {
		t.Fatalf("script resource error = %v, want %s", err, IssueUnsafeResource)
	}
}

func TestCodexRejectsSymlinkedPackageResource(t *testing.T) {
	root := t.TempDir()
	packageRoot := filepath.Join(root, "pets", "link")
	if err := os.MkdirAll(packageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(packageRoot, "pet.json"), map[string]any{
		"id":              "link",
		"displayName":     "Link",
		"spritesheetPath": "spritesheet.png",
	})
	outside := filepath.Join(root, "outside.png")
	if err := os.WriteFile(outside, spritePNG(t, 1536, 1872), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(packageRoot, "spritesheet.png")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink creation is unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "link")); !isIssue(err, IssueSymlinkEscape) {
		t.Fatalf("symlink resource error = %v, want %s", err, IssueSymlinkEscape)
	}
}

func TestCodexEnforcesManifestFileAndImageBudgets(t *testing.T) {
	t.Run("manifest", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := filepath.Join(root, "pets", "large-manifest")
		if err := os.MkdirAll(packageRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest := append([]byte(`{"id":"large","displayName":"Large","spritesheetPath":"spritesheet.png","description":"`), bytes.Repeat([]byte("x"), int(MaxManifestBytes))...)
		manifest = append(manifest, []byte(`"}`)...)
		if err := os.WriteFile(filepath.Join(packageRoot, "pet.json"), manifest, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "large-manifest")); !isIssue(err, IssueManifestTooLarge) {
			t.Fatalf("large manifest error = %v, want %s", err, IssueManifestTooLarge)
		}
	})

	t.Run("file count", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := writeCodexPackage(t, root, SourceCodexPets, "many-files", map[string]any{
			"id": "many-files", "displayName": "Many files", "spritesheetPath": "spritesheet.png",
		}, 1536, 1872, "spritesheet.png")
		for index := 0; index < MaxPackageFiles; index++ {
			path := filepath.Join(packageRoot, "data-"+strconv.Itoa(index)+".json")
			if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "many-files")); !isIssue(err, IssueFileCountExceeded) {
			t.Fatalf("file count error = %v, want %s", err, IssueFileCountExceeded)
		}
	})

	t.Run("decoded image", func(t *testing.T) {
		root := t.TempDir()
		packageRoot := writeCodexPackage(t, root, SourceCodexPets, "large-image", map[string]any{
			"id": "large-image", "displayName": "Large image", "spritesheetPath": "spritesheet.png",
		}, 4097, 1, "spritesheet.png")
		if _, err := NewCodexAdapter().Load(context.Background(), NewCodexPackageSource(packageRoot, SourceCodexPets, "large-image")); !isIssue(err, IssueDecodedImageTooLarge) {
			t.Fatalf("large image error = %v, want %s", err, IssueDecodedImageTooLarge)
		}
	})
}

func TestPetCatalogDiscoversDirectChildrenWithPetsPrecedence(t *testing.T) {
	home := t.TempDir()
	writeCodexPackage(t, home, SourceCodexPets, "alpha", map[string]any{
		"id": "alpha", "displayName": "Alpha", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")
	writeCodexPackage(t, home, SourceCodexAvatars, "alpha", map[string]any{
		"id": "avatar-alpha", "displayName": "Avatar Alpha", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")
	writeCodexPackage(t, home, SourceCodexAvatars, "beta", map[string]any{
		"id": "beta", "displayName": "Beta", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")
	nested := filepath.Join(home, "pets", "nested", "ignored")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(nested, "pet.json"), map[string]any{
		"id": "ignored", "displayName": "Ignored", "spritesheetPath": "spritesheet.png",
	})
	if err := os.WriteFile(filepath.Join(nested, "spritesheet.png"), spritePNG(t, 1536, 1872), 0o600); err != nil {
		t.Fatal(err)
	}

	catalog, err := NewPetCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache")})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if snapshot.ScanState != ScanReady || snapshot.Revision != 1 || len(snapshot.Items) != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Items[0].StableSourceKey != StableSourceKey(SourceCodexPets, "alpha") || snapshot.Items[1].StableSourceKey != StableSourceKey(SourceCodexAvatars, "beta") {
		t.Fatalf("items = %#v", snapshot.Items)
	}
	if snapshot.Items[0].SourceBadge != BadgeCodex || snapshot.Items[1].SourceBadge != BadgeCodex {
		t.Fatalf("badges = %#v", snapshot.Items)
	}
	if !hasIssue(snapshot.Issues, IssueDiscoveryConflict) {
		t.Fatalf("issues = %#v, want precedence conflict", snapshot.Issues)
	}
	for _, item := range snapshot.Items {
		if strings.Contains(item.DisplayName, "Ignored") || strings.Contains(item.StableSourceKey, "nested") {
			t.Fatalf("nested package leaked into catalog: %#v", item)
		}
	}
}

func TestPetCatalogMaterializesAndDefensivelyResolvesPackages(t *testing.T) {
	home := t.TempDir()
	packageRoot := writeCodexPackage(t, home, SourceCodexPets, "stable", map[string]any{
		"id": "stable", "displayName": "Original", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	catalog, err := NewPetCatalog(CatalogConfig{CodexHome: home, CacheRoot: cacheRoot})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil || len(snapshot.Items) != 1 {
		t.Fatalf("Refresh() = %#v, %v", snapshot, err)
	}
	key := snapshot.Items[0].StableSourceKey
	definition, err := catalog.Resolve(context.Background(), key)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if definition.Source.Digest == "" || definition.Identity.DisplayName != "Original" {
		t.Fatalf("resolved definition = %#v", definition)
	}
	originalBytes := append([]byte(nil), definition.Assets[0].Data...)
	definition.Assets[0].Data[0] ^= 0xff
	writeJSON(t, filepath.Join(packageRoot, "pet.json"), map[string]any{
		"id": "stable", "displayName": "Changed", "spritesheetPath": "spritesheet.png",
	})
	resolvedAgain, err := catalog.Resolve(context.Background(), key)
	if err != nil {
		t.Fatalf("Resolve() after source change error = %v", err)
	}
	if resolvedAgain.Identity.DisplayName != "Original" || !bytes.Equal(resolvedAgain.Assets[0].Data, originalBytes) {
		t.Fatalf("resolved data changed after source/memory mutation: %#v", resolvedAgain.Identity)
	}
	cacheEntries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(cacheEntries) != 1 || !cacheEntries[0].IsDir() {
		t.Fatalf("cache entries = %#v", cacheEntries)
	}
	cacheAsset := filepath.Join(cacheRoot, cacheEntries[0].Name(), "spritesheet.png")
	if got, err := os.ReadFile(cacheAsset); err != nil || !bytes.Equal(got, originalBytes) {
		t.Fatalf("materialized asset mismatch: err=%v bytes=%d", err, len(got))
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(cacheAsset); err != nil || info.Mode().Perm()&0o222 != 0 {
			t.Fatalf("cache asset permissions = %v, err=%v; want read-only", info.Mode().Perm(), err)
		}
	}
}

func TestPetCatalogSnapshotRevisionStaleAndDefensiveCopy(t *testing.T) {
	home := t.TempDir()
	writeCodexPackage(t, home, SourceCodexPets, "one", map[string]any{
		"id": "one", "displayName": "One", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	catalog, err := NewPetCatalog(CatalogConfig{CodexHome: home, CacheRoot: cacheRoot})
	if err != nil {
		t.Fatal(err)
	}
	first, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.Items[0].Capabilities[0] = "mutated"
	if len(first.Issues) > 0 {
		first.Issues[0].Args["folder"] = "mutated"
	}
	current, err := catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if current.Items[0].Capabilities[0] == "mutated" {
		t.Fatal("Snapshot() exposed mutable item slices")
	}
	second, err := catalog.Refresh(context.Background())
	if err != nil || second.Revision != first.Revision+1 {
		t.Fatalf("second refresh = %#v, %v", second, err)
	}

	moved := filepath.Join(t.TempDir(), "moved-home")
	if err := os.Rename(home, moved); err != nil {
		t.Fatal(err)
	}
	failed, err := catalog.Refresh(context.Background())
	if err == nil || failed.ScanState != ScanFailed || !failed.Stale || len(failed.Items) != 1 || failed.Revision != second.Revision {
		t.Fatalf("failed refresh = %#v, %v", failed, err)
	}
	if !hasIssue(failed.Issues, IssueHomeUnavailable) {
		t.Fatalf("failed issues = %#v", failed.Issues)
	}
}

func TestPetCatalogCoalescesConcurrentRefreshAndDoesNotPublishCancellation(t *testing.T) {
	home := t.TempDir()
	writeCodexPackage(t, home, SourceCodexPets, "blocked", map[string]any{
		"id": "blocked", "displayName": "Blocked", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872, "spritesheet.png")
	adapter := &blockingAdapter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	catalog, err := NewPetCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache"), Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	firstDone := make(chan refreshResult, 1)
	go func() {
		snapshot, err := catalog.Refresh(context.Background())
		firstDone <- refreshResult{snapshot: snapshot, err: err}
	}()
	<-adapter.entered
	secondDone := make(chan refreshResult, 1)
	go func() {
		snapshot, err := catalog.Refresh(context.Background())
		secondDone <- refreshResult{snapshot: snapshot, err: err}
	}()
	select {
	case result := <-secondDone:
		t.Fatalf("coalesced refresh completed before release: %#v", result)
	case <-time.After(20 * time.Millisecond):
	}
	if adapter.calls.Load() != 1 {
		t.Fatalf("adapter calls while coalesced = %d, want 1", adapter.calls.Load())
	}
	close(adapter.release)
	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil || first.snapshot.Revision != 1 || second.snapshot.Revision != 1 {
		t.Fatalf("coalesced results = %#v / %#v", first, second)
	}

	cancelAdapter := &blockingAdapter{entered: make(chan struct{}), release: make(chan struct{})}
	cancelCatalog, err := NewPetCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache"), Adapter: cancelAdapter})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	resultDone := make(chan refreshResult, 1)
	go func() {
		snapshot, err := cancelCatalog.Refresh(ctx)
		resultDone <- refreshResult{snapshot: snapshot, err: err}
	}()
	<-cancelAdapter.entered
	cancel()
	result := <-resultDone
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("cancel result error = %v, want context canceled", result.err)
	}
	for attempt := 0; attempt < 20; attempt++ {
		snapshot, snapshotErr := cancelCatalog.Snapshot(context.Background())
		if snapshotErr != nil {
			t.Fatal(snapshotErr)
		}
		if snapshot.ScanState != ScanScanning {
			if snapshot.Revision != 0 || snapshot.ScanState != ScanNeverScanned {
				t.Fatalf("canceled snapshot = %#v", snapshot)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("canceled refresh left snapshot in scanning state")
}

type refreshResult struct {
	snapshot CatalogSnapshot
	err      error
}

type blockingAdapter struct {
	delegate CodexAdapter
	entered  chan struct{}
	release  chan struct{}
	once     sync.Once
	calls    atomic.Int32
}

func (a *blockingAdapter) Probe(ctx context.Context, source PackageSource) (ProbeResult, error) {
	return a.delegate.Probe(ctx, source)
}

func (a *blockingAdapter) Load(ctx context.Context, source PackageSource) (PetDefinition, error) {
	a.calls.Add(1)
	a.once.Do(func() { close(a.entered) })
	select {
	case <-a.release:
	case <-ctx.Done():
		return PetDefinition{}, ctx.Err()
	}
	return a.delegate.Load(ctx, source)
}

func writeCodexPackage(t *testing.T, home string, kind SourceKind, folder string, manifest map[string]any, width, height int, imageName string) string {
	t.Helper()
	directory := "pets"
	manifestName := "pet.json"
	if kind == SourceCodexAvatars {
		directory = "avatars"
		manifestName = "avatar.json"
	}
	packageRoot := filepath.Join(home, directory, folder)
	if err := os.MkdirAll(packageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(packageRoot, manifestName), manifest)
	if err := os.WriteFile(filepath.Join(packageRoot, imageName), spritePNG(t, width, height), 0o600); err != nil {
		t.Fatal(err)
	}
	return packageRoot
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func spritePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	key := [2]int{width, height}
	if value, ok := spritePNGCache.Load(key); ok {
		return append([]byte(nil), value.([]byte)...)
	}
	imageValue := image.NewNRGBA(image.Rect(0, 0, width, height))
	imageValue.SetNRGBA(0, 0, color.NRGBA{R: 255, G: 80, B: 160, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, imageValue); err != nil {
		t.Fatal(err)
	}
	encoded := append([]byte(nil), buffer.Bytes()...)
	actual, _ := spritePNGCache.LoadOrStore(key, encoded)
	return append([]byte(nil), actual.([]byte)...)
}

func frameIndexes(track TrackSpec) []int {
	indexes := make([]int, len(track.Frames))
	for index, frame := range track.Frames {
		indexes[index] = frame.Index
	}
	return indexes
}

func frameDurations(track TrackSpec) []int {
	durations := make([]int, len(track.Frames))
	for index, frame := range track.Frames {
		durations[index] = frame.DurationMS
	}
	return durations
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func isIssue(err error, code string) bool {
	for _, issue := range Diagnostics(err) {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func hasIssue(issues []Issue, code string) bool {
	for _, issue := range issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
