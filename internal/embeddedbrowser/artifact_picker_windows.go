//go:build windows

package embeddedbrowser

import (
	"context"
	"github.com/local/dsh-work/internal/embeddedbrowser/downloads"
	"github.com/local/dsh-work/internal/nativeui"
	"github.com/local/dsh-work/internal/settings"
)

func chooseArtifactFile(ctx context.Context, peer PresenterPeer, parent uintptr, locale settings.Locale) (string, error) {
	return downloads.ChooseHTMLArtifact(ctx, downloads.DialogOwner{Window: parent, PID: peer.PID, Started: peer.Started}, nativeui.ArtifactPickerFor(locale))
}
