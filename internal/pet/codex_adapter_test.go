package pet

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var _ PetAdapter = NewCodexAdapter()

func TestCodexAdapterLoadsV1Package(t *testing.T) {
	root := t.TempDir()
	manifest := map[string]any{
		"id":              "test.pet",
		"displayName":     "Test Pet",
		"description":     "A fixture pet.",
		"spritesheetPath": "spritesheet.png",
	}
	writeJSON(t, filepath.Join(root, "pet.json"), manifest)
	writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 1872)

	adapter := NewCodexAdapter()
	profile, err := adapter.Probe(context.Background(), root)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	if profile.Format != SourceFormatCodex || profile.Version != 1 || profile.Legacy {
		t.Fatalf("unexpected source profile: %+v", profile)
	}

	definition, err := adapter.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if definition.Identity.ID != "test.pet" || definition.Identity.DisplayName != "Test Pet" {
		t.Fatalf("unexpected identity: %+v", definition.Identity)
	}
	if definition.Source.Version != 1 || definition.Canvas.Width != 192 || definition.Canvas.Height != 208 {
		t.Fatalf("unexpected normalized source/canvas: %+v %+v", definition.Source, definition.Canvas)
	}
	if len(definition.Tracks) != 9 {
		t.Fatalf("track count = %d, want 9", len(definition.Tracks))
	}
	running, ok := definition.Track("running")
	if !ok {
		t.Fatal("normalized definition is missing running track")
	}
	if len(running.Frames) != 6 || running.FPS != 0 || running.Loop || running.Fallback != "idle" || running.RepeatCount != 3 {
		t.Fatalf("unexpected running track: %+v", running)
	}
	if got := definition.Aliases["move_right"]; got != "running-right" {
		t.Fatalf("move_right alias = %q, want running-right", got)
	}
	if got := running.Frames[0].DurationMS; got != 120 {
		t.Fatalf("running first frame duration = %d, want 120ms", got)
	}
}

func TestCodexLoadReturnsDefaultDeniedPermissions(t *testing.T) {
	definition := loadCodexDefinition(t, 1, 1872, nil)
	if definition.Permissions == nil {
		t.Fatal("Codex definition permissions must be a non-nil default-deny set")
	}
	if len(definition.Permissions) != 0 {
		t.Fatalf("Codex definition permissions = %#v, want empty default-deny set", definition.Permissions)
	}
	encoded, err := json.Marshal(definition)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	if got := string(object["permissions"]); got != "{}" {
		t.Fatalf("JSON permissions = %s, want {}", got)
	}
}

func TestCodexAdapterRequiresExplicitLegacyProfile(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "avatar.json"), map[string]any{
		"id":              "legacy.pet",
		"displayName":     "Legacy Pet",
		"spritesheetPath": "spritesheet.png",
	})
	writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 1872)

	if _, err := NewCodexAdapter().Load(context.Background(), root); err == nil {
		t.Fatal("Load() accepted legacy avatar without an explicit profile")
	} else {
		assertDiagnosticCode(t, err, CodeLegacyProfileDisabled)
	}

	definition, err := NewCodexAdapter(WithLegacyAvatars(true)).Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() with explicit legacy profile error = %v", err)
	}
	if !definition.Source.Legacy || definition.Source.ProfileID != "codex-legacy-v1" || definition.Source.Entry != "avatar.json" {
		t.Fatalf("unexpected legacy source profile: %+v", definition.Source)
	}
}

func TestCodexAdapterLoadsWebPPackage(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
		"id":              "webp.pet",
		"displayName":     "WebP Pet",
		"spritesheetPath": "spritesheet.webp",
	})
	writeWebP(t, filepath.Join(root, "spritesheet.webp"))

	definition, err := NewCodexAdapter().Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() WebP error = %v", err)
	}
	if len(definition.Assets) != 1 || definition.Assets[0].Format != "webp" {
		t.Fatalf("unexpected WebP asset: %+v", definition.Assets)
	}
}

