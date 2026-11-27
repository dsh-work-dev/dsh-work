//go:build darwin && cgo

package macowner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
)

const installedManifest = "dsh-cef-publication.json"
const installedBrowser = "Frameworks/DSHWorkBrowser.app"

// OpenInstalledPublication opens the browser sealed inside the running app's
// signed bundle. All paths are relative to Contents so the app can move.
func OpenInstalledPublication(ctx context.Context, executable string) (Publication, error) {
	var publication Publication
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return publication, err
	}
	macOS := filepath.Dir(executable)
	contents := filepath.Dir(macOS)
	app := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" || filepath.Ext(app) != ".app" {
		return publication, os.ErrNotExist
	}
	manifest := filepath.Join(contents, "Resources", installedManifest)
	if _, err = os.Lstat(manifest); err != nil {
		return publication, err
	}
	if resolved, err := filepath.EvalSymlinks(manifest); err != nil || resolved != manifest {
		return publication, errors.New("installed CEF resource path rejected")
	}
	// Verify the outer seal before trusting its manifest or any nested code.
	if err = exec.CommandContext(ctx, "/usr/bin/codesign", "--verify", "--strict", app).Run(); err != nil {
		return publication, errors.New("installed CEF host signature rejected")
	}
	if err = readPublication(manifest, &publication); err != nil {
		return publication, err
	}
	if publication.App != installedBrowser {
		return publication, errors.New("installed CEF bundle path rejected")
	}
	expected := []string{"", " (Alerts)", " (GPU)", " (Plugin)", " (Renderer)"}
	helpers := make([]string, 0, len(expected))
	for _, suffix := range expected {
		name := browserExecutable + " Helper" + suffix
		helpers = append(helpers, filepath.Join(installedBrowser, "Contents", "Frameworks", name+".app", "Contents", "MacOS", name))
	}
	slices.Sort(helpers)
	slices.Sort(publication.Helpers)
	if !slices.Equal(publication.Helpers, helpers) {
		return publication, errors.New("installed CEF helper layout rejected")
	}
	paths := []*string{&publication.App, &publication.Executable, &publication.Owner}
	for i := range publication.Helpers {
		paths = append(paths, &publication.Helpers[i])
	}
	for _, path := range paths {
		if !filepath.IsLocal(*path) || filepath.Clean(*path) != *path {
			return publication, errors.New("installed CEF component path rejected")
		}
		*path = filepath.Join(contents, *path)
		if resolved, err := filepath.EvalSymlinks(*path); err != nil || resolved != *path {
			return publication, errors.New("installed CEF component path rejected")
		}
	}
	return verifyPublication(ctx, publication)
}

// OpenHostPublication shares the installed locator with isolated preview runs.
func OpenHostPublication(ctx context.Context, previewManifest string) (Publication, error) {
	executable, err := os.Executable()
	if err != nil {
		return Publication{}, err
	}
	publication, err := OpenInstalledPublication(ctx, executable)
	if errors.Is(err, os.ErrNotExist) && previewManifest != "" {
		return OpenPublication(ctx, previewManifest)
	}
	return publication, err
}
