package pet

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDshNativeLoadsIndependentRasterTracks(t *testing.T) {
	manifest := nativeManifestFixture()
	manifest["assets"] = []any{
		map[string]any{"id": "idle", "renderer": "dsh-raster-v1", "type": "image", "path": "idle.png"},
		map[string]any{"id": "wave", "renderer": "dsh-raster-v1", "type": "image", "path": "wave.png"},
	}
	manifest["tracks"] = map[string]any{
		"idle": map[string]any{
			"asset": "idle", "frames": []any{map[string]any{"index": 0, "durationMs": 100}},
			"fps": 10, "loop": true, "fallback": "idle", "priority": 1, "interruptible": true,
		},
		"wave": map[string]any{
			"asset": "wave", "frames": []any{map[string]any{"index": 0, "durationMs": 125}},
			"fps": 8, "loop": false, "fallback": "idle", "priority": 7, "interruptible": false,
			"timeoutMs": 1500,
		},
	}
	manifest["states"] = map[string]any{
		"idle": map[string]any{"track": "idle"},
	}
	manifest["actions"] = map[string]any{
		"wave": map[string]any{"track": "wave", "priority": 9, "interruptible": false, "fallback": "idle"},
	}

	source := writeNativeFixture(t, manifest, map[string][]byte{
		"idle.png": nativePNG(3, 4),
		"wave.png": nativePNG(5, 6),
	})
	definition, err := NewDshNativeAdapter().Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}

	if definition.Identity != (Identity{ID: "native-fixture", DisplayName: "Native Fixture", Description: "native test"}) {
		t.Fatalf("identity = %+v", definition.Identity)
	}
	if definition.Source.Format != "dsh-native" || definition.Source.FormatVersion != "1" || definition.Source.Profile != "dsh-raster-v1" || definition.Source.Badge != BadgeDshNative {
		t.Fatalf("source = %+v", definition.Source)
	}
	if definition.Source.StableKey != source.StableSourceKey || definition.Source.Entry != "dsh-pet.json" || definition.Source.Digest == "" {
		t.Fatalf("source identity = %+v, source = %+v", definition.Source, source)
	}
	if definition.Geometry.CellWidth != 0 || definition.Geometry.CellHeight != 0 || definition.Geometry.Columns != 0 || definition.Geometry.Rows != 0 || definition.Geometry.FrameCount != 2 {
		t.Fatalf("independent-image geometry imported as a Codex grid: %+v", definition.Geometry)
	}
	if len(definition.Assets) != 2 || definition.Assets[0].Kind != "image" || definition.Assets[1].Kind != "image" {
		t.Fatalf("assets = %+v", definition.Assets)
	}
	if definition.Assets[0].Width != 3 || definition.Assets[0].Height != 4 || definition.Assets[1].Width != 5 || definition.Assets[1].Height != 6 {
		t.Fatalf("asset dimensions = %+v", definition.Assets)
	}

	idle := definition.Tracks["idle"]
	if idle.RendererKind != "dsh-raster-v1" || idle.FPS != 10 || !idle.Loop || idle.Priority != 1 || !idle.Interruptible || idle.Fallback != "idle" || len(idle.Frames) != 1 {
		t.Fatalf("idle track = %+v", idle)
	}
	if idle.Frames[0] != (FrameRef{Index: 0, DurationMS: 100, AssetID: "idle", Width: 3, Height: 4}) {
		t.Fatalf("idle frame = %+v", idle.Frames[0])
	}
	wave := definition.Tracks["wave"]
	if wave.TimeoutMS != 1500 || wave.Frames[0].AssetID != "wave" || wave.Frames[0].Width != 5 || wave.Frames[0].Height != 6 {
		t.Fatalf("wave track = %+v", wave)
	}
	if definition.States["idle"] != (StateSpec{Track: "idle", Loop: true, Fallback: "idle"}) {
		t.Fatalf("states = %+v", definition.States)
	}
	if definition.Actions["wave"] != (ActionSpec{Track: "wave", Priority: 9, Interruptible: false, Fallback: "idle"}) {
		t.Fatalf("actions = %+v", definition.Actions)
	}
	if definition.Permissions != (PermissionPolicy{Network: "deny", Process: "deny", Scripts: "deny", Audio: "deny"}) {
		t.Fatalf("permissions = %+v", definition.Permissions)
	}
	if definition.Canvas != (CanvasSpec{}) {
		t.Fatalf("default canvas = %+v", definition.Canvas)
	}
}

