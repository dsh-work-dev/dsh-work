package dshmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
)

// profileBundleManifest is the part of a DSH profile manifest needed to inspect
// installed packages and selected bundle layers.
type profileBundleManifest struct {
	Dependencies map[string]string `json:"dependencies"`
	DSH          struct {
		Profile struct {
			Bundles *[]string `json:"bundles"`
		} `json:"profile"`
	} `json:"dsh"`
}

func (manifest profileBundleManifest) Bundles() []string {
	if manifest.DSH.Profile.Bundles == nil {
		return nil
	}
	return *manifest.DSH.Profile.Bundles
}

func readProfileBundleManifest(profilePath string) (profileBundleManifest, error) {
	data, err := readProfileManifestBytes(profilePath)
	if err != nil {
		return profileBundleManifest{}, err
	}
	var manifest profileBundleManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return profileBundleManifest{}, err
	}
	return manifest, nil
}

func readProfileManifestBytes(profilePath string) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(profilePath, "package.json"))
	if err != nil {
		return nil, err
	}
	if len(data) > maxProfileManifestBytes {
		return nil, errors.New("the profile manifest exceeds its size limit")
	}
	return data, nil
}

// ProfilePluginPackages lists third-party packages installed in the resolved
// launch's profile so startup failures can only name possible plugin causes.
func (m *Manager) ProfilePluginPackages(ctx context.Context, launch ResolvedLaunch) ([]string, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	manifest, err := readProfileBundleManifest(filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name))
	if err != nil {
		return nil, err
	}
	packages := make([]string, 0, len(manifest.Dependencies))
	for name := range manifest.Dependencies {
		if validPackageName(name) && !IsCorePluginPackage(name) {
			packages = append(packages, name)
		}
	}
	sort.Strings(packages)
	return packages, nil
}

// requireNoCurrentRunContext prevents package-manager operations from editing
// a profile while this Manager's active Run context may be using it.
func (m *Manager) requireNoCurrentRunContext() error {
	m.mu.RLock()
	current := m.current
	m.mu.RUnlock()
	if current != nil {
		return errors.New("plugin changes require a stopped Run context")
	}
	return nil
}
