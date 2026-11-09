//go:build !darwin || !cgo || !e67presenter || !e67previewfixture || production || server

package desktopapp

import (
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func configureEmbeddedPreviewAssets(*desktopbridge.Surface) {}
func installEmbeddedPreviewConsumer(*application.App, application.Window, *desktopclient.EmbeddedBrowserService) {
}
