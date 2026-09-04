//go:build windows

package windows

import (
	"fmt"
	"path/filepath"

	"github.com/local/dsh-work/internal/settings"
	win "golang.org/x/sys/windows"
)

// FileReplacer publishes a completed file over an existing destination using
// the Windows replace primitive. It is kept in the native adapter because the
// standard os.Rename contract does not replace an existing file on Windows.
type FileReplacer struct{}

func NewFileReplacer() settings.FileReplacer {
	return FileReplacer{}
}

func (FileReplacer) Replace(source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("source and destination are required")
	}
	sourcePath, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	destinationPath, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	sourcePtr, err := win.UTF16PtrFromString(sourcePath)
	if err != nil {
		return err
	}
	destinationPtr, err := win.UTF16PtrFromString(destinationPath)
	if err != nil {
		return err
	}
	return win.MoveFileEx(sourcePtr, destinationPtr, win.MOVEFILE_REPLACE_EXISTING|win.MOVEFILE_WRITE_THROUGH)
}

var _ settings.FileReplacer = FileReplacer{}
