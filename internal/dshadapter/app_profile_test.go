package dshadapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestInitializeAppProfileCreatesTemplateFiles(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "profiles", "dsh-work")
	adapter := &Adapter{}
	if err := adapter.InitializeAppProfile(profile); err != nil {
		t.Fatal(err)
	}
	patch, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wantPatch := "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n# dsh-work enables browser-facing capabilities, keeps file-write observation,\n# and prevents optional diagnostics or Session history from leaving the machine.\n- id: ui-sidebar-browser\n  disabled: false\n- id: fs-observation-policy\n  disabled: false\n- id: web-fetch-http\n  disabled: false\n- id: desktop-product-telemetry\n  disabled: true\n- id: product-analytics\n  disabled: true\n- id: session-telemetry-otel\n  disabled: true\n- id: session-log-deepseek\n  disabled: true\n- id: plugin-package-inventory-deepseek\n  disabled: true\n"
	if string(patch) != wantPatch {
		t.Fatalf("patch = %q, want %q", patch, wantPatch)
	}
	workspace, err := os.ReadFile(filepath.Join(profile, "pnpm-workspace.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	wantWorkspace := "packages:\n  - .\n\nnodeLinker: hoisted\nautoInstallPeers: false\n"
	if string(workspace) != wantWorkspace {
		t.Fatalf("workspace = %q, want %q", workspace, wantWorkspace)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(profile, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest any
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	wantManifest := map[string]any{
		"name":         "dsh-profile-dsh-work",
		"private":      true,
		"dependencies": map[string]any{},
		"dsh": map[string]any{
			"profile": map[string]any{
				"bundles": []any{"@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app"},
			},
		},
	}
	if !reflect.DeepEqual(manifest, wantManifest) {
		t.Fatalf("manifest = %#v, want %#v", manifest, wantManifest)
	}
}

func TestInitializeAppProfilePreservesExistingFilesAndMigratesPatch(t *testing.T) {
	profile := t.TempDir()
	want := map[string]string{
		"pnpm-workspace.yaml": "keep workspace\n",
		"package.json":        "not json; keep it\n",
	}
	for name, contents := range want {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	patch := "# keep this custom entry\n- id: custom-entry\n  config:\n    value: !!js \"ctx.get('value')\"\n- id: product-analytics\n  disabled: false\n- id: desktop-product-telemetry\n  disabled: false\n"
	if err := os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Adapter{}).InitializeAppProfile(profile); err != nil {
		t.Fatal(err)
	}
	for name, contents := range want {
		actual, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil || string(actual) != contents {
			t.Fatalf("%s = %q, %v; want %q", name, actual, err, contents)
		}
	}
	actualPatch, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(actualPatch), "# keep this custom entry") || !strings.Contains(string(actualPatch), "- id: custom-entry") || !strings.Contains(string(actualPatch), "!!js") {
		t.Fatalf("custom patch content was not preserved:\n%s", actualPatch)
	}
	assertAppProfilePatchDisabled(t, actualPatch, "ui-sidebar-browser", false)
	assertAppProfilePatchDisabled(t, actualPatch, "fs-observation-policy", false)
	assertAppProfilePatchDisabled(t, actualPatch, "web-fetch-http", false)
	assertAppProfilePatchDisabled(t, actualPatch, "desktop-product-telemetry", true)
	assertAppProfilePatchDisabled(t, actualPatch, "product-analytics", true)
	assertAppProfilePatchDisabled(t, actualPatch, "session-telemetry-otel", true)
	assertAppProfilePatchDisabled(t, actualPatch, "session-log-deepseek", true)
	assertAppProfilePatchDisabled(t, actualPatch, "plugin-package-inventory-deepseek", true)
	beforeSecondInitialization := string(actualPatch)
	if err := (&Adapter{}).InitializeAppProfile(profile); err != nil {
		t.Fatal(err)
	}
	afterSecondInitialization, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(afterSecondInitialization) != beforeSecondInitialization {
		t.Fatalf("second initialization changed migrated patch:\nbefore:\n%s\nafter:\n%s", beforeSecondInitialization, afterSecondInitialization)
	}
}

func TestInitializeAppProfileMigratesLegacyEmptyPatch(t *testing.T) {
	profile := t.TempDir()
	legacyPatch := "# Previous dsh-work patch template\n[]\n"
	if err := os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte(legacyPatch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Adapter{}).InitializeAppProfile(profile); err != nil {
		t.Fatal(err)
	}
	migratedPatch, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(migratedPatch), "[]") || !strings.Contains(string(migratedPatch), "# Previous dsh-work patch template") {
		t.Fatalf("legacy empty patch was not migrated while preserving its comment:\n%s", migratedPatch)
	}
	assertAppProfilePatchDisabled(t, migratedPatch, "ui-sidebar-browser", false)
	assertAppProfilePatchDisabled(t, migratedPatch, "fs-observation-policy", false)
	assertAppProfilePatchDisabled(t, migratedPatch, "web-fetch-http", false)
	assertAppProfilePatchDisabled(t, migratedPatch, "desktop-product-telemetry", true)
	assertAppProfilePatchDisabled(t, migratedPatch, "product-analytics", true)
	assertAppProfilePatchDisabled(t, migratedPatch, "session-telemetry-otel", true)
	assertAppProfilePatchDisabled(t, migratedPatch, "session-log-deepseek", true)
	assertAppProfilePatchDisabled(t, migratedPatch, "plugin-package-inventory-deepseek", true)
}

func TestInitializeAppProfileFillsOnlyMissingManifest(t *testing.T) {
	profile := t.TempDir()
	want := map[string]string{
		"pnpm-workspace.yaml": "custom workspace\n",
	}
	for name, contents := range want {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	patch := "# custom patch\n- id: custom-entry\n"
	if err := os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Adapter{}).InitializeAppProfile(profile); err != nil {
		t.Fatal(err)
	}
	for name, contents := range want {
		actual, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil || string(actual) != contents {
			t.Fatalf("%s = %q, %v; want %q", name, actual, err, contents)
		}
	}
	if _, err := os.Stat(filepath.Join(profile, "package.json")); err != nil {
		t.Fatalf("manifest was not created: %v", err)
	}
	migratedPatch, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(migratedPatch), "- id: custom-entry") {
		t.Fatalf("custom patch entry was not preserved:\n%s", migratedPatch)
	}
	assertAppProfilePatchDisabled(t, migratedPatch, "ui-sidebar-browser", false)
}

