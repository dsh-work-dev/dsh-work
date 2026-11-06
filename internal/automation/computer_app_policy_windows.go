//go:build windows

package automation

import (
	"path/filepath"
	"strings"
)

// windowsComputerAppPolicy consumes the process image path returned by Windows.
// Its native path semantics and executable-name policy stay at this boundary.
func windowsComputerAppPolicy(executable string) (computerAppKind, string) {
	switch strings.ToLower(filepath.Base(executable)) {
	case "chrome.exe", "msedge.exe", "firefox.exe", "brave.exe", "opera.exe", "vivaldi.exe", "arc.exe", "chromium.exe":
		return computerAppBrowser, "view"
	case "windowsterminal.exe", "wt.exe", "cmd.exe", "powershell.exe", "pwsh.exe", "conhost.exe", "openconsole.exe", "wsl.exe", "mintty.exe", "alacritty.exe", "wezterm-gui.exe":
		return computerAppTerminal, "click"
	case "code.exe", "code - insiders.exe", "cursor.exe", "windsurf.exe", "zed.exe", "devenv.exe", "idea64.exe", "pycharm64.exe", "goland64.exe", "webstorm64.exe", "rider64.exe", "clion64.exe":
		return computerAppOrdinary, "click"
	case "1password.exe", "bitwarden.exe", "keepass.exe", "keepassxc.exe", "enpass.exe", "dashlane.exe", "proton pass.exe", "nordpass.exe", "consent.exe", "credentialuibroker.exe", "logonui.exe", "sechealthui.exe":
		return computerAppOrdinary, "blocked"
	default:
		return computerAppOrdinary, "full"
	}
}