func TestCodexRejectsSpritesheetWithoutTransparency(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
		"id":              "opaque.pet",
		"displayName":     "Opaque Pet",
		"spritesheetPath": "spritesheet.png",
	})
	writeOpaquePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 1872)

	_, err := NewCodexAdapter().Load(context.Background(), root)
	if err == nil {
		t.Fatal("Load() accepted a spritesheet without a transparent pixel")
	}
	diagnostic := assertDiagnosticCode(t, err, CodeInvalidSpritesheet)
	if diagnostic.CorrelationID == "" || strings.Contains(diagnostic.Summary, root) {
		t.Fatalf("unsafe opaque-spritesheet diagnostic: %+v", diagnostic)
	}
}

func TestCodexLoadRejectsDirectorySpritesheet(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
		"id":              "directory-spritesheet.pet",
		"displayName":     "Directory Spritesheet Pet",
		"spritesheetPath": "spritesheet.png",
	})
	if err := os.Mkdir(filepath.Join(root, "spritesheet.png"), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := NewCodexAdapter().Load(context.Background(), root)
	if err == nil {
		t.Fatal("Load() accepted a directory as spritesheet")
	}
	diagnostic := assertDiagnosticCode(t, err, CodePackageUnreadable)
	if diagnostic.CorrelationID == "" {
		t.Fatalf("directory spritesheet diagnostic is missing correlation ID: %+v", diagnostic)
	}
}

func TestSourceProfileKeepsCodexGeometryPrivate(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
		"id":              "profile.pet",
		"displayName":     "Profile Pet",
		"spritesheetPath": "spritesheet.png",
	})
	writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 1872)

	profile, err := NewCodexAdapter().Probe(context.Background(), root)
	if err != nil {
		t.Fatalf("Probe() error = %v", err)
	}
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "geometry") || strings.Contains(string(encoded), "formatVersion") {
		t.Fatalf("source profile exposes source geometry/version duplicates: %s", encoded)
	}
}

func TestDiagnosticCarriesOpaqueCorrelationID(t *testing.T) {
	root := t.TempDir()
	manifest := `{"id":"diagnostic.pet","displayName":"Diagnostic Pet","spritesheetPath":"secret/path.png","private":"secret-value"}`
	if err := os.WriteFile(filepath.Join(root, "pet.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := NewCodexAdapter().Load(context.Background(), root)
	if err == nil {
		t.Fatal("Load() accepted a manifest with an unavailable spritesheet")
	}
	diagnostic := assertDiagnosticCode(t, err, CodeInvalidSpritesheet)
	if diagnostic.CorrelationID == "" {
		t.Fatal("diagnostic correlation ID is empty")
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "secret-value") || strings.Contains(diagnostic.Summary, root) {
		t.Fatalf("diagnostic leaked package details: %v / %+v", err, diagnostic)
	}
}

func TestCodexAdapterLoadsV2DirectionalProfile(t *testing.T) {
	root := t.TempDir()
	writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
		"id":                  "v2.pet",
		"displayName":         "V2 Pet",
		"spriteVersionNumber": 2,
		"spritesheetPath":     "spritesheet.png",
	})
	writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 2288)

	definition, err := NewCodexAdapter().Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() v2 error = %v", err)
	}
	if definition.Source.ProfileID != "codex-v2" || definition.Source.Version != 2 {
		t.Fatalf("unexpected v2 source profile: %+v", definition.Source)
	}
	if definition.Look.Mode != LookModeDirectional16 || definition.Look.NeutralPolicy != NeutralPolicyDeadzoneToIdle || len(definition.Look.Directions) != 16 {
		t.Fatalf("unexpected v2 look profile: %+v", definition.Look)
	}
	if first, last := definition.Look.Directions[0], definition.Look.Directions[15]; first.Degrees != 0 || last.Degrees != 337.5 || first.Frame.Region.Y != 9*208 || last.Frame.Region.Y != 10*208 {
		t.Fatalf("unexpected v2 direction mapping: first=%+v last=%+v", first, last)
	}
	hasDirectionalCapability := false
	for _, capability := range definition.Capabilities {
		if capability == CapabilityLookDirectional16 {
			hasDirectionalCapability = true
			break
		}
	}
	if !hasDirectionalCapability {
		t.Fatalf("v2 capabilities = %v, missing %q", definition.Capabilities, CapabilityLookDirectional16)
	}
	hasWarning := false
	for _, diagnostic := range definition.Diagnostics {
		if diagnostic.Code == CodeV2SemanticsUnverified && diagnostic.CorrelationID != "" {
			hasWarning = true
			break
		}
	}
	if !hasWarning {
		t.Fatalf("v2 diagnostics = %+v, missing warning", definition.Diagnostics)
	}
}