func TestDshNativeHonorsStandardEntryIntegrityCanvasAndCapabilities(t *testing.T) {
	manifest := nativeManifestFixture()
	asset := manifest["assets"].([]any)[0].(map[string]any)
	delete(asset, "path")
	asset["entry"] = "assets/idle.png"
	asset["integrity"] = "sha256:PLACEHOLDER"
	asset["capabilities"] = []string{"visual.static", "visual.frame-sequence"}
	manifest["capabilities"] = []string{"window.monitor-aware"}
	manifest["canvas"] = map[string]any{
		"width": 192, "height": 208,
		"pivot":     map[string]any{"x": 0.5, "y": 1.0},
		"scaleMode": "contain", "safePadding": 8,
	}
	manifest["inputs"] = map[string]any{"look.continuous": true, "interaction.drag": false}
	manifest["projection"] = map[string]any{"task.state": "working"}
	manifest["presentation"] = map[string]any{"anchorX": 0.25, "anchorY": 0.75, "scale": 1.5}
	imageData := nativePNG(3, 4)
	asset["integrity"] = "sha256:" + HashBytes(imageData)
	source := writeNativeFixture(t, manifest, map[string][]byte{"assets/idle.png": imageData})
	definition, err := NewDshNativeAdapter().Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Assets[0].Path != "assets/idle.png" || definition.Canvas != (CanvasSpec{Width: 192, Height: 208, PivotX: 0.5, PivotY: 1, ScaleMode: "contain", SafePadding: 8}) {
		t.Fatalf("standard asset/canvas = asset=%+v canvas=%+v", definition.Assets[0], definition.Canvas)
	}
	if !reflect.DeepEqual(definition.Capabilities, []string{"visual.frame-sequence", "visual.static", "window.monitor-aware"}) {
		t.Fatalf("capabilities = %#v", definition.Capabilities)
	}
	if !reflect.DeepEqual(definition.Inputs, map[string]bool{"look.continuous": true, "interaction.drag": false}) || !reflect.DeepEqual(definition.Projection, map[string]string{"task.state": "working"}) || definition.Presentation != (PresentationHints{AnchorX: 0.25, AnchorY: 0.75, Scale: 1.5}) {
		t.Fatalf("native metadata = inputs=%#v projection=%#v presentation=%+v", definition.Inputs, definition.Projection, definition.Presentation)
	}

	asset["integrity"] = "sha256:" + strings.Repeat("0", 64)
	invalid := writeNativeFixture(t, manifest, map[string][]byte{"assets/idle.png": imageData})
	definition, err = NewDshNativeAdapter().Load(context.Background(), invalid)
	if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
		t.Fatalf("integrity mismatch returned definition=%+v err=%v", definition, err)
	}
	assertIssueCode(t, err, nativeIssueIntegrityMismatch)
}

func TestDshNativeLoadsNativeAtlasWithoutCodexRows(t *testing.T) {
	manifest := nativeManifestFixture()
	manifest["assets"] = []any{
		map[string]any{
			"id": "atlas", "renderer": "dsh-raster-v1", "type": "atlas", "path": "atlas.png",
			"frameWidth": 2, "frameHeight": 3, "columns": 2, "rows": 2,
		},
	}
	manifest["tracks"] = map[string]any{
		"idle": map[string]any{
			"asset": "atlas", "frames": []any{
				map[string]any{"index": 0, "durationMs": 100},
				map[string]any{"index": 3, "durationMs": 200},
			},
			"loop": false,
		},
	}

	source := writeNativeFixture(t, manifest, map[string][]byte{"atlas.png": nativePNG(4, 6)})
	definition, err := NewDshNativeAdapter().Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Geometry != (Geometry{CellWidth: 2, CellHeight: 3, Columns: 2, Rows: 2, ImageWidth: 4, ImageHeight: 6, FrameCount: 4}) {
		t.Fatalf("atlas geometry = %+v", definition.Geometry)
	}
	if definition.Assets[0].Kind != "atlas" || definition.Assets[0].Width != 4 || definition.Assets[0].Height != 6 {
		t.Fatalf("atlas asset = %+v", definition.Assets)
	}
	frames := definition.Tracks["idle"].Frames
	if len(frames) != 2 || frames[0] != (FrameRef{Index: 0, DurationMS: 100, AssetID: "atlas", Width: 2, Height: 3}) || frames[1] != (FrameRef{Index: 3, DurationMS: 200, AssetID: "atlas", X: 2, Y: 3, Width: 2, Height: 3}) {
		t.Fatalf("atlas frames = %+v", frames)
	}
}

