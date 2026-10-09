package hostplugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Overlay owns the per-launch core overlay that mounts every host plugin
// without editing the user's profile. The plugin files are installed once per
// process; each launch only writes the small patch that carries that
// generation's configuration. One Worker generation runs at a time, so a new
// patch replaces the previous one.
type Overlay struct {
	root    string
	version string
	install func(root, version string) (map[string]Package, error)

	mu       sync.Mutex
	packages map[string]Package
}

var safeGeneration = regexp.MustCompile("^[A-Za-z0-9_-]{1,64}$")

// NewOverlay keeps installed plugins and launch patches below root.
func NewOverlay(root, version string) *Overlay {
	return &Overlay{root: root, version: version, install: Install}
}

// Prepare installs this application version's plugins and writes the patch
// that inserts them for generation. configs holds each plugin's per-launch
// configuration by package name (shell, account, pet).
func (o *Overlay) Prepare(generation string, configs map[string]map[string]string) (string, error) {
	if !safeGeneration.MatchString(generation) {
		return "", errors.New("host plugin overlay needs a safe generation")
	}
	root, err := filepath.Abs(o.root)
	if err != nil {
		return "", fmt.Errorf("resolve host plugin root: %w", err)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create host plugin root: %w", err)
	}
	packages, err := o.installed(root)
	if err != nil {
		return "", err
	}
	entries := make([]any, 0, len(packageNames))
	for _, name := range packageNames {
		pkg, ok := packages[name]
		if !ok {
			return "", fmt.Errorf("host plugin %s is unavailable", name)
		}
		config := configs[name]
		if config == nil {
			config = map[string]string{}
		}
		entries = append(entries, map[string]any{
			"id": pkg.ID, "name": moduleURL(filepath.Join(pkg.Directory, "host.js")), "config": config,
		})
	}
	// JSON is valid YAML; the launcher consumes a patch-list overlay.
	data, err := json.Marshal([]any{map[string]any{"insert": entries}})
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, "launch-"+generation+".patch.yml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("write host plugin overlay: %w", err)
	}
	removeOtherPatches(root, path)
	return path, nil
}

// installed installs and verifies the plugins on first use, then removes
// plugin versions this process does not use.
func (o *Overlay) installed(root string) (map[string]Package, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.packages != nil {
		return o.packages, nil
	}
	packages, err := o.install(root, o.version)
	if err != nil {
		return nil, fmt.Errorf("install host plugins: %w", err)
	}
	removeOtherVersions(root, o.version, filepath.Dir(packages[packageNames[0]].Directory))
	o.packages = packages
	return packages, nil
}

// Removal is best effort: a locked file is retried on the next launch.
func removeOtherPatches(root, currentPatch string) {
	patches, err := filepath.Glob(filepath.Join(root, "launch-*.patch.yml"))
	if err != nil {
		return
	}
	for _, patch := range patches {
		if patch != currentPatch {
			_ = os.Remove(patch)
		}
	}
}

func removeOtherVersions(root, currentVersion, currentDigestDir string) {
	versions, err := os.ReadDir(filepath.Join(root, "versions"))
	if err != nil {
		return
	}
	for _, version := range versions {
		versionDir := filepath.Join(root, "versions", version.Name())
		digests, err := os.ReadDir(versionDir)
		if err != nil {
			continue
		}
		for _, digest := range digests {
			candidate := filepath.Join(versionDir, digest.Name())
			if candidate != currentDigestDir {
				_ = os.RemoveAll(candidate)
			}
		}
		if version.Name() != currentVersion {
			_ = os.Remove(versionDir)
		}
	}
}

func moduleURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}