func TestCodexAdapterRejectsV2VersionGeometryMismatches(t *testing.T) {
	cases := []struct {
		name    string
		version int
		width   int
		height  int
	}{
		{name: "v2 manifest with v1 atlas", version: 2, width: 1536, height: 1872},
		{name: "v1 manifest with v2 atlas", version: 1, width: 1536, height: 2288},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			manifest := map[string]any{
				"id":              "mismatch.pet",
				"displayName":     "Mismatch Pet",
				"spritesheetPath": "spritesheet.png",
			}
			if testCase.version == 2 {
				manifest["spriteVersionNumber"] = 2
			}
			writeJSON(t, filepath.Join(root, "pet.json"), manifest)
			writePNG(t, filepath.Join(root, "spritesheet.png"), testCase.width, testCase.height)
			_, err := NewCodexAdapter().Load(context.Background(), root)
			if err == nil {
				t.Fatal("Load() accepted a version/geometry mismatch")
			}
			assertDiagnosticCode(t, err, CodeVersionGeometryMismatch)
		})
	}
}

func TestCodexDefaultTracksUseConfirmedTimingForBothVersions(t *testing.T) {
	expectedTracks := []struct {
		id        string
		durations []int
		loop      bool
		repeat    int
		fallback  string
	}{
		{id: "idle", durations: []int{1680, 660, 660, 840, 840, 1920}, loop: true, repeat: 1, fallback: "idle"},
		{id: "running-right", durations: []int{120, 120, 120, 120, 120, 120, 120, 220}, repeat: 3, fallback: "idle"},
		{id: "running-left", durations: []int{120, 120, 120, 120, 120, 120, 120, 220}, repeat: 3, fallback: "idle"},
		{id: "waving", durations: []int{140, 140, 140, 280}, repeat: 3, fallback: "idle"},
		{id: "jumping", durations: []int{140, 140, 140, 140, 280}, repeat: 3, fallback: "idle"},
		{id: "failed", durations: []int{140, 140, 140, 140, 140, 140, 140, 240}, repeat: 3, fallback: "idle"},
		{id: "waiting", durations: []int{150, 150, 150, 150, 150, 260}, repeat: 3, fallback: "idle"},
		{id: "running", durations: []int{120, 120, 120, 120, 120, 220}, repeat: 3, fallback: "idle"},
		{id: "review", durations: []int{150, 150, 150, 150, 150, 280}, repeat: 3, fallback: "idle"},
	}
	versions := []struct {
		name   string
		value  int
		height int
	}{
		{name: "v1", value: 1, height: 1872},
		{name: "v2", value: 2, height: 2288},
	}
	for _, version := range versions {
		t.Run(version.name, func(t *testing.T) {
			definition := loadCodexDefinition(t, version.value, version.height, nil)
			for _, want := range expectedTracks {
				track, ok := definition.Track(want.id)
				if !ok {
					t.Fatalf("missing track %q", want.id)
				}
				if len(track.Frames) != len(want.durations) {
					t.Errorf("%s frame count = %d, want %d", want.id, len(track.Frames), len(want.durations))
					continue
				}
				for index, duration := range want.durations {
					if track.Frames[index].DurationMS != duration {
						t.Errorf("%s frame %d duration = %d, want %d", want.id, index, track.Frames[index].DurationMS, duration)
					}
				}
				if track.Loop != want.loop || track.RepeatCount != want.repeat || track.Fallback != want.fallback {
					t.Errorf("%s timing metadata = loop %t repeat %d fallback %q, want loop %t repeat %d fallback %q", want.id, track.Loop, track.RepeatCount, track.Fallback, want.loop, want.repeat, want.fallback)
				}
			}
			aliases := map[string]string{
				"move_right": "running-right",
				"move_left":  "running-left",
				"wave":       "waving",
				"bounce":     "jumping",
				"sad":        "failed",
			}
			if len(definition.Aliases) != len(aliases) {
				t.Errorf("alias count = %d, want %d", len(definition.Aliases), len(aliases))
			}
			for alias, target := range aliases {
				if definition.Aliases[alias] != target {
					t.Errorf("alias %q = %q, want %q", alias, definition.Aliases[alias], target)
				}
			}
		})
	}
}

