//go:build windows

package maintenance

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Global spans RDP and fast-user-switching sessions. The token SID suffix
// keeps independent Windows users on separate lifecycle objects without
// trusting a mutable environment variable such as USERNAME.
const installerMutexPrefix = `Global\dsh-work-installer-`

func installerMutexName() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	if user == nil || user.User.Sid == nil {
		return "", errors.New("current process token has no user SID")
	}
	return installerMutexPrefix + user.User.Sid.String(), nil
}

func installerInProgress() bool {
	nameString, err := installerMutexName()
	if err != nil {
		return true
	}
	name, err := windows.UTF16PtrFromString(nameString)
	if err != nil {
		return true
	}
	handle, err := windows.OpenMutex(windows.SYNCHRONIZE, false, name)
	if err == nil {
		_ = windows.CloseHandle(handle)
		return true
	}
	// An access failure is treated as busy so a restricted process cannot
	// race an installer it is unable to inspect.
	return !errors.Is(err, windows.ERROR_FILE_NOT_FOUND)
}

func acquireInstallerBoundary() (func(), error) {
	nameString, err := installerMutexName()
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(nameString)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, ErrInstallerBusy
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = windows.CloseHandle(handle) }, nil
}

func showInstallerBusy() {
	message, messageErr := windows.UTF16PtrFromString("DSH Work is being updated. Wait for the installer to finish, then reopen DSH Work.")
	caption, captionErr := windows.UTF16PtrFromString("DSH Work")
	if messageErr != nil || captionErr != nil {
		return
	}
	_, _ = windows.MessageBox(0, message, caption, windows.MB_OK|windows.MB_ICONINFORMATION|windows.MB_SYSTEMMODAL)
}
