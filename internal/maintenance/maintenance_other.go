//go:build !windows

package maintenance

func installerInProgress() bool { return false }

func acquireInstallerBoundary() (func(), error) { return func() {}, nil }

func showInstallerBusy() {}