func TestCodexCustomAnimationsNormalizeThroughLoad(t *testing.T) {
	validCases := []struct {
		name       string
		animation  map[string]any
		wantFPS    float64
		wantLoop   bool
		wantFallbk string
		wantTiming int
	}{
		{
			name:       "defaults",
			animation:  map[string]any{"frames": []int{0, 1}},
			wantFPS:    8,
			wantLoop:   true,
			wantFallbk: "idle",
			wantTiming: 125,
		},
		{
			name:       "explicit values and non looping fallback",
			animation:  map[string]any{"frames": []int{0, 1}, "fps": 20, "loop": false, "fallback": "waiting"},
			wantFPS:    20,
			wantLoop:   false,
			wantFallbk: "waiting",
			wantTiming: 50,
		},
	}
	for _, testCase := range validCases {
		t.Run(testCase.name, func(t *testing.T) {
			definition := loadCodexDefinition(t, 1, 1872, map[string]any{"custom": testCase.animation})
			track, ok := definition.Track("custom")
			if !ok {
				t.Fatal("Load() did not return the custom animation track")
			}
			if track.FPS != testCase.wantFPS || track.Loop != testCase.wantLoop || track.Fallback != testCase.wantFallbk {
				t.Fatalf("custom track metadata = fps %v loop %t fallback %q, want fps %v loop %t fallback %q", track.FPS, track.Loop, track.Fallback, testCase.wantFPS, testCase.wantLoop, testCase.wantFallbk)
			}
			if len(track.Frames) != 2 || track.Frames[0].DurationMS != testCase.wantTiming || track.Frames[1].DurationMS != testCase.wantTiming {
				t.Fatalf("custom track frames = %+v, want two frames at %dms", track.Frames, testCase.wantTiming)
			}
		})
	}

	invalidCases := []struct {
		name      string
		animation map[string]any
	}{
		{name: "empty frames", animation: map[string]any{"frames": []int{}}},
		{name: "negative frame", animation: map[string]any{"frames": []int{-1}}},
		{name: "frame at total grid size", animation: map[string]any{"frames": []int{72}}},
		{name: "zero fps", animation: map[string]any{"frames": []int{0}, "fps": 0}},
		{name: "fps above maximum", animation: map[string]any{"frames": []int{0}, "fps": 61}},
		{name: "empty fallback", animation: map[string]any{"frames": []int{0}, "fallback": ""}},
		{name: "missing fallback", animation: map[string]any{"frames": []int{0}, "fallback": "missing"}},
	}
	for _, testCase := range invalidCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
				"id":              "invalid-animation.pet",
				"displayName":     "Invalid Animation Pet",
				"spritesheetPath": "spritesheet.png",
				"animations":      map[string]any{"custom": testCase.animation},
			})
			writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 1872)
			_, err := NewCodexAdapter().Load(context.Background(), root)
			if err == nil {
				t.Fatal("Load() accepted an invalid custom animation")
			}
			diagnostic := assertDiagnosticCode(t, err, CodeInvalidAnimation)
			if diagnostic.CorrelationID == "" || strings.Contains(diagnostic.Summary, root) {
				t.Fatalf("unsafe invalid-animation diagnostic: %+v", diagnostic)
			}
		})
	}
}

