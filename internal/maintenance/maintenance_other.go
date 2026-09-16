//go:build !windows

package maintenance

func installerInProgress() bool { return false }

func showInstallerBusy() {}
