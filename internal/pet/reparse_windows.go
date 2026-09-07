//go:build windows

package pet

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

func isReparsePoint(path string) bool {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return true
	}
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return true
	}
	attributes, err := windows.GetFileAttributes(name)
	if err != nil {
		// An attribute lookup failure is not proof that the path is a regular
		// file. Fail closed so a disappearing or inaccessible path cannot pass
		// the package boundary as a trusted resource.
		return true
	}
	return attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0
}