func TestCodexCustomTrackFallbackPrefersRealTrackName(t *testing.T) {
	definition := loadCodexDefinition(t, 1, 1872, map[string]any{
		"wave": map[string]any{"frames": []int{0}},
		"custom": map[string]any{
			"frames":   []int{1},
			"loop":     false,
			"fallback": "wave",
		},
	})
	if _, ok := definition.Track("wave"); !ok {
		t.Fatal("Load() did not preserve the custom wave track")
	}
	track, ok := definition.Track("custom")
	if !ok {
		t.Fatal("Load() did not return the custom track")
	}
	if track.Fallback != "wave" {
		t.Fatalf("custom fallback = %q, want real custom track wave", track.Fallback)
	}
}

func TestCodexIOErrorsUseStableClassification(t *testing.T) {
	t.Run("missing explicit manifest", func(t *testing.T) {
		root := t.TempDir()
		_, err := NewCodexAdapter().Probe(context.Background(), filepath.Join(root, "pet.json"))
		if err == nil {
			t.Fatal("Probe() accepted a missing explicit manifest")
		}
		assertDiagnosticCode(t, err, CodeManifestMissing)
	})

	t.Run("missing spritesheet", func(t *testing.T) {
		root := t.TempDir()
		writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
			"id":              "missing-resource.pet",
			"displayName":     "Missing Resource Pet",
			"spritesheetPath": "missing.png",
		})
		_, err := NewCodexAdapter().Load(context.Background(), root)
		if err == nil {
			t.Fatal("Load() accepted a missing spritesheet")
		}
		assertDiagnosticCode(t, err, CodeInvalidSpritesheet)
	})

	t.Run("other manifest stat error", func(t *testing.T) {
		_, err := NewCodexAdapter().Probe(context.Background(), string([]byte{0}))
		if err == nil {
			t.Fatal("Probe() accepted an invalid package path")
		}
		assertDiagnosticCode(t, err, CodePackageUnreadable)
	})
}

func TestCodexLoadRejectsNonRegularPetManifest(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, manifestName)
	if err := os.Mkdir(manifestPath, 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := NewCodexAdapter().Load(context.Background(), root)
	if err == nil {
		t.Fatal("Load() accepted a directory as pet.json")
	}
	diagnostic := assertDiagnosticCode(t, err, CodePackageUnreadable)
	if diagnostic.CorrelationID == "" {
		t.Fatalf("directory diagnostic is missing correlation ID: %+v", diagnostic)
	}
}

func TestCodexAdapterHonorsPreCanceledContext(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, operation := range []struct {
		name string
		call func(context.Context) error
	}{
		{name: "Probe", call: func(ctx context.Context) error { _, err := NewCodexAdapter().Probe(ctx, root); return err }},
		{name: "Load", call: func(ctx context.Context) error { _, err := NewCodexAdapter().Load(ctx, root); return err }},
	} {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call(ctx)
			if err == nil {
				t.Fatal("operation accepted a canceled context")
			}
			diagnostic := assertDiagnosticCode(t, err, CodeLoadCanceled)
			if diagnostic.CorrelationID == "" || strings.Contains(diagnostic.Summary, root) {
				t.Fatalf("unsafe cancellation diagnostic: %+v", diagnostic)
			}
		})
	}
}

