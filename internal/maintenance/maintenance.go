// Package maintenance exposes the narrow process boundary used while a
// Windows installer owns the current user's application files.
package maintenance

// InstallerInProgress reports whether the per-user installer mutex is held.
// Non-Windows builds return false because their installer lifecycle is not
// implemented by this package yet.
func InstallerInProgress() bool { return installerInProgress() }

// ShowInstallerBusy gives a newly launched GUI process an actionable result
// while the installer owns the current user's application files. Console
// callers continue to receive the typed busy error from their command path.
func ShowInstallerBusy() { showInstallerBusy() }
