package pet

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCodexCatalogDiscoveryUsesEnvironmentPrecedenceAndDirectChildren(t *testing.T) {
	envHome := t.TempDir()
	configuredHome := t.TempDir()

	writeCodexFixture(t, envHome, SourceCodexPets, "shared", map[string]any{
		"id":              "pets-shared",
		"displayName":     "Pet Shared",
		"spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	writeCodexFixture(t, envHome, SourceCodexAvatars, "SHARED", map[string]any{
		"id": "avatar-shared", "displayName": "Avatar Shared", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	writeCodexFixture(t, envHome, SourceCodexAvatars, "avatar-only", map[string]any{
		"id": "avatar-only", "displayName": "Avatar Only", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	writeCodexFixture(t, configuredHome, SourceCodexPets, "configured-only", map[string]any{
		"id":              "configured-only",
		"displayName":     "Wrong Home",
		"spritesheetPath": "spritesheet.png",
	}, 1536, 1872)

	nestedRoot := filepath.Join(envHome, "pets", "nested", "child")
	if err := os.MkdirAll(nestedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(nestedRoot, "pet.json"), map[string]any{
		"id":              "nested-child",
		"displayName":     "Must Not Be Discovered",
		"spritesheetPath": "spritesheet.png",
	})
	if err := os.MkdirAll(filepath.Join(envHome, "catalog", "builtin"), 0o700); err != nil {
		t.Fatal(err)
	}
	nativeOnlyRoot := filepath.Join(envHome, "pets", "native-only")
	if err := os.MkdirAll(nativeOnlyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(nativeOnlyRoot, "dsh-pet.json"), map[string]any{"id": "native-only"})

	catalog, err := NewCatalog(CatalogConfig{
		CodexHome: configuredHome,
		CacheRoot: filepath.Join(t.TempDir(), "cache"),
		Env: func(name string) string {
			if name == "CODEX_HOME" {
				return envHome
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.ScanState != ScanReady || snapshot.Revision != 1 || snapshot.Stale {
		t.Fatalf("unexpected discovery snapshot: %+v", snapshot)
	}

	keys := make(map[string]bool, len(snapshot.Items))
	for _, item := range snapshot.Items {
		keys[item.StableSourceKey] = true
		if strings.Contains(item.DisplayName, "Wrong Home") || strings.Contains(item.DisplayName, "Must Not") {
			t.Fatalf("discovered an out-of-scope package: %+v", item)
		}
	}
	for _, key := range []string{"codex:pets:shared", "codex:avatars:avatar-only"} {
		if !keys[key] {
			t.Fatalf("missing discovered key %q in %#v", key, keys)
		}
	}
	if len(keys) != 2 {
		t.Fatalf("discovered keys = %#v, want exactly pets/shared and avatars/avatar-only", keys)
	}
	if countIssue(snapshot.Issues, IssueDiscoveryConflict) != 1 {
		t.Fatalf("conflict diagnostics = %#v, want one bounded conflict", snapshot.Issues)
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(envHome)) || bytes.Contains(encoded, []byte(configuredHome)) {
		t.Fatalf("snapshot leaked an absolute root: %s", encoded)
	}
}

func TestCatalogUsesUserHomeCodexFallbackWhenEnvironmentIsEmpty(t *testing.T) {
	userHome := t.TempDir()
	codexHome := filepath.Join(userHome, ".codex")
	writeCodexFixture(t, codexHome, SourceCodexAvatars, "fallback", map[string]any{
		"id": "fallback", "displayName": "Home Fallback", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	catalog, err := NewCatalog(CatalogConfig{
		CacheRoot: filepath.Join(t.TempDir(), "cache"),
		HomeDir:   func() (string, error) { return userHome, nil },
		Env:       func(string) string { return "" },
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil || len(snapshot.Items) != 1 || snapshot.Items[0].StableSourceKey != "codex:avatars:fallback" {
		t.Fatalf("home fallback snapshot=%+v err=%v", snapshot, err)
	}
}

func TestCatalogPetsPrecedenceAlsoReservesInvalidSameFolderEntries(t *testing.T) {
	home := t.TempDir()
	writeCodexFixture(t, home, SourceCodexPets, "reserved", map[string]any{
		"id": "reserved", "displayName": "Invalid pet", "spritesheetPath": "../outside.png",
	}, 1536, 1872)
	writeCodexFixture(t, home, SourceCodexAvatars, "reserved", map[string]any{
		"id": "reserved-avatar", "displayName": "Should Not Bypass", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	catalog, err := NewCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache")})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 0 || countIssue(snapshot.Issues, IssueDiscoveryConflict) != 1 || countIssue(snapshot.Issues, IssuePathOutsideRoot) != 1 {
		t.Fatalf("invalid pet precedence snapshot = %+v", snapshot)
	}
}

func TestStableSourceKeyIsBoundedAndPathIndependent(t *testing.T) {
	folder := strings.Repeat("unsafe/identity\\", 100)
	key := StableSourceKey(SourceCodexPets, folder)
	volume := filepath.VolumeName(key)
	if (volume != "" && strings.Contains(key, volume)) || strings.ContainsAny(key, `/\\`) || len([]rune(key)) > len("codex:pets:")+MaxFolderIdentityRunes {
		t.Fatalf("unstable source key = %q", key)
	}
	if strings.Contains(key, "C:") || strings.Contains(key, "\\Users\\") {
		t.Fatalf("source key contains an absolute path: %q", key)
	}
}

func TestStableSourceKeyIdentifiesNativeSourceFormat(t *testing.T) {
	key := StableSourceKey(SourceDshPets, "native")
	if key != "dsh:native-pets:native" {
		t.Fatalf("native stable source key = %q", key)
	}
}

func TestCodexAdapterNormalizesV1V2ProfilesTracksAndCustomAnimations(t *testing.T) {
	root := t.TempDir()
	v1Manifest := map[string]any{
		"id":              "v1-id",
		"displayName":     "Version One",
		"description":     "v1 description",
		"spritesheetPath": "spritesheet.png",
		"frame": map[string]any{
			"width": 192, "height": 208, "columns": 8, "rows": 9,
		},
		"animations": map[string]any{
			"blink": map[string]any{
				"frames":   []int{0, 71},
				"fps":      10,
				"loop":     false,
				"fallback": "idle",
				"aliases":  []string{"blink-now"},
			},
		},
	}
	v1Source := writeCodexFixture(t, root, SourceCodexPets, "v1", v1Manifest, 1536, 1872)
	v1, err := NewCodexAdapter().Load(context.Background(), v1Source)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Source.Format != "codex" || v1.Source.FormatVersion != "1" || v1.Source.Profile != profileCodexV1 {
		t.Fatalf("v1 source = %+v", v1.Source)
	}
	if v1.Identity != (Identity{ID: "v1-id", DisplayName: "Version One", Description: "v1 description"}) {
		t.Fatalf("v1 identity = %+v", v1.Identity)
	}
	if v1.Geometry != (Geometry{CellWidth: 192, CellHeight: 208, Columns: 8, Rows: 9, ImageWidth: 1536, ImageHeight: 1872, FrameCount: 72}) {
		t.Fatalf("v1 geometry = %+v", v1.Geometry)
	}
	assertTrack(t, v1.Tracks["idle"], []int{0, 1, 2, 3, 4, 5}, []int{1680, 660, 660, 840, 840, 1920}, true, "", 0)
	assertTrack(t, v1.Tracks["running-right"], []int{8, 9, 10, 11, 12, 13, 14, 15}, []int{120, 120, 120, 120, 120, 120, 120, 220}, false, "idle", 3)
	if !reflect.DeepEqual(v1.Tracks["running-right"].Aliases, []string{"move_right"}) {
		t.Fatalf("running-right aliases = %#v", v1.Tracks["running-right"].Aliases)
	}
	if v1.Actions["bounce"].Track != "jumping" || v1.Actions["sad"].Track != "failed" {
		t.Fatalf("standard action aliases = %#v", v1.Actions)
	}
	blink := v1.Tracks["blink"]
	if blink.FPS != 10 || blink.Loop || blink.Fallback != "idle" || !reflect.DeepEqual(framesOnly(blink.Frames), []int{0, 71}) || !reflect.DeepEqual(durations(blink.Frames), []int{100, 100}) {
		t.Fatalf("custom animation = %+v", blink)
	}
	if !reflect.DeepEqual(v1.Actions["blink-now"], ActionSpec{Track: "blink", Fallback: "idle", Interruptible: true}) {
		t.Fatalf("custom action alias = %+v", v1.Actions["blink-now"])
	}

	v2Source := writeCodexFixture(t, root, SourceCodexPets, "v2", map[string]any{
		"id": "v2-id", "displayName": "Version Two", "spriteVersionNumber": 2,
		"spritesheetPath": "spritesheet.png",
		"frame":           map[string]any{"width": 192, "height": 208, "columns": 8, "rows": 11},
	}, 1536, 2288)
	v2, err := NewCodexAdapter().Load(context.Background(), v2Source)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Source.FormatVersion != "2" || v2.Source.Profile != profileCodexV2 || v2.Geometry.Rows != 11 || v2.Geometry.FrameCount != 88 {
		t.Fatalf("v2 profile = %+v geometry=%+v", v2.Source, v2.Geometry)
	}
	if v2.Directions == nil || len(v2.Directions.Directions) != 16 || v2.Directions.Directions[0] != (Direction{Angle: 0, FrameIndex: 72}) || v2.Directions.Directions[8] != (Direction{Angle: 180, FrameIndex: 80}) || v2.Directions.Directions[15] != (Direction{Angle: 337.5, FrameIndex: 87}) {
		t.Fatalf("v2 directions = %+v", v2.Directions)
	}
	if v2.Directions.NeutralPolicy != neutralDeadzoneIdle || v2.Directions.DesktopSemanticsVerified {
		t.Fatalf("v2 direction semantics = %+v", v2.Directions)
	}
	if countIssue(v2.Diagnostics, IssueV2SemanticsUnverified) != 1 {
		t.Fatalf("v2 diagnostics = %#v", v2.Diagnostics)
	}

	missingVersion := writeCodexFixture(t, root, SourceCodexPets, "v2-without-version", map[string]any{
		"id": "wrong-version", "displayName": "Wrong Version", "spritesheetPath": "spritesheet.png",
	}, 1536, 2288)
	definition, err := NewCodexAdapter().Load(context.Background(), missingVersion)
	if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
		t.Fatalf("height-inferred v2 was accepted: definition=%+v err=%v", definition, err)
	}
	assertIssueCode(t, err, IssueVersionGeometryMismatch)
}

func TestCodexAvatarCompatibilityPathReportsStandardProfile(t *testing.T) {
	source := writeCodexFixture(t, t.TempDir(), SourceCodexAvatars, "avatar", map[string]any{
		"id": "avatar", "displayName": "Avatar", "description": "Codex avatar compatibility path",
		"spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	definition, err := NewCodexAdapter().Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Identity.ID != "avatar" || definition.Identity.DisplayName != "Avatar" || definition.Source.FormatVersion != "1" || definition.Source.Profile != profileCodexV1 || definition.Source.Badge != BadgeCodex {
		t.Fatalf("avatar identity/source = %+v / %+v", definition.Identity, definition.Source)
	}
	if definition.Geometry.FrameCount != 72 || definition.Geometry.Columns != 8 || definition.Geometry.Rows != 9 {
		t.Fatalf("avatar geometry = %+v", definition.Geometry)
	}
	if len(definition.Tracks) < 1 || definition.Tracks["idle"].Frames[0].Index != 0 || len(definition.Actions) == 0 {
		t.Fatalf("avatar tracks/actions = %#v / %#v", definition.Tracks, definition.Actions)
	}
}

func TestCatalogInspectAndAdapterProbeExposeNormalizedSafeResults(t *testing.T) {
	home := t.TempDir()
	source := writeCodexFixture(t, home, SourceCodexPets, "inspect", map[string]any{
		"id": "inspect", "displayName": "Inspect", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	probe, err := NewCodexAdapter().Probe(context.Background(), source)
	if err != nil || probe.Source.StableKey != "codex:pets:inspect" || probe.Geometry.FrameCount != 72 {
		t.Fatalf("probe=%+v err=%v", probe, err)
	}
	catalog, err := NewCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache")})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := catalog.Inspect(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Item.StableSourceKey != source.StableSourceKey || inspection.Definition.Source.Digest == "" || inspection.Definition.Assets[0].Path != "spritesheet.png" {
		t.Fatalf("inspection=%+v", inspection)
	}
	encoded, err := json.Marshal(inspection)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(source.Root)) || bytes.Contains(encoded, []byte("file:")) {
		t.Fatalf("inspection leaked source path or URL: %s", encoded)
	}
}

func TestCodexRejectsInvalidPathsManifestAnimationAndResourcesWithoutPartialDefinitions(t *testing.T) {
	tests := []struct {
		name     string
		manifest map[string]any
		mutate   func(t *testing.T, source PackageSource)
		wantCode string
	}{
		{
			name: "remote-resource",
			manifest: map[string]any{
				"id": "remote", "displayName": "Remote", "spritesheetPath": "https://example.invalid/pet.webp",
			},
			wantCode: IssueRemoteResource,
		},
		{
			name: "parent-path",
			manifest: map[string]any{
				"id": "parent", "displayName": "Parent", "spritesheetPath": "../outside.png",
			},
			wantCode: IssuePathOutsideRoot,
		},
		{
			name: "absolute-path",
			manifest: map[string]any{
				"id": "absolute", "displayName": "Absolute", "spritesheetPath": filepath.Join(t.TempDir(), "outside.png"),
			},
			wantCode: IssuePathOutsideRoot,
		},
		{
			name: "invalid-frame",
			manifest: map[string]any{
				"id": "frame", "displayName": "Frame", "spritesheetPath": "spritesheet.png",
				"frame": map[string]any{"width": 191, "height": 208, "columns": 8, "rows": 9},
			},
			wantCode: IssueVersionGeometryMismatch,
		},
		{
			name: "invalid-fps",
			manifest: map[string]any{
				"id": "fps", "displayName": "FPS", "spritesheetPath": "spritesheet.png",
				"animations": map[string]any{"bad": map[string]any{"frames": []int{0}, "fps": 0}},
			},
			wantCode: IssueInvalidAnimation,
		},
		{
			name: "missing-fallback",
			manifest: map[string]any{
				"id": "fallback", "displayName": "Fallback", "spritesheetPath": "spritesheet.png",
				"animations": map[string]any{"bad": map[string]any{"frames": []int{0}, "fallback": "missing"}},
			},
			wantCode: IssueInvalidAnimation,
		},
		{
			name: "missing-spritesheet",
			manifest: map[string]any{
				"id": "missing", "displayName": "Missing", "spritesheetPath": "not-present.png",
			},
			mutate: func(t *testing.T, source PackageSource) {
				if err := os.Remove(filepath.Join(source.Root, "not-present.png")); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: IssueInvalidSpritesheet,
		},
		{
			name: "malformed-json",
			manifest: map[string]any{
				"id": "malformed", "displayName": "Malformed", "spritesheetPath": "spritesheet.png",
			},
			mutate: func(t *testing.T, source PackageSource) {
				if err := os.WriteFile(filepath.Join(source.Root, source.ManifestPath), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: IssueManifestInvalid,
		},
		{
			name: "null-manifest",
			manifest: map[string]any{
				"id": "null", "displayName": "Null", "spritesheetPath": "spritesheet.png",
			},
			mutate: func(t *testing.T, source PackageSource) {
				if err := os.WriteFile(filepath.Join(source.Root, source.ManifestPath), []byte("null"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: IssueManifestInvalid,
		},
		{
			name: "script-resource",
			manifest: map[string]any{
				"id": "script", "displayName": "Script", "spritesheetPath": "spritesheet.png",
			},
			mutate: func(t *testing.T, source PackageSource) {
				if err := os.WriteFile(filepath.Join(source.Root, "evil.js"), []byte("console.log('no');"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: IssueUnsafeResource,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := cloneMap(test.manifest)
			source := writeCodexFixture(t, t.TempDir(), SourceCodexPets, test.name, manifest, 1536, 1872)
			if test.mutate != nil {
				test.mutate(t, source)
			}
			definition, err := NewCodexAdapter().Load(context.Background(), source)
			if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
				t.Fatalf("invalid package returned definition=%+v err=%v", definition, err)
			}
			assertIssueCode(t, err, test.wantCode)
		})
	}
}

func TestCodexRejectsSymlinkEscapes(t *testing.T) {
	root := t.TempDir()
	source := writeCodexFixture(t, root, SourceCodexPets, "symlink", map[string]any{
		"id": "symlink", "displayName": "Symlink", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(source.Root, "outside.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	definition, err := NewCodexAdapter().Load(context.Background(), source)
	if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
		t.Fatalf("symlink package returned definition=%+v err=%v", definition, err)
	}
	assertIssueCode(t, err, IssueSymlinkEscape)
}

func TestCodexEnforcesManifestPackageImagePixelAndAnimationBudgets(t *testing.T) {
	t.Run("manifest-size", func(t *testing.T) {
		source := writeCodexFixture(t, t.TempDir(), SourceCodexPets, "manifest-size", map[string]any{
			"id": "manifest-size", "displayName": "Manifest Size", "spritesheetPath": "spritesheet.png",
		}, 1536, 1872)
		data := bytes.Repeat([]byte("x"), int(MaxManifestBytes)+1)
		if err := os.WriteFile(filepath.Join(source.Root, source.ManifestPath), data, 0o600); err != nil {
			t.Fatal(err)
		}
		definition, err := NewCodexAdapter().Load(context.Background(), source)
		assertZeroDefinitionIssue(t, definition, err, IssueManifestTooLarge)
	})

	t.Run("package-size", func(t *testing.T) {
		source := writeCodexFixture(t, t.TempDir(), SourceCodexPets, "package-size", map[string]any{
			"id": "package-size", "displayName": "Package Size", "spritesheetPath": "spritesheet.png",
		}, 1536, 1872)
		file, err := os.Create(filepath.Join(source.Root, "large.dat"))
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Truncate(MaxPackageBytes + 1); err != nil {
			_ = file.Close()
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		definition, err := NewCodexAdapter().Load(context.Background(), source)
		assertZeroDefinitionIssue(t, definition, err, IssuePackageTooLarge)
	})

	t.Run("file-count", func(t *testing.T) {
		source := writeCodexFixture(t, t.TempDir(), SourceCodexPets, "file-count", map[string]any{
			"id": "file-count", "displayName": "File Count", "spritesheetPath": "spritesheet.png",
		}, 1536, 1872)
		for index := 0; index < MaxPackageFiles; index++ {
			name := filepath.Join(source.Root, "resource-"+strconv.Itoa(index)+".dat")
			if err := os.WriteFile(name, []byte("data"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		definition, err := NewCodexAdapter().Load(context.Background(), source)
		assertZeroDefinitionIssue(t, definition, err, IssueFileCountExceeded)
	})

	t.Run("decoded-image-dimensions", func(t *testing.T) {
		source := writeCodexFixture(t, t.TempDir(), SourceCodexPets, "image-size", map[string]any{
			"id": "image-size", "displayName": "Image Size", "spritesheetPath": "spritesheet.png",
		}, MaxImageWidth+1, 1)
		definition, err := NewCodexAdapter().Load(context.Background(), source)
		assertZeroDefinitionIssue(t, definition, err, IssueDecodedImageTooLarge)
	})

	t.Run("decoded-pixels", func(t *testing.T) {
		source := NewCodexPackageSource(t.TempDir(), SourceCodexPets, "pixels")
		inventory := packageInventory{
			Source:  source,
			Ordered: []string{"spritesheet.png", "other.png"},
			Files: map[string]packageFile{
				"other.png": {RelativePath: "other.png", Data: pngData(1, 1)},
			},
		}
		err := validateAllImages(inventory, "spritesheet.png", MaxDecodedPixels)
		assertIssueCode(t, err, IssueDecodedPixelsExceeded)
	})

	t.Run("non-loop-duration", func(t *testing.T) {
		frames := make([]int, 13)
		source := writeCodexFixture(t, t.TempDir(), SourceCodexPets, "duration", map[string]any{
			"id": "duration", "displayName": "Duration", "spritesheetPath": "spritesheet.png",
			"animations": map[string]any{"slow": map[string]any{"frames": frames, "fps": 0.1, "loop": false}},
		}, 1536, 1872)
		definition, err := NewCodexAdapter().Load(context.Background(), source)
		assertZeroDefinitionIssue(t, definition, err, IssueInvalidAnimation)
	})
}

func TestCatalogRefreshPublishesRevisionedStaleSnapshotsAndRetainsItems(t *testing.T) {
	validHome := t.TempDir()
	writeCodexFixture(t, validHome, SourceCodexAvatars, "stable", map[string]any{
		"id": "stable", "displayName": "Stable", "spritesheetPath": "spritesheet.png",
	}, 1536, 1872)
	currentHome := validHome
	catalog, err := NewCatalog(CatalogConfig{
		CacheRoot: filepath.Join(t.TempDir(), "cache"),
		Env: func(name string) string {
			if name == "CODEX_HOME" {
				return currentHome
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ready, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ready.Revision != 1 || ready.ScanState != ScanReady || ready.Stale || len(ready.Items) != 1 {
		t.Fatalf("initial snapshot = %+v", ready)
	}

	currentHome = filepath.Join(t.TempDir(), "missing-codex-home")
	failed, err := catalog.Refresh(context.Background())
	if err == nil {
		t.Fatal("failed refresh returned nil error")
	}
	assertIssueCode(t, err, IssueHomeUnavailable)
	if failed.Revision != 1 || failed.ScanState != ScanFailed || !failed.Stale || !reflect.DeepEqual(failed.Items, ready.Items) {
		t.Fatalf("stale failure snapshot = %+v, previous=%+v", failed, ready)
	}

	firstFailureCatalog, err := NewCatalog(CatalogConfig{
		CacheRoot: filepath.Join(t.TempDir(), "cache"),
		Env: func(name string) string {
			if name == "CODEX_HOME" {
				return filepath.Join(t.TempDir(), "never-there")
			}
			return ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	firstFailure, err := firstFailureCatalog.Refresh(context.Background())
	if err == nil || firstFailure.Revision != 0 || firstFailure.ScanState != ScanFailed || firstFailure.Stale || len(firstFailure.Items) != 0 {
		t.Fatalf("first failure snapshot=%+v err=%v", firstFailure, err)
	}
}

func TestCatalogAndDefinitionsAreDefensiveCopies(t *testing.T) {
	home := t.TempDir()
	source := writeCodexFixture(t, home, SourceCodexPets, "copy", map[string]any{
		"id": "copy", "displayName": "Copy", "spriteVersionNumber": 2, "spritesheetPath": "spritesheet.png",
	}, 1536, 2288)
	catalog, err := NewCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache")})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := catalog.Refresh(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Items) != 1 || len(snapshot.Issues) == 0 {
		t.Fatalf("copy fixture snapshot = %+v", snapshot)
	}
	key := snapshot.Items[0].StableSourceKey
	snapshot.Items[0].Capabilities[0] = "mutated"
	snapshot.Issues[0].Args["folder"] = "mutated"
	if snapshot.ScannedAt != nil {
		snapshot.ScannedAt = nil
	}
	again, err := catalog.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if again.Items[0].Capabilities[0] == "mutated" || again.Issues[0].Args["folder"] == "mutated" || again.ScannedAt == nil {
		t.Fatalf("snapshot was not defensive: %+v", again)
	}

	definition, err := catalog.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	definition.Tracks["idle"] = TrackSpec{}
	definition.Assets[0].Data[0] ^= 0xff
	resolvedAgain, err := catalog.Resolve(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolvedAgain.Tracks["idle"].Frames) == 0 || resolvedAgain.Assets[0].Data[0] == definition.Assets[0].Data[0] {
		t.Fatalf("definition was not defensive: first=%+v second=%+v", definition, resolvedAgain)
	}

	_ = source
}

func TestCatalogRefreshCoalescesConcurrentScansAndCancellationPublishesNothing(t *testing.T) {
	home := t.TempDir()
	newBarePackage(t, home, "blocking")
	adapter := &blockingPetAdapter{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	catalog, err := NewCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache"), Adapter: adapter})
	if err != nil {
		t.Fatal(err)
	}
	type refreshResult struct {
		snapshot CatalogSnapshot
		err      error
	}
	firstDone := make(chan refreshResult, 1)
	go func() {
		snapshot, err := catalog.Refresh(context.Background())
		firstDone <- refreshResult{snapshot: snapshot, err: err}
	}()
	select {
	case <-adapter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach adapter")
	}
	secondEntered := make(chan struct{})
	secondDone := make(chan refreshResult, 1)
	go func() {
		close(secondEntered)
		snapshot, err := catalog.Refresh(context.Background())
		secondDone <- refreshResult{snapshot: snapshot, err: err}
	}()
	<-secondEntered
	select {
	case <-secondDone:
		t.Fatal("coalesced refresh completed before the scan was released")
	case <-time.After(50 * time.Millisecond):
	}
	if got := adapter.loads.Load(); got != 1 {
		t.Fatalf("adapter Load calls before release = %d, want one", got)
	}
	close(adapter.release)
	first := <-firstDone
	second := <-secondDone
	if first.err != nil || second.err != nil || first.snapshot.Revision != 1 || second.snapshot.Revision != 1 {
		t.Fatalf("coalesced results = %#v / %#v", first, second)
	}
	if got := adapter.loads.Load(); got != 1 {
		t.Fatalf("adapter Load calls after coalescing = %d, want one", got)
	}

	cancelAdapter := &blockingPetAdapter{started: make(chan struct{}), release: make(chan struct{})}
	cancelCatalog, err := NewCatalog(CatalogConfig{CodexHome: home, CacheRoot: filepath.Join(t.TempDir(), "cache"), Adapter: cancelAdapter})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelDone := make(chan refreshResult, 1)
	go func() {
		snapshot, err := cancelCatalog.Refresh(ctx)
		cancelDone <- refreshResult{snapshot: snapshot, err: err}
	}()
	select {
	case <-cancelAdapter.started:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellable refresh did not reach adapter")
	}
	cancel()
	select {
	case result := <-cancelDone:
		if !errors.Is(result.err, context.Canceled) {
			t.Fatalf("cancelled refresh error = %v", result.err)
		}
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		for {
			published, snapshotErr := cancelCatalog.Snapshot(context.Background())
			if snapshotErr != nil {
				t.Fatal(snapshotErr)
			}
			if published.ScanState != ScanScanning {
				if published.Revision != 0 || published.ScanState != ScanNeverScanned || len(published.Items) != 0 {
					t.Fatalf("cancelled refresh published data: result=%#v snapshot=%+v", result, published)
				}
				break
			}
			select {
			case <-deadline.C:
				t.Fatal("cancelled refresh did not restore the prior snapshot")
			case <-time.After(10 * time.Millisecond):
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancellable refresh did not finish")
	}
}

type blockingPetAdapter struct {
	started  chan struct{}
	release  chan struct{}
	startOne sync.Once
	loads    atomic.Int32
}

func (a *blockingPetAdapter) Probe(context.Context, PackageSource) (ProbeResult, error) {
	return ProbeResult{}, nil
}

func (a *blockingPetAdapter) Load(ctx context.Context, source PackageSource) (PetDefinition, error) {
	a.loads.Add(1)
	a.startOne.Do(func() { close(a.started) })
	select {
	case <-a.release:
		return PetDefinition{
			Identity: Identity{ID: "fake", DisplayName: "Fake"},
			Source: SourceInfo{
				Format: "fake", FormatVersion: "1", Profile: "fake",
				Entry: source.ManifestPath, StableKey: source.StableSourceKey, Badge: BadgeCodex,
			},
			Geometry: Geometry{CellWidth: 1, CellHeight: 1, Columns: 1, Rows: 1, ImageWidth: 1, ImageHeight: 1, FrameCount: 1},
			Tracks:   map[string]TrackSpec{"idle": {ID: "idle", Frames: []FrameRef{{Index: 0, DurationMS: 1000}}, Loop: true}},
		}, nil
	case <-ctx.Done():
		return PetDefinition{}, ctx.Err()
	}
}

func writeCodexFixture(t *testing.T, home string, kind SourceKind, folder string, manifest map[string]any, width, height int) PackageSource {
	t.Helper()
	rootName := "pets"
	manifestName := "pet.json"
	if kind == SourceCodexAvatars {
		rootName = "avatars"
		manifestName = "avatar.json"
	}
	packageRoot := filepath.Join(home, rootName, folder)
	if err := os.MkdirAll(packageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(packageRoot, manifestName), manifest)
	spritePath := "spritesheet.webp"
	if value, ok := manifest["spritesheetPath"].(string); ok && value != "" {
		spritePath = value
	}
	if isLocalFixturePath(spritePath) {
		if err := os.WriteFile(filepath.Join(packageRoot, filepath.FromSlash(spritePath)), pngData(width, height), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewCodexPackageSource(packageRoot, kind, folder)
}

func newBarePackage(t *testing.T, home, folder string) PackageSource {
	t.Helper()
	packageRoot := filepath.Join(home, "pets", folder)
	if err := os.MkdirAll(packageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSONFile(t, filepath.Join(packageRoot, "pet.json"), map[string]any{"id": folder, "displayName": folder})
	if err := os.WriteFile(filepath.Join(packageRoot, "resource.dat"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewCodexPackageSource(packageRoot, SourceCodexPets, folder)
}

func writeJSONFile(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func pngData(width, height int) []byte {
	buffer := new(bytes.Buffer)
	if err := png.Encode(buffer, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}

func isLocalFixturePath(path string) bool {
	return path != "" && !strings.Contains(path, "://") && !strings.HasPrefix(strings.ToLower(path), "data:") && !strings.HasPrefix(strings.ToLower(path), "file:") && !filepath.IsAbs(path) && !strings.Contains(path, "..")
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func framesOnly(frames []FrameRef) []int {
	indexes := make([]int, len(frames))
	for index, frame := range frames {
		indexes[index] = frame.Index
	}
	return indexes
}

func durations(frames []FrameRef) []int {
	values := make([]int, len(frames))
	for index, frame := range frames {
		values[index] = frame.DurationMS
	}
	return values
}

func assertTrack(t *testing.T, track TrackSpec, indexes, wantDurations []int, loop bool, fallback string, repeatCount int) {
	t.Helper()
	if !reflect.DeepEqual(framesOnly(track.Frames), indexes) || !reflect.DeepEqual(durations(track.Frames), wantDurations) || track.Loop != loop || track.Fallback != fallback || track.RepeatCount != repeatCount {
		t.Fatalf("track = %+v", track)
	}
}

func assertZeroDefinitionIssue(t *testing.T, definition PetDefinition, err error, wantCode string) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
		t.Fatalf("definition=%+v err=%v, want zero definition and %s", definition, err, wantCode)
	}
	assertIssueCode(t, err, wantCode)
}

func assertIssueCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	for _, issue := range Diagnostics(err) {
		if issue.Code == wantCode {
			return
		}
	}
	t.Fatalf("error=%v diagnostics=%#v, want code %s", err, Diagnostics(err), wantCode)
}

func countIssue(issues []Issue, code string) int {
	count := 0
	for _, issue := range issues {
		if issue.Code == code {
			count++
		}
	}
	return count
}