func TestCodexCapabilitiesDeriveFromFinalTracks(t *testing.T) {
	cases := []struct {
		name            string
		version         int
		animations      map[string]any
		wantDirectional bool
		wantOneShot     bool
	}{
		{name: "v1 non looping actions", version: 1, wantOneShot: true},
		{name: "v2 non looping actions", version: 2, wantDirectional: true, wantOneShot: true},
		{
			name:    "no non looping action after normalization",
			version: 1,
			animations: map[string]any{
				"waving":  map[string]any{"frames": []int{0}, "loop": true},
				"jumping": map[string]any{"frames": []int{0}, "loop": true},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			definition := loadCodexDefinition(t, testCase.version, map[int]int{1: 1872, 2: 2288}[testCase.version], testCase.animations)
			directionalCount := 0
			oneShotCount := 0
			for _, capability := range definition.Capabilities {
				switch capability {
				case CapabilityLookDirectional16:
					directionalCount++
				case CapabilityAnimationOneShot:
					oneShotCount++
				}
			}
			if (directionalCount == 1) != testCase.wantDirectional {
				t.Errorf("directional16 capability count = %d, want present=%t exactly once", directionalCount, testCase.wantDirectional)
			}
			if (oneShotCount == 1) != testCase.wantOneShot {
				t.Errorf("one-shot capability count = %d, want present=%t exactly once", oneShotCount, testCase.wantOneShot)
			}
		})
	}
}

func TestCodexLoadRestrictsSpritesheetPaths(t *testing.T) {
	cases := []struct {
		name           string
		resource       func(string) string
		wantSuccessful bool
	}{
		{name: "nested relative path", resource: func(root string) string { return "assets/spritesheet.png" }, wantSuccessful: true},
		{name: "parent escape", resource: func(root string) string { return "../escape.png" }},
		{name: "absolute path", resource: func(root string) string { return filepath.Join(root, "spritesheet.png") }},
		{name: "remote URL", resource: func(root string) string { return "https://example.invalid/spritesheet.png" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			resource := testCase.resource(root)
			if testCase.wantSuccessful {
				if err := os.MkdirAll(filepath.Join(root, "assets"), 0o700); err != nil {
					t.Fatal(err)
				}
				writePNG(t, filepath.Join(root, "assets", "spritesheet.png"), 1536, 1872)
			} else {
				writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, 1872)
			}
			writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
				"id":              "path.pet",
				"displayName":     "Path Pet",
				"spritesheetPath": resource,
			})
			definition, err := NewCodexAdapter().Load(context.Background(), root)
			if testCase.wantSuccessful {
				if err != nil {
					t.Fatalf("Load() nested path error = %v", err)
				}
				if definition.Assets[0].Resource != "assets/spritesheet.png" {
					t.Fatalf("normalized nested resource = %q", definition.Assets[0].Resource)
				}
				return
			}
			if err == nil {
				t.Fatal("Load() accepted an unsafe spritesheet path")
			}
			diagnostic := assertDiagnosticCode(t, err, CodePathOutsideRoot)
			if diagnostic.CorrelationID == "" || strings.Contains(diagnostic.Summary, root) {
				t.Fatalf("unsafe path diagnostic: %+v", diagnostic)
			}
		})
	}
}

func TestCodexLoadRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "spritesheet.png"), 1536, 1872)

	firstLink := filepath.Join(root, "link-target")
	if err := os.Symlink(outside, firstLink); err != nil {
		t.Skipf("os.Symlink unavailable; Windows may require developer mode or elevated link privilege: %v", err)
	}
	secondLink := filepath.Join(root, "link-chain")
	if err := os.Symlink(firstLink, secondLink); err != nil {
		t.Skipf("os.Symlink chain unavailable; Windows may require developer mode or elevated link privilege: %v", err)
	}
	writeJSON(t, filepath.Join(root, "pet.json"), map[string]any{
		"id":              "symlink.pet",
		"displayName":     "Symlink Pet",
		"spritesheetPath": "link-chain/spritesheet.png",
	})

	_, err := NewCodexAdapter().Load(context.Background(), root)
	if err == nil {
		t.Fatal("Load() followed a spritesheet symlink outside the package root")
	}
	diagnostic := assertDiagnosticCode(t, err, CodePathOutsideRoot)
	if diagnostic.CorrelationID == "" || strings.Contains(diagnostic.Summary, root) || strings.Contains(diagnostic.Summary, outside) {
		t.Fatalf("unsafe symlink diagnostic: %+v", diagnostic)
	}
}

