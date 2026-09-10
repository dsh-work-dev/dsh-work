package nativeui

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// ChooseDirectory is the application-facing native directory picker contract.
// Cancellation returns an empty selection on every platform.
func ChooseDirectory(app *application.App, currentPath string) (string, error) {
	dialog := app.Dialog.OpenFile().CanChooseDirectories(true).CanChooseFiles(false).CanCreateDirectories(true)
	if directory := existingDirectory(currentPath); directory != "" {
		dialog.SetDirectory(directory)
	}
	path, err := dialog.PromptForSingleSelection()
	// Wails beta.16 leaks internal/cfd.ErrorCancelled on Windows; that sentinel
	// is not publicly importable. Keep its exact pinned error compatibility here,
	// rather than teaching every UI control to interpret native error messages.
	if runtime.GOOS == "windows" && err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}
	return path, err
}

func ChooseProfileBackup(app *application.App) (string, error) {
	path, err := app.Dialog.OpenFile().CanChooseDirectories(false).CanChooseFiles(true).AddFilter("Profile archive", "*.zip").PromptForSingleSelection()
	if runtime.GOOS == "windows" && err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}
	return path, err
}

// A pending/default location may not exist yet. Start at its nearest existing
// parent, leaving empty or relative input to the system dialog's own default.
func existingDirectory(path string) string {
	if !filepath.IsAbs(path) {
		return ""
	}
	path = filepath.Clean(path)
	for {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return path
		}
		parent := filepath.Dir(path)
		if parent == path {
			return ""
		}
		path = parent
	}
}

func SaveProfileArchive(app *application.App, filename string) (string, error) {
	path, err := app.Dialog.SaveFile().SetFilename(filename).AddFilter("Profile archive", "*.zip").CanCreateDirectories(true).PromptForSingleSelection()
	if runtime.GOOS == "windows" && err != nil && err.Error() == "cancelled by user" {
		return "", nil
	}
	return path, err
}
