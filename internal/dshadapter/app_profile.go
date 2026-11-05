package dshadapter

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const appProfilePatchTemplate = `# Your patch layer for this dsh profile, applied after every bundle layer:
# a top-level YAML array of loader patch entries (id-targeted config
# overrides, disables, and insert lists; ` + "`!!js`" + ` expressions allowed).
# dsh-work enables browser-facing capabilities, keeps file-write observation,
# and prevents optional diagnostics or Session history from leaving the machine.
- id: ui-sidebar-browser
  disabled: false
- id: fs-observation-policy
  disabled: false
- id: web-fetch-http
  disabled: false
- id: desktop-product-telemetry
  disabled: true
- id: product-analytics
  disabled: true
- id: session-telemetry-otel
  disabled: true
- id: session-log-deepseek
  disabled: true
- id: plugin-package-inventory-deepseek
  disabled: true
`

const appProfileWorkspace = `packages:
  - .

nodeLinker: hoisted
autoInstallPeers: false
`

// AppProfileBundles is the bundle list of the dsh-work profile: the same
// layers as DSH's web template, which official Desktop also uses.
func (a *Adapter) AppProfileBundles() []string {
	return []string{"@deepseek-ai/dsh-base", "@deepseek-ai/dsh-web-app"}
}

// InitializeAppProfile writes the files DSH's initProfile would write for
// profilePath, creating only those that are missing.
func (a *Adapter) InitializeAppProfile(profilePath string) error {
	if err := os.MkdirAll(profilePath, 0o700); err != nil {
		return err
	}
	if err := ensureAppProfilePatch(filepath.Join(profilePath, "cordis.patch.yml")); err != nil {
		return err
	}
	if err := writeAppProfileFile(filepath.Join(profilePath, "pnpm-workspace.yaml"), []byte(appProfileWorkspace)); err != nil {
		return err
	}
	manifest := struct {
		Name         string            `json:"name"`
		Private      bool              `json:"private"`
		Dependencies map[string]string `json:"dependencies"`
		DSH          struct {
			Profile struct {
				Bundles []string `json:"bundles"`
			} `json:"profile"`
		} `json:"dsh"`
	}{
		Name:         "dsh-profile-dsh-work",
		Private:      true,
		Dependencies: map[string]string{},
	}
	manifest.DSH.Profile.Bundles = a.AppProfileBundles()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeAppProfileFile(filepath.Join(profilePath, "package.json"), data)
}

// ensureAppProfilePatch applies dsh-work's defaults as an id-targeted layer.
// Browser, web fetch, and file observation defaults yield to an explicit user choice.
// Reporting rows are always pinned off.
func ensureAppProfilePatch(path string) error {
	if err := writeAppProfileFile(path, []byte(appProfilePatchTemplate)); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse dsh-work profile patch: %w", err)
	}
	if len(document.Content) == 0 {
		document.Content = []*yaml.Node{{Kind: yaml.SequenceNode, Tag: "!!seq"}}
	}
	sequence := document.Content[0]
	if sequence.Kind != yaml.SequenceNode {
		return errors.New("dsh-work profile patch must be a YAML list")
	}
	changed := false
	for _, id := range []string{"ui-sidebar-browser", "fs-observation-policy", "web-fetch-http"} {
		if appProfilePatchDisabledOverride(sequence, id) == nil {
			sequence.Content = append(sequence.Content, appProfilePatchEntry(id, false))
			changed = true
		}
	}
	for _, id := range []string{
		"desktop-product-telemetry",
		"product-analytics",
		"session-telemetry-otel",
		"session-log-deepseek",
		"plugin-package-inventory-deepseek",
	} {
		if !appProfilePatchDisabledTrue(sequence, id) {
			sequence.Content = append(sequence.Content, appProfilePatchEntry(id, true))
			changed = true
		}
	}
	if !changed {
		return nil
	}
	sequence.Style &^= yaml.FlowStyle
	var encoded bytes.Buffer
	encoder := yaml.NewEncoder(&encoded)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		_ = encoder.Close()
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	return replaceAppProfilePatch(path, encoded.Bytes())
}

func appProfilePatchEntry(id string, disabled bool) *yaml.Node {
	value := "false"
	if disabled {
		value = "true"
	}
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Tag:  "!!map",
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "id"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: id},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "disabled"},
			{Kind: yaml.ScalarNode, Tag: "!!bool", Value: value},
		},
	}
}

func appProfilePatchDisabledOverride(sequence *yaml.Node, id string) *yaml.Node {
	var override *yaml.Node
	for _, item := range sequence.Content {
		if appProfilePatchID(item) == id {
			if disabled := appProfilePatchValue(item, "disabled"); disabled != nil {
				override = disabled
			}
		}
		inserted := appProfilePatchValue(item, "insert")
		if inserted == nil || inserted.Kind != yaml.SequenceNode {
			continue
		}
		for _, row := range inserted.Content {
			if appProfilePatchID(row) == id {
				if disabled := appProfilePatchValue(row, "disabled"); disabled != nil {
					override = disabled
				}
			}
		}
	}
	return override
}

func appProfilePatchDisabledTrue(sequence *yaml.Node, id string) bool {
	value := appProfilePatchDisabledOverride(sequence, id)
	return value != nil && value.Kind == yaml.ScalarNode && value.ShortTag() == "!!bool" && strings.EqualFold(value.Value, "true")
}

func appProfilePatchID(mapping *yaml.Node) string {
	id := appProfilePatchValue(mapping, "id")
	if id == nil || id.Kind != yaml.ScalarNode {
		return ""
	}
	return id.Value
}

func appProfilePatchValue(mapping *yaml.Node, key string) *yaml.Node {
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

func replaceAppProfilePatch(path string, contents []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("dsh-work profile patch is not a regular file: %s", path)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".cordis.patch.yml.dsh-work-tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		_ = temporary.Close()
		if !keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keepTemporary = true
	return nil
}

func writeAppProfileFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err = file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}
