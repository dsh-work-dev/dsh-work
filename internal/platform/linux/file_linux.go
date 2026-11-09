//go:build linux

package linux

import "os"

// FileReplacer uses Linux's atomic same-filesystem rename-over-destination.
// Callers finish and sync the source before publication, as on Windows.
type FileReplacer struct{}

func (FileReplacer) Replace(source, destination string) error { return os.Rename(source, destination) }
