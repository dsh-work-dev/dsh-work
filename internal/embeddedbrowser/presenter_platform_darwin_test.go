//go:build darwin && cgo && !server

package embeddedbrowser

import "testing"

func TestMacDesktopBuildIncludesPresenter(t *testing.T) {
	// The normal desktop and production builds do not set e67presenter.
	peer := PresenterPeer{}
	_, err := presenterBounds(peer, 0, SlotBounds{}, 0)
	if err == nil || err.Error() != "CEF_ZOOM_REJECTED" {
		t.Fatalf("Mac desktop compiled without its native presenter: %v", err)
	}
	if err := ConfigureMacPresenter(peer, 0, "", ""); err == nil || err.Error() != "CEF_MAC_WINDOW_OWNER_REJECTED" {
		t.Fatalf("Mac desktop compiled without its native window registrar: %v", err)
	}
}

func TestMacDesktopBuildIncludesDownloadSheet(t *testing.T) {
	if !macDownloadSheetAvailable {
		t.Fatal("Mac desktop compiled without its native download sheet")
	}
}
