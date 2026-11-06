package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	browserextension "github.com/local/dsh-work/browser-extension"
	"os"
	"path/filepath"
	"strings"
)

// PackageRevision identifies a frozen dependency set, independently of DSH.
func PackageRevision(c Capability) string {
	files, _ := DependencyFiles(c)
	sum := sha256.Sum256(files["package-lock.json"])
	return hex.EncodeToString(sum[:16])
}

func ExtensionComponent(directory string) Component {
	r := Component{ID: "browser-extension", Name: "DSH Work browser extension", Capability: Browser, Location: directory, ExpectedSource: browserextension.SourceIdentity, ManagedWithApp: true, BackendAvailable: true, State: "missing"}
	var manifest struct {
		Version string `json:"version"`
	}
	if b, e := browserextension.Files.ReadFile("manifest.json"); e == nil && json.Unmarshal(b, &manifest) == nil {
		r.SupportedVersion = manifest.Version
	}
	if b, e := os.ReadFile(filepath.Join(directory, "manifest.json")); e == nil && json.Unmarshal(b, &manifest) == nil {
		r.InstalledVersion = manifest.Version
		r.State = "prepared"
	}
	if b, e := os.ReadFile(filepath.Join(directory, "bundle-source.txt")); e == nil {
		r.Source = strings.TrimSpace(string(b))
	}
	if r.InstalledVersion != "" && r.Source != browserextension.SourceIdentity {
		r.State = "update"
	}
	return r
}

type PackagePublication struct {
	Revision  string `json:"revision"`
	Directory string `json:"directory"`
}

func PackageRoot(cache string, c Capability) string {
	return filepath.Join(cache, "automation", string(c))
}
func PackagePointer(cache string, c Capability) string {
	return filepath.Join(PackageRoot(cache, c), "current.json")
}

// PublishedPackage never resolves outside this capability's owned store.
func PublishedPackage(cache string, c Capability) (string, error) {
	return publishedPackage(cache, c, true)
}

// InstalledPackage resolves the owned publication for read-only inventory, including an older lock.
func InstalledPackage(cache string, c Capability) (string, error) {
	return publishedPackage(cache, c, false)
}

func publishedPackage(cache string, c Capability, current bool) (string, error) {
	if !c.Valid() {
		return "", errors.New("unknown automation capability")
	}
	b, err := os.ReadFile(PackagePointer(cache, c))
	if err != nil {
		return "", err
	}
	var p PackagePublication
	if json.Unmarshal(b, &p) != nil || (current && p.Revision != PackageRevision(c)) {
		return "", errors.New("AUTOMATION_PACKAGE_VERSION_MISMATCH")
	}
	root := PackageRoot(cache, c)
	target := filepath.Clean(filepath.Join(root, p.Directory))
	rel, err := filepath.Rel(root, target)
	if err != nil || filepath.IsAbs(p.Directory) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("AUTOMATION_PACKAGE_PATH_INVALID")
	}
	realRoot, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	realTarget, e := filepath.EvalSymlinks(target)
	if e != nil {
		return "", e
	}
	realRel, e := filepath.Rel(realRoot, realTarget)
	if e != nil || realRel == "." || realRel == ".." || strings.HasPrefix(realRel, ".."+string(filepath.Separator)) {
		return "", errors.New("AUTOMATION_PACKAGE_PATH_INVALID")
	}
	return target, nil
}
