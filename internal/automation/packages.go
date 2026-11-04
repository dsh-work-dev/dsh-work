package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	if !c.Valid() {
		return "", errors.New("unknown automation capability")
	}
	b, err := os.ReadFile(PackagePointer(cache, c))
	if err != nil {
		return "", err
	}
	var p PackagePublication
	if json.Unmarshal(b, &p) != nil || p.Revision != PackageRevision(c) {
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
