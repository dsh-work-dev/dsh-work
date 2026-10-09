package hostplugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
)

type Package struct {
	ID        string
	Name      string
	Directory string
}

var packageNames = []string{"shell", "account", "activity"}
var safeVersion = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$")
var packageFiles = []string{"package.json", "host.js", "client.js", "README.md"}

// Install publishes embedded DSH plugins below a content-addressed application
// version directory. These are application-owned files, not npm packages; an
// immutable path prevents DSH's package resolver from serving mixed revisions.
func Install(root, version string) (map[string]Package, error) {
	if root == "" || !safeVersion.MatchString(version) {
		return nil, errors.New("host plugin root and safe application version are required")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve host plugin root: %w", err)
	}
	contents := make(map[string]map[string][]byte, len(packageNames))
	hash := sha256.New()
	for _, name := range packageNames {
		files := make(map[string][]byte, len(packageFiles))
		for _, filename := range packageFiles {
			data, err := pluginAssets.ReadFile(path.Join(name, filename))
			if err != nil {
				return nil, fmt.Errorf("read embedded %s/%s: %w", name, filename, err)
			}
			if filename == "package.json" {
				manifest := map[string]any{}
				if err := json.Unmarshal(data, &manifest); err != nil {
					return nil, fmt.Errorf("parse embedded %s manifest: %w", name, err)
				}
				if manifest["name"] != "@dsh-work/"+name {
					return nil, fmt.Errorf("embedded %s manifest has an unexpected package name", name)
				}
				manifest["version"] = version
				data, err = json.MarshalIndent(manifest, "", "  ")
				if err != nil {
					return nil, fmt.Errorf("encode %s manifest: %w", name, err)
				}
				data = append(data, byte(10))
			}
			files[filename] = data
			hash.Write([]byte(name))
			hash.Write([]byte{0})
			hash.Write([]byte(filename))
			hash.Write([]byte{0})
			hash.Write(data)
		}
		contents[name] = files
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	versionRoot := filepath.Join(root, "versions", version, digest)
	if err := os.MkdirAll(versionRoot, 0700); err != nil {
		return nil, fmt.Errorf("create host plugin version root: %w", err)
	}
	result := make(map[string]Package, len(packageNames))
	for _, name := range packageNames {
		directory := filepath.Join(versionRoot, name)
		if err := os.MkdirAll(directory, 0700); err != nil {
			return nil, fmt.Errorf("create %s package directory: %w", name, err)
		}
		for _, filename := range packageFiles {
			path := filepath.Join(directory, filename)
			if err := writeImmutable(path, contents[name][filename]); err != nil {
				return nil, fmt.Errorf("install %s/%s: %w", name, filename, err)
			}
		}
		result[name] = Package{
			ID:        "dsh-work-" + name,
			Name:      "@dsh-work/" + name,
			Directory: directory,
		}
	}
	return result, nil
}

func writeImmutable(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !bytes.Equal(existing, data) {
			return errors.New("refusing to change files in an installed version")
		}
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			existing, readErr := os.ReadFile(path)
			if readErr == nil && bytes.Equal(existing, data) {
				return nil
			}
			return errors.New("refusing to change files in an installed version")
		}
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		os.Remove(path)
		return err
	}
	return file.Close()
}
