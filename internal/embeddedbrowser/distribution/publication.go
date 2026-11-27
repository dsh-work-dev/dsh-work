package distribution

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	Directory = "embedded-cef"
	Manifest  = "publication.json"
)

//go:embed cef-lock.json
var cefLockJSON []byte

var cefLock = func() struct {
	CEFVersion    string   `json:"cefVersion"`
	RequiredFiles []string `json:"requiredFiles"`
} {
	var lock struct {
		CEFVersion    string   `json:"cefVersion"`
		RequiredFiles []string `json:"requiredFiles"`
	}
	if err := json.Unmarshal(cefLockJSON, &lock); err != nil || lock.CEFVersion == "" || len(lock.RequiredFiles) == 0 {
		panic("invalid embedded CEF distribution lock")
	}
	return lock
}()

var CEFVersion = cefLock.CEFVersion
var required = cefLock.RequiredFiles

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type Publication struct {
	SchemaVersion int    `json:"schemaVersion"`
	AppVersion    string `json:"appVersion"`
	CEFVersion    string `json:"cefVersion"`
	Architecture  string `json:"architecture"`
	Files         []File `json:"files"`
}

type State string

const (
	Ready               State = "ready"
	Missing             State = "missing"
	Incompatible        State = "incompatible"
	Corrupt             State = "corrupt"
	UnsupportedRuntime  State = "unsupported-runtime"
	UnsupportedPlatform State = "unsupported-platform"
)

type Availability struct {
	State State  `json:"state"`
	Code  string `json:"code"`
}

type Result struct {
	State      State
	Executable string
	Reason     error
}

// Inspect accepts only the exact application-owned publication adjacent to the
// running executable. An environment override is never a release locator.
func Inspect(applicationExe, applicationVersion, architecture, expectedManifestSHA256 string) Result {
	root := filepath.Join(filepath.Dir(applicationExe), Directory)
	data, err := os.ReadFile(filepath.Join(root, Manifest))
	if errors.Is(err, os.ErrNotExist) {
		return Result{State: Missing, Reason: err}
	}
	if err != nil {
		return Result{State: Corrupt, Reason: err}
	}
	if len(expectedManifestSHA256) != 64 {
		return Result{State: Incompatible, Reason: errors.New("CEF_PUBLICATION_UNBOUND")}
	}
	manifestDigest := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(manifestDigest[:]), expectedManifestSHA256) {
		return Result{State: Corrupt, Reason: errors.New("CEF_PUBLICATION_MANIFEST_DIGEST_MISMATCH")}
	}
	var pub Publication
	if err = json.Unmarshal(data, &pub); err != nil {
		return Result{State: Corrupt, Reason: err}
	}
	if pub.SchemaVersion != 1 || pub.AppVersion != applicationVersion || pub.CEFVersion != CEFVersion || pub.Architecture != architecture {
		return Result{State: Incompatible, Reason: errors.New("CEF_PUBLICATION_VERSION_MISMATCH")}
	}
	seen := make(map[string]bool, len(pub.Files))
	for _, f := range pub.Files {
		path := filepath.FromSlash(f.Path)
		if f.Path == "" || strings.Contains(f.Path, "\\") || strings.Contains(f.Path, ":") || filepath.IsAbs(path) || filepath.VolumeName(path) != "" || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) || seen[f.Path] || f.Size < 0 || len(f.SHA256) != 64 {
			return Result{State: Corrupt, Reason: fmt.Errorf("CEF_PUBLICATION_INVALID_FILE: %q", f.Path)}
		}
		seen[f.Path] = true
		full := filepath.Join(root, path)
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() != f.Size {
			return Result{State: Corrupt, Reason: fmt.Errorf("CEF_PUBLICATION_MISSING_FILE: %s", f.Path)}
		}
		file, err := os.Open(full)
		if err != nil {
			return Result{State: Corrupt, Reason: err}
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), f.SHA256) {
			return Result{State: Corrupt, Reason: fmt.Errorf("CEF_PUBLICATION_DIGEST_MISMATCH: %s", f.Path)}
		}
	}
	for _, path := range required {
		if !seen[path] {
			return Result{State: Corrupt, Reason: fmt.Errorf("CEF_PUBLICATION_MISSING_FILE: %s", path)}
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("CEF_PUBLICATION_LINK_REJECTED")
		}
		if entry.IsDir() || path == filepath.Join(root, Manifest) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if !seen[name] {
			return fmt.Errorf("CEF_PUBLICATION_UNLISTED_FILE: %s", name)
		}
		return nil
	}); err != nil {
		return Result{State: Corrupt, Reason: err}
	}
	return Result{State: Ready, Executable: filepath.Join(root, "dsh_cef_broker.exe")}
}
