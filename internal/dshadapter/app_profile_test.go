package dshadapter

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"testing"
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
	wantPatch := "# Your patch layer for this dsh profile, applied after every bundle layer:\n# a top-level YAML array of loader patch entries (id-targeted config\n# overrides, disables, and insert lists; `!!js` expressions allowed).\n[]\n"
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

func TestInitializeAppProfileLeavesExistingFilesUntouched(t *testing.T) {
	profile := t.TempDir()
	want := map[string]string{
		"cordis.patch.yml":    "keep patch\n",
		"pnpm-workspace.yaml": "keep workspace\n",
		"package.json":        "not json; keep it\n",
	}
	for name, contents := range want {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
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
}

func TestInitializeAppProfileFillsOnlyMissingManifest(t *testing.T) {
	profile := t.TempDir()
	want := map[string]string{
		"cordis.patch.yml":    "custom patch\n",
		"pnpm-workspace.yaml": "custom workspace\n",
	}
	for name, contents := range want {
		if err := os.WriteFile(filepath.Join(profile, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
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
}

func TestAppProfileBundlesMatchPinnedDSHWebTemplate(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate test source")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "tools", "dsh", "node_modules", "@deepseek-ai", "dsh-app-boot", "lib", "index.js")
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
