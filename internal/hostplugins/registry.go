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

var packageNames = []string{"shell", "account", "pet"}
var safeVersion = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9.+-]{0,63}$")
var packageFiles = map[string][]string{
	"shell":   {"package.json", "host.js", "client.js", "README.md"},
	"account": {"package.json", "host.js", "client.js", "README.md"},
	"pet":     {"package.json", "host.js", "client.js", "README.md"},
}

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
		files := make(map[string][]byte, len(packageFiles[name]))
		for _, filename := range packageFiles[name] {
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
		for _, filename := range packageFiles[name] {
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

// writeImmutable writes one file of a content-addressed version directory.
// Matching content is left alone. Anything else at that path can only be a
// partial write from an interrupted launch, so it is replaced atomically.
func writeImmutable(path string, data []byte) error {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, path)
}
