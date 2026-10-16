//go:build !windows

package selfupdate

import (
	"fmt"

	"github.com/local/dsh-work/internal/release"
)

// verifyInstallerIdentity is only meaningful for the Windows installer release
// unit. Prepare refuses a non-Windows host before it reaches this function, so
// reaching it is an implementation error rather than a user-visible condition.
func verifyInstallerIdentity(string, release.Version, Options) error {
	return fmt.Errorf("%w: installer identity is only defined on Windows", ErrUnsupportedPlatform)
}

// installDir returns the recorded install directory. Only the Windows release
// unit has an installer today, so there is nothing to resolve.
func installDir() (string, error) {
	return "", fmt.Errorf("%w: installer directories are only defined on Windows", ErrUnsupportedPlatform)
}
