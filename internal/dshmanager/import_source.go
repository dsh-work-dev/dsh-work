package dshmanager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/local/dsh-work/internal/lifecycle"
)

// importSource is a user-selected folder resolved to its DSH home and the
// locations where that home stores shared data.
type importSource struct {
	Home        string
	Profiles    []string
	Sessions    string
	Storages    string
	Attachments string
	Credentials string
}

func resolveImportSource(chosen string, managed DataDirectoryInfo) (importSource, error) {
	chosen = strings.TrimSpace(chosen)
	if !filepath.IsAbs(chosen) {
		return importSource{}, importSourceNotFound()
	}
	chosen = filepath.Clean(chosen)
	resolved, err := filepath.EvalSymlinks(chosen)
	if err != nil {
		return importSource{}, importSourceNotFound()
	}
	resolved = filepath.Clean(resolved)
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return importSource{}, importSourceNotFound()
	}

	home := ""
	if directoryExists(filepath.Join(resolved, "profiles")) {
		home = resolved
	} else {
		environment := filepath.Join(resolved, "environment")
		if directoryExists(filepath.Join(environment, "profiles")) {
			home = environment
		}
	}
	if home == "" {
		return importSource{}, importSourceNotFound()
	}

	managedPath := canonicalImportPath(managed.Path)
	if importPathsOverlap(home, managedPath) {
		return importSource{}, failure(
			lifecycle.ErrorProfileInvalid,
			"Choose a folder other than DSH Work's own data.",
			"the selected folder contains or is inside the managed DSH Work data directory",
		)
	}

	source := importSource{
		Home:        home,
		Sessions:    filepath.Join(home, "sessions"),
		Storages:    filepath.Join(home, "storages"),
		Attachments: filepath.Join(home, "attachments", "v1"),
		Credentials: filepath.Join(home, ".credentials.yaml"),
	}
	applyLegacyImportRedirects(filepath.Join(home, "user-data.patch.json"), &source)

	profiles, err := importProfiles(filepath.Join(home, "profiles"))
	if err != nil {
		return importSource{}, importSourceNotFound()
	}
	source.Profiles = profiles
	return source, nil
}

func importSourceNotFound() error {
	return failure(
		lifecycle.ErrorProfileNotFound,
		"This folder is not a DSH data folder.",
		"the selected folder does not contain a readable profiles directory",
	)
}

func importProfiles(path string) ([]string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	profiles := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if name == "node_modules" || strings.HasPrefix(name, ".") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, err := os.Lstat(filepath.Join(path, name))
		if err != nil {
			continue
		}
		if info.Mode().IsDir() && info.Mode()&os.ModeSymlink == 0 {
			profiles = append(profiles, name)
		}
	}
	sort.Strings(profiles)
	return profiles, nil
}

type legacyImportRedirect struct {
	ID     string            `json:"id"`
	Config map[string]string `json:"config"`
}

func applyLegacyImportRedirects(path string, source *importSource) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var redirects []legacyImportRedirect
	if err := json.Unmarshal(data, &redirects); err != nil {
		return
	}
	for _, redirect := range redirects {
		switch redirect.ID {
		case "session-persistence-jsonl":
			if value := absoluteRedirect(redirect.Config, "root"); value != "" {
				source.Sessions = value
			}
		case "storage-json":
			if value := absoluteRedirect(redirect.Config, "root"); value != "" {
				source.Storages = value
			}
		case "attachment-local":
			if value := absoluteRedirect(redirect.Config, "dshHome"); value != "" {
				source.Attachments = filepath.Join(value, "attachments", "v1")
			}
		case "credentials":
			if value := absoluteRedirect(redirect.Config, "path"); value != "" {
				source.Credentials = value
			}
		}
	}
}

func absoluteRedirect(config map[string]string, key string) string {
	value := strings.TrimSpace(config[key])
	if value == "" || !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func canonicalImportPath(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(resolved)
	}
	return absolute
}

func importPathsOverlap(left, right string) bool {
	return importPathInside(left, right) || importPathInside(right, left)
}

func importPathInside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(relative)
}
