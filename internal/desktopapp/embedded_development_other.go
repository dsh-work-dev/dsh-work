//go:build (!windows && !linux) || !e67presenter || production || (linux && (!cgo || server))

package desktopapp

import (
	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/dshadapter"
	"github.com/local/dsh-work/internal/dshmanager"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func embeddedDevelopmentRoot() string                       { return "" }
func embeddedComposition(*application.WebviewWindowOptions) {}

func embeddedConfigureManager(*dshmanager.Config, *dshadapter.Adapter) error { return nil }

func embeddedRunnerPeer(daemon.ProcessPeer) bool { return false }