func TestInitializeAppProfilePreservesExplicitBrowserAndObservationDisable(t *testing.T) {
	profile := t.TempDir()
	patch := "# user's explicit choices\n- id: ui-sidebar-browser\n  disabled: true\n- id: fs-observation-policy\n  disabled: true\n- id: web-fetch-http\n  disabled: true\n"
	if err := os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Adapter{}).InitializeAppProfile(profile); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	assertAppProfilePatchDisabled(t, actual, "ui-sidebar-browser", true)
	assertAppProfilePatchDisabled(t, actual, "fs-observation-policy", true)
	assertAppProfilePatchDisabled(t, actual, "web-fetch-http", true)
	assertAppProfilePatchDisabled(t, actual, "desktop-product-telemetry", true)
	assertAppProfilePatchDisabled(t, actual, "product-analytics", true)
	assertAppProfilePatchDisabled(t, actual, "session-telemetry-otel", true)
	assertAppProfilePatchDisabled(t, actual, "session-log-deepseek", true)
	assertAppProfilePatchDisabled(t, actual, "plugin-package-inventory-deepseek", true)
}

func TestInitializeAppProfileLeavesMalformedPatchForRepair(t *testing.T) {
	profile := t.TempDir()
	patch := "invalid: [yaml\n"
	if err := os.WriteFile(filepath.Join(profile, "cordis.patch.yml"), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := (&Adapter{}).InitializeAppProfile(profile); err == nil {
		t.Fatal("InitializeAppProfile accepted a malformed patch")
	}
	actual, err := os.ReadFile(filepath.Join(profile, "cordis.patch.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != patch {
		t.Fatalf("malformed patch changed during initialization: %q", actual)
	}
}

func assertAppProfilePatchDisabled(t *testing.T, data []byte, id string, want bool) {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse profile patch: %v", err)
	}
	if len(document.Content) == 0 || document.Content[0].Kind != yaml.SequenceNode {
		t.Fatalf("profile patch is not a sequence: %#v", document.Content)
	}
	var got *yaml.Node
	for _, item := range document.Content[0].Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		var entryID, disabled *yaml.Node
		for index := 0; index+1 < len(item.Content); index += 2 {
			switch item.Content[index].Value {
			case "id":
				entryID = item.Content[index+1]
			case "disabled":
				disabled = item.Content[index+1]
			}
		}
		if entryID != nil && entryID.Value == id && disabled != nil {
			got = disabled
		}
	}
	if got == nil || got.Kind != yaml.ScalarNode || got.ShortTag() != "!!bool" || (got.Value == "true") != want {
		t.Fatalf("patch disabled for %s = %#v, want %t", id, got, want)
	}
}

func TestAppProfileBundlesMatchPinnedDSHWebTemplate(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "tools", "dsh", "node_modules", "@deepseek-ai", "dsh-official-bundles", "lib", "index.js")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Skip("DSH tools not installed")
	}
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`web:\s*\{\s*bundles:\s*\[([^\]]*)\]`).FindSubmatch(data)
	if len(match) != 2 {
		t.Fatal("web bundle template not found")
	}
	values := regexp.MustCompile(`"([^\"]+)"`).FindAllSubmatch(match[1], -1)
	got := make([]string, 0, len(values))
	for _, value := range values {
		got = append(got, string(value[1]))
	}
	if !reflect.DeepEqual(got, (&Adapter{}).AppProfileBundles()) {
		t.Fatalf("web bundles = %v, app bundles = %v", got, (&Adapter{}).AppProfileBundles())
	}
}
