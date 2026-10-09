package nativeui

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// SaveSessionLogArchive writes the DSH export to a sibling temporary file and
// only replaces the chosen destination after the stream completes successfully.
func SaveSessionLogArchive(app *application.App, filename string, write func(io.Writer) error) (bool, error) {
	path, err := app.Dialog.SaveFile().SetFilename(filename).AddFilter("Session archive", "*.zip").CanCreateDirectories(true).PromptForSingleSelection()
	if err != nil {
		if err.Error() == "cancelled by user" {
			return false, nil
		}
		return false, err
	}
	if path == "" {
		return false, nil
	}
	if !filepath.IsAbs(path) {
		return false, errors.New("session export destination must be absolute")
	}
	if err := writeSessionExportAtomically(path, write); err != nil {
		return false, err
	}
	return true, nil
}

func writeSessionExportAtomically(destination string, write func(io.Writer) error) (resultErr error) {
	if write == nil {
		return errors.New("session export writer is required")
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".dsh-session-export-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	closed := false
	defer func() {
		if !closed {
			if closeErr := temporary.Close(); resultErr == nil && closeErr != nil {
				resultErr = closeErr
			}
		}
	}()
	if err := write(temporary); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	closeErr := temporary.Close()
	closed = true
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return err
	}
	return nil
}
