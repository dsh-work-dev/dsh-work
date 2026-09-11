package dshadapter

import (
	"errors"
	"github.com/google/safeopen"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

// safeopen exposes NTSTATUS directly on Windows. Convert it to the usual
// filesystem error so optional inputs and exclusive temporary files work.
func versionFileError(err error) error {
	var status windows.NTStatus
	if errors.As(err, &status) {
		return status.Errno()
	}
	return err
}

func openVersionFile(root *os.Root, name string, flags int) (*os.File, error) {
	// Go 1.25's OBJ_DONT_REPARSE rejects ordinary files in this Windows
	// AppData environment. safeopen uses component-wise no-follow handles;
	// keep os.Root for rooted rename/removal and use no unchecked path fallback.
	// Start at the data home, so safeopen also checks profiles/<name> rather
	// than trusting potentially replaced intermediate profile directories.
	home := filepath.Dir(filepath.Dir(root.Name()))
	rel := filepath.Join("profiles", filepath.Base(root.Name()), name)
	f, err := safeopen.OpenFileBeneath(home, rel, flags, 0600)
	return f, versionFileError(err)
}
