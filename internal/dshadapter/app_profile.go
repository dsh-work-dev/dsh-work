package dshadapter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const appProfilePatchTemplate = `# Your patch layer for this dsh profile, applied after every bundle layer:
# a top-level YAML array of loader patch entries (id-targeted config
# overrides, disables, and insert lists; ` + "`!!js`" + ` expressions allowed).
[]
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
	if err := writeAppProfileFile(filepath.Join(profilePath, "cordis.patch.yml"), []byte(appProfilePatchTemplate)); err != nil {
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
