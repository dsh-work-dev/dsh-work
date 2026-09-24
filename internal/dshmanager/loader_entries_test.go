package dshmanager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The layer patches mirror DSH's shape: an insert list introduces rows, and
// later id-targeted rows override them. Row names are package names.
const basePatch = `# base
- insert:
    - id: web-search-deepseek
      name: '@deepseek-ai/dsh-web-search-deepseek'
      config:
        apiKeyEnv: DEEPSEEK_API_KEY
    - id: tool-bash
      name: '@deepseek-ai/dsh-bash-sandbox'
    - id: plugin-manager
      name: '@deepseek-ai/dsh-plugin-manager'
      disabled: !!js "!ctx.get('profileContext')"
`

const appPatch = `- id: tool-bash
  disabled: true
- insert:
    - id: workspace
      name: '@deepseek-ai/dsh-api-workspace-controller'
`

const userPatchDefault = `# Your patch layer for this dsh profile.
[]
`

func writeLayer(t *testing.T, root, name, patch string) {
	t.Helper()
	dir := filepath.Join(append([]string{root, "node_modules"}, strings.Split(name, "/")...)...)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","dsh":{"bundle":{"patch":"./cordis.patch.yml"}}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cordis.patch.yml"), []byte(patch), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newLoaderTestProfile(t *testing.T) (ResolvedLaunch, string) {
	t.Helper()
	root := t.TempDir()
	profilePath := filepath.Join(root, "dsh-home", "profiles", "web")
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{
  "dependencies": {"@acme/widget": "1.0.0"},
  "dsh": {"profile": {"bundles": ["@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app", "@acme/widget"]}}
}
`
	if err := os.WriteFile(filepath.Join(profilePath, "package.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	launch := ResolvedLaunch{Runtime: RuntimeInfo{Path: filepath.Join(root, "dsh.cmd")}}
	runtimeRoot := filepath.Dir(launch.Runtime.Path)
	writeLayer(t, runtimeRoot, "@deepseek-ai/dsh-base", basePatch)
	writeLayer(t, runtimeRoot, "@deepseek-ai/dsh-web-app", appPatch)
	// A third-party layer may override an official row too.
	writeLayer(t, profilePath, "@acme/widget", "- id: workspace\n  disabled: true\n")
	if err := os.WriteFile(filepath.Join(profilePath, "cordis.patch.yml"), []byte(userPatchDefault), 0o600); err != nil {
		t.Fatal(err)
	}
	return launch, profilePath
}

func loaderEntry(t *testing.T, layers []LoaderLayer, id string) LoaderEntry {
	t.Helper()
	for _, layer := range layers {
		for _, entry := range layer.Entries {
			if entry.ID == id {
				return entry
			}
		}
	}
	t.Fatalf("loader entry %s is missing from %#v", id, layers)
	return LoaderEntry{}
}

func TestOfficialLoaderEntriesAreGroupedByTheLayerThatInsertsThem(t *testing.T) {
	launch, profilePath := newLoaderTestProfile(t)
	layers, err := officialLoaderLayers(profilePath, launch.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	if len(layers) != 2 || layers[0].Package != "@deepseek-ai/dsh-base" || layers[1].Package != "@deepseek-ai/dsh-web-app" {
		t.Fatalf("layers = %#v", layers)
	}
	if got := len(layers[0].Entries); got != 3 {
		t.Fatalf("base entries = %d", got)
	}
	search := loaderEntry(t, layers, "web-search-deepseek")
	if search.Package != "@deepseek-ai/dsh-web-search-deepseek" || search.DefaultDisabled || search.Disabled || search.Conditional {
		t.Fatalf("web search = %#v", search)
	}
	// A later layer's override decides the default, not the inserting layer.
	if bash := loaderEntry(t, layers, "tool-bash"); !bash.DefaultDisabled {
		t.Fatalf("tool-bash = %#v", bash)
	}
	if manager := loaderEntry(t, layers, "plugin-manager"); manager.DefaultDisabled || !manager.Conditional {
		t.Fatalf("plugin-manager = %#v", manager)
	}
	if workspace := loaderEntry(t, layers, "workspace"); !workspace.DefaultDisabled {
		t.Fatalf("third-party override was ignored: %#v", workspace)
	}
}
