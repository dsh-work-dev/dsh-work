package dshmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/local/dsh-work/internal/lifecycle"
)

// DSH composes a profile's plugin tree from patches: each layer in
// `dsh.profile.bundles` applies its package's `cordis.patch.yml`, then the
// profile's own `cordis.patch.yml`. A patch is a YAML list whose items either
// insert rows (`- insert: [{id, name, ...}]`) or target an existing row by id
// (`- id: x, disabled: true`), with the last write winning. An official
// loader entry is turned off by an id-targeted `disabled: true` row in the
// profile's own patch layer, which DSH's plugin commands never rewrite.

const userPatchName = "cordis.patch.yml"

var loaderEntryIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,127}$`)

type loaderDisabledState int

const (
	loaderEnabled loaderDisabledState = iota
	loaderDisabled
	loaderConditional
)

// ListLoaderEntries lists the official loader entries of the current profile
// as a tree of the layers that insert them. It only reads files, so like
// Snapshot it does not wait behind a running plugin command.
func (m *Manager) ListLoaderEntries(ctx context.Context, request LoaderEntryListRequest) ([]LoaderLayer, error) {
	dataDirectory, runtime, _, err := m.resolvePluginTarget(ctx, request.Target, false, true)
	if err != nil {
		return nil, err
	}
	layers, err := officialLoaderLayers(filepath.Join(dataDirectory.Path, "profiles", request.Target.Profile.Name), runtime)
	if err != nil {
		return nil, failure(lifecycle.ErrorProfileInvalid, "DSH profile could not be read", "the profile or one of its layer patches is invalid")
	}
	return layers, nil
}

// SetLoaderEntryDisabled turns one official loader entry off or back on in the
// current profile. The Host wraps this in its stopped-Worker transaction.
func (m *Manager) SetLoaderEntryDisabled(ctx context.Context, request LoaderEntryDisableRequest) (PluginResult, error) {
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return PluginResult{}, err
	}
	defer release()
	dataDirectory, runtime, _, err := m.resolvePluginTarget(ctx, request.Target, false, true)
	if err != nil {
		return PluginResult{}, err
	}
	return m.setLoaderEntryDisabledLocked(ctx, filepath.Join(dataDirectory.Path, "profiles", request.Target.Profile.Name), request.Target.Profile, runtime, request.ID, request.Disabled)
}

// ApplyLoaderEntryDisabled is SetLoaderEntryDisabled inside the Host's
// stopped-Worker transaction, for the already-resolved launch.
func (m *Manager) ApplyLoaderEntryDisabled(ctx context.Context, launch ResolvedLaunch, id string, disabled bool) (PluginResult, error) {
	if err := m.requireNoCurrentRunContext(); err != nil {
		return PluginResult{}, err
	}
	release, err := m.acquireOperation(ctx)
	if err != nil {
		return PluginResult{}, err
	}
	defer release()
	profilePath := filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name)
	return m.setLoaderEntryDisabledLocked(ctx, profilePath, launch.Target.Profile, launch.Runtime, id, disabled)
}

func (m *Manager) setLoaderEntryDisabledLocked(ctx context.Context, profilePath string, profile ProfileRef, runtime RuntimeInfo, id string, disabled bool) (PluginResult, error) {
	if err := contextError(ctx); err != nil {
		return PluginResult{}, err
	}
	id = strings.TrimSpace(id)
	if !loaderEntryIDPattern.MatchString(id) {
		return PluginResult{}, failure(lifecycle.ErrorPluginSpecInvalid, "loader entry id is invalid", "use one listed loader entry")
	}
	layers, err := officialLoaderLayers(profilePath, runtime)
	if err != nil {
		return PluginResult{}, failure(lifecycle.ErrorProfileInvalid, "DSH profile could not be read", "the profile or one of its layer patches is invalid")
	}
	entry, found := findLoaderEntry(layers, id)
	if !found {
		return PluginResult{}, failure(lifecycle.ErrorPluginNotInstalled, "the loader entry is not part of this profile", "refresh the plugin list and try again")
	}
	if disabled && entry.DefaultDisabled {
		return PluginResult{}, failure(lifecycle.ErrorPluginProtected, "this loader entry is already off by default", "only entries that load by default can be disabled")
	}
	if err := setUserPatchDisabled(filepath.Join(profilePath, userPatchName), id, disabled); err != nil {
		return PluginResult{}, failureWithMeta(lifecycle.ErrorPluginDisableFailed, "The loader entry could not be changed", err.Error(), true, false)
	}
	return m.pluginResult(ctx, profilePath, profile)
}

func findLoaderEntry(layers []LoaderLayer, id string) (LoaderEntry, bool) {
	for _, layer := range layers {
		for _, entry := range layer.Entries {
			if entry.ID == id {
				return entry, true
			}
		}
	}
	return LoaderEntry{}, false
}

// officialLoaderLayers replays the profile's layer patches in order. Entries
// are grouped under the official layer that inserts them; any later layer,
// official or not, may change their default state.
func officialLoaderLayers(profilePath string, runtime RuntimeInfo) ([]LoaderLayer, error) {
	manifest, err := readProfileBundleManifest(profilePath)
	if err != nil {
		return nil, err
	}
	layers := []LoaderLayer{}
	owner := map[string]int{}
	state := map[string]loaderDisabledState{}
	for _, bundle := range manifest.Bundles() {
		patch, err := layerPatch(profilePath, runtime, bundle)
		if err != nil {
			return nil, err
		}
		if patch == nil {
			continue
		}
		official := IsCorePluginPackage(bundle)
		layerIndex := -1
		for _, item := range patch.Content {
			if item.Kind != yaml.MappingNode {
				continue
			}
			if inserted := mappingValue(item, "insert"); inserted != nil {
				for _, row := range inserted.Content {
					id := scalarValue(mappingValue(row, "id"))
					if id == "" {
						continue
					}
					state[id] = disabledState(mappingValue(row, "disabled"))
					if _, exists := owner[id]; exists || !official {
						continue
					}
					if layerIndex < 0 {
						layers = append(layers, LoaderLayer{Package: bundle, Entries: []LoaderEntry{}})
						layerIndex = len(layers) - 1
					}
					owner[id] = layerIndex
					layers[layerIndex].Entries = append(layers[layerIndex].Entries, LoaderEntry{ID: id, Package: scalarValue(mappingValue(row, "name"))})
				}
				continue
			}
			if id := scalarValue(mappingValue(item, "id")); id != "" {
				if value := mappingValue(item, "disabled"); value != nil {
					state[id] = disabledState(value)
				}
			}
		}
	}
	userDisabled, err := userPatchDisables(filepath.Join(profilePath, userPatchName))
	if err != nil {
		return nil, err
	}
	for layerIndex := range layers {
		for entryIndex := range layers[layerIndex].Entries {
			entry := &layers[layerIndex].Entries[entryIndex]
			entry.DefaultDisabled = state[entry.ID] == loaderDisabled
			entry.Conditional = state[entry.ID] == loaderConditional
			entry.Disabled = userDisabled[entry.ID]
		}
	}
	return layers, nil
}

// layerPatch reads the patch a layer package declares, from the profile's own
// packages first and then the runtime's. A layer without a patch returns nil.
func layerPatch(profilePath string, runtime RuntimeInfo, packageName string) (*yaml.Node, error) {
	if !validPackageName(packageName) {
		return nil, nil
	}
	parts := strings.Split(packageName, "/")
	roots := []string{profilePath}
	// The runtime path is its npm shim (`<root>/node_modules/.bin/dsh.cmd`) or
	// a launcher in the root; like Node, look for node_modules upward from it.
	if runtime.Path != "" {
		for dir, depth := filepath.Dir(runtime.Path), 0; depth < 4; dir, depth = filepath.Dir(dir), depth+1 {
			roots = append(roots, dir)
		}
	}
	for _, root := range roots {
		packageDir := filepath.Join(append([]string{root, "node_modules"}, parts...)...)
		data, err := os.ReadFile(filepath.Join(packageDir, "package.json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var manifest struct {
			DSH struct {
				Bundle struct {
					Patch string `json:"patch"`
				} `json:"bundle"`
			} `json:"dsh"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return nil, err
		}
		if manifest.DSH.Bundle.Patch == "" {
			return nil, nil
		}
		patchPath := filepath.Join(packageDir, filepath.FromSlash(manifest.DSH.Bundle.Patch))
		if relative, err := filepath.Rel(packageDir, patchPath); err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("layer patch path leaves its package")
		}
		return readPatchSequence(patchPath)
	}
	return nil, nil
}

