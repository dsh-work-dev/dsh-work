//go:build windows && production

package desktopapp

import (
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func embeddedDevelopmentRoot() string { return "" }

func embeddedComposition(options *application.WebviewWindowOptions) {
	if embeddedRuntimeReady() {
		options.Windows.WebView2CompositionBehindChildWindows = true
	}
}

// Production keeps the user's actual managed runtime and Run context. Only
// the pinned BrowserPage seam is admitted later, at the current selection.
func embeddedConfigureManager(*dshmanager.Config, *dshadapter.Adapter) error { return nil }

// QuitOwned is the development two-UI runner's authority, not a release peer.
func embeddedRunnerPeer(daemon.ProcessPeer) bool { return false }
