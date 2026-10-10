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

// ProfilePlugin is one installed third-party bundle of a profile; Enabled is
// whether the profile manifest currently selects it.
type ProfilePlugin struct {
	Package string
	Enabled bool
}

// ProfilePlugins lists the third-party bundles installed in the resolved
// launch's profile, sorted by package name, so a startup failure can offer
// each one without a running Worker. Dependencies that are not DSH bundles
// are left out because there is nothing to disable.
func (m *Manager) ProfilePlugins(ctx context.Context, launch ResolvedLaunch) ([]ProfilePlugin, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	profilePath := filepath.Join(launch.DataDirectory.Path, "profiles", launch.Target.Profile.Name)
	manifest, err := readProfileBundleManifest(profilePath)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(manifest.Bundles()))
	for _, bundle := range manifest.Bundles() {
		selected[bundle] = true
	}
	plugins := make([]ProfilePlugin, 0, len(manifest.Dependencies))
	for name := range manifest.Dependencies {
		if !validPackageName(name) || IsCorePluginPackage(name) {
			continue
		}
		if selected[name] || installedPackageHasBundle(profilePath, name) {
			plugins = append(plugins, ProfilePlugin{Package: name, Enabled: selected[name]})
		}
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].Package < plugins[j].Package })
	return plugins, nil
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