func readPatchSequence(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return &yaml.Node{Kind: yaml.SequenceNode}, nil
	}
	if document.Content[0].Kind != yaml.SequenceNode {
		return nil, errors.New("patch is not a YAML list")
	}
	return document.Content[0], nil
}

// userPatchDisables reports the ids the profile's own patch layer turns off.
func userPatchDisables(path string) (map[string]bool, error) {
	disabled := map[string]bool{}
	patch, err := readPatchSequence(path)
	if errors.Is(err, os.ErrNotExist) {
		return disabled, nil
	}
	if err != nil {
		return nil, err
	}
	for _, item := range patch.Content {
		if item.Kind != yaml.MappingNode || mappingValue(item, "insert") != nil {
			continue
		}
		if id := scalarValue(mappingValue(item, "id")); id != "" && disabledState(mappingValue(item, "disabled")) == loaderDisabled {
			disabled[id] = true
		}
	}
	return disabled, nil
}

// setUserPatchDisabled adds or removes `disabled: true` on the id-targeted row
// of the profile's patch layer. Other rows, keys and comments stay in place; a
// row that held only the disable is removed again on enable.
func setUserPatchDisabled(path, id string, disabled bool) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	if len(document.Content) == 0 {
		document = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}}
	}
	patch := document.Content[0]
	if patch.Kind != yaml.SequenceNode {
		return errors.New("the profile patch layer is not a YAML list")
	}
	rowIndex := -1
	for index, item := range patch.Content {
		if item.Kind == yaml.MappingNode && mappingValue(item, "insert") == nil && scalarValue(mappingValue(item, "id")) == id {
			rowIndex = index
		}
	}
	if disabled {
		if rowIndex >= 0 {
			setMappingValue(patch.Content[rowIndex], "disabled", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		} else {
			patch.Content = append(patch.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "id"}, {Kind: yaml.ScalarNode, Tag: "!!str", Value: id},
				{Kind: yaml.ScalarNode, Tag: "!!str", Value: "disabled"}, {Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"},
			}})
		}
		patch.Style &^= yaml.FlowStyle
	} else {
		if rowIndex < 0 {
			return nil
		}
		row := patch.Content[rowIndex]
		deleteMappingKey(row, "disabled")
		if len(row.Content) == 2 {
			patch.Content = append(patch.Content[:rowIndex], patch.Content[rowIndex+1:]...)
			// A comment above the removed row stays in the file.
			if comment := joinComments(row.HeadComment, row.Content[0].HeadComment); comment != "" {
				if rowIndex < len(patch.Content) {
					patch.Content[rowIndex].HeadComment = joinComments(comment, patch.Content[rowIndex].HeadComment)
				} else {
					patch.HeadComment = joinComments(patch.HeadComment, comment)
				}
			}
		}
		if len(patch.Content) == 0 {
			patch.Style |= yaml.FlowStyle
		}
	}
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return writeFileAtomic(path, buffer.Bytes())
}

func disabledState(value *yaml.Node) loaderDisabledState {
	if value == nil {
		return loaderEnabled
	}
	if value.Kind == yaml.ScalarNode && value.ShortTag() == "!!bool" {
		if strings.EqualFold(value.Value, "true") {
			return loaderDisabled
		}
		return loaderEnabled
	}
	return loaderConditional
}

func mappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func setMappingValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content[index+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, value)
}

func deleteMappingKey(mapping *yaml.Node, key string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return
		}
	}
}

func joinComments(comments ...string) string {
	kept := comments[:0]
	for _, comment := range comments {
		if comment != "" {
			kept = append(kept, comment)
		}
	}
	return strings.Join(kept, "\n")
}

func scalarValue(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.ScalarNode {
		return ""
	}
	return node.Value
}