func TestDshNativeRejectsUnsafeResourcesAndDeclarations(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(map[string]any)
		files    map[string][]byte
		wantCode string
	}{
		{
			name: "parent path",
			mutate: func(manifest map[string]any) {
				manifest["assets"].([]any)[0].(map[string]any)["path"] = "../outside.png"
			},
			wantCode: IssuePathOutsideRoot,
		},
		{
			name: "remote path",
			mutate: func(manifest map[string]any) {
				manifest["assets"].([]any)[0].(map[string]any)["path"] = "https://example.invalid/pet.png"
			},
			wantCode: IssueRemoteResource,
		},
		{
			name: "unknown asset renderer",
			mutate: func(manifest map[string]any) {
				manifest["assets"].([]any)[0].(map[string]any)["renderer"] = "codex-atlas"
			},
			wantCode: nativeIssueUnknownRenderer,
		},
		{
			name: "unknown track renderer",
			mutate: func(manifest map[string]any) {
				manifest["tracks"].(map[string]any)["idle"].(map[string]any)["renderer"] = "codex-atlas"
			},
			wantCode: nativeIssueUnknownRenderer,
		},
		{
			name: "network permission",
			mutate: func(manifest map[string]any) {
				manifest["permissions"] = map[string]any{"network": true}
			},
			wantCode: nativeIssuePermissionsDenied,
		},
		{
			name: "canvas permission",
			mutate: func(manifest map[string]any) {
				manifest["canvas"] = true
			},
			wantCode: nativeIssueCanvasDenied,
		},
		{
			name: "unknown track",
			mutate: func(manifest map[string]any) {
				manifest["tracks"].(map[string]any)["idle"].(map[string]any)["fallback"] = "missing"
			},
			wantCode: IssueInvalidAnimation,
		},
		{
			name: "bad frame reference",
			mutate: func(manifest map[string]any) {
				manifest["tracks"].(map[string]any)["idle"].(map[string]any)["frames"] = []any{map[string]any{"index": 2}}
			},
			wantCode: IssueInvalidAnimation,
		},
		{
			name: "missing state reference",
			mutate: func(manifest map[string]any) {
				manifest["states"] = map[string]any{"idle": map[string]any{"track": "missing"}}
			},
			wantCode: IssueInvalidAnimation,
		},
		{
			name: "missing action reference",
			mutate: func(manifest map[string]any) {
				manifest["actions"] = map[string]any{"wave": map[string]any{"track": "missing"}}
			},
			wantCode: IssueInvalidAnimation,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := nativeManifestFixture()
			test.mutate(manifest)
			source := writeNativeFixture(t, manifest, map[string][]byte{"idle.png": nativePNG(3, 4)})
			definition, err := NewDshNativeAdapter().Load(context.Background(), source)
			if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
				t.Fatalf("invalid native package returned definition=%+v err=%v", definition, err)
			}
			assertIssueCode(t, err, test.wantCode)
		})
	}
}

