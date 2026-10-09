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
)

// Overlay owns the per-launch core overlay that mounts every host plugin
// without editing the user's profile. One Worker generation runs at a time,
// so preparing a launch also removes the previous launch's patch and any
// plugin versions it no longer uses.
type Overlay struct {
	root    string
	version string
}

var safeGeneration = regexp.MustCompile("^[A-Za-z0-9_-]{1,64}$")

// NewOverlay keeps installed plugins and launch patches below root.
func NewOverlay(root, version string) *Overlay {
	return &Overlay{root: root, version: version}
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
	packages, err := Install(root, o.version)
	if err != nil {
		return "", fmt.Errorf("install host plugins: %w", err)
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
	o.prune(root, path, filepath.Dir(packages[packageNames[0]].Directory))
	return path, nil
}

// prune removes earlier launch patches and plugin version directories; it is
// best effort, so a locked file is retried on the next launch.
func (o *Overlay) prune(root, currentPatch, currentVersion string) {
	if patches, err := filepath.Glob(filepath.Join(root, "launch-*.patch.yml")); err == nil {
		for _, patch := range patches {
			if patch != currentPatch {
				_ = os.Remove(patch)
			}
		}
	}
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
			if candidate != currentVersion {
				_ = os.RemoveAll(candidate)
			}
		}
		if version.Name() != o.version {
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
