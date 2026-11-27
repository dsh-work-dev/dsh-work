//go:build !windows

package embeddedbrowser

import (
	"context"
	"errors"
	"github.com/local/dsh-work/internal/settings"
)

func chooseArtifactFile(context.Context, PresenterPeer, uintptr, settings.Locale) (string, error) {
	return "", errors.New("CEF_ARTIFACT_PICKER_UNAVAILABLE")
}
