//go:build windows

package browserhost

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/local/dsh-work/internal/selfupdate"
	"github.com/local/dsh-work/internal/settings"
	"golang.org/x/sys/windows"
)

type profileLauncher struct{}

func (profileLauncher) Resolve(browser, executable, directory string) (settings.BrowserLaunchHint, error) {
	if !localAbsolute(executable) || !localAbsolute(directory) {
		return settings.BrowserLaunchHint{}, errors.New("BROWSER_PROFILE_DIRECTORY_INVALID")
	}
	expected := "chrome.exe"
	if browser == "edge" {
		expected = "msedge.exe"
	} else if browser != "chrome" {
		return settings.BrowserLaunchHint{}, ErrUnsupported
	}
	if !strings.EqualFold(filepath.Base(executable), expected) {
		return settings.BrowserLaunchHint{}, errors.New("BROWSER_PROFILE_LAUNCH_UNAVAILABLE")
	}
	return ResolveProfileDirectory(executable, directory)
}
func (p profileLauncher) Open(ctx context.Context, browser string, h settings.BrowserLaunchHint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := p.Resolve(browser, h.Executable, filepath.Join(h.UserDataDirectory, h.ProfileDirectory)); err != nil {
		return err
	}
	publisher := "Google LLC"
	if browser == "edge" {
		publisher = "Microsoft Corporation"
	}
	if err := selfupdate.VerifyAuthenticodePublisher(h.Executable, publisher); err != nil {
		return errors.New("BROWSER_PROFILE_LAUNCH_UNAVAILABLE")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	args := []string{"--user-data-dir=" + h.UserDataDirectory, "--profile-directory=" + h.ProfileDirectory, "--no-first-run", "--no-default-browser-check"}
	for i := range args {
		args[i] = syscall.EscapeArg(args[i])
	}
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(h.Executable)
	if err != nil {
		return err
	}
	params, err := windows.UTF16PtrFromString(strings.Join(args, " "))
	if err != nil {
		return err
	}
	// The browser remains user-owned. Closing a conversation must not kill it.
	// SW_SHOWNOACTIVATE asks Windows to show without taking the foreground.
	if err = windows.ShellExecute(0, verb, file, params, nil, 4); err != nil {
		return errors.New("BROWSER_PROFILE_LAUNCH_FAILED")
	}
	return nil
}
