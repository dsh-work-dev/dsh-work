//go:build !darwin || !cgo || !e67presenter || !e67previewfixture || production || server

package desktopapp

import (
	"net/http"

	"github.com/local/dsh-work/internal/daemon"
	"github.com/local/dsh-work/internal/desktopbridge"
	"github.com/local/dsh-work/internal/desktopclient"
	"github.com/local/dsh-work/internal/embeddedbrowser"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/pet"
	"github.com/wailsapp/wails/v3/pkg/application"
	wailsnotifications "github.com/wailsapp/wails/v3/pkg/services/notifications"
)

func macPreviewRecordBroker(any) error { return nil }

func recordMacPreviewNotificationResponse(wailsnotifications.NotificationResult) {}
func clearMacPreviewNotification()                                               {}

func macPreviewDownloadConsent() embeddedbrowser.DownloadConsent { return nil }
func configureEmbeddedPreviewServices(*daemon.Server, *embeddedBrowserService, *application.App, nativeui.Labels, func(float64, float64) bool) {
}
func configureEmbeddedPreviewPet(*pet.CatalogConfig, *pet.OverlayCapabilities, string) bool {
	return false
}
func macPreviewDownloadShutdownChoice() int           { return 0 }
func macPreviewRecordDownloadShutdown(int, int) error { return nil }

func configureEmbeddedPreviewAssets(*desktopbridge.Surface)     {}
func embeddedPreviewShellAssets(next http.Handler) http.Handler { return next }
func installEmbeddedPreviewConsumer(*application.App, application.Window, *desktopclient.EmbeddedBrowserService, func() int) {
}