func TestDshNativeRejectsBudgetsAndScriptResources(t *testing.T) {
	tests := []struct {
		name     string
		manifest map[string]any
		files    map[string][]byte
		wantCode string
	}{
		{
			name: "atlas frame budget",
			manifest: func() map[string]any {
				manifest := nativeManifestFixture()
				manifest["assets"] = []any{map[string]any{
					"id": "atlas", "renderer": "dsh-raster-v1", "type": "atlas", "path": "atlas.png",
					"frameWidth": 1, "frameHeight": 1, "columns": 17, "rows": 17,
				}}
				manifest["tracks"] = map[string]any{"idle": map[string]any{"asset": "atlas", "frames": []any{0}}}
				return manifest
			}(),
			files:    map[string][]byte{"atlas.png": nativePNG(17, 17)},
			wantCode: IssueInvalidSpritesheet,
		},
		{
			name: "decoded image dimensions",
			manifest: func() map[string]any {
				manifest := nativeManifestFixture()
				return manifest
			}(),
			files:    map[string][]byte{"idle.png": nativePNG(MaxImageWidth+1, 1)},
			wantCode: IssueDecodedImageTooLarge,
		},
		{
			name:     "script resource",
			manifest: nativeManifestFixture(),
			files: map[string][]byte{
				"idle.png": nativePNG(3, 4),
				"evil.js":  []byte("alert('no')"),
			},
			wantCode: IssueUnsafeResource,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := writeNativeFixture(t, test.manifest, test.files)
			definition, err := NewDshNativeAdapter().Load(context.Background(), source)
			if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
				t.Fatalf("budget violation returned definition=%+v err=%v", definition, err)
			}
			assertIssueCode(t, err, test.wantCode)
		})
	}
}

func TestDshNativeRejectsMalformedSchemaAndHonorsExplicitDeny(t *testing.T) {
	manifest := nativeManifestFixture()
	manifest["canvas"] = map[string]any{"allow": false, "width": 8, "height": 9}
	manifest["permissions"] = map[string]any{
		"network": false, "process": "deny", "scripts": "deny", "audio": false,
	}
	source := writeNativeFixture(t, manifest, map[string][]byte{"idle.png": nativePNG(3, 4)})
	definition, err := NewDshNativeAdapter().Load(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if definition.Canvas.Width != 8 || definition.Canvas.Height != 9 || definition.Permissions.Network != "deny" || definition.Permissions.Process != "deny" {
		t.Fatalf("explicit deny policy = canvas=%+v permissions=%+v", definition.Canvas, definition.Permissions)
	}

	for _, mutation := range []func(map[string]any){
		func(value map[string]any) { value["schema"] = "wrong.schema" },
		func(value map[string]any) { value["schemaVersion"] = 2 },
		func(value map[string]any) { delete(value, "schemaVersion") },
	} {
		invalid := nativeManifestFixture()
		mutation(invalid)
		invalidSource := writeNativeFixture(t, invalid, map[string][]byte{"idle.png": nativePNG(3, 4)})
		definition, err := NewDshNativeAdapter().Load(context.Background(), invalidSource)
		if err == nil || !reflect.DeepEqual(definition, PetDefinition{}) {
			t.Fatalf("malformed schema returned definition=%+v err=%v", definition, err)
		}
		assertIssueCode(t, err, IssueManifestInvalid)
	}
}

func nativeManifestFixture() map[string]any {
	return map[string]any{
		"schema":        "dsh.pet",
		"schemaVersion": 1,
		"id":            "native-fixture",
		"displayName":   "Native Fixture",
		"description":   "native test",
		"assets": []any{
			map[string]any{"id": "idle", "renderer": "dsh-raster-v1", "type": "image", "path": "idle.png"},
		},
		"tracks": map[string]any{
			"idle": map[string]any{"asset": "idle", "frames": []any{map[string]any{"index": 0, "durationMs": 125}}},
		},
	}
}

func writeNativeFixture(t *testing.T, manifest map[string]any, files map[string][]byte) PackageSource {
	t.Helper()
	root := t.TempDir()
	writeNativeJSON(t, filepath.Join(root, "dsh-pet.json"), manifest)
	for relative, data := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return NewDshNativePackageSource(root, "native-fixture")
}

func writeNativeJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nativePNG(width, height int) []byte {
	buffer := new(bytes.Buffer)
	if err := png.Encode(buffer, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		panic(err)
	}
	return buffer.Bytes()
}