func TestCodexLoadRejectsManifestSymlinkEscape(t *testing.T) {
	cases := []struct {
		name     string
		entry    string
		legacy   bool
		explicit bool
	}{
		{name: "pet entry discovered in package directory", entry: "pet.json"},
		{name: "explicit pet manifest", entry: "pet.json", explicit: true},
		{name: "legacy avatar entry", entry: "avatar.json", legacy: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			source := filepath.Join(outside, "manifest-source.json")
			writeJSON(t, source, map[string]any{
				"id":              "escaped-manifest.pet",
				"displayName":     "Escaped Manifest Pet",
				"spritesheetPath": "spritesheet.png",
			})
			firstLink := filepath.Join(root, "manifest-target")
			if err := os.Symlink(source, firstLink); err != nil {
				t.Skipf("os.Symlink unavailable; Windows may require developer mode or elevated link privilege: %v", err)
			}
			manifestPath := filepath.Join(root, testCase.entry)
			if err := os.Symlink(firstLink, manifestPath); err != nil {
				t.Skipf("os.Symlink chain unavailable; Windows may require developer mode or elevated link privilege: %v", err)
			}
			adapter := NewCodexAdapter()
			if testCase.legacy {
				adapter = NewCodexAdapter(WithLegacyAvatars(true))
			}
			input := root
			if testCase.explicit {
				input = manifestPath
			}
			_, err := adapter.Load(context.Background(), input)
			if err == nil {
				t.Fatal("Load() accepted a manifest symlink escaping the package root")
			}
			diagnostic := assertDiagnosticCode(t, err, CodePathOutsideRoot)
			if diagnostic.CorrelationID == "" || strings.Contains(diagnostic.Summary, root) || strings.Contains(diagnostic.Summary, outside) {
				t.Fatalf("unsafe manifest diagnostic: %+v", diagnostic)
			}
		})
	}
}

func assertDiagnosticCode(t *testing.T, err error, want string) Diagnostic {
	t.Helper()
	var diagnostic Diagnostic
	if !errors.As(err, &diagnostic) {
		t.Fatalf("error = %v, want Diagnostic", err)
	}
	if diagnostic.Code != want {
		t.Fatalf("diagnostic code = %q, want %q", diagnostic.Code, want)
	}
	return diagnostic
}

func loadCodexDefinition(t *testing.T, version, height int, animations map[string]any) PetDefinition {
	t.Helper()
	root := t.TempDir()
	manifest := map[string]any{
		"id":              "table.pet",
		"displayName":     "Table Pet",
		"spritesheetPath": "spritesheet.png",
	}
	if version == 2 {
		manifest["spriteVersionNumber"] = 2
	}
	if animations != nil {
		manifest["animations"] = animations
	}
	writeJSON(t, filepath.Join(root, "pet.json"), manifest)
	writePNG(t, filepath.Join(root, "spritesheet.png"), 1536, height)
	definition, err := NewCodexAdapter().Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load() version %d error = %v", version, err)
	}
	return definition
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

func writePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeOpaquePNG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

func writeWebP(t *testing.T, path string) {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString(minimalWebPBase64)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

const minimalWebPBase64 = "UklGRpwAAABXRUJQVlA4TI8AAAAv/8XTEQ8Q8x/zHwwBSVL//2NE//P4z3/+85///Oc///nPf/7zn//85z//+c9//vOf//znP//5z3/+85///Oc///nPf/7zn//85z//+c9//vOf//znP//5z3/+85///Oc///nPf/7zn//85z//+c9//vOf//znP//5z3/+85///Oc///nPf/7zn//85z8qAAA="
