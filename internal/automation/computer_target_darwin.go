//go:build darwin && cgo

package automation

/*
#cgo LDFLAGS: -framework Foundation -framework CoreGraphics -framework Security
#include "computer_target_darwin.h"
*/
import "C"

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

func InspectComputerTarget(pid uint32, window uint64) (ComputerTarget, error) {
	if pid == 0 || pid > math.MaxInt32 || window == 0 || window > math.MaxUint32 {
		return ComputerTarget{}, errors.New("COMPUTER_TARGET_UNAVAILABLE")
	}
	var native C.dsh_computer_target
	switch C.dsh_inspect_computer_target(C.uint32_t(pid), C.uint32_t(window), &native) {
	case -1:
		return ComputerTarget{}, errors.New("COMPUTER_ELEVATED_TARGET_BLOCKED")
	case 1:
	default:
		return ComputerTarget{}, errors.New("COMPUTER_TARGET_UNAVAILABLE")
	}
	executable := C.GoString(&native.executable[0])
	identifier := C.GoString(&native.identifier[0])
	team := C.GoString(&native.team[0])
	title := C.GoString(&native.title[0])
	app := C.GoString(&native.app[0])
	if app == "" {
		app = filepath.Base(executable)
	}
	kind, level := macComputerAppPolicy(identifier, executable)
	if native.owned != 0 || pid == uint32(os.Getpid()) {
		level = "blocked"
	}
	identity := "path:" + executable
	if identifier != "" && team != "" {
		identity = "signer:" + identifier + "|" + team
	}
	return ComputerTarget{
		kind: kind, Level: level, Identity: identity, PID: pid, WindowID: window,
		Executable: executable, Created: uint64(native.started), App: app, Title: title,
		WindowIdentity: fmt.Sprintf("%d/%q/%g,%g,%g,%g", window, title, float64(native.x), float64(native.y), float64(native.width), float64(native.height)),
	}, nil
}

func macComputerAppPolicy(identifier, executable string) (computerAppKind, string) {
	id := strings.ToLower(identifier)
	name := strings.ToLower(filepath.Base(executable))
	switch name {
	case "dsh-work", "dshworkbrowser", "dsh-cef-owner", "cefsimple",
		"securityagent", "authorizationhost", "coreautha", "passwords", "keychain access",
		"loginwindow", "1password", "bitwarden", "keepassxc", "enpass", "dashlane", "proton pass", "nordpass":
		return computerAppOrdinary, "blocked"
	}
	switch id {
	case "com.apple.safari", "com.apple.safaritechnologypreview", "com.google.chrome", "com.google.chrome.canary",
		"org.mozilla.firefox", "org.mozilla.nightly", "com.microsoft.edgemac", "com.brave.browser",
		"com.operasoftware.opera", "com.vivaldi.vivaldi", "company.thebrowser.browser", "org.chromium.chromium":
		return computerAppBrowser, "view"
	case "com.apple.terminal", "com.googlecode.iterm2", "com.mitchellh.ghostty", "org.alacritty", "com.github.wez.wezterm", "dev.warp.warp-stable":
		return computerAppTerminal, "click"
	case "com.microsoft.vscode", "com.microsoft.vscodeinsiders", "com.todesktop.230313mzl4w4u92", "dev.zed.zed", "com.apple.dt.xcode":
		return computerAppOrdinary, "click"
	case "com.apple.securityagent", "com.apple.security.authorizationhost", "com.apple.coreautha.uiagent",
		"com.apple.passwords", "com.apple.keychainaccess", "com.apple.loginwindow",
		"com.1password.1password", "com.bitwarden.desktop", "org.keepassxc.keepassxc":
		return computerAppOrdinary, "blocked"
	}
	if strings.HasPrefix(id, "com.jetbrains.") || strings.HasPrefix(id, "com.codeium.windsurf") {
		return computerAppOrdinary, "click"
	}
	switch name {
	case "safari", "google chrome", "firefox", "microsoft edge", "brave browser", "opera", "vivaldi", "arc", "chromium":
		return computerAppBrowser, "view"
	case "terminal", "iterm2", "ghostty", "alacritty", "wezterm-gui", "warp":
		return computerAppTerminal, "click"
	case "code", "cursor", "windsurf", "zed", "xcode":
		return computerAppOrdinary, "click"
	default:
		return computerAppOrdinary, "full"
	}
}
